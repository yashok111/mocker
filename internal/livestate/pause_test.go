package livestate

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// pause_test.go covers the release primitive: the per-workspace wake
// channel, the park accounting behind Effect.Pause/Refused/Unpark, and
// Close. Nothing here sleeps to "give the store a moment" — every
// assertion either waits on a channel the Store itself closed or reads a
// count under the Store's own lock, because a test that waits a bit passes
// just as well against a Store that never signalled at all.
//
// Every goroutine these tests start is joined before the test returns:
// goleak runs in this package, and a parked request outliving its test is
// precisely the defect it exists to catch.

// waitForRelease bounds how long a test waits for a wakeup that should be
// immediate. Long enough that a loaded CI box never trips it, short enough
// that a broken release fails the test instead of the package timeout.
const waitForRelease = 5 * time.Second

// waiter is the wait loop the mock plane owns, reduced to what this package
// can test on its own: park on the channel Apply handed out, re-evaluate
// through Recheck (never Apply) on every wakeup, park again on the channel
// Recheck returns, and stop when the pause is gone.
type waiter struct {
	released chan struct{} // closed when the loop returns for good
	rechecks chan bool     // one value per wakeup: what Recheck reported
}

// parkWaiter starts one request parked on a pause, and fails the test if
// Apply did not actually park it.
func parkWaiter(t *testing.T, s *Store, workspaceID int64, method, path string) *waiter {
	t.Helper()

	eff := s.Apply(workspaceID, method, path)
	if !eff.Pause {
		t.Fatalf("Apply(%d, %s, %s) = %+v, want Pause", workspaceID, method, path, eff)
	}
	if eff.Wake == nil || eff.Recheck == nil || eff.Unpark == nil {
		t.Fatalf("Apply(%d, %s, %s) = %+v, want Wake, Recheck and Unpark wired", workspaceID, method, path, eff)
	}

	w := &waiter{
		released: make(chan struct{}),
		// Buffered so the loop never blocks on a test that only reads some
		// of its wakeups — a blocked loop would be a leaked goroutine, and
		// goleak would report it as this test's fault rather than the
		// Store's.
		rechecks: make(chan bool, 8),
	}
	go func() {
		defer close(w.released)
		defer eff.Unpark() // idempotent: Recheck may already have released the slot

		wake := eff.Wake
		for {
			<-wake
			stillPaused, next := eff.Recheck()
			select {
			case w.rechecks <- stillPaused:
			default: // the test is not reading; the loop must not stall on it
			}
			if !stillPaused {
				return
			}
			wake = next
		}
	}()
	return w
}

// awaitRelease waits for the parked request to finish waiting.
func (w *waiter) awaitRelease(t *testing.T) {
	t.Helper()
	select {
	case <-w.released:
	case <-time.After(waitForRelease):
		t.Fatalf("parked request was never released")
	}
}

// awaitRecheck waits for one wakeup and reports what Recheck said about it.
func (w *waiter) awaitRecheck(t *testing.T) bool {
	t.Helper()
	select {
	case stillPaused := <-w.rechecks:
		return stillPaused
	case <-time.After(waitForRelease):
		t.Fatalf("parked request was never woken")
		return false
	}
}

// assertStillParked is only ever called after a wakeup has been observed,
// so it is a statement about a settled state rather than a race with one.
func (w *waiter) assertStillParked(t *testing.T) {
	t.Helper()
	select {
	case <-w.released:
		t.Fatalf("parked request was released, want it still parked")
	default:
	}
}

