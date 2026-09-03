package livestate

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mustSet is a t.Helper wrapper for the overwhelming majority of these
// tests, which only care that the fixture directive was accepted.
func mustSet(t *testing.T, s *Store, workspaceID int64, d Directive) {
	t.Helper()
	if err := s.Set(workspaceID, d); err != nil {
		t.Fatalf("Set(%d, %+v): %v", workspaceID, d, err)
	}
}

// isInert reports whether an Effect asks the serving path to do nothing at
// all. Effect stopped being comparable the moment it grew the func fields a
// parked request needs (Recheck, Unpark), so every `eff != (Effect{})` this
// file used to write is this call instead — field by field, and failing
// closed: a field added to Effect and forgotten here reads as inert, which
// is why the fields are listed rather than reflected over.
func isInert(eff Effect) bool {
	return eff.Status == 0 && eff.DelayMs == 0 && !eff.Pause && !eff.Refused &&
		eff.Wake == nil && eff.Recheck == nil && eff.Unpark == nil
}

func TestNewStore_Defaults(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	if s.ttl != DefaultTTL {
		t.Fatalf("ttl<=0 gave %v, want DefaultTTL", s.ttl)
	}
	if s.now == nil {
		t.Fatalf("now nil was not defaulted to time.Now")
	}
}

// TestStore_Apply_UnknownWorkspace_ReturnsZero is the overwhelmingly common
// path this package is built to keep cheap: a workspace nobody has ever
// touched must answer with a zero Effect.
func TestStore_Apply_UnknownWorkspace_ReturnsZero(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	if eff := s.Apply(1, "GET", "/anything"); !isInert(eff) {
		t.Fatalf("Apply on untouched workspace = %+v, want zero Effect", eff)
	}
}

// TestStore_Apply_StatusForcesUntilCleared: a status directive has no
// counter, so it must keep firing across many requests and only stop once
// Clear removes it.
func TestStore_Apply_StatusForcesUntilCleared(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionStatus, Status: 503})

	for i := range 5 {
		if eff := s.Apply(1, "GET", "/x"); eff.Status != 503 {
			t.Fatalf("Apply #%d = %+v, want Status 503", i, eff)
		}
	}

	if dropped := s.Clear(1); dropped != 1 {
		t.Fatalf("Clear = %d, want 1", dropped)
	}
	if eff := s.Apply(1, "GET", "/x"); !isInert(eff) {
		t.Fatalf("Apply after Clear = %+v, want zero Effect", eff)
	}
}

// TestStore_Apply_FailStopsAfterN: a fail directive with N=3 forces exactly
// three responses and then the target sees nothing at all.
func TestStore_Apply_FailStopsAfterN(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionFail, Status: 500, N: 3})

	for i := range 3 {
		if eff := s.Apply(1, "GET", "/x"); eff.Status != 500 {
			t.Fatalf("Apply #%d = %+v, want Status 500", i, eff)
		}
	}
	for range 2 {
		if eff := s.Apply(1, "GET", "/x"); !isInert(eff) {
			t.Fatalf("Apply after exhausting N = %+v, want zero Effect", eff)
		}
	}
}

// TestStore_Apply_OnceFiresExactlyOnceRegardlessOfN: once:true must win
// over whatever N the caller sent, because normalize forces N to 1 for it —
// this exercises that guarantee end to end, through Set then Apply.
func TestStore_Apply_OnceFiresExactlyOnceRegardlessOfN(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionFail, Status: 500, Once: true, N: 99})

	if eff := s.Apply(1, "GET", "/x"); eff.Status != 500 {
		t.Fatalf("first Apply = %+v, want Status 500", eff)
	}
	if eff := s.Apply(1, "GET", "/x"); !isInert(eff) {
		t.Fatalf("second Apply = %+v, want zero Effect (once already fired)", eff)
	}
}

// TestStore_Apply_ExactTargetBeatsStar: an exact (method, path) directive
// must win over a "*" directive on the very same request, even though both
// are live at once.
func TestStore_Apply_ExactTargetBeatsStar(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionStatus, Status: 500})
	mustSet(t, s, 1, Directive{Target: Target{Method: "POST", Path: "/auth/login"}, Action: ActionStatus, Status: 409})

	if eff := s.Apply(1, "POST", "/auth/login"); eff.Status != 409 {
		t.Fatalf("Apply on the exact target = %+v, want Status 409 (exact beats star)", eff)
	}
	if eff := s.Apply(1, "GET", "/other"); eff.Status != 500 {
		t.Fatalf("Apply off the exact target = %+v, want Status 500 (falls through to star)", eff)
	}
}

