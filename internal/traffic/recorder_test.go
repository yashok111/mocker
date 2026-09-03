package traffic_test

import (
	"context"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/traffic"
)

// newTestDB opens a fresh, migrated SQLite file under t.TempDir(), mirroring
// internal/overrides/repo_test.go's harness — this package's own copy, per
// that package's convention of not sharing test helpers across packages.
func newTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), t.TempDir()+"/mocker.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// insertWorkspace writes a minimal workspaces row directly, the FK target
// traffic rows require.
func insertWorkspace(t *testing.T, db *store.DB, slug string) int64 {
	t.Helper()
	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO workspaces (slug, name, revision, settings, created_at, updated_at)
		VALUES (?, ?, 1, '{}', unixepoch(), unixepoch())`,
		slug, slug,
	)
	if err != nil {
		t.Fatalf("insert workspace %q: %v", slug, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	return id
}

func testEvent(workspaceID int64, path string) traffic.Event {
	return traffic.Event{
		WorkspaceID: workspaceID,
		TS:          time.Now(),
		Method:      "GET",
		Path:        path,
		PeerIP:      "203.0.113.7",
		MatchedKind: "operation",
		MatchedID:   42,
		Status:      200,
		Duration:    5 * time.Millisecond,
	}
}

// TestRecorder_Record_neverBlocks fills the queue past capacity from one
// goroutine with NO consumer running (Run is never started) and asserts
// Record still returns and Dropped() counts the overflow — the "never
// blocks" guarantee holds independent of anything reading the other end.
func TestRecorder_Record_neverBlocks(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{Queue: 4})

	const total = 40 // well past the queue capacity of 4
	done := make(chan struct{})
	go func() {
		for i := range total {
			rec.Record(testEvent(wsID, "/x/"+strconv.Itoa(i)))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Record() did not return promptly with no consumer running — it blocked")
	}

	if got := rec.Dropped(); got == 0 {
		t.Errorf("Dropped() = 0, want > 0 after overfilling a queue of capacity 4 with %d events", total)
	}
}

// TestRecorder_Flush_oneBatchOneFlush covers "a batch of N events lands in
// ONE transaction", verified the way the task spec itself prescribes: count
// rows after a single Flush.
func TestRecorder_Flush_oneBatchOneFlush(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{})
	repo := traffic.NewRepo(db)

	const n = 10
	for i := range n {
		rec.Record(testEvent(wsID, "/items/"+strconv.Itoa(i)))
	}

	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	rows, err := repo.List(t.Context(), wsID, 100)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(rows) != n {
		t.Fatalf("List() after one Flush = %d rows, want %d", len(rows), n)
	}
}

// TestRecorder_Retention_keepsNewestOnly writes Retention+20 events to one
// workspace, flushes, and asserts exactly Retention rows remain and that
// they are the newest ones — Flush forces retention regardless of the
// periodic every-N-events counter, so this holds after a single call.
func TestRecorder_Retention_keepsNewestOnly(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	const retention = 50 // smaller than DefaultRetention so the test is fast
	rec := traffic.NewRecorder(db, nil, traffic.Options{Retention: retention, Batch: 16})
	repo := traffic.NewRepo(db)

	const total = retention + 20
	for i := range total {
		rec.Record(testEvent(wsID, "/n/"+strconv.Itoa(i)))
	}
	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	rows, err := repo.List(t.Context(), wsID, total+10)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(rows) != retention {
		t.Fatalf("row count = %d, want exactly %d (retention)", len(rows), retention)
	}

	// List is newest-first; the surviving rows must be the LAST `retention`
	// paths written, in that same newest-first order.
	for i, row := range rows {
		wantIdx := total - 1 - i
		wantPath := "/n/" + strconv.Itoa(wantIdx)
		if row.Path != wantPath {
			t.Errorf("rows[%d].Path = %q, want %q (newest %d rows must survive)", i, row.Path, wantPath, retention)
		}
	}
}

// TestRecorder_SuppressBodies_storesNeitherBody covers the auth-path
// contract: SuppressBodies stores no body at all, not a redacted one.
func TestRecorder_SuppressBodies_storesNeitherBody(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{})
	repo := traffic.NewRepo(db)

	ev := testEvent(wsID, "/login")
	ev.ReqBody = []byte(`{"password":"hunter2"}`)
	ev.RespBody = []byte(`{"token":"abc.def.ghi"}`)
	ev.SuppressBodies = true
	rec.Record(ev)

	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	rows, err := repo.List(t.Context(), wsID, 10)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List() = %d rows, want 1", len(rows))
	}
	got := rows[0]
	if got.ReqBody != "" {
		t.Errorf("ReqBody = %q, want empty (suppressed, not redacted)", got.ReqBody)
	}
	if got.RespBody != "" {
		t.Errorf("RespBody = %q, want empty (suppressed, not redacted)", got.RespBody)
	}
	if !got.HasNote(traffic.NoteSuppressed) {
		t.Errorf("Notes = %q, want it to carry %q", got.Notes, traffic.NoteSuppressed)
	}
	if !got.Redacted() {
		t.Error("Redacted() = false, want true for a suppressed row")
	}
}

// TestRecorder_MaxBody_cutsAndStillRedacts proves MaxBody cutting happens
// AFTER redaction (so a secret near the cut boundary is still caught) and
// that it sets Truncated with the matching per-body note.
func TestRecorder_MaxBody_cutsAndStillRedacts(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{MaxBody: 32})
	repo := traffic.NewRepo(db)

	// encoding/json sorts object keys alphabetically on marshal, and
	// "password" < "username", so the redacted field lands inside the first
	// 32 bytes of the re-marshaled body either way this test's assertion
	// runs — what it actually distinguishes is whether the secret's RAW
	// value ever reaches the cut bytes. Redact-then-cut: "hunter2" is
	// already replaced before the cut, so it can never appear. Cut-then-
	// redact (the bug this guards against): cutting first would slice into
	// a body that's no longer valid JSON, RedactJSONBody would see the
	// parse fail and hand the cut bytes back untouched — "hunter2" survives
	// in the clear.
	ev := testEvent(wsID, "/orders")
	ev.Method = "POST"
	ev.ReqBody = []byte(`{"password":"hunter2","username":"alexalexalexalexalexalex"}`)
	rec.Record(ev)

	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	rows, err := repo.List(t.Context(), wsID, 10)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List() = %d rows, want 1", len(rows))
	}
	got := rows[0]
	if !got.Truncated {
		t.Error("Truncated = false, want true (body exceeds MaxBody)")
	}
	if !got.HasNote(traffic.NoteTruncatedReq) {
		t.Errorf("Notes = %q, want it to carry %q", got.Notes, traffic.NoteTruncatedReq)
	}
	if !got.HasNote(traffic.NoteRedacted) {
		t.Errorf("Notes = %q, want it to carry %q (redaction must run before the cut)", got.Notes, traffic.NoteRedacted)
	}
	if len(got.ReqBody) > 32 {
		t.Errorf("ReqBody length = %d, want <= 32", len(got.ReqBody))
	}
	if strings.Contains(got.ReqBody, "hunter2") {
		t.Errorf("ReqBody = %q contains the raw secret — redaction did not run before the cut", got.ReqBody)
	}
}

// TestRecorder_FormAndTextBodies_RedactedByFieldName is round-1 review
// finding 5, at the level a real request actually flows through this
// package: Recorder.prepare must pick [traffic.RedactBody]'s form/text
// branches off Event's own ReqContentType/RespContentType, not fall through
// to the JSON-only path that shipped before this fix — the exact
// reproduction that stored "username=bob&password=FORMSECRET123..." in
// cleartext and handed it back through GET .../traffic/poll.
func TestRecorder_FormAndTextBodies_RedactedByFieldName(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{})
	repo := traffic.NewRepo(db)

	ev := testEvent(wsID, "/api/v1/signin")
	ev.Method = "POST"
	ev.ReqBody = []byte("username=bob&password=FORMSECRET123&grant_type=password")
	ev.ReqContentType = "application/x-www-form-urlencoded"
	ev.RespBody = []byte("token: TEXTSECRET")
	ev.RespContentType = "text/plain"
	rec.Record(ev)

	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	rows, err := repo.List(t.Context(), wsID, 10)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List() = %d rows, want 1", len(rows))
	}
	got := rows[0]
	if strings.Contains(got.ReqBody, "FORMSECRET123") {
		t.Errorf("ReqBody = %q, the form password leaked in cleartext", got.ReqBody)
	}
	if !strings.Contains(got.ReqBody, "bob") || !strings.Contains(got.ReqBody, "grant_type") {
		t.Errorf("ReqBody = %q, non-secret form fields must survive", got.ReqBody)
	}
	if strings.Contains(got.RespBody, "TEXTSECRET") {
		t.Errorf("RespBody = %q, the text/plain token leaked in cleartext", got.RespBody)
	}
	if !got.HasNote(traffic.NoteRedacted) {
		t.Errorf("Notes = %q, want it to carry %q", got.Notes, traffic.NoteRedacted)
	}
}

// TestRecorder_UpstreamTruncatedJSONBody_doesNotLeakSecret is the recorder-
// level regression test for the gate-2 finding: mockplane's own
// request-body cap (reqbody.go) cuts an oversized body at a raw byte
// offset, upstream of this package entirely, and hands the recorder
// Event.ReqBody already truncated (with Event.Truncated set to say so).
// Before the fix, RedactJSONBody's json.Unmarshal failed on that mid-string
// cut and handed the raw, unredacted bytes straight through to storage —
// this proves the secret sitting near the front of the body never reaches
// the traffic table, even though it was well within the byte offset where
// the cut happened.
func TestRecorder_UpstreamTruncatedJSONBody_doesNotLeakSecret(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{}) // default MaxBody: far above this body's size
	repo := traffic.NewRepo(db)

	// A byte-offset cut mid-string, exactly what captureRequestBody leaves
	// behind for an oversized body — NOT syntactically valid JSON, but the
	// secret is fully intact in the surviving prefix.
	raw := `{"password":"hunter2","padding":"` + strings.Repeat("a", 200) // deliberately no closing quote/brace
	ev := testEvent(wsID, "/register")
	ev.Method = "POST"
	ev.ReqBody = []byte(raw)
	ev.Truncated = true // mockplane sets this whenever captureRequestBody cut the body
	rec.Record(ev)

	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	rows, err := repo.List(t.Context(), wsID, 10)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List() = %d rows, want 1", len(rows))
	}
	got := rows[0]
	if strings.Contains(got.ReqBody, "hunter2") {
		t.Errorf("ReqBody = %q contains the raw secret — an upstream byte-cut body was stored unredacted", got.ReqBody)
	}
	if !got.HasNote(traffic.NoteRedacted) {
		t.Errorf("Notes = %q, want it to carry %q for a dropped truncated-JSON body", got.Notes, traffic.NoteRedacted)
	}
	if !got.Redacted() {
		t.Error("Redacted() = false, want true (the admin to-override conversion must refuse this row)")
	}
}

