package stream

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Registry is the process-wide connection registry (D4): it admits, tracks
// and closes every live SSE connection this process holds, across every
// workspace. There is exactly one Registry per process — D8's cap is a
// process-wide number, not a per-workspace one, and a second Registry would
// make that cap a fiction.
type Registry struct {
	mu     sync.Mutex
	closed bool
	conns  map[*Conn]struct{}

	// perWorkspace, when true, makes cap a PER-WORKSPACE number counted
	// over conns of the same workspaceID (P6b D9, DESIGN §30.11's
	// MOCKER_STREAM_MAX_CONNS: "200 per workspace"); when false the
	// registry is the admin feed's, capped process-wide at maxStreamConns.
	// Two registries, two instances: an unauthenticated plane must not be
	// able to exhaust the authenticated one's feed by sharing a counter.
	perWorkspace bool
	cap          int
	// nextID is P6c's connection counter (D2): the id the next Open hands
	// out, advanced under mu, starting at 1 per registry and never reused
	// while the process runs.
	nextID int64
	// wg is Done by a Conn only once its own Serve call has returned — the
	// "wait for their handlers to return" step of D13's three-step Close(),
	// never merely "signalled to close".
	wg sync.WaitGroup

	refusedCap         atomic.Int64
	refusedUnsupported atomic.Int64
	// coalescedNudges is D5's own counter: incremented every time a Notify
	// finds a connection's one-slot channel already full and drops the send
	// — "drop-and-count", never a lost row, because the read the full slot
	// already promises returns everything the dropped nudge would have
	// announced.
	coalescedNudges atomic.Int64
}

// NewRegistry builds an empty, open Registry with the admin feed's
// process-wide cap (maxStreamConns).
func NewRegistry() *Registry {
	return &Registry{conns: make(map[*Conn]struct{}), cap: maxStreamConns}
}

// NewWorkspaceRegistry builds a Registry whose cap is counted PER WORKSPACE
// — the mock plane's (P6b D9). cap 0 is not "unlimited": §30.11 says a
// zero MOCKER_STREAM_MAX_CONNS refuses streaming outright, so every Open
// on such a registry answers ErrCapExceeded.
func NewWorkspaceRegistry(perWorkspaceCap int) *Registry {
	if perWorkspaceCap < 0 {
		perWorkspaceCap = 0
	}
	return &Registry{conns: make(map[*Conn]struct{}), cap: perWorkspaceCap, perWorkspace: true}
}

// Open admits a new connection scoped to workspaceID, or refuses one.
//
// It does no I/O and touches no http.ResponseWriter: D8's cap and D13's
// closed flag are both decided with nothing but the registry's own state, and
// keeping this call I/O-free is what lets a caller check the cap BEFORE it
// has committed to anything on the response — the response-support check of
// D9 needs a live ResponseWriter and runs separately, in [Conn.Serve].
//
// ctx is the connection's own lifetime source: Open derives a child context
// from it, and [Registry.Close] cancels every such child on shutdown (D13's
// second step) without needing to reach back into the caller's own request
// context.
func (rg *Registry) Open(ctx context.Context, workspaceID int64) (*Conn, error) {
	rg.mu.Lock()
	if rg.closed {
		rg.mu.Unlock()
		return nil, ErrClosed
	}
	if rg.countLocked(workspaceID) >= rg.cap {
		rg.mu.Unlock()
		rg.refusedCap.Add(1)
		return nil, ErrCapExceeded
	}
	cctx, cancel := context.WithCancel(ctx)
	rg.nextID++
	c := &Conn{
		registry:    rg,
		workspaceID: workspaceID,
		ctx:         cctx,
		cancel:      cancel,
		// Capacity one: the whole of D5's "wakeup is a ONE-SLOT signal".
		nudge:    make(chan struct{}, 1),
		id:       rg.nextID,
		openedAt: time.Now(),
	}
	if rg.perWorkspace {
		// P6c D3: only the mock plane's connections take pushes — their
		// loop drains the inbox. The admin feed's Serve has no such case,
		// so a Push into one of its connections would park forever; a nil
		// inbox makes Push answer ErrConnClosed instead.
		c.inbox = make(chan PushRequest, inboxDepth)
	}
	rg.conns[c] = struct{}{}
	rg.wg.Add(1)
	rg.mu.Unlock()
	return c, nil
}

// countLocked is what the cap is compared against: every live connection
// for the admin registry, this workspace's alone for a per-workspace one.
// Called with rg.mu held.
func (rg *Registry) countLocked(workspaceID int64) int {
	if !rg.perWorkspace {
		return len(rg.conns)
	}
	n := 0
	for c := range rg.conns {
		if c.workspaceID == workspaceID {
			n++
		}
	}
	return n
}