// parkedCount reads a workspace's reserved-slot count. Reaching inside the
// Store on purpose: the count is what rule "never holds two slots" is
// about, and no exported call reports it.
func parkedCount(s *Store, workspaceID int64) int {
	ws, ok := s.lookup(workspaceID)
	if !ok {
		return 0
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.parked
}

// TestStore_Pause_UnparkIsIdempotent: the plane is expected to call Unpark
// from a defer that runs however the wait ended, including after a Recheck
// that already gave the slot back. A second decrement there would free a
// slot nobody holds, and the bound would drift up by one per request until
// it stopped bounding anything.
func TestStore_Pause_UnparkIsIdempotent(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionPause})

	eff := s.Apply(1, "GET", "/x")
	if !eff.Pause {
		t.Fatalf("Apply = %+v, want Pause", eff)
	}
	if got := parkedCount(s, 1); got != 1 {
		t.Fatalf("parked = %d after Apply, want 1", got)
	}

	eff.Unpark()
	eff.Unpark()
	eff.Unpark()
	if got := parkedCount(s, 1); got != 0 {
		t.Fatalf("parked = %d after three Unparks, want 0", got)
	}

	// And Unpark ends the wait for good: a handler that gave up (its hold
	// cap expired, its client went away) and then took one more trip round
	// its loop must not reserve a slot nothing will ever give back.
	if stillPaused, wake := eff.Recheck(); stillPaused || wake != nil {
		t.Fatalf("Recheck after Unpark = (%v, %v), want (false, nil)", stillPaused, wake)
	}
	if got := parkedCount(s, 1); got != 0 {
		t.Fatalf("parked = %d after a Recheck past Unpark, want 0", got)
	}
}

// TestStore_Pause_WokenByClear is the operator's release button: DELETE on
// either plane's session endpoint reaches Clear, and every parked request
// must come back.
func TestStore_Pause_WokenByClear(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{Method: "GET", Path: "/widgets"}, Action: ActionPause})
	w := parkWaiter(t, s, 1, "GET", "/widgets")

	if cleared := s.Clear(1); cleared != 1 {
		t.Fatalf("Clear = %d, want 1", cleared)
	}
	w.awaitRelease(t)
	if got := parkedCount(s, 1); got != 0 {
		t.Fatalf("parked = %d after the release, want 0", got)
	}
}

// TestStore_Pause_WokenByReplacingSet: re-setting the very directive a
// request is parked on wakes it, and its re-check finds a pause still in
// force, so it parks again — on the NEW channel. The slot count is the
// assertion that matters: releasing and re-reserving must net to the one
// slot it already held, never two.
func TestStore_Pause_WokenByReplacingSet(t *testing.T) {
	t.Parallel()

	pause := Directive{Target: Target{Method: "GET", Path: "/widgets"}, Action: ActionPause}
	s := NewStore(0, nil)
	mustSet(t, s, 1, pause)
	w := parkWaiter(t, s, 1, "GET", "/widgets")

	mustSet(t, s, 1, pause) // same (target, action): a replacement, not a second directive

	if !w.awaitRecheck(t) {
		t.Fatalf("Recheck reported the pause gone, want it still in force after a replacing Set")
	}
	w.assertStillParked(t)
	if got := parkedCount(s, 1); got != 1 {
		t.Fatalf("parked = %d after re-parking, want exactly 1 (never two slots for one request)", got)
	}

	s.Clear(1)
	w.awaitRelease(t)
}

// TestStore_Pause_WokenBySweep: the janitor dropping an abandoned workspace
// is the one change a parked request could not survive by itself — after
// the delete, no Set and no Clear can reach that state again, so an unwoken
// request would sit there until its own hold cap ran out.
func TestStore_Pause_WokenBySweep(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := NewStore(time.Minute, func() time.Time { return t0 })
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionPause})
	w := parkWaiter(t, s, 1, "GET", "/widgets")

	if dropped := s.Sweep(t0.Add(90 * time.Second)); dropped != 1 {
		t.Fatalf("Sweep = %d, want 1", dropped)
	}
	w.awaitRelease(t)
}

