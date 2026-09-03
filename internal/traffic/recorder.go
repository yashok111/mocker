package traffic

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/store"
)

// retentionEveryN is how many EVENTS (not batches, not transactions) the
// Recorder writes between retention passes on its periodic Run path. It sits
// well above DefaultBatch (64) so a normal-sized batch's transaction is not
// carrying a retention DELETE on top of its INSERTs every single time — that
// DELETE is a per-workspace scan of up to Options.Retention rows, cheap once
// in a while and wasted overhead on every write under steady low traffic.
//
// Flush and the shutdown drain do NOT wait for this counter — see
// [Recorder.writeBatch].
const retentionEveryN = 256

// Recorder buffers [Event]s on a channel and writes them out in batches on a
// single writer goroutine, per DESIGN §18: batched in one transaction rather
// than an INSERT per request, retention cleaned every N inserts, nothing on
// the hot path that blocks.
type Recorder struct {
	db  *store.DB
	log *slog.Logger

	maxBody    int64
	retention  int
	batchSize  int
	flushEvery time.Duration

	queue chan queuedEvent

	dropped           atomic.Int64 // lifetime total (queue-full drops plus failed-insert drops), for Dropped()
	unreportedDropped atomic.Int64 // drops not yet folded into a stored row's notes

	// writeMu serializes every transaction this Recorder issues, whether
	// triggered by Run's ticker or by a caller's Flush. Flush must be safe
	// to call concurrently with Run (HARD RULE from the task), and the
	// simplest way to guarantee that without a goroutine-handshake protocol
	// is to make BOTH paths go through the same drain-and-write function
	// under the same lock: whichever gets there first does the work, the
	// other finds an empty (or partially refilled) queue and returns
	// quickly. This is also why Flush never deadlocks when Run is not
	// running at all — Flush does the write itself, it never waits on Run.
	writeMu sync.Mutex
	// sinceRetention is touched only inside writeMu, so it needs no atomic.
	sinceRetention int

	// notifier is P6a's one seam out of this package (decisions.md
	// mocker-p6a-sse D5): told which workspaces a batch touched, AFTER the
	// batch committed and the ids exist, so a subscriber that wakes on it
	// and reads Repo.Since sees the rows the nudge announced. nil until
	// SetNotifier — this package does not import internal/stream and never
	// will; the registry arrives through the interface, from main.go.
	notifierMu sync.RWMutex
	notifier   Notifier
}

// Notifier is what [Recorder.SetNotifier] takes: something that wants to
// know a batch of traffic rows for workspaceIDs has just been COMMITTED. The
// recorder calls it from its own writer goroutine, outside writeMu and after
// the transaction returned, and expects it never to block — internal/stream's
// registry satisfies that by a non-blocking send into a one-slot channel per
// connection, dropped and counted when the slot is already full (D5's
// "drop-and-count"). A Notify that blocked would stall the next flush.
type Notifier interface {
	Notify(workspaceIDs []int64)
}

// SetNotifier wires n as the recorder's [Notifier]. Safe to call before or
// after Run; a nil n disables notification again. Like every other setter in
// this tree it exists because NewRecorder's signature is shared with
// cmd/mocker/main.go, and because the thing being wired (the stream
// registry) is built AFTER the recorder is.
func (rec *Recorder) SetNotifier(n Notifier) {
	rec.notifierMu.Lock()
	rec.notifier = n
	rec.notifierMu.Unlock()
}

// notify hands the distinct workspace ids of committed batches to the wired
// Notifier, if any. Called by drainAndWrite AFTER writeMu is released (D5:
// "outside writeMu and without blocking") — a Notifier that blocked, or one
// that called back into Flush, would otherwise stall or deadlock the writer
// under its own lock — and after every transaction in that drain committed,
// so every id the nudge announces is already readable through Repo.Since.
func (rec *Recorder) notify(touched map[int64]struct{}) {
	if len(touched) == 0 {
		return
	}
	rec.notifierMu.RLock()
	n := rec.notifier
	rec.notifierMu.RUnlock()
	if n == nil {
		return
	}
	ids := make([]int64, 0, len(touched))
	for id := range touched {
		ids = append(ids, id)
	}
	n.Notify(ids)
}

