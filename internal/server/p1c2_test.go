package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/server"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/traffic"
	"github.com/yashok111/mocker/internal/workspaces"
)

// p1c2InlineSpec is this slice's own self-contained OAS 3.0 document —
// deliberately NOT the acceptance corpus, for the same reason p1cInlineSpec
// (p1c_test.go) is not: when it was written internal/testspec SKIPPED on a
// fresh clone, and DESIGN §19 line 1111's criterion had to stay provable
// there.
//
// It declares exactly what the task's numbered assertions need and nothing
// else:
//   - GET /widgets: query params limit/offset (pagination) AND status —
//     status is ALSO a property of Widget, with a THREE-value enum and no
//     "example", so different list items land on different values
//     (internal/gen's enumValue hashes the item's own index into the enum —
//     a single-valued enum or a pinned example would make every item
//     identical and the filtering assertion would pass for nothing).
//   - GET /widgets/{widgetId}: the detail route of the same family, same
//     Widget schema — list/card equivalence (assertion 1) is asserted
//     field-by-field against this route's own response.
//   - POST /widgets: a 200 and a 409, so a when[] override (assertion 4)
//     has two statuses to choose between.
//   - GET /auth/me: an auth-trigger path (segments "auth" and "me" both
//     match authpreset.authTriggerSegments) with no counterpart write
//     route — traffic.go's suppression check runs on the PATH alone, so a
//     POST to this same path (assertion 7) is suppressed even though it
//     never matches this GET-only operation.
const p1c2InlineSpec = `{
  "openapi": "3.0.3",
  "info": { "title": "P1c2 smoke spec", "version": "1.0.0" },
  "paths": {
    "/widgets": {
      "get": {
        "operationId": "listWidgets",
        "parameters": [
          { "name": "limit", "in": "query", "schema": { "type": "integer" } },
          { "name": "offset", "in": "query", "schema": { "type": "integer" } },
          { "name": "status", "in": "query", "schema": { "type": "string" } }
        ],
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": { "$ref": "#/components/schemas/Widget" }
                }
              }
            }
          }
        }
      },
      "post": {
        "operationId": "createWidget",
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "properties": { "name": { "type": "string" } },
                "required": ["name"]
              }
            }
          }
        },
        "responses": {
          "200": {
            "description": "created",
            "content": {
              "application/json": { "schema": { "$ref": "#/components/schemas/Widget" } }
            }
          },
          "409": {
            "description": "conflict",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": { "error": { "type": "string" } }
                }
              }
            }
          }
        }
      }
    },
    "/widgets/{widgetId}": {
      "get": {
        "operationId": "getWidget",
        "parameters": [
          { "name": "widgetId", "in": "path", "required": true, "schema": { "type": "integer" } }
        ],
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": { "schema": { "$ref": "#/components/schemas/Widget" } }
            }
          }
        }
      }
    },
    "/auth/me": {
      "get": {
        "operationId": "authMe",
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": { "id": { "type": "integer" } }
                }
              }
            }
          }
        }
      }
    }
  },
  "components": {
    "schemas": {
      "Widget": {
        "type": "object",
        "properties": {
          "id": { "type": "integer" },
          "name": { "type": "string" },
          "status": { "type": "string", "enum": ["active", "inactive", "pending"] }
        },
        "required": ["id", "name", "status"]
      }
    }
  }
}`

// p1c2Widget is Widget as this file reads it back — enough to compare the
// list row against the card field-by-field (assertion 1).
type p1c2Widget struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// p1c2WorkspaceView is the sliver of [admin's workspaceView] this file
// reads: the full [domain.Settings] (so a base-path/notFoundBody edit can
// round-trip the REST of the settings object unchanged, exactly like a real
// admin UI always does — [Server.handlePatchWorkspace]'s own doc comment:
// "not deep-merged... the admin UI always round-trips the full settings
// object it fetched, never a sparse diff") plus Revision, which assertion 5
// reads before and after a live-state write to prove session counters never
// bump it.
type p1c2WorkspaceView struct {
	ID          int64           `json:"id"`
	Slug        string          `json:"slug"`
	Revision    int64           `json:"revision"`
	Settings    domain.Settings `json:"settings"`
	EditVersion int64           `json:"editVersion"`
}

