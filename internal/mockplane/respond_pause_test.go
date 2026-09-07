// respond_pause_test.go: P2a's park/release primitives (awaitPause,
// resolvePause) in isolation, then Plane.serveGenerated driving them end to
// end — parked then released serves the normal status, a canceled context
// writes nothing, and the delay is paid exactly once, after release. Split
// out of the former respond_test.go (package mockplane, not mockplane_test
// — see helpers_test.go's own comment for why).
package mockplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
)

// TestAwaitPause_NotPausedProceedsImmediately is the zero-Effect case every
// unpaused request gets: nothing to wait on, ctx/wake never touched.
func TestAwaitPause_NotPausedProceedsImmediately(t *testing.T) {
	if !awaitPause(t.Context(), livestate.Effect{}, time.Second) {
		t.Error("awaitPause(no pause) = false, want true")
	}
}

// TestAwaitPause_ReleasedByClearReturnsTrue is the ordinary release path: a
// pause matched, the operator clears it, and the wait ends without ever
// reaching the hold cap.
func TestAwaitPause_ReleasedByClearReturnsTrue(t *testing.T) {
	const wsID = 1
	store, eff := planeP2aPause(t, wsID)
	defer eff.Unpark()

	done := make(chan bool, 1)
	go func() { done <- awaitPause(context.Background(), eff, 2*time.Second) }()

	time.Sleep(20 * time.Millisecond) // give the goroutine time to park
	store.Clear(wsID)

	select {
	case ok := <-done:
		if !ok {
			t.Error("awaitPause = false, want true (released, not a context cancel)")
		}
	case <-time.After(time.Second):
		t.Fatal("awaitPause never returned after Clear")
	}
}

// TestAwaitPause_UnrelatedSetWakesThenReparks is DESIGN §14 rule 1 end to
// end: a Set for a DIFFERENT target wakes the parked request (Wake is a
// change signal, not a pause-specific one), but the pause directive it was
// actually parked on still matches, so it re-parks rather than proceeding —
// only a real Clear ends the wait.
func TestAwaitPause_UnrelatedSetWakesThenReparks(t *testing.T) {
	const wsID = 1
	store, eff := planeP2aPause(t, wsID)
	defer eff.Unpark()

	done := make(chan bool, 1)
	go func() { done <- awaitPause(context.Background(), eff, 2*time.Second) }()
	time.Sleep(20 * time.Millisecond)

	if err := store.Set(wsID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/other"}, Action: livestate.ActionStatus, Status: 500,
	}); err != nil {
		t.Fatalf("store.Set(unrelated): %v", err)
	}

	select {
	case <-done:
		t.Fatal("awaitPause returned after an unrelated Set; want it to re-check and re-park")
	case <-time.After(80 * time.Millisecond):
		// still parked — correct
	}

	store.Clear(wsID)
	select {
	case ok := <-done:
		if !ok {
			t.Error("awaitPause = false after Clear, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("awaitPause never returned after Clear")
	}
}

// TestAwaitPause_ContextCanceledReturnsFalse proves ctx ends the wait even
// while the pause itself is still in force — the request is being torn
// down, not answered late.
func TestAwaitPause_ContextCanceledReturnsFalse(t *testing.T) {
	const wsID = 1
	_, eff := planeP2aPause(t, wsID)
	defer eff.Unpark()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if awaitPause(ctx, eff, 5*time.Second) {
		t.Fatal("awaitPause = true, want false (context was canceled; the pause was never lifted)")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("elapsed = %v, want well under the 5s hold (cancellation must cut it short)", elapsed)
	}
}

// TestAwaitPause_HoldCapExpiresReturnsTrue is DESIGN §14 rule 3, proven
// without ever waiting out the real [livestate.MaxPauseHold]: hold is a
// parameter precisely so this is possible — the same split
// [clampedDelay]/[maxSimulatedDelay] already uses for the delay side.
func TestAwaitPause_HoldCapExpiresReturnsTrue(t *testing.T) {
	const wsID = 1
	_, eff := planeP2aPause(t, wsID) // never released: only the cap can end this wait
	defer eff.Unpark()

	const hold = 20 * time.Millisecond
	start := time.Now()
	if !awaitPause(context.Background(), eff, hold) {
		t.Fatal("awaitPause = false, want true (the cap must serve normally, never invent a status)")
	}
	if elapsed := time.Since(start); elapsed < hold {
		t.Errorf("elapsed = %v, want at least the %v hold", elapsed, hold)
	}
}

// TestResolvePause_NoEffectProceedsImmediately proves resolvePause is a
// no-op for a request with neither Pause nor Refused set — the zero Effect
// every request without a matching pause directive gets.
func TestResolvePause_NoEffectProceedsImmediately(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	if !resolvePause(req, livestate.Effect{}) {
		t.Error("resolvePause(zero Effect) = false, want true")
	}
}

// TestResolvePause_RefusedProceedsImmediately is DESIGN §14 rule 7: a
// Refused park (the bound was already full) is served normally with no
// wait at all — resolvePause never touches Wake/Recheck/Unpark for this
// case, which are all nil on a Refused Effect anyway (reserveParkLocked).
func TestResolvePause_RefusedProceedsImmediately(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	start := time.Now()
	if !resolvePause(req, livestate.Effect{Refused: true}) {
		t.Error("resolvePause(Refused) = false, want true (serve normally)")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Errorf("elapsed = %v, want effectively instant — Refused must never wait", elapsed)
	}
}

// TestServeGenerated_Pause_ParksThenReleasedServesNormalStatus is DESIGN
// §14's pause end to end through the real serving path: nothing is written
// to the response while parked (checked before any release fires), and once
// the operator clears the pause the request answers exactly the status it
// would have without ever having been paused at all.
func TestServeGenerated_Pause_ParksThenReleasedServesNormalStatus(t *testing.T) {
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/items")

	ws := respondTestWorkspace()
	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/items"}, Action: livestate.ActionPause,
	}); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	p := respondTestPlaneWithLiveState(store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("serveGenerated returned before the pause was ever released")
	case <-time.After(50 * time.Millisecond):
		// still parked — correct
	}
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Fatalf("wrote something while parked: body=%q content-type=%q", rec.Body.String(), rec.Header().Get("Content-Type"))
	}

	store.Clear(ws.ID)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("serveGenerated never returned after Clear")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (the item route's own normal status, unaffected by having been paused)", rec.Code)
	}
}