// deregister removes c from the registry and releases the wg slot Open
// added. It runs from Conn.Serve's own defer, so it fires exactly once, on
// every return path — the request context being cancelled, the lifetime
// expiring, a recheck failure, a write failure, or the D9 refusal before a
// single frame was written.
//
// The nudge channel is deliberately left to be garbage collected, never
// closed: closing it here would race a Notify already in flight — Notify
// reads rg.conns and sends into c.nudge without holding a lock across the
// send — and a send on a closed channel panics the recorder's own goroutine,
// not this one. An unclosed, unreferenced channel is inert.
func (rg *Registry) deregister(c *Conn) {
	rg.mu.Lock()
	_, live := rg.conns[c]
	delete(rg.conns, c)
	rg.mu.Unlock()
	if !live {
		// Already deregistered: the wg slot was released then. A second
		// call is a no-op rather than a negative WaitGroup counter — the
		// contract stays "exactly once", this only makes a violation
		// harmless instead of a panic in a handler holding a connection.
		return
	}
	// P6c D4: a pusher waiting on this connection's context must not
	// outlive the loop that would have served it. The request context
	// cancels this child anyway once the handler returns; cancelling here
	// makes that true at the moment the connection stops being listed,
	// and a second cancel is a no-op.
	c.cancel()
	rg.wg.Done()
}

// Snapshot is one row of [Registry.Snapshot] (P6c D2, D8): the connection
// as a listing shows it. JSON tags because internal/admin marshals the row
// as-is, the way it already marshals [Stats].
type Snapshot struct {
	ID         int64     `json:"id"`
	EndpointID int64     `json:"endpointId"`
	Path       string    `json:"path"`
	Kind       string    `json:"kind"`
	RemoteAddr string    `json:"remoteAddr"`
	OpenedAt   time.Time `json:"openedAt"`
	Frames     int64     `json:"frames"`
	Pushed     int64     `json:"pushed"`
	Skipped    int64     `json:"skipped"`
	// FramesIn is P6d's inbound count; 0 on every SSE connection.
	FramesIn int64 `json:"framesIn"`
}

