package livestate

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultTTL is how long a workspace's session state survives without a
// fresh Set before Sweep is entitled to drop it.
const DefaultTTL = time.Hour

// MaxDirectivesPerWorkspace bounds the number of distinct (Target, Action)
// directives a single workspace may hold. See ErrTooManyDirectives for why
// this exists at all: POST {prefix}/state is unauthenticated by design.
const MaxDirectivesPerWorkspace = 64

// MaxPausedPerWorkspace bounds how many requests one workspace may hold
// parked on a pause directive at the same time. Past it Apply reports
// [Effect.Refused] and the request is served normally.
//
// A duration cap ([MaxPauseHold]) bounds ONE request; only a count bounds
// the plane, which is unauthenticated by design (DESIGN §15: "мок открыт")
// and by DESIGN §18 deliberately carries no rate limit. Without this, one
// `{"action":"pause","target":"*"}` plus a load generator pins a goroutine
// and a connection per request for the whole hold, which is a denial of
// service with a URL. 32 is far more than any test harness parks on purpose
// and small enough that the worst case is a few dozen held connections.
const MaxPausedPerWorkspace = 32

// MaxPauseHold caps how long ONE request may stay parked on a pause, measured
// from its FIRST park and never reset by a spurious wakeup.
//
// Exported because this package deliberately does not wait: the serving path
// owns the wait loop (it is the only layer that can see the request context),
// so it is the layer that has to enforce the cap, and it cannot read an
// unexported constant. When the cap expires the request is served NORMALLY —
// no 504. Pause is a testing aid, and inventing a status the operator never
// asked for is worse than letting a request through late.
const MaxPauseHold = 10 * time.Second

// Store holds every workspace's live session directives in RAM. It is safe
// for concurrent use: Apply runs on every request of every workspace,
// concurrently, and mutates (it decrements a fail counter and drops it at
// zero), so the locking here is the entire point of the package.
//
// Two-level locking: a top-level RWMutex guards which workspaces exist at
// all, and each workspace gets its own *sync.Mutex guarding just that
// workspace's directives. Apply's overwhelmingly common case — a workspace
// that has never had a directive set — costs one RLock, one map read, and
// an RUnlock: no per-workspace lock is even reached. A workspace that DOES
// have directives pays a per-workspace lock, which serializes concurrent
// Applies against EACH OTHER (required for the atomic decrement) without
// serializing against Applies on any other workspace (the top-level lock is
// only ever held for the read that finds the pointer).
//
// The Store starts NO goroutine and blocks nobody, pause included: it hands
// out a wake channel and the serving path does the waiting (see Effect).
type Store struct {
	ttl time.Duration
	now func() time.Time

	// closed is set by Close and read by Apply and by a park's re-check.
	// atomic rather than a field under mu because Apply reads it on every
	// request while Close writes it exactly once at shutdown, and because
	// Close must be able to set it BEFORE taking any per-workspace lock: a
	// request that slips past the check and parks anyway is then woken by
	// the very close that follows, re-checks, sees the flag and leaves.
	closed atomic.Bool

	mu         sync.RWMutex
	workspaces map[int64]*workspaceState
}

// workspaceState is one workspace's directives plus the mutex that makes
// reading and mutating them safe. Reachable only through Store.workspaces,
// so the top-level lock always guards which *workspaceState a given
// workspace ID currently maps to, while this mutex guards what is inside
// the one it found.
type workspaceState struct {
	mu         sync.Mutex
	directives map[directiveKey]*directiveEntry

	// wake is this workspace's change signal, and the only broadcast
	// primitive in the package. Every mutation — Set, Clear, Sweep, Close —
	// closes it and installs a fresh one, so a parked request selecting on
	// the channel it was handed is released by a close it cannot miss (a
	// closed channel stays readable, which a condition variable's Broadcast
	// or a token send would not). It is a CHANGE signal, never "your pause
	// was lifted": the waiter re-evaluates through Effect.Recheck, because
	// otherwise setting an unrelated status directive would silently
	// un-pause every parked request forever.
	wake chan struct{}

	// parked counts the park slots currently reserved for this workspace,
	// bounded by MaxPausedPerWorkspace. Apply reserves the slot itself and
	// hands back Unpark/Recheck to release it — a plane that had to reserve
	// its own would be one `return` away from leaking one.
	parked int
}

// broadcastLocked releases every request parked on this workspace and
// installs the channel the next park will use. Called with ws.mu held, by
// every mutation of this workspace's state.
func (ws *workspaceState) broadcastLocked() {
	close(ws.wake)
	ws.wake = make(chan struct{})
}