// buildP1c2Stack is [buildP1cStack] (p1c_test.go) with the SLICE 2 sources
// added on top, wired exactly as cmd/mocker/main.go wires them: the SAME
// *livestate.Store and *traffic.Recorder instance reach both planes (that
// file's own comment on this wiring is the rule this copies — two Stores or
// two Recorders would mean the admin API shows directives or traffic the
// mock plane's serving path never actually produced or consumed), and
// custom endpoints reach the mock plane through [mockplane.Plane.
// SetCustomEndpoints] while the admin side builds its own read repo
// internally (server.go's own established rule for a stateless DB reader —
// see this package's own header comment).
//
// The recorder's Run loop is started on t.Context(), which cancels the
// instant this test ends — the periodic-tick/shutdown-prune code paths get
// a live goroutine to exercise, but every assertion below waits on an
// explicit Flush, never on Run's own timer (HARD RULE: no sleep waits on
// asynchronous work). The registered cleanup flushes with a FRESH
// context.Background(), not t.Context() — testing.T.Context() is already
// canceled by the time Cleanup funcs run, and Flush-ing with an already-
// canceled context would make the final drain fail silently.
func buildP1c2Stack(t *testing.T) (http.Handler, *traffic.Recorder) {
	t.Helper()
	cfg := testConfig(t)
	// testConfig leaves TrafficMaxBody at its zero value, which mockplane's
	// own write-through tee (traffic.go's newTrafficWriter) reads directly
	// as its capture cap — unlike [traffic.Options.MaxBody], which
	// [traffic.NewRecorder] defaults when <=0, this one has no such
	// fallback: a zero here caps every captured response at ZERO bytes, so
	// every traffic row's RespBody comes back empty and Truncated=true (the
	// P1c2 near-miss this comment exists to name). Set explicitly, well
	// above anything this file's tiny generated bodies ever produce.
	cfg.TrafficMaxBody = 64 << 10

	db, err := store.Open(t.Context(), cfg.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	provider := auth.NewSharedPassword(cfg)
	sessions := auth.NewManager(db, cfg, provider)
	ws := workspaces.NewRepo(db)
	specsRepo := specs.NewRepo(db, cfg)
	log := testLogger()

	adminSrv := admin.New(cfg, sessions, ws, db, log)
	mockPlane := mockplane.New(cfg, ws, specsRepo, log)
	mockPlane.SetOverrides(overrides.NewRepo(db))

	liveState := livestate.NewStore(livestate.DefaultTTL, nil)
	trafficRec := traffic.NewRecorder(db, log, traffic.Options{})
	customRepo := customep.NewRepo(db)

	mockPlane.SetLiveState(liveState)
	mockPlane.SetTraffic(trafficRec)
	mockPlane.SetCustomEndpoints(customRepo)
	adminSrv.SetLiveState(liveState)
	adminSrv.SetTraffic(trafficRec)

	dispatcher := server.New(cfg, adminSrv.Handler(), mockPlane, log)
	handler := httpx.Chain(dispatcher, httpx.Recover(log), httpx.RequestLog(log), httpx.MaxBody(cfg.MaxBody))

	go trafficRec.Run(t.Context())
	t.Cleanup(func() {
		_ = trafficRec.Flush(context.Background())
	})

	return handler, trafficRec
}

// p1c2Decode fails the test if rec's body does not decode into v.
func p1c2Decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, rec.Body)
	}
}

// p1c2RequireStatus fails the test with the response body attached — every
// assertion below needs that body to diagnose a wrong status, and a bare
// "status = 500, want 200" tells a reviewer nothing about why.
func p1c2RequireStatus(t *testing.T, label string, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("%s: status = %d, want %d; body = %s", label, rec.Code, want, rec.Body)
	}
}

// p1c2GetWorkspace fetches wsID's admin view — the one call assertion 5
// makes twice to prove revision does not move.
func p1c2GetWorkspace(t *testing.T, handler http.Handler, wsID int64, cookie *http.Cookie, csrf string) p1c2WorkspaceView {
	t.Helper()
	rec := do(handler, jsonRequest(t, http.MethodGet,
		"http://mocker.local/api/workspaces/"+strconv.FormatInt(wsID, 10), nil, cookie, csrf))
	p1c2RequireStatus(t, "get workspace", rec, http.StatusOK)
	var v p1c2WorkspaceView
	p1c2Decode(t, rec, &v)
	return v
}