// queuedEvent is what actually rides rec.queue: an Event AFTER Record has
// redacted, truncated and composed its Notes. The channel never carries a
// raw Event — DESIGN §15 requires redaction before anything is buffered, so
// Record does all of that work synchronously, before the send, never in the
// writer goroutine.
type queuedEvent struct {
	workspaceID    int64
	ts             int64 // epoch seconds, see Event.TS's doc comment
	method, path   string
	peerIP, fwdIP  string
	matchedKind    string
	matchedID      sql.NullInt64 // NULL iff matchedKind == "none"
	status         int
	durationMS     float64
	reqHeadersJSON string // "" means nothing to store
	reqBody        []byte // nil means nothing to store (suppressed or empty)
	respBody       []byte
	notes          string
	truncated      bool
}

// NewRecorder builds a Recorder over db. It does not start writing anything
// until [Recorder.Run] (or a direct [Recorder.Flush]) is called.
func NewRecorder(db *store.DB, log *slog.Logger, opts Options) *Recorder {
	rec := &Recorder{
		db:         db,
		log:        log,
		maxBody:    opts.MaxBody,
		retention:  opts.Retention,
		batchSize:  opts.Batch,
		flushEvery: opts.FlushEvery,
	}
	if rec.maxBody <= 0 {
		rec.maxBody = DefaultMaxBody
	}
	if rec.retention <= 0 {
		rec.retention = DefaultRetention
	}
	if rec.batchSize <= 0 {
		rec.batchSize = DefaultBatch
	}
	if rec.flushEvery <= 0 {
		rec.flushEvery = DefaultFlushEvery
	}
	queue := opts.Queue
	if queue <= 0 {
		queue = DefaultQueue
	}
	rec.queue = make(chan queuedEvent, queue)
	return rec
}

// Record redacts and truncates ev, then queues it. It NEVER blocks: a full
// queue drops the event, counts it (both in the lifetime Dropped() total and
// in the running "not yet reported" count), and returns immediately — no
// goroutine is spawned per event and the queue is never grown past
// Options.Queue, exactly the two escape hatches DESIGN §18 forbids for the
// hot path.
func (rec *Recorder) Record(ev Event) {
	pending := rec.unreportedDropped.Swap(0)
	qe := rec.prepare(ev, pending)

	select {
	case rec.queue <- qe:
		// pending was consumed by qe's own notes above; nothing left to do.
	default:
		// The send failed, so qe (and the gap note it carries) is thrown
		// away along with ev. Put pending BACK — it must ride the next
		// event that actually makes it onto the queue, not vanish here —
		// and count this failure on top of it.
		rec.unreportedDropped.Add(pending + 1)
		rec.dropped.Add(1)
	}
}