// TestRecorder_EventTruncated_foldsIntoColumnButNoPerBodyNote pins the
// documented interpretation of Event.Truncated: it forces the Row.Truncated
// column true (the caller's signal must not be silently lost) but, because
// it does not say WHICH body was pre-cut, it does not by itself add
// NoteTruncatedReq or NoteTruncatedRsp — only the recorder's own MaxBody cut
// can attribute a cut to one side.
func TestRecorder_EventTruncated_foldsIntoColumnButNoPerBodyNote(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{}) // ample MaxBody, no cut of its own
	repo := traffic.NewRepo(db)

	ev := testEvent(wsID, "/small")
	ev.ReqBody = []byte(`{"ok":true}`) // well under MaxBody
	ev.Truncated = true
	rec.Record(ev)

	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	rows, err := repo.List(t.Context(), wsID, 10)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	got := rows[0]
	if !got.Truncated {
		t.Error("Truncated = false, want true (Event.Truncated must fold into the column)")
	}
	if got.HasNote(traffic.NoteTruncatedReq) || got.HasNote(traffic.NoteTruncatedRsp) {
		t.Errorf("Notes = %q, want neither per-body token (Event.Truncated does not say which side)", got.Notes)
	}
}

// TestRecorder_Notes_droppedTokenAndCallerFreeTextOrder proves the drop
// counter surfaces as a dropped: token on the next stored event, and that
// caller free text (Event.Notes) is appended AFTER the recorder's own
// tokens rather than merged with them.
func TestRecorder_Notes_droppedTokenAndCallerFreeTextOrder(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{Queue: 1})
	repo := traffic.NewRepo(db)

	// Overfill the queue of 1 with no consumer running so the second and
	// third Record()s are dropped, then flush the one that made it plus a
	// fresh event with its own free text — the fresh event should carry the
	// dropped: token.
	rec.Record(testEvent(wsID, "/first"))
	rec.Record(testEvent(wsID, "/dropped-a"))
	rec.Record(testEvent(wsID, "/dropped-b"))
	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush() 1: %v", err)
	}

	if rec.Dropped() != 2 {
		t.Fatalf("Dropped() = %d, want 2", rec.Dropped())
	}

	ev := testEvent(wsID, "/next")
	ev.Notes = "caller-note"
	rec.Record(ev)
	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush() 2: %v", err)
	}

	rows, err := repo.List(t.Context(), wsID, 10)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	// newest first: rows[0] is "/next".
	next := rows[0]
	if next.Path != "/next" {
		t.Fatalf("rows[0].Path = %q, want /next", next.Path)
	}
	if next.DroppedBefore() != 2 {
		t.Errorf("DroppedBefore() = %d, want 2", next.DroppedBefore())
	}
	wantSuffix := traffic.NoteDroppedPrefix + "2,caller-note"
	if next.Notes != wantSuffix {
		t.Errorf("Notes = %q, want %q (recorder tokens first, caller text last)", next.Notes, wantSuffix)
	}
}

