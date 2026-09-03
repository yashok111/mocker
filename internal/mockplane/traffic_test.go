// traffic_test.go is a WHITE-BOX test file (package mockplane, not
// mockplane_test): [trafficWriter], [attachTrafficMatch]/[markTrafficMatch]
// and [authCheckPath] are all unexported, and this file's own
// TestTrafficWriter_* case is squarely about trafficWriter's contract in
// isolation — the implicit-200 guarantee [httpx.StatusRecorder] gives it —
// before ever going through HTTP. Everything else here drives a real Plane
// end to end through [Plane.ServeHTTP], reusing runtime_test.go's own
// widgets fixtures (widgetsSource/widgetsRoute/widgetsWorkspace) rather than
// routes_test.go's fakeSpecSource: that fixture lives in the sibling
// mockplane_test package, a separate compilation unit this file cannot see.
package mockplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/traffic"
	"github.com/yashok111/mocker/internal/workspaces"
)

// trafficFakeSource is this file's own [Source]: white-box test files in
// this package cannot reuse plane_test.go's fakeSource — that lives in the
// sibling mockplane_test package, a separate compilation unit.
type trafficFakeSource struct {
	bySlug map[string]*workspaces.Workspace
}

func (f *trafficFakeSource) BySlug(_ context.Context, slug string) (*workspaces.Workspace, error) {
	if ws, ok := f.bySlug[slug]; ok {
		return ws, nil
	}
	return nil, workspaces.ErrNotFound
}

// trafficPlane builds a Plane wired for these tests: runtime_test.go's own
// runtimeTestConfig with MOCKER_TRAFFIC_MAX_BODY overridden to maxBody (that
// config leaves it at its zero value, which this file's cap tests need
// control over), and sink wired via [Plane.SetTraffic] only when non-nil —
// exactly the "never called" contract SetTraffic's own doc comment fixes
// for a nil sink.
func trafficPlane(t *testing.T, maxBody int64, spec SpecSource, sink TrafficSink, wss ...*workspaces.Workspace) *Plane {
	t.Helper()
	src := &trafficFakeSource{bySlug: make(map[string]*workspaces.Workspace, len(wss))}
	for _, ws := range wss {
		src.bySlug[ws.Slug] = ws
	}
	cfg := runtimeTestConfig(4<<20, 32)
	cfg.TrafficMaxBody = maxBody
	p := New(cfg, src, spec, runtimeTestLogger())
	if sink != nil {
		p.SetTraffic(sink)
	}
	return p
}

// fakeTrafficSink collects every [traffic.Event] handed to it — no database
// involved, the same in-memory pattern every other fake source in this
// package already uses.
type fakeTrafficSink struct {
	mu     sync.Mutex
	events []traffic.Event
}

func (f *fakeTrafficSink) Record(ev traffic.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func (f *fakeTrafficSink) all() []traffic.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]traffic.Event, len(f.events))
	copy(out, f.events)
	return out
}

// loginDoc/loginVariant are this file's own auth-shaped fixture — a route
// under "/auth/login", the exact DESIGN §10 trigger segment
// [authpreset.IsAuthPath] matches — kept separate from widgetsDoc's
// non-auth "/widgets" so the suppression tests below never depend on
// widgetsDoc happening to also carry a trigger segment.
const loginOpRowID = int64(2)

const loginDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "Auth", "version": "1.0.0" },
  "paths": {
    "/auth/login": {
      "post": {
        "operationId": "login",
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": { "type": "object", "properties": { "token": { "type": "string" } } }
              }
            }
          }
        }
      }
    }
  }
}`

func loginRoute() router.Route {
	return router.Route{
		OpRowID:        loginOpRowID,
		OperationLabel: "login",
		Method:         "POST",
		Path:           "/auth/login",
		CanonicalPath:  "/auth/login",
		SourceOrder:    1,
	}
}

func loginVariant() gen.ResponseVariant {
	return gen.ResponseVariant{
		OpRowID:    loginOpRowID,
		Selector:   "200",
		HTTPStatus: 200,
		IsDefault:  false,
		MediaType:  "application/json",
		SchemaPtr:  "#/paths/~1auth~1login/post/responses/200/content/application~1json/schema",
		OpPointer:  "#/paths/~1auth~1login/post",
	}
}

func loginSource() *fakeRuntimeSource {
	return &fakeRuntimeSource{
		normalized: []byte(loginDoc),
		routes:     []router.Route{loginRoute()},
		variants:   map[int64][]gen.ResponseVariant{loginOpRowID: {loginVariant()}},
	}
}

// TestCaptureTraffic_MatchedRequest_RecordsOperationEvent covers the first
// required case: a request matching a spec operation records exactly one
// event, kind "operation", the right OpRowID, the right status, a positive
// duration and a non-empty peer.
func TestCaptureTraffic_MatchedRequest_RecordsOperationEvent(t *testing.T) {
	sink := &fakeTrafficSink{}
	ws := widgetsWorkspace(7, domain.DefaultSettings())
	p := trafficPlane(t, 1<<20, widgetsSource(), sink, ws)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.MatchedKind != "operation" || ev.MatchedID != widgetsOpRowID {
		t.Errorf("matched = (%q, %d), want (\"operation\", %d)", ev.MatchedKind, ev.MatchedID, widgetsOpRowID)
	}
	if ev.WorkspaceID != ws.ID {
		t.Errorf("workspaceID = %d, want %d", ev.WorkspaceID, ws.ID)
	}
	if ev.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", ev.Status)
	}
	if ev.Duration <= 0 {
		t.Errorf("duration = %v, want > 0", ev.Duration)
	}
	if ev.PeerIP == "" {
		t.Errorf("peerIP is empty, want the request's remote peer")
	}
}

// TestCaptureTraffic_RecordsContentTypes is round-1 review finding 5's
// mockplane-side wiring: [traffic.Event]'s ReqContentType/RespContentType
// must carry the SAME Content-Type headers the request arrived with and the
// response actually left under, since [traffic.Recorder.prepare] uses them
// to pick which [traffic.RedactBody] branch applies (JSON alone, before this
// fix, regardless of what the client actually sent) — a wiring bug here
// would silently defeat finding 5's fix at the recorder layer no matter how
// correct RedactBody itself is.
func TestCaptureTraffic_RecordsContentTypes(t *testing.T) {
	sink := &fakeTrafficSink{}
	ws := widgetsWorkspace(7, domain.DefaultSettings())
	p := trafficPlane(t, 1<<20, widgetsSource(), sink, ws)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.ReqContentType != "application/x-www-form-urlencoded" {
		t.Errorf("ReqContentType = %q, want the request's own Content-Type header", ev.ReqContentType)
	}
	wantRespCT := rec.Header().Get("Content-Type")
	if wantRespCT == "" {
		t.Fatal("test fixture sanity: the response carries no Content-Type to compare against")
	}
	if ev.RespContentType != wantRespCT {
		t.Errorf("RespContentType = %q, want the response's own Content-Type header %q", ev.RespContentType, wantRespCT)
	}
}

// TestCaptureTraffic_UnmatchedRequest_RecordsNoneEvent covers the second
// required case: an unmatched request still records exactly one event, kind
// "none", id 0 — the whole input DESIGN §6's "create endpoint from
// observed traffic" flow needs.
func TestCaptureTraffic_UnmatchedRequest_RecordsNoneEvent(t *testing.T) {
	sink := &fakeTrafficSink{}
	ws := widgetsWorkspace(7, domain.DefaultSettings())
	p := trafficPlane(t, 1<<20, widgetsSource(), sink, ws)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/nope", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].MatchedKind != "none" || events[0].MatchedID != 0 {
		t.Errorf("matched = (%q, %d), want (\"none\", 0)", events[0].MatchedKind, events[0].MatchedID)
	}
}

// TestCaptureTraffic_ReservedPrefixAndPreflight_RecordNothing covers the
// third required case: control traffic (health, under the reserved prefix)
// and a CORS preflight both reach ServeHTTP but neither ever reaches Step 5,
// so neither ever produces an event.
func TestCaptureTraffic_ReservedPrefixAndPreflight_RecordNothing(t *testing.T) {
	sink := &fakeTrafficSink{}
	ws := widgetsWorkspace(7, domain.DefaultSettings())
	p := trafficPlane(t, 1<<20, widgetsSource(), sink, ws)

	health := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/__mocker/health", nil)
	healthRec := httptest.NewRecorder()
	p.ServeHTTP(healthRec, health)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", healthRec.Code)
	}

	preflight := httptest.NewRequest(http.MethodOptions, "http://alex.mock.local/widgets", nil)
	preflight.Header.Set("Origin", "https://app.example")
	preflight.Header.Set("Access-Control-Request-Method", "GET")
	preflightRec := httptest.NewRecorder()
	p.ServeHTTP(preflightRec, preflight)
	if preflightRec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", preflightRec.Code)
	}

	if got := len(sink.all()); got != 0 {
		t.Fatalf("events = %d, want 0 (reserved prefix and preflight must never be recorded)", got)
	}
}

// TestCaptureTraffic_ResponseBodyCappedButClientReceivesFull covers the
// fourth required case, and the one this stage's own task calls "the worst
// possible bug here": the stored copy is capped and marked truncated, but
// the CLIENT still gets the full, uncapped body. A very long unmatched path
// segment inflates serveNoRoute's own 404 message past a tiny cap without
// this test needing any generator/schema machinery at all.
func TestCaptureTraffic_ResponseBodyCappedButClientReceivesFull(t *testing.T) {
	sink := &fakeTrafficSink{}
	ws := widgetsWorkspace(7, domain.DefaultSettings())
	const bodyCap = 32
	p := trafficPlane(t, bodyCap, widgetsSource(), sink, ws)

	longSegment := strings.Repeat("x", 500)
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/"+longSegment, nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	fullBody := rec.Body.Bytes()
	if len(fullBody) <= bodyCap {
		t.Fatalf("fixture too small: full body is %d bytes, want > cap %d", len(fullBody), bodyCap)
	}

	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	ev := events[0]
	if !ev.Truncated {
		t.Errorf("Truncated = false, want true")
	}
	if len(ev.RespBody) != bodyCap {
		t.Errorf("captured RespBody = %d bytes, want exactly the cap (%d)", len(ev.RespBody), bodyCap)
	}
	if string(ev.RespBody) != string(fullBody[:bodyCap]) {
		t.Errorf("captured RespBody is not a verbatim prefix of what the client received")
	}
}

// TestCaptureTraffic_AuthPathSuppressesBothBodies covers the fifth required
// case: a request under a DESIGN §10 trigger segment never has either body
// stored, regardless of routing outcome — a typo'd password belongs nowhere
// near this table.
func TestCaptureTraffic_AuthPathSuppressesBothBodies(t *testing.T) {
	sink := &fakeTrafficSink{}
	ws := widgetsWorkspace(7, domain.DefaultSettings())
	p := trafficPlane(t, 1<<20, loginSource(), sink, ws)

	req := httptest.NewRequest(http.MethodPost, "http://alex.mock.local/auth/login",
		strings.NewReader(`{"password":"hunter2"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if !events[0].SuppressBodies {
		t.Errorf("SuppressBodies = false, want true for an auth-triggering path")
	}
}