// directiveKey is a Directive's identity for storage: Set replaces the
// directive with the same (Target, Action) rather than appending a second
// one, and Target is comparable so this needs no extra plumbing to be a map
// key.
type directiveKey struct {
	target Target
	action Action
}

// directiveEntry is the mutable state behind one directiveKey. Apply
// decrements n and deletes the entry at the map level (not here) once it
// reaches zero — see consumeTarget.
type directiveEntry struct {
	status int
	ms     int // ActionDelay only
	once   bool
	n      int
	setAt  time.Time
}

// NewStore builds a Store. ttl<=0 uses DefaultTTL; now nil uses time.Now.
// now is the clock Set stamps SetAt from — inject a fake one in tests so
// Sweep's TTL math is exact and instant, never a sleep.
func NewStore(ttl time.Duration, now func() time.Time) *Store {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if now == nil {
		now = time.Now
	}
	return &Store{
		ttl:        ttl,
		now:        now,
		workspaces: make(map[int64]*workspaceState),
	}
}

// lookup returns the workspace's state without ever creating one. Used by
// every method that must not grow the map just by being asked about a
// workspace that has no live state — Apply above all.
func (s *Store) lookup(workspaceID int64) (*workspaceState, bool) {
	s.mu.RLock()
	ws, ok := s.workspaces[workspaceID]
	s.mu.RUnlock()
	return ws, ok
}