// prepare does every bit of work DESIGN §15 requires happen before an event
// is buffered: header and body redaction, the MaxBody cut (in that order —
// cutting a redacted JSON body would truncate mid-token and fail to parse,
// which is exactly backwards from cutting first and redacting a mangled
// prefix), and composing Notes as the pinned token set plus the caller's
// free text.
func (rec *Recorder) prepare(ev Event, pendingDropped int64) queuedEvent {
	qe := queuedEvent{
		workspaceID: ev.WorkspaceID,
		ts:          ev.TS.Unix(),
		method:      ev.Method,
		path:        ev.Path,
		peerIP:      ev.PeerIP,
		fwdIP:       ev.FwdIP,
		matchedKind: ev.MatchedKind,
		status:      ev.Status,
		durationMS:  float64(ev.Duration) / float64(time.Millisecond),
	}
	if ev.MatchedKind != "none" {
		qe.matchedID = sql.NullInt64{Int64: ev.MatchedID, Valid: true}
	}

	var notes []string
	switch {
	case ev.SuppressBodies:
		// Neither body is stored, full stop — not redacted-then-stored,
		// ABSENT. qe.reqBody/respBody stay nil.
		notes = append(notes, NoteSuppressed)
	default:
		reqBody, reqChanged := RedactBody(ev.ReqBody, ev.ReqContentType)
		respBody, respChanged := RedactBody(ev.RespBody, ev.RespContentType)

		// Copied on cut, never re-sliced: a re-slice keeps the whole
		// captured buffer (up to 64 KiB for a request body) alive in the
		// queue for every event until its batch is written.
		reqCut := int64(len(reqBody)) > rec.maxBody
		if reqCut {
			reqBody = append([]byte(nil), reqBody[:rec.maxBody]...)
		}
		respCut := int64(len(respBody)) > rec.maxBody
		if respCut {
			respBody = append([]byte(nil), respBody[:rec.maxBody]...)
		}

		qe.reqBody = reqBody
		qe.respBody = respBody
		qe.truncated = reqCut || respCut || ev.Truncated

		if reqChanged || respChanged {
			notes = append(notes, NoteRedacted)
		}
		if reqCut {
			notes = append(notes, NoteTruncatedReq)
		}
		if respCut {
			notes = append(notes, NoteTruncatedRsp)
		}
	}

	if len(ev.ReqHeaders) > 0 {
		if b, err := jsonx.Marshal(RedactHeaders(ev.ReqHeaders)); err == nil {
			if int64(len(b)) > rec.maxBody {
				// Headers are diagnostic; a row must not carry more of
				// them than it may carry of a body. Cutting JSON leaves an
				// unparsable column, so an oversized header set is
				// DROPPED and the row says so, the same way a cut body
				// does. http.Server's MaxHeaderBytes (cmd/mocker) is what
				// bounds the marshal itself.
				b = []byte("{}")
				notes = append(notes, NoteTruncatedHdr)
				qe.truncated = true
			}
			qe.reqHeadersJSON = string(b)
		} else if rec.log != nil {
			// Headers are diagnostic, not the security-critical half of
			// this function — losing them on a marshal error (which
			// map[string]string cannot actually produce, but nothing
			// enforces that at the type level) is a log line, not a
			// reason to fail the whole event.
			rec.log.Error("traffic: marshal redacted headers failed", "error", err)
		}
	}

	if pendingDropped > 0 {
		notes = append(notes, NoteDroppedPrefix+strconv.FormatInt(pendingDropped, 10))
	}
	if ev.Notes != "" {
		notes = append(notes, ev.Notes)
	}
	qe.notes = strings.Join(notes, ",")

	return qe
}

// Run is the single writer goroutine: it flushes on Options.FlushEvery, and
// on ctx cancellation it drains what is queued, writes it, and returns — a
// shutdown must not lose the last batch.
func (rec *Recorder) Run(ctx context.Context) {
	ticker := time.NewTicker(rec.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// The final drain MUST NOT use ctx: database/sql fails a query
			// against an already-cancelled context immediately, so the
			// drain that exists to save the last batch would write nothing
			// while Run still returns looking like it succeeded.
			// context.WithoutCancel keeps this independent of ctx's
			// cancellation while still descending from it (no request-scoped
			// values to lose here, but the pattern is the right one), and
			// the bounded timeout keeps a wedged DB from hanging shutdown
			// forever instead of trading one bug for another.
			drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			if err := rec.drainAndWrite(drainCtx, true); err != nil && rec.log != nil {
				rec.log.Error("traffic: final drain on shutdown failed", "error", err)
			}
			cancel()
			return
		case <-ticker.C:
			// Detached from ctx like the final drain: a periodic write in
			// flight at recorderCancel() would otherwise be rolled back by
			// database/sql, and its batch — already off the queue — lost
			// before the final drain ever ran.
			tickCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			if err := rec.drainAndWrite(tickCtx, false); err != nil && rec.log != nil {
				rec.log.Error("traffic: periodic drain failed", "error", err)
			}
			cancel()
		}
	}
}