// TestServeGenerated_Pause_ContextCanceledWritesNothing mirrors
// TestServeGenerated_DelayCanceledWritesNothing for a paused request:
// canceling r's own context while parked ends the wait writing nothing at
// all — the same "the request is being torn down" contract the delay side
// already has.
func TestServeGenerated_Pause_ContextCanceledWritesNothing(t *testing.T) {
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/items")

	ws := respondTestWorkspace()
	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/items"}, Action: livestate.ActionPause,
	}); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	p := respondTestPlaneWithLiveState(store)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed = %v, want well under a second (cancellation must cut the park short)", elapsed)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want nothing written after a canceled park", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type = %q, want unset (nothing should have been written at all)", ct)
	}
}

// TestServeGenerated_Pause_DelayPaidOnceAfterRelease is DESIGN §14 rule 4:
// a matched delay is paid exactly once, AFTER the pause releases — never
// before the park, never per wakeup. A workspace holding both a pause and a
// delay is a legitimate "pause it, AND delay it once released" the operator
// asked for.
func TestServeGenerated_Pause_DelayPaidOnceAfterRelease(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.DelayMs = 40
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings)
	m := mustMatch(t, rt, "GET", "/items")

	ws := respondTestWorkspace()
	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/items"}, Action: livestate.ActionPause,
	}); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	p := respondTestPlaneWithLiveState(store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	done := make(chan struct{})
	go func() {
		p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))
		close(done)
	}()

	const parkFor = 60 * time.Millisecond
	time.Sleep(parkFor)
	store.Clear(ws.ID)

	// No mid-point peek at rec here: rec is owned by the background
	// goroutine from the moment it might start writing, and a bare
	// time.Sleep on this side establishes no happens-before edge the race
	// detector would honor — TestServeGenerated_Pause_ParksThenReleasedServesNormalStatus
	// already proves the "nothing written while parked" half, synchronized
	// through the Store's own locking. This test's job is purely the timing
	// math below: if the delay had been paid DURING the park instead of
	// after release, the two would overlap and total elapsed would fall
	// short of parkFor+40ms.
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("serveGenerated never returned")
	}
	if elapsed := time.Since(start); elapsed < parkFor+40*time.Millisecond {
		t.Errorf("elapsed = %v, want at least %v (parked %v, then the 40ms delay paid AFTER release)",
			elapsed, parkFor+40*time.Millisecond, parkFor)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