// TestRecorder_CtxCancellationFlushesTail starts Run, records events, then
// cancels ctx and proves the tail landed in the database — not merely that
// Run returned. The final drain must use a context independent of the
// cancelled one, or this would flake into "Run returns but nothing is
// written".
func TestRecorder_CtxCancellationFlushesTail(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	// A long FlushEvery so the ticker cannot be what saves this test — only
	// the ctx-cancellation drain path can.
	rec := traffic.NewRecorder(db, nil, traffic.Options{FlushEvery: time.Hour})
	repo := traffic.NewRepo(db)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		rec.Run(ctx)
		close(runDone)
	}()

	const n = 5
	for i := range n {
		rec.Record(testEvent(wsID, "/tail/"+strconv.Itoa(i)))
	}
	cancel()

	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after ctx cancellation")
	}

	// Bounded retry: Run's own final write happens synchronously before it
	// returns, so this should already be true, but poll briefly rather than
	// assume — there is no Flush to call here, this test exists precisely
	// to cover the path that is not Flush.
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, err := repo.List(t.Context(), wsID, n+5)
		if err != nil {
			t.Fatalf("List(): %v", err)
		}
		if len(rows) == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out: List() = %d rows, want %d", len(rows), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRecorder_Flush_safeConcurrentWithRunAndRecord drives Record, Run and