// Flush forces the current buffer out and returns only when it has been
// committed. It is safe to call concurrently with Record and with Run, and
// it does not depend on Run being active at all: it drains and writes the
// queue itself, under the same lock Run's ticker uses, rather than signalling
// a goroutine that might not exist.
func (rec *Recorder) Flush(ctx context.Context) error {
	return rec.drainAndWrite(ctx, true)
}

// Dropped reports the lifetime count of events dropped, either because the
// queue was full or because a row's INSERT failed (see [Recorder.writeBatch]).
func (rec *Recorder) Dropped() int64 { return rec.dropped.Load() }

// drainAndWrite pulls events off rec.queue and writes them in as few
// transactions as it can, holding writeMu for the duration so a concurrent
// Flush and Run tick never interleave their transactions or race on
// sinceRetention.
//
// all=false (Run's periodic tick): write at most one Batch-sized chunk and
// return, even if more is queued — the next tick picks up the rest. This
// bounds how long a single tick can hold the writer connection under a
// traffic burst, rather than one huge tick starving Flush/shutdown of the
// lock.
// all=true (Flush, and the shutdown drain): keep going until the queue is
// empty.
func (rec *Recorder) drainAndWrite(ctx context.Context, all bool) error {
	touched, err := rec.drainAndWriteLocked(ctx, all)
	// Outside the lock, on purpose (see notify). What committed is
	// announced even when a LATER batch of the same drain failed: those
	// rows exist and a subscriber is entitled to hear about them.
	rec.notify(touched)
	return err
}

// drainAndWriteLocked is drainAndWrite's body under writeMu; it returns the
// union of every committed batch's touched workspace ids for the caller to
// announce once the lock is gone.
func (rec *Recorder) drainAndWriteLocked(ctx context.Context, all bool) (map[int64]struct{}, error) {
	rec.writeMu.Lock()
	defer rec.writeMu.Unlock()

	var touchedAll map[int64]struct{}
	// A periodic tick keeps writing while a full batch is still waiting,
	// bounded by half its own interval: one batch per tick capped
	// steady-state recording at batchSize/flushEvery = 128 rows/s
	// regardless of how fast the database was, after which the queue
	// filled and Record dropped. The per-batch transaction is unchanged, so
	// the writer is still released between batches.
	tickBudget := time.Now().Add(rec.flushEvery / 2)
	for {
		batch := rec.drainUpTo(rec.batchSize)
		if len(batch) == 0 {
			return touchedAll, nil
		}
		touched, err := rec.writeBatchSafe(ctx, batch, all)
		if err != nil {
			// The batch is off the queue and the transaction rolled back:
			// those events are gone, and they count where every other
			// lost event counts (the panic guard in writeBatchSafe already
			// does this for its own case).
			rec.dropped.Add(int64(len(batch)))
			rec.unreportedDropped.Add(int64(len(batch)))
			if rec.log != nil {
				rec.log.Error("traffic: batch write failed", "error", err, "events", len(batch))
			}
			return touchedAll, err
		}
		if len(touched) > 0 {
			if touchedAll == nil {
				touchedAll = make(map[int64]struct{}, len(touched))
			}
			for id := range touched {
				touchedAll[id] = struct{}{}
			}
		}
		if len(rec.queue) == 0 {
			return touchedAll, nil
		}
		if !all && (len(rec.queue) < rec.batchSize || time.Now().After(tickBudget)) {
			return touchedAll, nil
		}
	}
}

// writeBatchFn is [Recorder.writeBatch], reached through a variable for ONE
// reason: a test has to be able to make it panic to prove the guard below
// actually holds. Nothing in production ever reassigns it.
var writeBatchFn = (*Recorder).writeBatch