// TestCaptureTraffic_AuthCheckStripsWorkspaceBasePathFirst is the regression
// this stage's own task calls out by name: a workspace whose OWN base path
// contains a trigger segment ("/api/auth") must not suppress every body in
// the workspace — only a REQUEST path whose trigger segment survives base-
// path stripping does.
func TestCaptureTraffic_AuthCheckStripsWorkspaceBasePathFirst(t *testing.T) {
	sink := &fakeTrafficSink{}
	settings := domain.DefaultSettings()
	settings.BasePath = "/api/auth"
	ws := widgetsWorkspace(7, settings)
	p := trafficPlane(t, 1<<20, widgetsSource(), sink, ws)

	// widgetsRoute's own Path is relative ("/widgets"); router.Build glues
	// the workspace's base path onto it for matching (runtime.go), so the
	// request itself must carry the base path too.
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/api/auth/widgets", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].SuppressBodies {
		t.Errorf("SuppressBodies = true, want false: the trigger segment is the workspace's OWN base path, not the request's real path")
	}
}

// TestCaptureTraffic_AuthCheckStripsParameterisedBasePath is P17b
// (mocker-p3h-basepath D12/D14): the sibling case
// TestCaptureTraffic_AuthCheckStripsWorkspaceBasePathFirst does not cover —
// a workspace whose base path itself carries a {param} segment
// ("/orgs/{orgId}/auth"), where a real request's segments never equal
// basePath's own literal text at the param's position ("7" != "{orgId}").
// authCheckPath's mutation is restoring the plain slices.Equal comparison
// it replaced; under that mutation the base path fails to strip at all (its
// own {orgId} segment can never equal a real request's "7"), the "auth"
// trigger segment from basePath itself survives into what authCheckPath
// hands [authpreset.IsAuthPath], and the request below is wrongly
// suppressed even though its own relative path ("/widgets") carries no
// trigger segment at all — the exact false positive this property exists
// to catch.
func TestCaptureTraffic_AuthCheckStripsParameterisedBasePath(t *testing.T) {
	sink := &fakeTrafficSink{}
	settings := domain.DefaultSettings()
	settings.BasePath = "/orgs/{orgId}/auth"
	ws := widgetsWorkspace(7, settings)
	p := trafficPlane(t, 1<<20, widgetsSource(), sink, ws)

	// widgetsRoute's own Path is relative ("/widgets"); router.Build glues
	// the workspace's base path onto it for matching (runtime.go), so the
	// compiled route is "/orgs/{orgId}/auth/widgets" and the request must
	// supply a real value ("7") at the {orgId} position.
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/orgs/7/auth/widgets", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].SuppressBodies {
		t.Errorf("SuppressBodies = true, want false: the trigger segment is inside the workspace's OWN parameterised base path, and it must still strip positionally rather than degrade to a literal, always-failing comparison")
	}
}