// Flush from multiple goroutines at once so -race has real concurrent
// writer traffic to check, and proves no event is lost: final row count
// equals the number of successful (non-dropped) Records.
func TestRecorder_Flush_safeConcurrentWithRunAndRecord(t *testing.T) {
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{Queue: 2000, Batch: 32, FlushEvery: 20 * time.Millisecond})
	repo := traffic.NewRepo(db)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		rec.Run(ctx)
		close(runDone)
	}()

	const perGoroutine = 100
	const writers = 5
	var wg sync.WaitGroup
	for g := range writers {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range perGoroutine {
				rec.Record(testEvent(wsID, "/g"+strconv.Itoa(g)+"/"+strconv.Itoa(i)))
				if i%10 == 0 {
					_ = rec.Flush(t.Context())
				}
			}
		}(g)
	}
	wg.Wait()

	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("final Flush(): %v", err)
	}
	cancel()
	<-runDone

	rows, err := repo.List(t.Context(), wsID, writers*perGoroutine+10)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	want := writers*perGoroutine - int(rec.Dropped())
	if len(rows) != want {
		t.Fatalf("row count = %d, want %d (%d written - %d dropped)", len(rows), want, writers*perGoroutine, rec.Dropped())
	}
}

// TestRecorder_HeadersRedactedBeforeStorage is a light end-to-end check that
// Record's own header redaction (not just RedactHeaders in isolation) is
// wired up: an Authorization header must never reach the stored row.
func TestRecorder_HeadersRedactedBeforeStorage(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{})
	repo := traffic.NewRepo(db)

	ev := testEvent(wsID, "/whoami")
	ev.ReqHeaders = http.Header{"Authorization": {"Bearer secret"}, "Accept": {"application/json"}}
	rec.Record(ev)
	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	rows, err := repo.List(t.Context(), wsID, 10)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	got := rows[0]
	if got.ReqHeaders["Authorization"] != traffic.RedactedValue {
		t.Errorf("stored Authorization = %q, want %q", got.ReqHeaders["Authorization"], traffic.RedactedValue)
	}
	if got.ReqHeaders["Accept"] != "application/json" {
		t.Errorf("stored Accept = %q, want survived", got.ReqHeaders["Accept"])
	}
}