// TestStore_Apply_FailBeatsStatusOnSameTarget: with both a status and a
// fail directive live on the identical target, fail wins while its counter
// has requests left, and status takes back over once fail is exhausted and
// dropped.
func TestStore_Apply_FailBeatsStatusOnSameTarget(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	target := Target{Method: "GET", Path: "/widgets"}
	mustSet(t, s, 1, Directive{Target: target, Action: ActionStatus, Status: 200})
	mustSet(t, s, 1, Directive{Target: target, Action: ActionFail, Status: 500, N: 2})

	for i := range 2 {
		if eff := s.Apply(1, "GET", "/widgets"); eff.Status != 500 {
			t.Fatalf("Apply #%d = %+v, want Status 500 (fail beats status)", i, eff)
		}
	}
	// The fail directive is now exhausted and dropped; the status directive
	// on the same target is still there and must take over.
	for i := range 2 {
		if eff := s.Apply(1, "GET", "/widgets"); eff.Status != 200 {
			t.Fatalf("Apply after fail exhausted #%d = %+v, want Status 200 (status remains)", i, eff)
		}
	}
}

// TestStore_Set_ReplacesNotAppends: Set with the same (Target, Action) must
// overwrite in place, never grow the workspace's directive count.
func TestStore_Set_ReplacesNotAppends(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	target := Target{Method: "GET", Path: "/widgets"}
	mustSet(t, s, 1, Directive{Target: target, Action: ActionStatus, Status: 500})
	mustSet(t, s, 1, Directive{Target: target, Action: ActionStatus, Status: 503})

	got := s.List(1)
	if len(got) != 1 {
		t.Fatalf("List = %+v, want exactly one directive after replacing", got)
	}
	if got[0].Status != 503 {
		t.Fatalf("List[0].Status = %d, want 503 (the replacement)", got[0].Status)
	}
}

// TestStore_Set_MaxDirectivesPerWorkspace: the 65th DISTINCT directive is
// rejected, but replacing one of the existing 64 keeps working right at the
// cap — the cap bounds distinct keys, not calls to Set.
func TestStore_Set_MaxDirectivesPerWorkspace(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	for i := range MaxDirectivesPerWorkspace {
		d := Directive{Target: Target{Method: "GET", Path: fmt.Sprintf("/p/%d", i)}, Action: ActionStatus, Status: 200}
		if err := s.Set(1, d); err != nil {
			t.Fatalf("Set #%d (filling the cap): %v", i, err)
		}
	}

	overflow := Directive{Target: Target{Method: "GET", Path: "/p/overflow"}, Action: ActionStatus, Status: 200}
	if err := s.Set(1, overflow); !errors.Is(err, ErrTooManyDirectives) {
		t.Fatalf("Set past the cap: err = %v, want ErrTooManyDirectives", err)
	}

	// Replacing directive #0 must still succeed: it is not a NEW key, so it
	// does not count against the cap.
	replacement := Directive{Target: Target{Method: "GET", Path: "/p/0"}, Action: ActionStatus, Status: 503}
	if err := s.Set(1, replacement); err != nil {
		t.Fatalf("Set replacing an existing directive at the cap: %v", err)
	}
	if got := s.List(1); len(got) != MaxDirectivesPerWorkspace {
		t.Fatalf("List after replace-at-cap has %d directives, want %d", len(got), MaxDirectivesPerWorkspace)
	}
}

// TestStore_List_Order: "*" first, then non-All targets ordered by method
// and path, matching the task's exact contract for List's snapshot order.
func TestStore_List_Order(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{Method: "POST", Path: "/b"}, Action: ActionStatus, Status: 200})
	mustSet(t, s, 1, Directive{Target: Target{Method: "GET", Path: "/a"}, Action: ActionStatus, Status: 200})
	mustSet(t, s, 1, Directive{Target: Target{Method: "GET", Path: "/a"}, Action: ActionFail, Status: 500, N: 1})
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionStatus, Status: 500})

	got := s.List(1)
	if len(got) != 4 {
		t.Fatalf("List returned %d directives, want 4", len(got))
	}

	wantTargets := []struct {
		all    bool
		method string
		path   string
		action Action
	}{
		{all: true, action: ActionStatus},
		{method: "GET", path: "/a", action: ActionFail},
		{method: "GET", path: "/a", action: ActionStatus},
		{method: "POST", path: "/b", action: ActionStatus},
	}
	for i, want := range wantTargets {
		g := got[i]
		if g.Target.All != want.all || g.Target.Method != want.method || g.Target.Path != want.path || g.Action != want.action {
			t.Fatalf("List[%d] = {All:%v Method:%q Path:%q Action:%q}, want {All:%v Method:%q Path:%q Action:%q}",
				i, g.Target.All, g.Target.Method, g.Target.Path, g.Action,
				want.all, want.method, want.path, want.action)
		}
	}
}

