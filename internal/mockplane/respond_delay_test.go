// respond_delay_test.go: delayMs's own behaviour — sleeping before the
// write, a canceled context writing nothing, the row-override clamp, a row
// override applying over workspace settings, cancellability, and the
// session-beats-row-beats-settings precedence. Split out of the former
// respond_test.go (package mockplane, not mockplane_test — see
// helpers_test.go's own comment for why).
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
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
)

// TestServeGenerated_DelaySleepsBeforeWriting proves settings.DelayMs is
// actually applied before anything is written (5a): the wall-clock time for
// one call must be at least the configured delay.
func TestServeGenerated_DelaySleepsBeforeWriting(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.DelayMs = 30
	rt := fixtureRuntime(t, blankDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {{OpRowID: 1, Selector: "default", HTTPStatus: 200}}}, settings)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Errorf("elapsed = %v, want at least the configured 30ms delay", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestServeGenerated_DelayCanceledWritesNothing proves the other half of
// 5a: a context canceled mid-delay must leave serveGenerated writing
// nothing at all — no status, no headers, no body — rather than answering
// late to a client (or shutdown drain) that already gave up.
func TestServeGenerated_DelayCanceledWritesNothing(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.DelayMs = 5000 // far longer than the cancellation below
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed = %v, want well under the 5s delay (cancellation must cut it short)", elapsed)
	}

	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want nothing written after a canceled delay", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type = %q, want unset (nothing should have been written at all)", ct)
	}
}

// TestServeGenerated_DelayMs_RowOverrideIsClamped is the direct, fast proof
// that a per-operation delay_ms goes through the exact same [clampedDelay]
// cap a workspace-wide settings.DelayMs already does — without a test ever
// having to wait out maxSimulatedDelay itself.
func TestServeGenerated_DelayMs_RowOverrideIsClamped(t *testing.T) {
	huge := 10_000_000 // far past maxSimulatedDelay
	if got := clampedDelay(effectiveDelayMs(0, &huge, 0)); got != maxSimulatedDelay {
		t.Errorf("clampedDelay(effectiveDelayMs(...)) = %v, want the shared cap %v — a per-operation delay_ms must not escape it", got, maxSimulatedDelay)
	}
}

// TestServeGenerated_DelayMs_RowOverrideAppliesOverWorkspaceSettings proves
// the row's own delay_ms is what actually gets awaited when the workspace
// setting itself is zero — not merely that SOME delay happens.
func TestServeGenerated_DelayMs_RowOverrideAppliesOverWorkspaceSettings(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.DelayMs = 0

	rowDelay := 30
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true, DelayMs: &rowDelay,
			Responses: map[string]overrides.Variant{},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings, rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Errorf("elapsed = %v, want at least the row's own 30ms delay_ms (workspace settings.DelayMs is 0)", elapsed)
	}
}

// TestServeGenerated_DelayMs_RowOverrideCancellable mirrors
// TestServeGenerated_DelayCanceledWritesNothing for a row-sourced delay:
// canceling the request context mid-sleep must still cut it short and write
// nothing, proving the row's delay flows through the SAME awaitDelay
// call — not a second, uncancellable sleep path.
func TestServeGenerated_DelayMs_RowOverrideCancellable(t *testing.T) {
	settings := domain.DefaultSettings()
	rowDelay := 5000 // far longer than the cancellation below
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true, DelayMs: &rowDelay,
			Responses: map[string]overrides.Variant{},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings, rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed = %v, want well under the row's own 5s delay_ms (cancellation must cut it short)", elapsed)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want nothing written after a canceled delay", rec.Body.String())
	}
}

// TestServeGenerated_DelayMs_SessionBeatsRowBeatsSettings is
// TestServeGenerated_DelayMs_RowOverrideAppliesOverWorkspaceSettings's P2a
// sibling: with a session (live-state) delay, a row delay AND a workspace
// setting all pinned on the same route, the session's own value is what
// actually gets awaited — DESIGN §4's Session is the outermost layer.
func TestServeGenerated_DelayMs_SessionBeatsRowBeatsSettings(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.DelayMs = 10 // smallest: must lose to both the row and the session

	rowDelay := 40 // middle: must lose to the session
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true, DelayMs: &rowDelay,
			Responses: map[string]overrides.Variant{},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings, rows)
	m := mustMatch(t, rt, "GET", "/items")

	ws := respondTestWorkspace()
	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/items"},
		Action: livestate.ActionDelay, Ms: 90, // largest: must win
	}); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	p := respondTestPlaneWithLiveState(store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond {
		t.Errorf("elapsed = %v, want at least the session's own 90ms delay (a row delay and a workspace setting are both pinned lower)", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
