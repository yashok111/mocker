// livestate_test.go is a BLACK-BOX test file (package mockplane_test,
// exactly like plane_test.go): everything it exercises — the
// GET/POST/DELETE {prefix}/state surface — is reached through
// [mockplane.Plane.ServeHTTP], the same entry point a real client uses.
// [mockplane.Plane.SetLiveState] is exported specifically so a caller
// outside this package can wire a *livestate.Store onto a Plane it already
// has, which is exactly what these tests do with [newPlane] from
// plane_test.go.
package mockplane_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/scenarios"
	"github.com/yashok111/mocker/internal/workspaces"
)

// liveStateWorkspace is the one workspace every test below addresses.
func liveStateWorkspace() *workspaces.Workspace {
	return &workspaces.Workspace{ID: 7, Slug: "alex", Settings: domain.DefaultSettings()}
}

// postState issues POST {prefix}/state with body as the raw JSON payload.
func postState(p httpHandler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://alex.mock.local/__mocker/state", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

// getState issues GET {prefix}/state.
func getState(p httpHandler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/__mocker/state", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

// deleteState issues DELETE {prefix}/state.
func deleteState(p httpHandler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "http://alex.mock.local/__mocker/state", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

// httpHandler is the one method these helpers need — satisfied by
// *mockplane.Plane, named locally so the helpers don't have to import
// mockplane just to spell the type twice.
type httpHandler interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}

func TestServeHTTP_LiveState_NoSourceWired(t *testing.T) {
	p := newPlane(liveStateWorkspace())

	rec := getState(p)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET with no LiveStateSource: status = %d, want 503; body=%s", rec.Code, rec.Body)
	}
	body := decodeJSON(t, rec.Body.Bytes())
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "service_unavailable" {
		t.Errorf("code = %v, want service_unavailable", errObj["code"])
	}
}

func TestServeHTTP_LiveState_EmptyListIsAnArrayNotNull(t *testing.T) {
	p := newPlane(liveStateWorkspace())
	p.SetLiveState(livestate.NewStore(0, nil))

	rec := getState(p)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"directives":[]`)) {
		t.Errorf("body = %s, want an empty ARRAY (never null) for a workspace with nothing set", rec.Body)
	}
}

// TestServeHTTP_LiveState_PostGetDelete drives the full read/write/clear
// cycle DESIGN §14 names for both the "*" and the {method,path} target
// shapes, and proves GET/POST both answer the FULL directive list — the
// digest's own wording — never merely the one directive just written.
func TestServeHTTP_LiveState_PostGetDelete(t *testing.T) {
	p := newPlane(liveStateWorkspace())
	p.SetLiveState(livestate.NewStore(0, nil))

	rec := postState(p, `{"target":{"method":"POST","path":"/auth/login"},"action":"status","status":503}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST #1: status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	rec = postState(p, `{"target":"*","action":"fail","status":500,"n":2}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST #2: status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var posted liveStateListBody
	decode(t, rec.Body.Bytes(), &posted)
	if posted.Workspace != "alex" {
		t.Errorf("workspace = %q, want alex", posted.Workspace)
	}
	if len(posted.Directives) != 2 {
		t.Fatalf("POST #2 response carries %d directives, want the FULL list of 2 (both directives set so far); body=%s",
			len(posted.Directives), rec.Body)
	}

	rec = getState(p)
	var got liveStateListBody
	decode(t, rec.Body.Bytes(), &got)
	if len(got.Directives) != 2 {
		t.Fatalf("GET after two POSTs: %d directives, want 2; body=%s", len(got.Directives), rec.Body)
	}

	rec = deleteState(p)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE: status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var cleared struct {
		Workspace string `json:"workspace"`
		Cleared   int    `json:"cleared"`
	}
	decode(t, rec.Body.Bytes(), &cleared)
	if cleared.Cleared != 2 {
		t.Errorf("cleared = %d, want 2", cleared.Cleared)
	}

	rec = getState(p)
	decode(t, rec.Body.Bytes(), &got)
	if len(got.Directives) != 0 {
		t.Errorf("GET after DELETE: %d directives, want 0 — DELETE must not leave anything for the next test to trip over", len(got.Directives))
	}
}

// TestServeHTTP_LiveState_PostRemainderIsReturned proves the "n going OUT is
// the REMAINDER" contract: List (and therefore what POST/GET answer) must
// read the counter from the SAME place Apply consumes it, not echo back
// whatever n the client originally sent.
func TestServeHTTP_LiveState_PostRemainderIsReturned(t *testing.T) {
	store := livestate.NewStore(0, nil)
	ws := liveStateWorkspace()
	p := newPlane(ws)
	p.SetLiveState(store)

	postState(p, `{"target":{"method":"GET","path":"/widgets"},"action":"fail","status":500,"n":3}`)

	// Consume one via the SAME Store the router itself would use.
	store.Apply(ws.ID, "GET", "/widgets")

	rec := getState(p)
	var got liveStateListBody
	decode(t, rec.Body.Bytes(), &got)
	if len(got.Directives) != 1 {
		t.Fatalf("directives = %d, want 1", len(got.Directives))
	}
	if got.Directives[0].N != 2 {
		t.Errorf("n = %d, want 2 (the remainder after one Apply), not 3 (what was originally posted)", got.Directives[0].N)
	}
}

// stubScenarioSource is [mockplane.ScenarioSource] for the switch tests: a
// name table, and a record of every SetActive argument in order. The record
// is what makes "an unknown name changes NOTHING" assertable — a 404 alone
// is satisfied by a handler that activated something first and then
// answered 404, and by one that never looked a name up at all.
type stubScenarioSource struct {
	byName   map[string]*scenarios.Scenario
	revision int64

	setActive []*int64
}

func (s *stubScenarioSource) Get(_ context.Context, _, _ int64) (*scenarios.Scenario, error) {
	return nil, scenarios.ErrNotFound // unused by {prefix}/state; buildRuntime's path has its own tests
}

func (s *stubScenarioSource) ByName(_ context.Context, _ int64, name string) (*scenarios.Scenario, error) {
	sc, ok := s.byName[name]
	if !ok {
		return nil, scenarios.ErrNotFound
	}
	return sc, nil
}

func (s *stubScenarioSource) SetActive(_ context.Context, _ int64, scenarioID *int64) (int64, error) {
	s.setActive = append(s.setActive, scenarioID)
	s.revision++
	return s.revision, nil
}

// scenarioSwitchBody is the wire shape a successful switch answers with —
// deliberately not the directive list, since a scenario is not a directive
// (livestate.go's scenarioSwitchResponse doc comment).
type scenarioSwitchBody struct {
	Workspace string  `json:"workspace"`
	Scenario  *string `json:"scenario"`
	Revision  int64   `json:"revision"`
}

// TestServeHTTP_LiveState_ScenarioSwitchesByName replaces
// TestServeHTTP_LiveState_ScenarioStillNotImplemented, which asserted the
// 501 this slice deletes. That test was the P2a survivor of an older bundled
// "delay/pause/scenario are all 501" case, and its premise — «scenario is
// the one action still named 501 rather than accepted» — is now false: the
// scenario layer exists, so "arrives in a later phase" would be a lie the
// wire tells every client. Delay and pause lost the same 501 in P2a; see
// TestServeHTTP_LiveState_DelayAndPauseAreAccepted below.
//
// What replaces it is the round trip DESIGN §12 actually asks for. The
// scenario key is still a TOP-LEVEL key on this endpoint and still never
// reaches the live-state Store (a scenario is a row in SQLite; the session
// layer is RAM) — only the answer changed.
func TestServeHTTP_LiveState_ScenarioSwitchesByName(t *testing.T) {
	const scenarioID = int64(11)

	newSwitchPlane := func() (*mockplane.Plane, *stubScenarioSource) {
		src := &stubScenarioSource{byName: map[string]*scenarios.Scenario{
			"loaded": {ID: scenarioID, WorkspaceID: 7, Name: "loaded"},
		}}
		p := newPlane(liveStateWorkspace())
		p.SetLiveState(livestate.NewStore(0, nil))
		p.SetScenarios(src)
		return p, src
	}

	t.Run("a name activates that scenario", func(t *testing.T) {
		p, src := newSwitchPlane()

		rec := postState(p, `{"scenario":"loaded"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		var got scenarioSwitchBody
		decode(t, rec.Body.Bytes(), &got)
		if got.Workspace != "alex" || got.Scenario == nil || *got.Scenario != "loaded" {
			t.Errorf("body = %+v, want workspace alex and scenario %q", got, "loaded")
		}
		if got.Revision == 0 {
			t.Error("revision = 0, want the revision SetActive returned — it is what proves the write landed and the runtime cache will miss")
		}
		if len(src.setActive) != 1 || src.setActive[0] == nil || *src.setActive[0] != scenarioID {
			t.Fatalf("SetActive calls = %v, want exactly one carrying scenario id %d (resolved BY NAME)", src.setActive, scenarioID)
		}
	})

	t.Run("the empty string deactivates", func(t *testing.T) {
		p, src := newSwitchPlane()

		rec := postState(p, `{"scenario":""}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		var got scenarioSwitchBody
		decode(t, rec.Body.Bytes(), &got)
		if got.Scenario != nil {
			t.Errorf("scenario = %q, want JSON null — a deactivation names nothing", *got.Scenario)
		}
		if len(src.setActive) != 1 || src.setActive[0] != nil {
			t.Fatalf("SetActive calls = %v, want exactly one carrying nil (deactivate)", src.setActive)
		}
	})

	t.Run("an explicit null is not a switch at all", func(t *testing.T) {
		p, src := newSwitchPlane()

		// livestate.Directive.HasScenario exists for exactly this: a client
		// that emits every field it knows about sends "scenario":null on an
		// ordinary status directive, and that must set the directive, not
		// deactivate the workspace's scenario. If null and "" ever collapse
		// into one meaning, this is the case that goes red.
		rec := postState(p, `{"target":"*","action":"status","status":503,"scenario":null}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		if len(src.setActive) != 0 {
			t.Fatalf("SetActive calls = %v, want none — an explicit null means \"no scenario key at all\"", src.setActive)
		}
		var list liveStateListBody
		decode(t, rec.Body.Bytes(), &list)
		if len(list.Directives) != 1 {
			t.Errorf("directives = %d, want the status directive to have been set normally", len(list.Directives))
		}
	})

	t.Run("an unknown name is a 404 that changes nothing", func(t *testing.T) {
		p, src := newSwitchPlane()

		rec := postState(p, `{"scenario":"never-saved"}`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
		}
		body := decodeJSON(t, rec.Body.Bytes())
		errObj, _ := body["error"].(map[string]any)
		// Not "not_implemented_yet": anything unknown under the reserved
		// prefix already answers 404 with THAT code (plane.go's
		// serveReserved), so a handler that never looked a name up would be
		// indistinguishable from this one on status alone.
		if errObj["code"] != "not_found" {
			t.Errorf("code = %v, want not_found (and specifically NOT not_implemented_yet, which is what an unrouted reserved path answers)", errObj["code"])
		}
		if len(src.setActive) != 0 {
			t.Fatalf("SetActive calls = %v, want none — an unknown name must move nothing", src.setActive)
		}
	})

	t.Run("a non-string scenario is a 400", func(t *testing.T) {
		p, src := newSwitchPlane()

		// This exact body used to answer 501. It is now a 400: the key
		// carries a NAME, and a client sending an object gets told so
		// instead of being told the feature does not exist yet.
		rec := postState(p, `{"target":"*","action":"status","status":200,"scenario":{"name":"x"}}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
		}
		body := decodeJSON(t, rec.Body.Bytes())
		errObj, _ := body["error"].(map[string]any)
		if errObj["code"] != "bad_request" {
			t.Errorf("code = %v, want bad_request", errObj["code"])
		}
		if len(src.setActive) != 0 {
			t.Fatalf("SetActive calls = %v, want none", src.setActive)
		}
	})

	t.Run("no ScenarioSource wired answers 503, never the old 501", func(t *testing.T) {
		p := newPlane(liveStateWorkspace())
		p.SetLiveState(livestate.NewStore(0, nil))
		// SetScenarios deliberately not called.

		rec := postState(p, `{"scenario":"loaded"}`)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body)
		}
		body := decodeJSON(t, rec.Body.Bytes())
		errObj, _ := body["error"].(map[string]any)
		if errObj["code"] != "service_unavailable" {
			t.Errorf("code = %v, want service_unavailable — the endpoint exists, the feature is simply not wired here", errObj["code"])
		}
	})
}