// p1c2NewWorkspace creates a workspace, attaches specID, and sets its base
// path (assertion 8/9's own reason this must be non-empty: to-override and
// to-endpoint resolve the requested path against it differently, and an
// empty base path would let a broken implementation of either pass by
// accident). notFoundBody, when non-nil, rides along in the SAME PATCH — a
// second round-trip would just be a second place for the "resend the whole
// settings object" rule above to be gotten wrong.
func p1c2NewWorkspace(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, name string, specID int64, basePath string, notFoundBody json.RawMessage) p1c2WorkspaceView {
	t.Helper()

	createRec := do(handler, jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
		map[string]string{"name": name}, cookie, csrf))
	p1c2RequireStatus(t, "create workspace "+name, createRec, http.StatusCreated)
	var ws p1c2WorkspaceView
	p1c2Decode(t, createRec, &ws)

	wsPath := "http://mocker.local/api/workspaces/" + strconv.FormatInt(ws.ID, 10)

	// A3/D10: editVersion is required on every PATCH now. A freshly created
	// workspace starts at 1 (workspaces.Repo.Create's own INSERT bootstraps
	// it there, never the column's DEFAULT of 0), and the second PATCH below
	// must send back what the FIRST one just returned, or it would refuse as
	// a stale expectation.
	attachRec := do(handler, jsonRequest(t, http.MethodPatch, wsPath,
		map[string]any{"specId": specID, "editVersion": 1}, cookie, csrf))
	p1c2RequireStatus(t, "attach spec to "+name, attachRec, http.StatusOK)
	p1c2Decode(t, attachRec, &ws)

	ws.Settings.BasePath = basePath
	ws.Settings.NotFoundBody = notFoundBody
	patchRec := do(handler, jsonRequest(t, http.MethodPatch, wsPath,
		map[string]any{"settings": ws.Settings, "editVersion": ws.EditVersion}, cookie, csrf))
	p1c2RequireStatus(t, "patch settings for "+name, patchRec, http.StatusOK)
	p1c2Decode(t, patchRec, &ws)

	return ws
}

// p1c2FindTrafficRows returns every row of rows matching pred, preserving
// order — used both where exactly one match is expected (redaction, a
// specific forced status) and where more than one legitimately is (the two
// consecutive fail-directive 500s).
func p1c2FindTrafficRows(rows []traffic.Row, pred func(traffic.Row) bool) []traffic.Row {
	out := make([]traffic.Row, 0, len(rows))
	for _, r := range rows {
		if pred(r) {
			out = append(out, r)
		}
	}
	return out
}

// p1c2Poll polls /traffic/poll?since=<since>&limit=500 and decodes it —
// [traffic.Row] itself is reused directly (its json tags already are the
// wire shape), so this file needs no shadow row type.
func p1c2Poll(t *testing.T, handler http.Handler, wsID, since int64, cookie *http.Cookie, csrf string) (rows []traffic.Row, lastID int64) {
	t.Helper()
	url := "http://mocker.local/api/workspaces/" + strconv.FormatInt(wsID, 10) +
		"/traffic/poll?since=" + strconv.FormatInt(since, 10) + "&limit=500"
	rec := do(handler, jsonRequest(t, http.MethodGet, url, nil, cookie, csrf))
	p1c2RequireStatus(t, "poll traffic", rec, http.StatusOK)
	var view struct {
		Rows   []traffic.Row `json:"rows"`
		LastID int64         `json:"lastId"`
	}
	p1c2Decode(t, rec, &view)
	return view.Rows, view.LastID
}

// p1c2LiveState is the wire shape POST/GET {prefix}/state answers with —
// reused for the admin session API too (they are byte-identical by
// [livestate.Directive]'s own doc comment). Directives decodes through
// [livestate.Directive]'s own UnmarshalJSON, so the "*"-or-{method,path}
// Target union round-trips without this file reimplementing it.
type p1c2LiveState struct {
	Directives []livestate.Directive `json:"directives"`
	Cleared    int                   `json:"cleared"`
}