// writeBatchSafe is writeBatch with a panic guard, and the guard is not
// ceremony. This runs on a BACKGROUND GOROUTINE (Run's ticker), and a panic on
// a goroutine is not caught by httpx.Recover — that middleware only wraps a
// request's own stack. So a panic here would not fail one request or one batch:
// it would take the entire process down, and with it every workspace's mock,
// long after the request that produced the offending event returned 200.
//
// The events this writes are attacker-shaped by construction — headers, bodies
// and paths straight off an UNAUTHENTICATED plane (DESIGN §15), redacted and
// truncated but never re-validated as Go values. A nil map, a type assertion or
// a slice index that only some future field can reach is exactly the kind of
// defect that ships. Losing one batch and logging it is the correct trade;
// losing the process is not.
func (rec *Recorder) writeBatchSafe(ctx context.Context, batch []queuedEvent, all bool) (touched map[int64]struct{}, err error) {
	defer func() {
		if p := recover(); p != nil {
			// The batch is already off the queue and cannot be put back, so
			// those events are lost; drainAndWriteLocked counts them on the
			// error path this returns into, the same place a batch whose
			// transaction merely failed is counted.
			err = fmt.Errorf("traffic: recovered from a panic while writing a batch of %d event(s): %v", len(batch), p)
			if rec.log != nil {
				rec.log.Error("traffic: panic while writing a batch; the batch is lost and the writer continues",
					"events", len(batch), "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			}
		}
	}()
	return writeBatchFn(rec, ctx, batch, all)
}

// drainUpTo removes up to n events from rec.queue without blocking: a
// channel with nothing left to give hits the default case and drainUpTo
// returns whatever it already had, exactly like [Recorder.Record]'s own
// non-blocking send.
func (rec *Recorder) drainUpTo(n int) []queuedEvent {
	batch := make([]queuedEvent, 0, n)
	for len(batch) < n {
		select {
		case ev := <-rec.queue:
			batch = append(batch, ev)
		default:
			return batch
		}
	}
	return batch
}

// writeBatch inserts batch in ONE transaction and, INSIDE that same
// transaction, prunes retention for every workspace the batch touched.
//
// Each row's INSERT runs under its own SAVEPOINT rather than sharing the
// transaction's fate with its neighbours. A workspace can be deleted (admin
// DELETE /api/workspaces/{id}, cascading via the traffic.workspace_id FK)
// while events for it are still sitting in rec.queue — Record has no way to
// know that, and by the time writeBatch runs, drainUpTo has already pulled
// those events out of the queue for good. Without per-row isolation, one
// FOREIGN KEY failure would fail fn, roll back the WHOLE db.Write transaction
// via its deferred tx.Rollback, and silently discard every other row in the
// batch too — including rows for OTHER, still-live workspaces that happened
// to land in the same flush. ROLLBACK TO the savepoint undoes only that row;
// the surviving rows commit normally. A row that fails this way is folded
// into the same dropped-count machinery [Recorder.Record] uses for a full
// queue, so the next successfully stored event's Notes carries a "dropped:N"
// token instead of the loss being invisible.
//
// forcePrune overrides sinceRetention's every-N-events pacing: Flush and the
// shutdown drain use it so a caller that just asked "make sure everything is
// written" gets a table that is also within Options.Retention the moment the
// call returns, rather than one that might still be over-cap because the
// counter had not yet reached retentionEveryN. Run's periodic ticks pass
// false and let the counter pace it — see [retentionEveryN].
//
// The returned set is the distinct workspace ids whose rows COMMITTED — a
// row whose INSERT failed under its savepoint is not in it — and it is what
// [Recorder.notify] hands to the Notifier once this transaction has
// returned (P6a D5: the nudge follows the ids, never precedes them). On an
// error the set is nil: nothing committed, nothing to announce.
func (rec *Recorder) writeBatch(ctx context.Context, batch []queuedEvent, forcePrune bool) (map[int64]struct{}, error) {
	touched := make(map[int64]struct{}, len(batch))
	err := rec.db.Write(ctx, func(tx *sql.Tx) error {
		var failed int64
		for _, qe := range batch {
			if _, err := tx.ExecContext(ctx, "SAVEPOINT traffic_row"); err != nil {
				return fmt.Errorf("savepoint traffic row: %w", err)
			}
			if err := insertEventTx(ctx, tx, qe); err != nil {
				if _, rbErr := tx.ExecContext(ctx, "ROLLBACK TO traffic_row"); rbErr != nil {
					// The connection itself is in trouble (not just this
					// row) — surface it and let the whole transaction roll
					// back rather than pressing on over an unknown state.
					return fmt.Errorf("rollback traffic row after insert error (%w): %w", err, rbErr)
				}
				if _, err := tx.ExecContext(ctx, "RELEASE traffic_row"); err != nil {
					return fmt.Errorf("release traffic row savepoint: %w", err)
				}
				failed++
				if rec.log != nil {
					rec.log.Error("traffic: dropping event, insert failed", "error", err, "workspace_id", qe.workspaceID)
				}
				continue
			}
			if _, err := tx.ExecContext(ctx, "RELEASE traffic_row"); err != nil {
				return fmt.Errorf("release traffic row savepoint: %w", err)
			}
			touched[qe.workspaceID] = struct{}{}
		}
		if failed > 0 {
			// Same counters Record's queue-full path uses (see there): the
			// notes token this produces on a later row is what makes a row
			// lost here visible to a reader of the traffic table, not just
			// to this log line.
			rec.dropped.Add(failed)
			rec.unreportedDropped.Add(failed)
		}

		rec.sinceRetention += len(batch)
		prune := forcePrune || rec.sinceRetention >= retentionEveryN
		if prune {
			rec.sinceRetention = 0
			for ws := range touched {
				if err := pruneRetentionTx(ctx, tx, ws, rec.retention); err != nil {
					return fmt.Errorf("prune retention for workspace %d: %w", ws, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return touched, nil
}

// insertEventTx writes one row. nil/empty req_headers, req_body, resp_body
// and notes are stored as SQL NULL rather than "" — "no body was captured"
// (suppressed, or a genuinely bodyless GET) and "an empty string body" are
// different facts, and NULL is what lets [Row]'s `omitempty` JSON tags and a
// direct SQL NULL check agree on which one this row is.
func insertEventTx(ctx context.Context, tx *sql.Tx, qe queuedEvent) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO traffic
			(workspace_id, ts, method, path, peer_ip, fwd_ip, matched_kind, matched_id,
			 status, duration_ms, req_headers, req_body, resp_body, notes, truncated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		qe.workspaceID, qe.ts, qe.method, qe.path, qe.peerIP, qe.fwdIP, qe.matchedKind, qe.matchedID,
		qe.status, qe.durationMS, textOrNull(qe.reqHeadersJSON), byteTextOrNull(qe.reqBody), byteTextOrNull(qe.respBody),
		textOrNull(qe.notes), boolToInt(qe.truncated),
	)
	if err != nil {
		return err
	}
	return nil
}

// pruneRetentionTx deletes workspaceID's rows beyond the retention newest,
// keyed by id (the traffic_ws index's own order) — never by ts, which the
// index does not cover. The inner SELECT and the outer DELETE both filter on
// workspace_id first, so this is a bounded scan of one workspace's rows via
// traffic_ws, never a full-table scan across workspaces.
func pruneRetentionTx(ctx context.Context, tx *sql.Tx, workspaceID int64, retention int) error {
	if retention <= 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		DELETE FROM traffic
		WHERE workspace_id = ? AND id NOT IN (
			SELECT id FROM traffic WHERE workspace_id = ? ORDER BY id DESC LIMIT ?
		)`, workspaceID, workspaceID, retention)
	return err
}

func textOrNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func byteTextOrNull(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