// TestServeHTTP_LiveState_DelayAndPauseAreAccepted pins P2a's actual change:
// serveLiveStatePost no longer hard-codes 501 for "delay" or "pause" before
// ever reaching the Store (this file's own header, and
// TestServeHTTP_LiveState_ScenarioStillNotImplemented above, prove scenario
// is the only action left doing that). Both directives now reach
// [livestate.Store.Set] and come back out of GET — a second, independent
// request against the SAME Store, not just an echo of the POST body — with
// the exact wire shape §A pins: Status and Ms both carry `,omitempty`, so a
// delay directive's "status" key and a pause directive's "status" AND "ms"
// keys are ABSENT on the wire, never rendered as "0".
func TestServeHTTP_LiveState_DelayAndPauseAreAccepted(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantAbsent  []string // wire keys that must be absent (omitempty at zero)
		wantPresent string   // a substring that must be present, proving the field round-tripped
	}{
		{
			name:        "delay",
			body:        `{"target":{"method":"GET","path":"/widgets"},"action":"delay","ms":300}`,
			wantAbsent:  []string{`"status"`},
			wantPresent: `"ms":300`,
		},
		{
			name:        "pause",
			body:        `{"target":{"method":"GET","path":"/widgets"},"action":"pause"}`,
			wantAbsent:  []string{`"status"`, `"ms"`},
			wantPresent: `"action":"pause"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlane(liveStateWorkspace())
			p.SetLiveState(livestate.NewStore(0, nil))

			rec := postState(p, tt.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("POST %s: status = %d, want 200; body=%s", tt.name, rec.Code, rec.Body)
			}
			for _, absent := range tt.wantAbsent {
				if bytes.Contains(rec.Body.Bytes(), []byte(absent)) {
					t.Errorf("POST %s response = %s, must NOT contain %s (omitempty at zero)", tt.name, rec.Body, absent)
				}
			}
			if !bytes.Contains(rec.Body.Bytes(), []byte(tt.wantPresent)) {
				t.Errorf("POST %s response = %s, want it to contain %s", tt.name, rec.Body, tt.wantPresent)
			}

			// GET reflects the SAME directive out of the SAME Store — proving
			// Set actually stored it rather than the handler merely echoing
			// the POST's own body back unstored.
			rec = getState(p)
			var got liveStateListBody
			decode(t, rec.Body.Bytes(), &got)
			if len(got.Directives) != 1 {
				t.Fatalf("GET after POST %s: %d directives, want 1; body=%s", tt.name, len(got.Directives), rec.Body)
			}
			if got.Directives[0].Action != livestate.Action(tt.name) {
				t.Errorf("GET after POST %s: action = %q, want %q", tt.name, got.Directives[0].Action, tt.name)
			}
		})
	}
}

// TestServeHTTP_LiveState_DelayPauseWireBoundsRejected proves neither
// handler validates delay/pause's field rules on its own: a delay carrying a
// non-zero status, or a pause carrying a non-zero ms, is rejected 400 by
// [livestate.Store.Set] (through normalize) exactly like every other
// malformed directive in TestServeHTTP_LiveState_BadRequest — this handler
// removed its own hard-coded 501 for these two actions but added no bounds
// check of its own.
func TestServeHTTP_LiveState_DelayPauseWireBoundsRejected(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"delay carrying a non-zero status", `{"target":{"method":"GET","path":"/widgets"},"action":"delay","ms":300,"status":200}`},
		{"pause carrying a non-zero ms", `{"target":{"method":"GET","path":"/widgets"},"action":"pause","ms":5}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlane(liveStateWorkspace())
			p.SetLiveState(livestate.NewStore(0, nil))

			rec := postState(p, tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
			}
		})
	}
}