// TestP1c2_ReadOnlyScenario is DESIGN §19 line 1111's phase criterion for
// this slice, proved end to end through the real dispatcher: a frontend
// logs in, walks a read-only scenario (list, card, pagination, filtering),
// and sees a live-state force, a fail counter, traffic capture with
// redaction and suppression, and both from-traffic conversions all agree
// with what the mock plane actually served — while a second, untouched
// workspace proves none of it leaked across the one boundary that matters
// (workspace_id).
//
// Subtests run SEQUENTIALLY, not in parallel: 5 through 9 share workspace 1
// and depend on each other's order (a directive set, then observed, then
// cleared; a request recorded, then converted). Assertion 10's BASELINE is
// captured here, in the parent function, BEFORE subtest 1 ever runs — inside
// a t.Run it would run after every leak had already happened once, proving
// nothing.
func TestP1c2_ReadOnlyScenario(t *testing.T) {
	handler, trafficRec := buildP1c2Stack(t)
	cookie, csrf := login(t, handler, "p1c2-admin")

	importRec := do(handler, jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
		map[string]string{"name": "P1c2 smoke", "source": "upload", "document": p1c2InlineSpec}, cookie, csrf))
	p1c2RequireStatus(t, "import spec", importRec, http.StatusCreated)
	var imported struct {
		ID     int64 `json:"id"`
		Report struct {
			Operations int `json:"operations"`
			Degraded   int `json:"degraded"`
		} `json:"report"`
	}
	p1c2Decode(t, importRec, &imported)
	if imported.Report.Operations != 4 || imported.Report.Degraded != 0 {
		t.Fatalf("import report = %+v, want exactly 4 clean operations", imported.Report)
	}

	ws1 := p1c2NewWorkspace(t, handler, cookie, csrf, "p1c2-demo", imported.ID, "/api/v1",
		json.RawMessage(`{"error":"nope"}`))
	ws2 := p1c2NewWorkspace(t, handler, cookie, csrf, "p1c2-isolation", imported.ID, "/api/v1", nil)

	mockHost1 := ws1.Slug + ".mock.local"
	mockHost2 := ws2.Slug + ".mock.local"

	// ASSERTION 10's baseline — see the test's own doc comment for why this
	// must run here, before anything else.
	baselineRec := do(handler, httptest.NewRequest(http.MethodGet, "http://"+mockHost2+"/api/v1/widgets", nil))
	p1c2RequireStatus(t, "ws2 baseline GET /widgets", baselineRec, http.StatusOK)
	baselineBytes := append([]byte(nil), baselineRec.Body.Bytes()...)
	t.Logf("MEASUREMENTS isolation baseline: %d bytes from workspace %s", len(baselineBytes), ws2.Slug)

	// Shared across subtests, sequential by construction (no t.Parallel).
	var (
		item3            p1c2Widget // assertion 1's list row, reused by assertion 8
		pollLastID       int64      // running poll cursor, carried from 07 into 09
		revBeforeSession int64
	)

	t.Run("01_list_card_agree", func(t *testing.T) {
		listRec := do(handler, httptest.NewRequest(http.MethodGet, "http://"+mockHost1+"/api/v1/widgets", nil))
		p1c2RequireStatus(t, "GET /widgets", listRec, http.StatusOK)
		var items []p1c2Widget
		p1c2Decode(t, listRec, &items)
		if len(items) < 3 {
			t.Fatalf("GET /widgets returned %d items, want at least 3", len(items))
		}
		item3 = items[2]
		t.Logf("MEASUREMENTS list: %d items, third = %+v", len(items), item3)

		cardRec := do(handler, httptest.NewRequest(http.MethodGet,
			"http://"+mockHost1+"/api/v1/widgets/"+strconv.FormatInt(item3.ID, 10), nil))
		p1c2RequireStatus(t, "GET /widgets/{id}", cardRec, http.StatusOK)
		var card p1c2Widget
		p1c2Decode(t, cardRec, &card)

		if card.ID != item3.ID || card.Name != item3.Name || card.Status != item3.Status {
			t.Errorf("card = %+v, want it to equal the list row %+v field-by-field", card, item3)
		}
	})

	t.Run("02_pagination", func(t *testing.T) {
		fullRec := do(handler, httptest.NewRequest(http.MethodGet, "http://"+mockHost1+"/api/v1/widgets", nil))
		p1c2RequireStatus(t, "GET /widgets (unpaged)", fullRec, http.StatusOK)
		var full []p1c2Widget
		p1c2Decode(t, fullRec, &full)
		if len(full) < 4 {
			t.Fatalf("unpaged list has %d items, want at least 4 to page into positions 2,3", len(full))
		}

		pageRec := do(handler, httptest.NewRequest(http.MethodGet,
			"http://"+mockHost1+"/api/v1/widgets?offset=2&limit=2", nil))
		p1c2RequireStatus(t, "GET /widgets?offset=2&limit=2", pageRec, http.StatusOK)
		var page []p1c2Widget
		p1c2Decode(t, pageRec, &page)

		want := full[2:4]
		if len(page) != 2 || page[0] != want[0] || page[1] != want[1] {
			t.Errorf("?offset=2&limit=2 = %+v, want exactly the unpaged list's positions 2,3 = %+v", page, want)
		}
	})

	t.Run("03_filtering", func(t *testing.T) {
		unfilteredRec := do(handler, httptest.NewRequest(http.MethodGet, "http://"+mockHost1+"/api/v1/widgets", nil))
		p1c2RequireStatus(t, "GET /widgets (unfiltered)", unfilteredRec, http.StatusOK)
		var unfiltered []p1c2Widget
		p1c2Decode(t, unfilteredRec, &unfiltered)

		counts := map[string]int{}
		for _, w := range unfiltered {
			counts[w.Status]++
		}
		var target string
		for status, n := range counts {
			if n > 0 && n < len(unfiltered) {
				target = status
				break
			}
		}
		if target == "" {
			t.Fatalf("every item in %+v shares one status; nothing to filter a strict subset with (enum diversity failed)", unfiltered)
		}

		filteredRec := do(handler, httptest.NewRequest(http.MethodGet,
			"http://"+mockHost1+"/api/v1/widgets?status="+target, nil))
		p1c2RequireStatus(t, "GET /widgets?status=", filteredRec, http.StatusOK)
		var filtered []p1c2Widget
		p1c2Decode(t, filteredRec, &filtered)

		t.Logf("MEASUREMENTS filtering: status=%q, unfiltered=%d, filtered=%d", target, len(unfiltered), len(filtered))

		if len(filtered) != counts[target] {
			t.Errorf("filtered count = %d, want %d (the unfiltered occurrence count of %q)", len(filtered), counts[target], target)
		}
		for _, w := range filtered {
			if w.Status != target {
				t.Errorf("filtered item %+v has status != %q", w, target)
			}
		}
		if len(filtered) == len(unfiltered) {
			t.Errorf("filtered set (%d) is the same size as unfiltered (%d): the filter was not applied", len(filtered), len(unfiltered))
		}
	})

	t.Run("04_when_binds_pinned_status", func(t *testing.T) {
		opKey := overrides.OpKey("POST", "/widgets")
		putBody := map[string]any{
			"overrideOn": true,
			"routeOff":   false,
			"responses": map[string]overrides.Variant{
				"409": {
					Mode:      "pinned",
					When:      []overrides.Condition{{In: "body", Name: "name", Op: "equals", Value: "taken"}},
					Body:      json.RawMessage(`{"error":"taken"}`),
					MediaType: "application/json",
				},
			},
			// A3: first write to this opKey, so 0 is the legal "I expect no
			// row" expectation (D7).
			"editVersion": 0,
		}
		putRec := do(handler, jsonRequest(t, http.MethodPut,
			"http://mocker.local/api/workspaces/"+strconv.FormatInt(ws1.ID, 10)+"/operations/"+opKey,
			putBody, cookie, csrf))
		p1c2RequireStatus(t, "PUT operation POST /widgets", putRec, http.StatusOK)

		takenReq := httptest.NewRequest(http.MethodPost, "http://"+mockHost1+"/api/v1/widgets",
			strings.NewReader(`{"name":"taken"}`))
		takenReq.Header.Set("Content-Type", "application/json")
		takenRec := do(handler, takenReq)
		p1c2RequireStatus(t, "POST /widgets {taken}", takenRec, http.StatusConflict)
		if got := takenRec.Body.String(); got != `{"error":"taken"}` {
			t.Errorf(`POST {"name":"taken"} body = %s, want the pinned {"error":"taken"}`, got)
		}

		// No Content-Type at all — DESIGN §8's tolerance: a bare JSON body
		// with no declared type is still parsed as JSON.
		freeReq := httptest.NewRequest(http.MethodPost, "http://"+mockHost1+"/api/v1/widgets",
			strings.NewReader(`{"name":"free"}`))
		freeRec := do(handler, freeReq)
		p1c2RequireStatus(t, "POST /widgets {free}, no Content-Type", freeRec, http.StatusOK)
	})

	t.Run("05_live_state_status_leaves_revision_alone", func(t *testing.T) {
		revBeforeSession = p1c2GetWorkspace(t, handler, ws1.ID, cookie, csrf).Revision

		setRec := do(handler, jsonRequest(t, http.MethodPost, "http://"+mockHost1+"/__mocker/state",
			livestate.Directive{
				Target: livestate.Target{Method: "GET", Path: "/widgets"},
				Action: livestate.ActionStatus,
				Status: http.StatusServiceUnavailable,
			}, nil, ""))
		p1c2RequireStatus(t, "POST {prefix}/state (status)", setRec, http.StatusOK)
		var setView p1c2LiveState
		p1c2Decode(t, setRec, &setView)
		if len(setView.Directives) != 1 || setView.Directives[0].Status != http.StatusServiceUnavailable {
			t.Fatalf("directives after set = %+v, want exactly one status=503 directive", setView.Directives)
		}

		forcedRec := do(handler, httptest.NewRequest(http.MethodGet, "http://"+mockHost1+"/api/v1/widgets", nil))
		p1c2RequireStatus(t, "GET /widgets under forced 503", forcedRec, http.StatusServiceUnavailable)

		clearRec := do(handler, httptest.NewRequest(http.MethodDelete, "http://"+mockHost1+"/__mocker/state", nil))
		p1c2RequireStatus(t, "DELETE {prefix}/state", clearRec, http.StatusOK)
		var clearView p1c2LiveState
		p1c2Decode(t, clearRec, &clearView)
		if clearView.Cleared != 1 {
			t.Errorf("cleared = %d, want 1", clearView.Cleared)
		}

		recoveredRec := do(handler, httptest.NewRequest(http.MethodGet, "http://"+mockHost1+"/api/v1/widgets", nil))
		p1c2RequireStatus(t, "GET /widgets after clear", recoveredRec, http.StatusOK)

		revAfterSession := p1c2GetWorkspace(t, handler, ws1.ID, cookie, csrf).Revision
		if revAfterSession != revBeforeSession {
			t.Errorf("revision moved %d -> %d across a live-state set/clear; DESIGN §12: session counters never touch revision",
				revBeforeSession, revAfterSession)
		}
		t.Logf("MEASUREMENTS live-state: forced status = %d, revision unchanged at %d", forcedRec.Code, revAfterSession)
	})

	t.Run("06_fail_counter_burns_down", func(t *testing.T) {
		setRec := do(handler, jsonRequest(t, http.MethodPost, "http://"+mockHost1+"/__mocker/state",
			livestate.Directive{
				Target: livestate.Target{Method: "GET", Path: "/widgets/{widgetId}"},
				Action: livestate.ActionFail,
				Status: http.StatusInternalServerError,
				N:      2,
			}, nil, ""))
		p1c2RequireStatus(t, "POST {prefix}/state (fail n=2)", setRec, http.StatusOK)

		first := do(handler, httptest.NewRequest(http.MethodGet, "http://"+mockHost1+"/api/v1/widgets/999", nil))
		p1c2RequireStatus(t, "GET /widgets/999 (1st forced)", first, http.StatusInternalServerError)

		midRec := do(handler, httptest.NewRequest(http.MethodGet, "http://"+mockHost1+"/__mocker/state", nil))
		p1c2RequireStatus(t, "GET {prefix}/state mid-burn", midRec, http.StatusOK)
		var midView p1c2LiveState
		p1c2Decode(t, midRec, &midView)
		remainder := -1
		for _, d := range midView.Directives {
			if d.Action == livestate.ActionFail && d.Target.Path == "/widgets/{widgetId}" {
				remainder = d.N
			}
		}
		if remainder != 1 {
			t.Errorf("remainder between the two forced requests = %d, want 1", remainder)
		}
		t.Logf("MEASUREMENTS fail counter: remainder after 1st forced request = %d", remainder)

		second := do(handler, httptest.NewRequest(http.MethodGet, "http://"+mockHost1+"/api/v1/widgets/999", nil))
		p1c2RequireStatus(t, "GET /widgets/999 (2nd forced)", second, http.StatusInternalServerError)

		third := do(handler, httptest.NewRequest(http.MethodGet, "http://"+mockHost1+"/api/v1/widgets/999", nil))
		p1c2RequireStatus(t, "GET /widgets/999 (3rd, normal)", third, http.StatusOK)
	})

	t.Run("07_traffic_polling_redaction_suppression", func(t *testing.T) {
		authHeaderReq := httptest.NewRequest(http.MethodGet, "http://"+mockHost1+"/api/v1/widgets", nil)
		authHeaderReq.Header.Set("Authorization", "Bearer secret-value")
		authHeaderRec := do(handler, authHeaderReq)
		p1c2RequireStatus(t, "GET /widgets with Authorization", authHeaderRec, http.StatusOK)

		authMeReq := httptest.NewRequest(http.MethodPost, "http://"+mockHost1+"/api/v1/auth/me",
			strings.NewReader(`{"probe":true}`))
		authMeReq.Header.Set("Content-Type", "application/json")
		do(handler, authMeReq) // status unconstrained: no POST /auth/me operation is declared

		if err := trafficRec.Flush(t.Context()); err != nil {
			t.Fatalf("flush traffic recorder: %v", err)
		}

		rows, lastID := p1c2Poll(t, handler, ws1.ID, 0, cookie, csrf)
		pollLastID = lastID
		t.Logf("MEASUREMENTS traffic: polled %d rows, lastId = %d", len(rows), lastID)
		for i := 1; i < len(rows); i++ {
			if rows[i].ID <= rows[i-1].ID {
				t.Fatalf("rows not in ascending id order: row %d id=%d <= row %d id=%d", i, rows[i].ID, i-1, rows[i-1].ID)
			}
		}

		forced503 := p1c2FindTrafficRows(rows, func(r traffic.Row) bool {
			return r.Method == http.MethodGet && r.Path == "/api/v1/widgets" && r.Status == http.StatusServiceUnavailable
		})
		if len(forced503) != 1 || forced503[0].MatchedKind != "operation" {
			t.Errorf("forced-503 rows = %+v, want exactly one with matchedKind=operation", forced503)
		}

		forced500 := p1c2FindTrafficRows(rows, func(r traffic.Row) bool {
			return r.Method == http.MethodGet && r.Path == "/api/v1/widgets/999" && r.Status == http.StatusInternalServerError
		})
		if len(forced500) != 2 {
			t.Errorf("forced-500 rows = %d, want exactly 2 (the fail directive's own n=2)", len(forced500))
		}
		for _, r := range forced500 {
			if r.MatchedKind != "operation" {
				t.Errorf("forced-500 row %+v: matchedKind = %q, want operation", r, r.MatchedKind)
			}
		}

		recovered200 := p1c2FindTrafficRows(rows, func(r traffic.Row) bool {
			return r.Method == http.MethodGet && r.Path == "/api/v1/widgets/999" && r.Status == http.StatusOK
		})
		if len(recovered200) != 1 {
			t.Errorf("recovered-200 rows for /widgets/999 = %d, want exactly 1 (the 3rd, unforced request)", len(recovered200))
		}

		redactedRows := p1c2FindTrafficRows(rows, func(r traffic.Row) bool {
			return r.ReqHeaders["Authorization"] == traffic.RedactedValue
		})
		if len(redactedRows) != 1 {
			t.Fatalf("rows with a redacted Authorization header = %d, want exactly 1", len(redactedRows))
		}
		rawRows, err := json.Marshal(rows)
		if err != nil {
			t.Fatalf("marshal polled rows: %v", err)
		}
		if bytes.Contains(rawRows, []byte("secret-value")) {
			t.Errorf("the literal secret value leaked into the traffic response: %s", rawRows)
		}

		suppressedRows := p1c2FindTrafficRows(rows, func(r traffic.Row) bool {
			return r.Method == http.MethodPost && r.Path == "/api/v1/auth/me"
		})
		if len(suppressedRows) != 1 {
			t.Fatalf("POST /api/v1/auth/me rows = %d, want exactly 1", len(suppressedRows))
		}
		if suppressedRows[0].ReqBody != "" {
			t.Errorf("POST /api/v1/auth/me: reqBody = %q, want empty (auth-path suppression)", suppressedRows[0].ReqBody)
		}
		if !suppressedRows[0].HasNote(traffic.NoteSuppressed) {
			t.Errorf("POST /api/v1/auth/me: notes = %q, want it to carry %q", suppressedRows[0].Notes, traffic.NoteSuppressed)
		}
	})

	// tid8 and expectedBody are captured here so 08's own t.Run can both
	// locate the traffic row it needs and, afterward, verify what got
	// pinned equals it byte-for-byte.
	var (
		tid8         int64
		expectedBody string
	)
	t.Run("08_to_override_uses_template_key", func(t *testing.T) {
		rows, _ := p1c2Poll(t, handler, ws1.ID, 0, cookie, csrf)
		cardPath := "/api/v1/widgets/" + strconv.FormatInt(item3.ID, 10)
		matches := p1c2FindTrafficRows(rows, func(r traffic.Row) bool {
			return r.Method == http.MethodGet && r.Path == cardPath && r.Status == http.StatusOK && r.MatchedKind == "operation"
		})
		if len(matches) != 1 {
			t.Fatalf("traffic rows for %s = %d, want exactly 1 (assertion 1's own card request)", cardPath, len(matches))
		}
		tid8 = matches[0].ID
		expectedBody = matches[0].RespBody
		t.Logf("MEASUREMENTS to-override: traffic row id = %d, recorded body = %s", tid8, expectedBody)

		toOverrideRec := do(handler, jsonRequest(t, http.MethodPost,
			"http://mocker.local/api/workspaces/"+strconv.FormatInt(ws1.ID, 10)+"/traffic/"+strconv.FormatInt(tid8, 10)+"/to-override",
			nil, cookie, csrf))
		p1c2RequireStatus(t, "POST .../to-override", toOverrideRec, http.StatusOK)
		var toOverride struct {
			OpKey  string `json:"opKey"`
			Status int    `json:"status"`
		}
		p1c2Decode(t, toOverrideRec, &toOverride)

		wantKey := overrides.OpKey("GET", "/widgets/{widgetId}")
		if toOverride.OpKey != wantKey {
			t.Errorf("to-override opKey = %q, want the TEMPLATE key %q — a concrete-path key would be orphaned (nothing the plane's lookup can ever produce)",
				toOverride.OpKey, wantKey)
		}
		if toOverride.Status != http.StatusOK {
			t.Errorf("to-override status = %d, want 200", toOverride.Status)
		}

		afterRec := do(handler, httptest.NewRequest(http.MethodGet, "http://"+mockHost1+cardPath, nil))
		p1c2RequireStatus(t, "GET card after to-override", afterRec, http.StatusOK)
		if got := afterRec.Body.String(); got != expectedBody {
			t.Errorf("after to-override, GET %s = %s, want byte-identical to the recorded traffic body %s", cardPath, got, expectedBody)
		}
	})

	t.Run("09_to_endpoint_pins_status_on_404", func(t *testing.T) {
		notFoundRec := do(handler, httptest.NewRequest(http.MethodGet, "http://"+mockHost1+"/api/v1/legacy/ping", nil))
		p1c2RequireStatus(t, "GET /legacy/ping (unmapped)", notFoundRec, http.StatusNotFound)
		notFoundBody := notFoundRec.Body.String()
		if notFoundBody != `{"error":"nope"}` {
			t.Errorf("404 body = %s, want the configured settings.notFoundBody %s", notFoundBody, `{"error":"nope"}`)
		}

		if err := trafficRec.Flush(t.Context()); err != nil {
			t.Fatalf("flush traffic recorder: %v", err)
		}
		rows, lastID := p1c2Poll(t, handler, ws1.ID, pollLastID, cookie, csrf)
		pollLastID = lastID
		matches := p1c2FindTrafficRows(rows, func(r traffic.Row) bool {
			return r.Method == http.MethodGet && r.Path == "/api/v1/legacy/ping" && r.Status == http.StatusNotFound && r.MatchedKind == "none"
		})
		if len(matches) != 1 {
			t.Fatalf("traffic rows for the unmapped GET = %d, want exactly 1 with matchedKind=none", len(matches))
		}
		tid9 := matches[0].ID

		toEndpointRec := do(handler, jsonRequest(t, http.MethodPost,
			"http://mocker.local/api/workspaces/"+strconv.FormatInt(ws1.ID, 10)+"/traffic/"+strconv.FormatInt(tid9, 10)+"/to-endpoint",
			nil, cookie, csrf))
		p1c2RequireStatus(t, "POST .../to-endpoint", toEndpointRec, http.StatusCreated)
		var toEndpoint struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
			Path   string `json:"path"`
		}
		p1c2Decode(t, toEndpointRec, &toEndpoint)
		if toEndpoint.Path != "/legacy/ping" {
			t.Errorf("to-endpoint path = %q, want the base-path-stripped %q", toEndpoint.Path, "/legacy/ping")
		}

		afterRec := do(handler, httptest.NewRequest(http.MethodGet, "http://"+mockHost1+"/api/v1/legacy/ping", nil))
		p1c2RequireStatus(t, "GET /legacy/ping after to-endpoint", afterRec, http.StatusOK)
		if got := afterRec.Body.String(); got != notFoundBody {
			t.Errorf("after to-endpoint, body = %s, want the PRESERVED observed body %s (the pinned-status-on-404 rule rewrites status only)", got, notFoundBody)
		}

		if err := trafficRec.Flush(t.Context()); err != nil {
			t.Fatalf("flush traffic recorder: %v", err)
		}
		rows2, lastID2 := p1c2Poll(t, handler, ws1.ID, pollLastID, cookie, csrf)
		pollLastID = lastID2
		customMatches := p1c2FindTrafficRows(rows2, func(r traffic.Row) bool {
			return r.Method == http.MethodGet && r.Path == "/api/v1/legacy/ping" && r.Status == http.StatusOK && r.MatchedKind == "custom"
		})
		if len(customMatches) != 1 {
			t.Fatalf("post-conversion rows for /legacy/ping = %d, want exactly 1 with matchedKind=custom", len(customMatches))
		}
		if customMatches[0].MatchedID == nil || *customMatches[0].MatchedID != toEndpoint.ID {
			t.Errorf("matchedId = %v, want the new endpoint's own id %d", customMatches[0].MatchedID, toEndpoint.ID)
		}
	})

	t.Run("10_isolation_no_leakage_into_workspace2", func(t *testing.T) {
		endRec := do(handler, httptest.NewRequest(http.MethodGet, "http://"+mockHost2+"/api/v1/widgets", nil))
		p1c2RequireStatus(t, "ws2 GET /widgets (end)", endRec, http.StatusOK)
		endBytes := endRec.Body.Bytes()

		if string(endBytes) != string(baselineBytes) {
			t.Errorf("workspace 2's GET /widgets changed across workspace 1's whole scenario:\nbefore: %s\nafter:  %s\n"+
				"— workspace 1's directives/overrides/endpoints leaked across workspace_id", baselineBytes, endBytes)
		}
	})
}