// TestStore_List_IsASnapshot: mutating the returned slice, or holding it
// across a later Set, must not observe or corrupt the store's own state.
func TestStore_List_IsASnapshot(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionStatus, Status: 500})

	got := s.List(1)
	got[0].Status = 999 // mutate the caller's copy

	again := s.List(1)
	if again[0].Status != 500 {
		t.Fatalf("List after mutating a previous snapshot = %+v, want Status still 500", again[0])
	}
}

// TestStore_Clear: returns the count dropped, and leaves other workspaces
// alone.
func TestStore_Clear(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionStatus, Status: 500})
	mustSet(t, s, 1, Directive{Target: Target{Method: "GET", Path: "/x"}, Action: ActionFail, Status: 500, N: 2})
	mustSet(t, s, 2, Directive{Target: Target{All: true}, Action: ActionStatus, Status: 503})

	if dropped := s.Clear(1); dropped != 2 {
		t.Fatalf("Clear(1) = %d, want 2", dropped)
	}
	if got := s.List(1); len(got) != 0 {
		t.Fatalf("List(1) after Clear = %+v, want empty", got)
	}
	if got := s.List(2); len(got) != 1 {
		t.Fatalf("List(2) after Clear(1) = %+v, want the untouched directive still there", got)
	}
}

func TestStore_Clear_UnknownWorkspace(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	if dropped := s.Clear(999); dropped != 0 {
		t.Fatalf("Clear(unknown) = %d, want 0", dropped)
	}
}

// TestStore_Sweep_DropsStaleKeepsFresh: the injected clock makes this
// exact and instant. Workspace 1's only directive is set at t0; workspace
// 2's at t0+45s. Sweeping at t0+90s with a one-minute TTL puts the cutoff
// at t0+30s, which is past workspace 1's SetAt but not workspace 2's.
func TestStore_Sweep_DropsStaleKeepsFresh(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := t0
	s := NewStore(time.Minute, func() time.Time { return clock })

	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionStatus, Status: 500})
	clock = t0.Add(45 * time.Second)
	mustSet(t, s, 2, Directive{Target: Target{All: true}, Action: ActionStatus, Status: 503})

	dropped := s.Sweep(t0.Add(90 * time.Second))
	if dropped != 1 {
		t.Fatalf("Sweep = %d, want 1 (only workspace 1 is stale)", dropped)
	}
	if got := s.List(1); got != nil {
		t.Fatalf("List(1) after Sweep = %+v, want nil (dropped)", got)
	}
	if got := s.List(2); len(got) != 1 {
		t.Fatalf("List(2) after Sweep = %+v, want the fresher directive kept", got)
	}
}

// TestStore_Sweep_DropsEmptyWorkspace: a workspace whose directives were
// all Cleared has no "newest SetAt" to be fresh by, so it must not survive
// a sweep just because it happens to be a recently-created map entry.
func TestStore_Sweep_DropsEmptyWorkspace(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := NewStore(time.Hour, func() time.Time { return now })

	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionStatus, Status: 500})
	if dropped := s.Clear(1); dropped != 1 {
		t.Fatalf("Clear = %d, want 1", dropped)
	}

	if dropped := s.Sweep(now); dropped != 1 {
		t.Fatalf("Sweep = %d, want 1 (the emptied workspace)", dropped)
	}
}

// TestStore_Apply_ConcurrentDecrementIsExact is CONCURRENCY IS THE WHOLE
// JOB, made literal: 200 goroutines race a single fail directive with
// N=100. The decrement must be atomic enough that the status is forced
// EXACTLY 100 times, never 101 — run this under -race.
func TestStore_Apply_ConcurrentDecrementIsExact(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	const total = 100
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionFail, Status: 503, N: total})

	const workers = 200
	var forced atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range workers {
		go func() {
			defer wg.Done()
			path := fmt.Sprintf("/p/%d", i%7) // different requests, all under the "*" target
			eff := s.Apply(1, "GET", path)
			switch eff.Status {
			case 503:
				forced.Add(1)
			case 0:
				// the counter was already exhausted by another goroutine — fine
			default:
				t.Errorf("Apply returned unexpected status %d", eff.Status)
			}
		}()
	}
	wg.Wait()

	if got := forced.Load(); got != total {
		t.Fatalf("forced = %d, want exactly %d", got, total)
	}
	if got := s.List(1); len(got) != 0 {
		t.Fatalf("List after exhausting the fail counter = %+v, want no directives left", got)
	}
}