// TestServeHTTP_LiveState_ScenarioNullIsNotAScenarioRequest proves the trap
// [livestate.Directive.HasScenario]'s own doc comment names by example: a
// client that always emits every field it knows about sends
// "scenario":null for "I am not asking for a scenario switch", and that
// must not divert an otherwise valid directive into the scenario branch.
// Once that branch answered 501; now it writes to SQLite, which makes the
// distinction more load-bearing, not less — see the "an explicit null is
// not a switch at all" case of TestServeHTTP_LiveState_ScenarioSwitchesByName
// for the stronger form, which wires a source and proves SetActive is never
// reached. This one keeps the no-source variant: null must not even get as
// far as noticing there is no source.
func TestServeHTTP_LiveState_ScenarioNullIsNotAScenarioRequest(t *testing.T) {
	p := newPlane(liveStateWorkspace())
	p.SetLiveState(livestate.NewStore(0, nil))

	rec := postState(p, `{"target":"*","action":"status","status":200,"scenario":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (scenario:null is not a scenario request); body=%s", rec.Code, rec.Body)
	}
}

func TestServeHTTP_LiveState_BadRequest(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"status out of range", `{"target":"*","action":"status","status":999}`},
		{"fail with n<=0 and no once", `{"target":"*","action":"fail","status":500,"n":0}`},
		{"unknown action", `{"target":"*","action":"bogus","status":200}`},
		{"malformed JSON", `{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlane(liveStateWorkspace())
			p.SetLiveState(livestate.NewStore(0, nil))

			rec := postState(p, tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
			}
		})
	}
}