// TestTrafficWriter_ImplicitWriteHeader_Records200 covers the sixth required
// case directly against trafficWriter, since every production path this
// phase adds always calls WriteHeader explicitly (respond.go's own doc
// comment enumerates every branch) — this is the one place the "reuse
// httpx.StatusRecorder rather than reimplement it" contract is actually
// exercised for a handler that never calls WriteHeader itself.
func TestTrafficWriter_ImplicitWriteHeader_Records200(t *testing.T) {
	rec := httptest.NewRecorder()
	tw := newTrafficWriter(rec, 1<<10, false)

	if _, err := tw.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if tw.Status != http.StatusOK {
		t.Errorf("Status = %d, want 200 (the implicit status net/http itself would answer with)", tw.Status)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("client-visible status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "hello" {
		t.Errorf("client body = %q, want %q", got, "hello")
	}
}

// TestTrafficWriter_HEAD_SkipsBodyCopy is respond.go's own HEAD trap, tested
// directly against the tee: a headWriter downstream (plane.go) already
// discards every byte it is asked to write while still reporting success,
// so a naive copy taken from Write's own argument would record a body that
// was never actually sent — DESIGN §8's "HEAD answers with no body" would
// otherwise leak into the traffic table even though it never reached the
// wire.
func TestTrafficWriter_HEAD_SkipsBodyCopy(t *testing.T) {
	rec := httptest.NewRecorder()
	head := &headWriter{ResponseWriter: rec}
	tw := newTrafficWriter(head, 1<<10, true)

	if _, err := tw.Write([]byte("a body that must never be recorded")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(tw.captured) != 0 {
		t.Errorf("captured = %d bytes, want 0 for HEAD", len(tw.captured))
	}
	if rec.Body.Len() != 0 {
		t.Errorf("client body = %q, want empty (headWriter's own contract)", rec.Body.String())
	}
}

// TestCaptureTraffic_NoSinkWired_BehavesExactlyAsBefore covers the seventh
// required case: with no [TrafficSink] wired, serving is unaffected — no
// panic, no wrapping, the identical status codes this package's pre-P1c2
// tests already assert.
func TestCaptureTraffic_NoSinkWired_BehavesExactlyAsBefore(t *testing.T) {
	ws := widgetsWorkspace(7, domain.DefaultSettings())
	p := trafficPlane(t, 1<<20, widgetsSource(), nil, ws) // SetTraffic never called

	matched := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
	matchedRec := httptest.NewRecorder()
	p.ServeHTTP(matchedRec, matched)
	if matchedRec.Code != http.StatusOK {
		t.Fatalf("matched status = %d, want 200; body=%s", matchedRec.Code, matchedRec.Body)
	}

	unmatched := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/nope", nil)
	unmatchedRec := httptest.NewRecorder()
	p.ServeHTTP(unmatchedRec, unmatched)
	if unmatchedRec.Code != http.StatusNotFound {
		t.Fatalf("unmatched status = %d, want 404; body=%s", unmatchedRec.Code, unmatchedRec.Body)
	}
}

// TestCaptureTraffic_PauseRefused_ServesNormallyAndNotesIt is DESIGN §14
// rule 7 through the full serving path — the traffic.go half of P2a's own
// task: a Refused park (the workspace already held MaxPausedPerWorkspace
// parked requests) is served normally with no wait at all, and the ONLY
// place that shows up is this row's own Notes, via markPauseRefused
// (traffic.go) and the trafficMatch this file's own doc comment describes
// as the sole channel from the serving path up to the recorded [traffic.Event].
func TestCaptureTraffic_PauseRefused_ServesNormallyAndNotesIt(t *testing.T) {
	sink := &fakeTrafficSink{}
	ws := widgetsWorkspace(7, domain.DefaultSettings())
	p := trafficPlane(t, 1<<20, widgetsSource(), sink, ws)

	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/widgets"}, Action: livestate.ActionPause,
	}); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	// Fill every park slot directly against the Store — Apply itself never
	// blocks (reserveParkLocked only increments a counter under its own
	// lock), so this needs no goroutines and no real waiting at all.
	for i := range livestate.MaxPausedPerWorkspace {
		eff := store.Apply(ws.ID, "GET", "/widgets")
		if !eff.Pause {
			t.Fatalf("park %d: Apply.Pause = false, want true", i)
		}
	}
	p.SetLiveState(store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	p.ServeHTTP(rec, req)
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("elapsed = %v, want effectively instant — a Refused park must never wait", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (served normally despite the refusal); body=%s", rec.Code, rec.Body)
	}

	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Notes != notePauseRefused {
		t.Errorf("Notes = %q, want %q", events[0].Notes, notePauseRefused)
	}
}

// TestCaptureTraffic_ConcurrentRequests_Race is the eighth required case:
// -race against many requests sharing one Plane and one sink, proving
// trafficMatch's per-request context value and the sink's own locking are
// enough — nothing here should ever touch shared, unsynchronized state.
func TestCaptureTraffic_ConcurrentRequests_Race(t *testing.T) {
	sink := &fakeTrafficSink{}
	ws := widgetsWorkspace(7, domain.DefaultSettings())
	p := trafficPlane(t, 1<<20, widgetsSource(), sink, ws)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, req)
		}()
	}
	wg.Wait()

	if got := len(sink.all()); got != n {
		t.Errorf("events = %d, want %d", got, n)
	}
}
