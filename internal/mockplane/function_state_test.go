// function_state_test.go is A23's black-box half of the serving branch: a
// custom endpoint whose function keeps state across requests through
// mock.state. It runs the REAL luastate store over SQLite — the round trip
// the feedback asked for ("POST assigned → GET shows" for a route in no
// spec) is a database question, and a fake would have proved only the seam.
package mockplane_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/luastate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/testkit"
)

// stateFunction counts its own hits: the canonical stateful-mock shape —
// read, mutate, store, answer.
const stateFunction = `
local n = mock.state.get("hits") or 0
n = n + 1
mock.state.set("hits", n)
return 200, { seen = n }
`

func TestServeCustom_statePersistsAcrossRequests(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{1: {}}}
	row := customRow(1, http.MethodGet, "/counter", 1)
	row.Responses["200"] = overrides.Variant{Function: stateFunction}

	db := testkit.NewDB(t)
	if _, err := db.W.ExecContext(t.Context(), `
		INSERT INTO workspaces (id, slug, name, revision, settings, created_at, updated_at)
		VALUES (1, 'alex', 'alex', 1, '{}', unixepoch(), unixepoch())`); err != nil {
		t.Fatal(err)
	}

	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))
	p.SetCustomEndpoints(&fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}})
	p.SetState(luastate.NewRepo(db))

	get := func() string {
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/counter", nil)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	for i, want := range []string{`{"seen":1}`, `{"seen":2}`, `{"seen":3}`} {
		if got := get(); got != want {
			t.Fatalf("request %d = %s, want %s — the whole point of mock.state", i+1, got, want)
		}
	}
}

// TestServeCustom_unwiredStateAnswersNoHost is the nil-degradation contract:
// a Plane without SetState keeps serving, the three verbs answer the
// preview's own word, and every other part of the function still runs.
func TestServeCustom_unwiredStateAnswersNoHost(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{1: {}}}
	row := customRow(1, http.MethodGet, "/sign-in", 1)
	row.Responses["200"] = overrides.Variant{Function: `
local _, err = mock.state.get("k")
return 200, { err = err }
`}
	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))
	p.SetCustomEndpoints(&fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}})

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/sign-in", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s — degradation, never a panic", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "no_host") {
		t.Fatalf("body = %s, want the preview's own word", rec.Body)
	}
}