// Snapshot lists workspaceID's live connections, sorted by id (P6c D2). A
// live read under the lock, like Stats: a connection that deregistered a
// microsecond ago is not in it, and the counters are whatever the loop had
// advanced them to at the instant of the read.
func (rg *Registry) Snapshot(workspaceID int64) []Snapshot {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	rows := make([]Snapshot, 0)
	for c := range rg.conns {
		if c.workspaceID != workspaceID || c.closing() || c.info.Kind == "" {
			// An unlabelled connection is one between Open and SetInfo —
			// microseconds on the mock plane, forever on the admin feed
			// (which never labels): neither is listed (diff-review
			// finding 3 of P6d).
			continue
		}
		rows = append(rows, Snapshot{
			ID:         c.id,
			EndpointID: c.info.EndpointID,
			Path:       c.info.Path,
			Kind:       c.info.Kind,
			RemoteAddr: c.info.Peer,
			OpenedAt:   c.openedAt,
			Frames:     c.frames.Load(),
			Pushed:     c.pushed.Load(),
			Skipped:    c.skipped.Load(),
			FramesIn:   c.framesIn.Load(),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

// Cap is the registry's own ceiling — per workspace for the mock plane's,
// process-wide for the admin feed's — the same number Stats reports, for a
// listing that shows the ceiling beside who holds it (P6c D8).
func (rg *Registry) Cap() int { return rg.cap }

// CloseByAdmin is D5's close as ONE registry operation: under the lock it
// finds workspaceID's live connection with the given id, and only while
// the connection is still registered and not already closing does it flip
// the flag and cancel — so a connection that deregistered between a
// caller's Lookup and its close cannot be "closed" a second time and
// answer 204 for a connection that ended on its own (diff-review finding
// 1). false means there was nothing live to close: never issued, another
// workspace's, already closing, or already deregistered — one 404 for all.
//
// The residual it does not remove: a loop that has already passed its own
// ClosedByAdmin read on the way out but not yet deregistered. That window
// is the return path of one handler, and the row it writes says the
// connection ended on its own, which is what happened first.
func (rg *Registry) CloseByAdmin(workspaceID, id int64) bool {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	for c := range rg.conns {
		if c.id == id && c.workspaceID == workspaceID && !c.closing() {
			return c.CloseByAdmin()
		}
	}
	return false
}

// Lookup finds workspaceID's live connection with the given id, or nil
// when there is none — an id that was never issued, one whose connection
// already deregistered, or one that belongs to ANOTHER workspace: the
// caller cannot tell the three apart and answers one 404 for all of them
// (P6c D2, D8), the shape resourceByFamily's callers already keep.
func (rg *Registry) Lookup(workspaceID, id int64) *Conn {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	for c := range rg.conns {
		if c.id == id && c.workspaceID == workspaceID && !c.closing() && c.info.Kind != "" {
			// Kind != "": a connection between Open and SetInfo is not yet
			// addressable — a push that found it would see no kind and
			// could accept an event a WebSocket loop then drops (P6d
			// diff-review finding 3).
			return c
		}
	}
	return nil
}

// Notify wakes every live connection scoped to one of workspaceIDs (D5): a
// non-blocking send into that connection's one-slot channel, dropped and
// counted (coalescedNudges) when the slot is already full. It never blocks
// the caller — internal/traffic's recorder calls this right after a batch
// commits, outside its own write lock, and a Notify that could block would
// turn a reader's slow drain into a stall on the write path Notify is not
// allowed to touch.
func (rg *Registry) Notify(workspaceIDs []int64) {
	if len(workspaceIDs) == 0 {
		return
	}
	want := make(map[int64]struct{}, len(workspaceIDs))
	for _, id := range workspaceIDs {
		want[id] = struct{}{}
	}

	rg.mu.Lock()
	// Copied out under the lock, then sent to outside it: a nudge send
	// itself never blocks (the channel is buffered and drained-not-closed),
	// but holding the registry's own mutex across every connection's send
	// would serialize Notify against Open/Close/deregister for no reason —
	// the copy is what lets those run concurrently with the fan-out below.
	var targets []*Conn
	for c := range rg.conns {
		if _, ok := want[c.workspaceID]; ok {
			targets = append(targets, c)
		}
	}
	rg.mu.Unlock()

	for _, c := range targets {
		select {
		case c.nudge <- struct{}{}:
		default:
			rg.coalescedNudges.Add(1)
		}
	}
}

// Close runs D13's three-step shutdown shape, in order: (1) set the closed
// flag, under the lock, so a handshake racing shutdown sees ErrClosed rather
// than registering into a registry that will not close it again; (2) cancel
// every live connection's context, outside the lock — cancellation must not
// itself wait on anything the lock would serialize against deregister; (3)
// wait for every one of their Serve calls to actually return.
//
// Idempotent: a second Close on an already-closed registry is a no-op, not a
// second cancel-and-wait over an already-empty conns map.
func (rg *Registry) Close() {
	rg.mu.Lock()
	if rg.closed {
		rg.mu.Unlock()
		return
	}
	rg.closed = true
	targets := make([]*Conn, 0, len(rg.conns))
	for c := range rg.conns {
		targets = append(targets, c)
	}
	rg.mu.Unlock()

	for _, c := range targets {
		c.cancel()
	}
	rg.wg.Wait()
}

// WorkspaceStats is one row of [Stats.ByWorkspace] — a workspace with at
// least one live connection (D15's open question: the array carries no cap
// of its own, because D8's process-wide cap already bounds it to at most 64
// entries).
type WorkspaceStats struct {
	WorkspaceID int64 `json:"workspaceId"`
	Open        int   `json:"open"`
}

// Stats is D15's own shape for GET /api/stream/stats, built here because
// every counter it reports is this package's state; internal/admin's handler
// only marshals it.
type Stats struct {
	Open               int              `json:"open"`
	Cap                int              `json:"cap"`
	RefusedCap         int64            `json:"refusedCap"`
	RefusedUnsupported int64            `json:"refusedUnsupported"`
	CoalescedNudges    int64            `json:"coalescedNudges"`
	ByWorkspace        []WorkspaceStats `json:"byWorkspace"`
}

// Stats reports the registry's current health. It is a live read, not a
// snapshot taken at some past instant: Open and ByWorkspace are recomputed
// from rg.conns under the lock every call, which is what lets A13's three
// reads see Open rise on a handshake and fall again on the matching close.
func (rg *Registry) Stats() Stats {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	byWorkspace := make(map[int64]int)
	for c := range rg.conns {
		byWorkspace[c.workspaceID]++
	}
	rows := make([]WorkspaceStats, 0, len(byWorkspace))
	for id, n := range byWorkspace {
		rows = append(rows, WorkspaceStats{WorkspaceID: id, Open: n})
	}
	// Map order is randomized per run; two reads of the same state must
	// marshal to the same bytes, or a client diffing them sees churn.
	sort.Slice(rows, func(i, j int) bool { return rows[i].WorkspaceID < rows[j].WorkspaceID })

	return Stats{
		Open:               len(rg.conns),
		Cap:                rg.cap,
		RefusedCap:         rg.refusedCap.Load(),
		RefusedUnsupported: rg.refusedUnsupported.Load(),
		CoalescedNudges:    rg.coalescedNudges.Load(),
		ByWorkspace:        rows,
	}
}