// TestStore_Pause_UnrelatedSetReparksAndConsumesNoFailCounter is why
// Recheck exists at all.
//
// The wake channel is a CHANGE signal, so a status directive on a different
// target wakes every parked request — and each one must re-evaluate and
// park again, or an unrelated write would silently un-pause the workspace.
// The re-evaluation must go through Recheck rather than Apply, because
// Apply CONSUMES: with an armed fail sitting on the same target, one
// unrelated Set per wakeup would burn the counter down for responses nobody
// ever received.
func TestStore_Pause_UnrelatedSetReparksAndConsumesNoFailCounter(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionPause})
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionFail, Status: 500, N: 3})

	// The one legitimate consumption: the request that parked also served
	// its fail unit, leaving a remainder of 2.
	w := parkWaiter(t, s, 1, "GET", "/widgets")
	if got := failRemainder(t, s, 1); got != 2 {
		t.Fatalf("fail remainder after the parking Apply = %d, want 2", got)
	}

	for i := range 3 {
		mustSet(t, s, 1, Directive{Target: Target{Method: "POST", Path: "/unrelated"}, Action: ActionStatus, Status: 418})
		if !w.awaitRecheck(t) {
			t.Fatalf("Recheck #%d reported the pause gone, want it still in force after an unrelated Set", i)
		}
		w.assertStillParked(t)
	}

	if got := failRemainder(t, s, 1); got != 2 {
		t.Fatalf("fail remainder after three re-checks = %d, want 2 — Recheck must consume nothing", got)
	}
	if got := parkedCount(s, 1); got != 1 {
		t.Fatalf("parked = %d after three re-parks, want 1", got)
	}

	s.Clear(1)
	w.awaitRelease(t)
}

// failRemainder reads the "*" fail directive's remaining count out of List,
// which is the same number the UI shows.
func failRemainder(t *testing.T, s *Store, workspaceID int64) int {
	t.Helper()
	for _, d := range s.List(workspaceID) {
		if d.Action == ActionFail && d.Target.All {
			return d.N
		}
	}
	t.Fatalf("no star-scoped fail directive left in List(%d)", workspaceID)
	return 0
}

// TestStore_Pause_BoundRefusesTheThirtyThird: MaxPausedPerWorkspace is the
// only bound on a plane that is unauthenticated by design and carries no
// rate limit. Past it a matched pause reports Refused and the request is
// served normally — never held, never given a status nobody asked for.
func TestStore_Pause_BoundRefusesTheThirtyThird(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionPause})

	waiters := make([]*waiter, 0, MaxPausedPerWorkspace)
	for range MaxPausedPerWorkspace {
		waiters = append(waiters, parkWaiter(t, s, 1, "GET", "/widgets"))
	}
	if got := parkedCount(s, 1); got != MaxPausedPerWorkspace {
		t.Fatalf("parked = %d, want %d", got, MaxPausedPerWorkspace)
	}

	over := s.Apply(1, "GET", "/widgets")
	if over.Pause || !over.Refused {
		t.Fatalf("Apply past the bound = %+v, want Pause false and Refused true", over)
	}
	if over.Wake != nil || over.Recheck != nil || over.Unpark != nil {
		t.Fatalf("Apply past the bound = %+v, want no park handles: there is no slot to give back", over)
	}
	if got := parkedCount(s, 1); got != MaxPausedPerWorkspace {
		t.Fatalf("parked = %d after a refusal, want it unchanged at %d", got, MaxPausedPerWorkspace)
	}

	s.Clear(1)
	for _, w := range waiters {
		w.awaitRelease(t)
	}
	if got := parkedCount(s, 1); got != 0 {
		t.Fatalf("parked = %d after releasing everyone, want 0", got)
	}
}