// TestStore_Apply_ResolvesEachActionIndependently: one target carrying a
// status, a delay and a pause at once must report all three on the same
// Effect. "Delay 300 ms AND force 503" is a thing an operator asks for, and
// before this slice the first matching directive returned and stopped.
func TestStore_Apply_ResolvesEachActionIndependently(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	target := Target{Method: "GET", Path: "/widgets"}
	mustSet(t, s, 1, Directive{Target: target, Action: ActionStatus, Status: 503})
	mustSet(t, s, 1, Directive{Target: target, Action: ActionDelay, Ms: 300})
	mustSet(t, s, 1, Directive{Target: target, Action: ActionPause})

	eff := s.Apply(1, "GET", "/widgets")
	defer eff.Unpark()

	if eff.Status != 503 || eff.DelayMs != 300 || !eff.Pause {
		t.Fatalf("Apply = %+v, want Status 503, DelayMs 300 and Pause together", eff)
	}
	if eff.Wake == nil || eff.Recheck == nil || eff.Unpark == nil {
		t.Fatalf("Apply = %+v, want Wake, Recheck and Unpark all wired for a paused request", eff)
	}
	if eff.Refused {
		t.Fatalf("Apply = %+v, want Refused false: the park bound is nowhere near", eff)
	}
}

// TestStore_Apply_ExactTargetOfOneActionDoesNotHideStarOfAnother is the
// precedence change stated as its failure mode: with one global first-match,
// an exact-target status on this operation returned before the "*" delay and
// the "*" pause were ever looked at, so a workspace-wide pause became
// invisible to exactly the operations someone had also pinned a status on.
func TestStore_Apply_ExactTargetOfOneActionDoesNotHideStarOfAnother(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{Method: "GET", Path: "/widgets"}, Action: ActionStatus, Status: 409})
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionDelay, Ms: 300})
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionPause})

	eff := s.Apply(1, "GET", "/widgets")
	defer eff.Unpark()

	if eff.Status != 409 || eff.DelayMs != 300 || !eff.Pause {
		t.Fatalf("Apply = %+v, want the exact status 409 AND the star-scoped delay AND the star-scoped pause", eff)
	}
}

// TestStore_Apply_StarFailIsNotHiddenByExactStatus: within ONE action an
// exact target still beats "*", but across actions nothing short-circuits,
// so a "*"-scoped fail is consumed even though this operation also carries
// an exact-target status. Fail beats status because the two share one
// Effect.Status field — and once the counter is spent, the status is still
// there.
func TestStore_Apply_StarFailIsNotHiddenByExactStatus(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{Method: "GET", Path: "/widgets"}, Action: ActionStatus, Status: 409})
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionFail, Status: 500, N: 1})

	if eff := s.Apply(1, "GET", "/widgets"); eff.Status != 500 {
		t.Fatalf("first Apply = %+v, want Status 500 (the star fail is not hidden by the exact status)", eff)
	}
	if eff := s.Apply(1, "GET", "/widgets"); eff.Status != 409 {
		t.Fatalf("second Apply = %+v, want Status 409 (fail spent, the exact status remains)", eff)
	}
}

// TestStore_Apply_DelayPrecedenceIsPerAction: exact beats "*" WITHIN the
// delay action, and an operation with no exact delay still gets the "*" one.
func TestStore_Apply_DelayPrecedenceIsPerAction(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionDelay, Ms: 300})
	mustSet(t, s, 1, Directive{Target: Target{Method: "GET", Path: "/widgets"}, Action: ActionDelay, Ms: 900})

	if eff := s.Apply(1, "GET", "/widgets"); eff.DelayMs != 900 {
		t.Fatalf("Apply on the exact target = %+v, want DelayMs 900", eff)
	}
	if eff := s.Apply(1, "GET", "/other"); eff.DelayMs != 300 {
		t.Fatalf("Apply off the exact target = %+v, want DelayMs 300 (falls through to star)", eff)
	}
}