// getOrCreate returns the workspace's state, creating it under the
// top-level write lock on first use. Only Set calls this; every read-side
// method uses lookup instead, so asking about a quiet workspace never
// allocates one.
func (s *Store) getOrCreate(workspaceID int64) *workspaceState {
	if ws, ok := s.lookup(workspaceID); ok {
		return ws
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ws, ok := s.workspaces[workspaceID]; ok {
		return ws // lost the race to create it; use the winner's
	}
	ws := &workspaceState{
		directives: make(map[directiveKey]*directiveEntry),
		wake:       make(chan struct{}),
	}
	s.workspaces[workspaceID] = ws
	return ws
}

// Set stores d, replacing any existing directive with the same
// (Target, Action). SetAt is stamped from the Store's own clock and the
// caller's value is ignored — this is a server-authoritative timestamp
// (Sweep's TTL depends on it meaning "when this Store last accepted a
// change for this workspace", not whatever a client claims).
//
// A successful Set wakes every request parked on this workspace, whatever
// the directive was: the waiters re-evaluate and park again if a pause
// still matches them. Waking on an unrelated change costs one re-check;
// NOT waking would mean a directive that lifts a pause — including the
// replacement of the pause itself — is only noticed at the hold cap.
func (s *Store) Set(workspaceID int64, d Directive) error {
	d, err := normalize(d)
	if err != nil {
		return err
	}
	d.SetAt = s.now()

	ws := s.getOrCreate(workspaceID)
	key := directiveKey{target: d.Target, action: d.Action}

	ws.mu.Lock()
	defer ws.mu.Unlock()
	if _, exists := ws.directives[key]; !exists && len(ws.directives) >= MaxDirectivesPerWorkspace {
		return ErrTooManyDirectives // nothing changed, so nothing to wake
	}
	ws.directives[key] = &directiveEntry{status: d.Status, ms: d.Ms, once: d.Once, n: d.N, setAt: d.SetAt}
	ws.broadcastLocked()
	return nil
}

// List returns a snapshot of workspaceID's directives, "*" first, then
// ordered by method and path. It is a copy: mutating the returned slice, or
// racing it against a concurrent Apply with -race, is safe — the map is
// only ever touched under ws.mu, which this method also holds while it
// builds the copy.
func (s *Store) List(workspaceID int64) []Directive {
	ws, ok := s.lookup(workspaceID)
	if !ok {
		return nil
	}

	ws.mu.Lock()
	defer ws.mu.Unlock()
	out := make([]Directive, 0, len(ws.directives))
	for key, e := range ws.directives {
		out = append(out, Directive{
			Target: key.target,
			Action: key.action,
			Status: e.status,
			Ms:     e.ms,
			Once:   e.once,
			N:      e.n,
			SetAt:  e.setAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

// less orders List's snapshot: the "*" target first, then non-All targets
// by method then path, and — only to make the order total when a target
// carries both a status and a fail directive — by Action last.
func less(a, b Directive) bool {
	if a.Target.All != b.Target.All {
		return a.Target.All
	}
	if !a.Target.All {
		if a.Target.Method != b.Target.Method {
			return a.Target.Method < b.Target.Method
		}
		if a.Target.Path != b.Target.Path {
			return a.Target.Path < b.Target.Path
		}
	}
	return a.Action < b.Action
}

// Clear drops every directive of workspaceID and reports how many were
// dropped. Other workspaces are untouched — Clear only ever reaches the one
// *workspaceState the lookup finds.
//
// This is the operator's release button for a pause: it wakes every parked
// request, and their re-check then finds no directive left to hold them.
// The wake is unconditional, even for a workspace that turned out to hold
// nothing — a request parked before an earlier Clear is exactly the caller
// that must not be forgotten here.
func (s *Store) Clear(workspaceID int64) int {
	ws, ok := s.lookup(workspaceID)
	if !ok {
		return 0
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	n := len(ws.directives)
	ws.directives = make(map[directiveKey]*directiveEntry)
	ws.broadcastLocked()
	return n
}

// Apply is the serving path's one call per request: given the workspace
// already resolved and the operation about to be served, it reports what to
// do RIGHT NOW — force a status, hold for a delay, park on a pause — and
// consumes exactly one unit of any fail counter it used.
//
// RESOLUTION IS PER ACTION. A workspace may hold a status, a fail, a delay
// and a pause at once, and each is resolved on its own: within one action
// an exact (method, path) target beats "*", and across actions nothing
// short-circuits anything. "Delay 300 ms AND force 503" is a thing an
// operator asks for, and a "*"-scoped pause must not become invisible
// because some operation happens to carry an exact-target status. (Status
// and fail are the one pair that must still be ordered against each other,
// because they produce the same single field — see consumeStatus.)
//
// The common case (no directive was ever set for this workspace) is one
// RLock'd map read and a zero Effect — see the Store doc comment. A
// workspace that has live state pays one per-workspace mutex, which is also
// what makes the decrement atomic: the read of the remainder, the
// decrement, and the delete-at-zero all happen inside that single
// critical section, so 200 concurrent requests against a fail N=100
// directive force exactly 100 responses and the 101st sees nothing.
//
// After Close, Apply never reports a pause again: the process is shutting
// down and holding a request open until an operator releases it is holding
// it until the drain gives up.
// Delete (A13, 2026-09-02) removes the directives on ONE target — every
// action on it when action is "", the one action otherwise — and reports
// how many went. It is [Store.Clear] narrowed by key: the same broadcast
// wakes every parked request, and a request parked on the deleted pause
// re-checks and is served, while one parked on another target stays
// parked. A target that fails the same validation [Store.Set] applies
// answers ErrInvalidDirective; a target that holds nothing answers 0 with
// no error, because "nothing to remove" is a state, not a mistake — the
// identical rule Clear's 0 already follows.
func (s *Store) Delete(workspaceID int64, target Target, action Action) (int, error) {
	if target.All {
		target = Target{All: true}
	} else {
		if err := validateTarget(target); err != nil {
			return 0, err
		}
		target.Method = strings.ToUpper(target.Method)
	}
	if action != "" && !validAction(action) {
		return 0, fmt.Errorf("%w: unknown action %q", ErrInvalidDirective, action)
	}
	ws, ok := s.lookup(workspaceID)
	if !ok {
		return 0, nil
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	n := 0
	for key := range ws.directives {
		if key.target == target && (action == "" || key.action == action) {
			delete(ws.directives, key)
			n++
		}
	}
	if n > 0 {
		ws.broadcastLocked()
	}
	return n, nil
}

// validAction reports whether a is one of the four directive actions.
func validAction(a Action) bool {
	switch a {
	case ActionStatus, ActionFail, ActionDelay, ActionPause:
		return true
	}
	return false
}

func (s *Store) Apply(workspaceID int64, method, path string) Effect {
	ws, ok := s.lookup(workspaceID)
	if !ok {
		return Effect{}
	}
	method = strings.ToUpper(method) // Target.Method is stored upper-case; match it regardless of caller casing

	ws.mu.Lock()
	defer ws.mu.Unlock()
	if len(ws.directives) == 0 {
		return Effect{}
	}

	eff := Effect{
		Status:  consumeStatus(ws.directives, method, path),
		DelayMs: matchedDelayMs(ws.directives, method, path),
	}
	if _, _, paused := matchDirective(ws.directives, ActionPause, method, path); paused && !s.closed.Load() {
		s.reserveParkLocked(ws, workspaceID, method, path, &eff)
	}
	return eff
}

// matchDirective is the per-action precedence in one place: an exact
// (method, path) target beats "*" WITHIN this action, and the caller asks
// once per action rather than once per target, so no action can hide
// another. It returns the key as well, so a consuming caller deletes the
// entry it actually used rather than the one it guessed. Called with ws.mu
// held.
func matchDirective(directives map[directiveKey]*directiveEntry, action Action, method, path string) (directiveKey, *directiveEntry, bool) {
	exact := directiveKey{target: Target{Method: method, Path: path}, action: action}
	if e, ok := directives[exact]; ok {
		return exact, e, true
	}
	star := directiveKey{target: Target{All: true}, action: action}
	if e, ok := directives[star]; ok {
		return star, e, true
	}
	return directiveKey{}, nil, false
}

// consumeStatus resolves the one status to force, and is the only part of
// Apply that mutates: it decrements the fail counter it used and deletes
// the entry at zero.
//
// Two actions feed one Effect.Status, so those two do need an order between
// them, and fail wins while it has requests left: a fail directive is an
// explicit "burn the next N", while a status directive is standing state
// that is still there once the counter runs out. Each is matched
// independently first, so — unlike the single global first-match this
// replaced — an exact-target status no longer swallows a "*"-scoped fail.
func consumeStatus(directives map[directiveKey]*directiveEntry, method, path string) int {
	if key, e, ok := matchDirective(directives, ActionFail, method, path); ok {
		status := e.status
		e.n-- // entries are only ever stored with n>=1 (normalize enforces it), so this can't go negative unseen
		if e.n <= 0 {
			delete(directives, key)
		}
		return status
	}
	if _, e, ok := matchDirective(directives, ActionStatus, method, path); ok {
		return e.status
	}
	return 0
}

// matchedDelayMs reports the delay to pay, 0 when no delay directive
// matches. Nothing is consumed: a delay behaves like a status, not like a
// fail — it stays until it is cleared or replaced.
func matchedDelayMs(directives map[directiveKey]*directiveEntry, method, path string) int {
	if _, e, ok := matchDirective(directives, ActionDelay, method, path); ok {
		return e.ms
	}
	return 0
}

// reserveParkLocked takes a park slot for a request a pause matched, and
// fills in the four pause fields of eff. Called with ws.mu held.
//
// The slot is reserved HERE rather than by the caller, and released only
// through the Unpark this hands back, because the plane is a `return`
// statement away from leaking one: every early exit on the serving path
// would have to remember to give it back, and a leaked slot is permanent —
// nothing sweeps a counter.
func (s *Store) reserveParkLocked(ws *workspaceState, workspaceID int64, method, path string, eff *Effect) {
	if ws.parked >= MaxPausedPerWorkspace {
		eff.Refused = true // rule 7: over the bound the request is served normally
		return
	}
	ws.parked++
	p := &park{store: s, workspaceID: workspaceID, method: method, path: path, held: ws}
	eff.Pause = true
	eff.Wake = ws.wake
	eff.Recheck = p.recheck
	eff.Unpark = p.release
}

// park is one request's reservation of a pause slot: the state behind
// [Effect.Recheck] and [Effect.Unpark].
//
// It remembers WHICH *workspaceState the slot was taken on, not just the
// workspace id. Sweep can drop a workspace and a later Set build a fresh
// one under the same id, and decrementing the new one's counter for a slot
// taken on the old one would corrupt a count that has no other way back to
// the truth.
type park struct {
	store       *Store
	workspaceID int64
	method      string
	path        string

	// mu guards held and done. Recheck and Unpark are one goroutine's to
	// call, but this mutex costs nothing next to a parked request and it is
	// what makes "Unpark after Recheck already released" a documented no-op
	// rather than a second decrement.
	mu   sync.Mutex
	held *workspaceState // nil once the slot has been given back
	done bool            // release was called: this request is finished waiting
}

// release is [Effect.Unpark]: give the slot back if this park still holds
// one. Idempotent, because the handler is expected to call it from a defer
// that runs whichever way the wait ended — released, cap expired, or
// request context cancelled — after any number of rechecks.
//
// It also ends the park for good: a recheck after it reserves nothing. A
// handler that gave up on the wait (its cap expired, its client went away)
// and then took one more trip round its loop would otherwise reserve a slot
// with nothing left to give it back.
func (p *park) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done = true
	p.releaseHeld()
}

// releaseHeld gives the slot back with p.mu already held.
func (p *park) releaseHeld() {
	ws := p.held
	if ws == nil {
		return
	}
	p.held = nil
	ws.mu.Lock()
	ws.parked--
	ws.mu.Unlock()
}

// recheck is [Effect.Recheck]: re-evaluate the pause after a wakeup without
// consuming anything, and report the channel to park on next.
func (p *park) recheck() (bool, <-chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return false, nil
	}

	// Resolved through the Store, never through the captured
	// *workspaceState: Sweep may have dropped the one this request parked
	// on, and re-checking a detached map would hold the request on a pause
	// directive no Set, Clear or Close can reach any more.
	ws, ok := p.store.lookup(p.workspaceID)
	if ok && ws == p.held {
		return p.recheckHeld(ws)
	}

	p.releaseHeld() // swept, or replaced by a fresh state for the same id
	if !ok {
		return false, nil
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	// Read AFTER taking ws.mu, not before: Close sets the flag and then
	// takes this same lock per workspace to broadcast (see Close's doc
	// comment). Reading it before the lock would let this goroutine observe
	// "not closed", then have Close run to completion — flag set, channel
	// replaced — before this goroutine parks on the NEW channel, which
	// nothing will ever close again. Under the lock, Close's broadcast for
	// this workspace either already happened (so the flag is visibly true
	// here) or cannot happen until this critical section releases the lock.
	if p.store.closed.Load() {
		return false, nil
	}
	if _, _, paused := matchDirective(ws.directives, ActionPause, p.method, p.path); !paused {
		return false, nil
	}
	if ws.parked >= MaxPausedPerWorkspace {
		return false, nil // rule 7 again: refused means served, not held longer
	}
	ws.parked++
	p.held = ws
	return true, ws.wake
}

// recheckHeld is the ordinary case: the request is re-checking the very
// *workspaceState it parked on. Releasing and re-reserving therefore happen
// in ONE critical section — which is to say the slot is simply kept — so a
// re-parking request never holds two and never has to win its own slot back.
// Holding two transiently would let 32 requests re-parking after a single
// unrelated Set hit the bound and come back refused, silently un-pausing
// every one of them.
func (p *park) recheckHeld(ws *workspaceState) (bool, <-chan struct{}) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	_, _, paused := matchDirective(ws.directives, ActionPause, p.method, p.path)
	if !paused || p.store.closed.Load() {
		ws.parked--
		p.held = nil
		return false, nil
	}
	return true, ws.wake
}

// Sweep drops every workspace whose newest directive is older than the
// Store's TTL relative to now, and reports how many workspaces it dropped.
// A workspace with no directives at all (e.g. right after Clear) has no
// "newest" and is always eligible — there is nothing left in it worth
// keeping around.
//
// now is an explicit argument, not s.now(): Sweep is meant to be driven by
// a caller's own ticker using the real wall clock, while s.now is the
// (possibly fake) clock Set stamps directives with. Tests hand both the
// same fake clock so TTL math is exact without a sleep.
func (s *Store) Sweep(now time.Time) int {
	cutoff := now.Add(-s.ttl)

	s.mu.Lock()
	defer s.mu.Unlock()

	dropped := 0
	for id, ws := range s.workspaces {
		ws.mu.Lock()
		var newest time.Time // zero value: no directives ever sets this, and zero is always before any real cutoff
		for _, e := range ws.directives {
			if e.setAt.After(newest) {
				newest = e.setAt
			}
		}
		stale := newest.Before(cutoff)
		if stale {
			// Being swept is a change like any other, and it is the one
			// change a parked request could not otherwise survive: after
			// the delete, no Set or Clear can reach this *workspaceState
			// again, so an unwoken request would sit here until its own
			// hold cap expired.
			ws.broadcastLocked()
		}
		ws.mu.Unlock()

		if stale {
			delete(s.workspaces, id)
			dropped++
		}
	}
	return dropped
}

// Close releases every request parked anywhere in this Store and makes
// Apply and Recheck report Pause=false from here on. Safe to call more than
// once, and it stops no goroutine because this package starts none.
//
// BOTH halves are load-bearing, and the flag is the one that is easy to
// leave out: without it a woken request re-checks, finds its pause
// directive still in place, and parks again on the fresh channel — Close
// would release nobody. Nothing in the test suite or in smoke would say so
// either, because cmd/mocker's shutdown drain (15s) outlasts MaxPauseHold
// (10s): the process still exits, just after holding every parked request
// for the full cap.
//
// Call it BEFORE the graceful drain begins. A parked request keeps its
// connection active, and http.Server.Shutdown is waiting for exactly that.
func (s *Store) Close() {
	// The flag first, then the channels. A request that read the flag as
	// false and is about to park holds ws.mu while it does, so it parks on
	// a channel this loop has not closed yet, wakes, re-checks, and sees
	// the flag — whereas closing first would leave it parked on a freshly
	// installed channel nobody is ever going to close again.
	s.closed.Store(true)

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ws := range s.workspaces {
		ws.mu.Lock()
		ws.broadcastLocked()
		ws.mu.Unlock()
	}
}