// TestRecorder_WriteBatch_isolatesFailedRowFromWorkspaceDeletion reproduces
// Gate 1 finding 1: a workspace deleted (cascading via the traffic FK) while
// its events are still sitting in rec.queue must fail ONLY its own rows when
// the batch is written — not roll back the whole transaction and take down
// every other row in the same flush, including rows for an unrelated,
// still-live workspace.
func TestRecorder_WriteBatch_isolatesFailedRowFromWorkspaceDeletion(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	doomed := insertWorkspace(t, db, "doomed")
	survivor := insertWorkspace(t, db, "survivor")
	rec := traffic.NewRecorder(db, nil, traffic.Options{})
	repo := traffic.NewRepo(db)

	// Queue events for both workspaces BEFORE either is written, so
	// writeBatch's single transaction has to insert doomed's rows alongside
	// survivor's rows in the same pass — the "one bad row in a shared flush"
	// shape the finding describes, with survivor's rows on both sides.
	rec.Record(testEvent(doomed, "/before-delete"))
	for i := range 5 {
		rec.Record(testEvent(survivor, "/keep/"+strconv.Itoa(i)))
	}
	rec.Record(testEvent(doomed, "/also-before-delete"))

	// Delete the workspace out from under the still-queued events, exactly
	// as DELETE /api/workspaces/{id} -> workspaces.Repo.Delete does. The
	// CASCADE only removes traffic rows already committed for it; these two
	// are still unwritten, sitting in rec.queue, and will hit the FK on
	// INSERT once Flush runs them.
	if _, err := db.W.ExecContext(t.Context(), "DELETE FROM workspaces WHERE id = ?", doomed); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	rows, err := repo.List(t.Context(), survivor, 100)
	if err != nil {
		t.Fatalf("List(survivor): %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("survivor rows = %d, want 5 — the doomed workspace's FK failure must not roll back survivor's inserts in the same transaction", len(rows))
	}

	if got := rec.Dropped(); got != 2 {
		t.Errorf("Dropped() = %d, want 2 (the two rows for the deleted workspace)", got)
	}

	// The two lost rows must surface as a dropped: note on the next
	// successfully stored row, via the same counters Record's queue-full
	// path already feeds — not vanish without a trace.
	rec.Record(testEvent(survivor, "/after"))
	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush() 2: %v", err)
	}
	rows2, err := repo.List(t.Context(), survivor, 1)
	if err != nil {
		t.Fatalf("List(survivor) 2: %v", err)
	}
	if len(rows2) == 0 {
		t.Fatal("List(survivor) 2: no rows")
	}
	if got := rows2[0].DroppedBefore(); got != 2 {
		t.Errorf("DroppedBefore() = %d, want 2", got)
	}
}