// TestStore_Apply_DelayIsNotConsumed: a delay behaves like a status, not
// like a fail — nothing decrements, so it keeps applying until it is cleared.
func TestStore_Apply_DelayIsNotConsumed(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionDelay, Ms: 300})

	for i := range 5 {
		if eff := s.Apply(1, "GET", "/x"); eff.DelayMs != 300 {
			t.Fatalf("Apply #%d = %+v, want DelayMs 300 every time", i, eff)
		}
	}
	if got := s.List(1); len(got) != 1 {
		t.Fatalf("List = %+v, want the delay directive still there", got)
	}
}

// A13: Delete narrows Clear to one target — every action on it, or one —
// wakes what it releases, and leaves every other target alone.
func TestStore_Delete_oneTargetOrOneAction(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{Method: "GET", Path: "/x"}, Action: ActionStatus, Status: 500})
	mustSet(t, s, 1, Directive{Target: Target{Method: "GET", Path: "/x"}, Action: ActionDelay, Ms: 5})
	mustSet(t, s, 1, Directive{Target: Target{All: true}, Action: ActionStatus, Status: 503})
	mustSet(t, s, 2, Directive{Target: Target{Method: "GET", Path: "/x"}, Action: ActionStatus, Status: 500})

	// One action on the target: the other action stays.
	if n, err := s.Delete(1, Target{Method: "get", Path: "/x"}, ActionDelay); err != nil || n != 1 {
		t.Fatalf("Delete(GET /x, delay) = %d, %v; want 1 (method matched case-insensitively)", n, err)
	}
	if got := s.List(1); len(got) != 2 {
		t.Fatalf("after one-action delete: %d directives, want 2", len(got))
	}
	// Every action on the target: the "*" directive and workspace 2 stay.
	if n, err := s.Delete(1, Target{Method: "GET", Path: "/x"}, ""); err != nil || n != 1 {
		t.Fatalf("Delete(GET /x) = %d, %v; want 1", n, err)
	}
	if got := s.List(1); len(got) != 1 || !got[0].Target.All {
		t.Fatalf("after target delete: %+v, want only the * directive", got)
	}
	if got := s.List(2); len(got) != 1 {
		t.Fatalf("workspace 2 lost a directive to workspace 1's delete: %+v", got)
	}
	// The wildcard target, nothing left, an unknown workspace: 0 and no error.
	if n, err := s.Delete(1, Target{All: true}, ""); err != nil || n != 1 {
		t.Fatalf("Delete(*) = %d, %v; want 1", n, err)
	}
	if n, err := s.Delete(1, Target{All: true}, ""); err != nil || n != 0 {
		t.Fatalf("Delete(*) again = %d, %v; want 0, nil", n, err)
	}
	if n, err := s.Delete(99, Target{All: true}, ""); err != nil || n != 0 {
		t.Fatalf("Delete on an unknown workspace = %d, %v; want 0, nil", n, err)
	}
	// The same validation Set applies, and an unknown action.
	if _, err := s.Delete(1, Target{Method: "GET", Path: "no-slash"}, ""); !errors.Is(err, ErrInvalidDirective) {
		t.Errorf("Delete with a bad path: err = %v, want ErrInvalidDirective", err)
	}
	if _, err := s.Delete(1, Target{All: true}, Action("nope")); !errors.Is(err, ErrInvalidDirective) {
		t.Errorf("Delete with an unknown action: err = %v, want ErrInvalidDirective", err)
	}
}

// A13: a request parked on the deleted pause is released; one parked on
// another target's pause is not.
func TestStore_Delete_releasesOnlyItsOwnPause(t *testing.T) {
	t.Parallel()

	s := NewStore(0, nil)
	mustSet(t, s, 1, Directive{Target: Target{Method: "GET", Path: "/a"}, Action: ActionPause})
	mustSet(t, s, 1, Directive{Target: Target{Method: "GET", Path: "/b"}, Action: ActionPause})
	effA := s.Apply(1, "GET", "/a")
	effB := s.Apply(1, "GET", "/b")
	if !effA.Pause || !effB.Pause {
		t.Fatalf("both requests should park: a=%v b=%v", effA.Pause, effB.Pause)
	}
	defer effA.Unpark()
	defer effB.Unpark()

	if n, err := s.Delete(1, Target{Method: "GET", Path: "/a"}, ""); err != nil || n != 1 {
		t.Fatalf("Delete(GET /a) = %d, %v", n, err)
	}
	select {
	case <-effA.Wake:
	case <-time.After(time.Second):
		t.Fatal("the deleted pause's wake channel did not fire")
	}
	if still, _ := effA.Recheck(); still {
		t.Error("GET /a still parked after its pause was deleted")
	}
	if still, _ := effB.Recheck(); !still {
		t.Error("GET /b released by a delete that named /a")
	}
}