// TestServeHTTP_LiveState_TooManyDirectivesIs409 proves
// [livestate.ErrTooManyDirectives] maps to 409 conflict, per the wire
// contract this file's own header pins down for both planes.
func TestServeHTTP_LiveState_TooManyDirectivesIs409(t *testing.T) {
	p := newPlane(liveStateWorkspace())
	p.SetLiveState(livestate.NewStore(0, nil))

	for i := range livestate.MaxDirectivesPerWorkspace {
		body := `{"target":{"method":"GET","path":"/w` + string(rune('a'+i%26)) + string(rune('0'+i/26)) + `"},"action":"status","status":200}`
		rec := postState(p, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("directive %d: status = %d, want 200; body=%s", i, rec.Code, rec.Body)
		}
	}

	rec := postState(p, `{"target":{"method":"GET","path":"/one-too-many"},"action":"status","status":200}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body)
	}
	body := decodeJSON(t, rec.Body.Bytes())
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "conflict" {
		t.Errorf("code = %v, want conflict", errObj["code"])
	}
}

// TestServeHTTP_LiveState_TargetPathTooLongIs400 is round-1 review finding 4:
// before this fix, normalize() bounded Status and the fail counter but never
// Target.Path's LENGTH, so an unauthenticated caller could pin an
// arbitrarily large string in RAM per directive (measured: 63 one-MiB-path
// directives retained ~63.5 MiB, entirely below MOCKER_MAX_BODY's own 10 MB
// default). A path over livestate's own bound must be refused with 400
// before it ever reaches the Store, exactly like every other malformed
// directive in TestServeHTTP_LiveState_BadRequest.
func TestServeHTTP_LiveState_TargetPathTooLongIs400(t *testing.T) {
	p := newPlane(liveStateWorkspace())
	p.SetLiveState(livestate.NewStore(0, nil))

	// Comfortably over the 2 KiB bound, comfortably under
	// livestate.MaxDirectiveBodyBytes — this proves the LENGTH check, not
	// the transport-level MaxBytesReader cap the next test covers.
	longPath := "/" + strings.Repeat("a", 3000)
	body := `{"target":{"method":"GET","path":"` + longPath + `"},"action":"status","status":200}`

	rec := postState(p, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
}

// TestServeHTTP_LiveState_BodyOverCapIs400 is round-1 review finding 4's
// other half: the transport-level cap. A body bigger than
// [livestate.MaxDirectiveBodyBytes] — padded via a field Directive's own
// decode simply ignores, so this proves the CAP fires, not the (unrelated)
// unknown-field handling — must be rejected before json.Decode ever reads
// all of it, via http.MaxBytesReader wrapping r.Body.
func TestServeHTTP_LiveState_BodyOverCapIs400(t *testing.T) {
	p := newPlane(liveStateWorkspace())
	p.SetLiveState(livestate.NewStore(0, nil))

	padding := strings.Repeat("a", livestate.MaxDirectiveBodyBytes+1024)
	body := `{"target":{"method":"GET","path":"/x"},"action":"status","status":200,"padding":"` + padding + `"}`

	rec := postState(p, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
}

// TestServeHTTP_LiveState_LogsPeerAddress proves the digest's own
// requirement — "it logs each directive with the peer address
// (httpx.ResolvePeer, as the rest of the plane does)" — against a real log
// line, not just the HTTP response.
func TestServeHTTP_LiveState_LogsPeerAddress(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	src := &fakeSource{bySlug: map[string]*workspaces.Workspace{"alex": liveStateWorkspace()}}
	p := mockplane.New(testConfig(), src, nil, log)
	p.SetLiveState(livestate.NewStore(0, nil))

	rec := postState(p, `{"target":"*","action":"status","status":200}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	logged := buf.String()
	if !bytes.Contains([]byte(logged), []byte("live-state directive set")) {
		t.Errorf("log = %q, want a line naming the directive being set", logged)
	}
	// httptest.NewRequest's default RemoteAddr is 192.0.2.1:1234 — its host
	// half is what httpx.ResolvePeer reports as Peer.String().
	if !bytes.Contains([]byte(logged), []byte("192.0.2.1")) {
		t.Errorf("log = %q, want the peer address in it", logged)
	}
}

// liveStateListBody mirrors the wire shape GET/POST answer with, decoded
// loosely enough that this test file does not need to import mockplane's
// own unexported response type.
type liveStateListBody struct {
	Workspace  string                `json:"workspace"`
	Directives []livestate.Directive `json:"directives"`
}

func decode(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode JSON body %q: %v", body, err)
	}
}