// recordingNotifier is a [traffic.Notifier] that keeps every call it gets,
// under a lock because the recorder calls it from whichever goroutine ran
// the flush.
type recordingNotifier struct {
	mu    sync.Mutex
	calls [][]int64
}

func (n *recordingNotifier) Notify(ids []int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	cp := append([]int64(nil), ids...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	n.calls = append(n.calls, cp)
}

func (n *recordingNotifier) snapshot() [][]int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([][]int64(nil), n.calls...)
}

// TestRecorder_notifiesTouchedWorkspacesAfterCommit is P6a D5's seam: the
// wired Notifier hears the DISTINCT workspace ids of a batch exactly once
// per batch, only after the batch committed (a Record alone tells it
// nothing), and the rows it announces are already readable through
// Repo.Since when it is told — which is what lets a subscriber wake, read,
// and see them.
func TestRecorder_notifiesTouchedWorkspacesAfterCommit(t *testing.T) {
	db := newTestDB(t)
	wsA := insertWorkspace(t, db, "notify-a")
	wsB := insertWorkspace(t, db, "notify-b")
	rec := traffic.NewRecorder(db, nil, traffic.Options{})
	repo := traffic.NewRepo(db)

	n := &recordingNotifier{}
	rec.SetNotifier(n)

	rec.Record(testEvent(wsA, "/one"))
	rec.Record(testEvent(wsA, "/two"))
	rec.Record(testEvent(wsB, "/three"))
	if got := n.snapshot(); len(got) != 0 {
		t.Fatalf("Notify called %d time(s) before any flush, want 0 — the nudge follows the ids, never precedes them", len(got))
	}

	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	got := n.snapshot()
	if len(got) != 1 {
		t.Fatalf("Notify called %d time(s) after one flush of three events, want exactly 1 (one batch)", len(got))
	}
	want := []int64{wsA, wsB}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("Notify ids = %v, want the distinct touched set %v", got[0], want)
	}
	rows, err := repo.Since(t.Context(), wsA, 0, 10)
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows readable for workspace A when the notifier was told = %d, want 2", len(rows))
	}

	// A flush with nothing queued announces nothing.
	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("second flush: %v", err)
	}
	if got := n.snapshot(); len(got) != 1 {
		t.Fatalf("Notify called %d time(s) after an empty flush, want still 1", len(got))
	}

	// Unwiring stops the calls; a nil Notifier is not a panic.
	rec.SetNotifier(nil)
	rec.Record(testEvent(wsB, "/four"))
	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("third flush: %v", err)
	}
	if got := n.snapshot(); len(got) != 1 {
		t.Fatalf("Notify called %d time(s) after SetNotifier(nil), want still 1", len(got))
	}
}

// TestRecorder_HeadersOverTheBodyCapAreDroppedAndNoted pins the 2026-09-03
// audit finding: headers and path were stored with no cap of their own
// while only bodies were cut, so an anonymous client could put a megabyte
// of headers into every row. An oversized header set is dropped whole
// (cut JSON would be unparsable) and the row says so.
func TestRecorder_HeadersOverTheBodyCapAreDroppedAndNoted(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{MaxBody: 256})
	repo := traffic.NewRepo(db)

	ev := testEvent(wsID, "/whoami")
	ev.ReqHeaders = http.Header{"X-Big": {strings.Repeat("h", 1024)}, "Accept": {"application/json"}}
	rec.Record(ev)
	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	rows, err := repo.List(t.Context(), wsID, 10)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	got := rows[0]
	if len(got.ReqHeaders) != 0 {
		t.Errorf("stored headers = %v, want none (dropped whole)", got.ReqHeaders)
	}
	if !got.Truncated || !strings.Contains(got.Notes, traffic.NoteTruncatedHdr) {
		t.Errorf("truncated = %v, notes = %q; want truncated with %q", got.Truncated, got.Notes, traffic.NoteTruncatedHdr)
	}
}