// TestStore_Close_ReleasesEveryParkedRequest covers both halves of Close,
// and the second is the one a plausible implementation leaves out: without
// the "no more pauses" flag, every woken request re-checks, finds its pause
// directive still in place, and parks again on the fresh channel — Close
// would wake everyone and release nobody. No bar in this project would say
// so, because the shutdown drain outlasts the hold cap and the process
// exits either way.
func TestStore_Close_ReleasesEveryParkedRequest(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionPause})
	mustSet(t, s, 2, Directive{Target: Target{Method: "GET", Path: "/widgets"}, Action: ActionPause})

	waiters := []*waiter{
		parkWaiter(t, s, 1, "GET", "/a"),
		parkWaiter(t, s, 1, "POST", "/b"),
		parkWaiter(t, s, 2, "GET", "/widgets"),
	}

	s.Close()
	for _, w := range waiters {
		w.awaitRelease(t)
	}

	// The directives are all still there — Close is not Clear — so an Apply
	// that still reported Pause would park a request nothing can release.
	if got := len(s.List(1)); got != 1 {
		t.Fatalf("List(1) after Close has %d directives, want the pause still stored", got)
	}
	eff := s.Apply(1, "GET", "/a")
	if eff.Pause || eff.Refused || eff.Wake != nil || eff.Recheck != nil || eff.Unpark != nil {
		t.Fatalf("Apply after Close = %+v, want no pause at all", eff)
	}

	// Idempotent: broadcasting installs a fresh channel every time, so a
	// second Close is a no-op rather than a close of a closed channel.
	s.Close()
	if got := parkedCount(s, 1); got != 0 {
		t.Fatalf("parked = %d after Close, want 0", got)
	}
}

// TestStore_Close_LeavesTheOtherActionsAlone: Close is about releasing held
// requests, not about tearing down state. A status or delay directive keeps
// applying — the drain is still answering in-flight requests, and they
// should be answered the way the operator asked.
func TestStore_Close_LeavesTheOtherActionsAlone(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionStatus, Status: 503})
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionDelay, Ms: 300})

	s.Close()

	eff := s.Apply(1, "GET", "/x")
	if eff.Status != 503 || eff.DelayMs != 300 {
		t.Fatalf("Apply after Close = %+v, want Status 503 and DelayMs 300 still in force", eff)
	}
}

// TestStore_Close_OnAnEmptyStore: nothing parked, nothing set, no panic —
// cmd/mocker calls Close on every exit path, including one where the
// listener failed before a single request arrived.
func TestStore_Close_OnAnEmptyStore(t *testing.T) {
	t.Parallel()

	NewStore(0, nil).Close()
}

// TestStore_Pause_ConcurrentParkAndRelease is the park accounting under
// -race: more requests than there are slots, arriving while the directives
// change under them, and the one invariant that has to survive all of it —
// every slot comes back. A leaked slot is permanent (nothing sweeps the
// counter), so the bound would erode one request at a time until it stopped
// bounding anything.
func TestStore_Pause_ConcurrentParkAndRelease(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionPause})

	const requests = MaxPausedPerWorkspace + 8
	var wg sync.WaitGroup
	wg.Add(requests)
	for i := range requests {
		go func(i int) {
			defer wg.Done()

			eff := s.Apply(1, "GET", fmt.Sprintf("/p/%d", i%5))
			if !eff.Pause {
				return // refused past the bound, or the pause was already gone: served normally
			}
			defer eff.Unpark()
			wake := eff.Wake
			for {
				<-wake
				stillPaused, next := eff.Recheck()
				if !stillPaused {
					return
				}
				wake = next
			}
		}(i)
	}

	// Churn while they arrive: every Set wakes everyone parked and every
	// woken request re-parks. Clear then ends it for good — a request that
	// reaches Apply after it finds no pause at all, and one that got in
	// just before is woken by the Clear itself.
	for range 20 {
		mustSet(t, s, 1, Directive{Target: Target{Method: "POST", Path: "/churn"}, Action: ActionStatus, Status: 418})
	}
	s.Clear(1)
	wg.Wait()

	if got := parkedCount(s, 1); got != 0 {
		t.Fatalf("parked = %d after every request finished, want 0", got)
	}
}