// deleteStateWithBody is A13's DELETE {prefix}/state with a body naming one
// target (and optionally one action).
func deleteStateWithBody(p httpHandler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "http://alex.mock.local/__mocker/state", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

func TestServeHTTP_LiveState_DeleteOneTarget(t *testing.T) {
	p := newPlane(liveStateWorkspace())
	p.SetLiveState(livestate.NewStore(0, nil))
	postState(p, `{"target":{"method":"GET","path":"/orders"},"action":"status","status":500}`)
	postState(p, `{"target":{"method":"GET","path":"/orders"},"action":"delay","ms":5}`)
	postState(p, `{"target":"*","action":"fail","status":503,"n":1}`)

	var cleared struct {
		Workspace string `json:"workspace"`
		Cleared   int    `json:"cleared"`
	}
	rec := deleteStateWithBody(p, `{"target":{"method":"get","path":"/orders"},"action":"delay"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE one action: status = %d; body=%s", rec.Code, rec.Body)
	}
	decode(t, rec.Body.Bytes(), &cleared)
	if cleared.Cleared != 1 || cleared.Workspace != "alex" {
		t.Errorf("one action: %+v, want cleared 1 on alex", cleared)
	}
	rec = deleteStateWithBody(p, `{"target":{"method":"GET","path":"/orders"}}`)
	decode(t, rec.Body.Bytes(), &cleared)
	if cleared.Cleared != 1 {
		t.Errorf("whole target: cleared = %d, want the remaining 1", cleared.Cleared)
	}
	var got liveStateListBody
	decode(t, getState(p).Body.Bytes(), &got)
	if len(got.Directives) != 1 || !got.Directives[0].Target.All {
		t.Errorf("after two deletes: %+v, want only the * directive", got.Directives)
	}

	// A body with no target is refused, never read as "everything".
	if rec := deleteStateWithBody(p, `{"action":"fail"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("DELETE without target: status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	if rec := deleteStateWithBody(p, `{"target":"*","action":"nope"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("DELETE unknown action: status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	decode(t, getState(p).Body.Bytes(), &got)
	if len(got.Directives) != 1 {
		t.Errorf("a refused DELETE removed something: %+v", got.Directives)
	}
	// An empty body is still the clear-all.
	rec = deleteState(p)
	decode(t, rec.Body.Bytes(), &cleared)
	if cleared.Cleared != 1 {
		t.Errorf("bodyless DELETE cleared %d, want 1", cleared.Cleared)
	}
}
