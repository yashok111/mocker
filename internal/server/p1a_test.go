package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/server"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/workspaces"
)

// inlineSpec is a tiny, self-contained OAS 3.0 document — deliberately NOT
// the real acceptance fixture, so this test keeps passing in a fresh clone
// that has never fetched testdata. It carries no root servers[], so
// [openapi.Document.BasePath] resolves to "" (BaseAbsent): the one operation
// below therefore matches the mock plane at exactly its own path, "/widgets",
// with no base-path gluing to reason about.
//
// The 200 response declares a real schema — a top-level array of
// {id, name} objects — so the P1b assertion below has something to check
// the generated body actually conforms to (DESIGN §9's list contract: a
// bare top-level array is a genuine list route), not just "some 200 landed".
const inlineSpec = `{
  "openapi": "3.0.3",
  "info": { "title": "P1a smoke spec", "version": "1.0.0" },
  "paths": {
    "/widgets": {
      "get": {
        "operationId": "listWidgets",
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": {
                      "id": { "type": "integer" },
                      "name": { "type": "string" }
                    },
                    "required": ["id", "name"]
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}`

// buildStackWithSpecs is [buildStack] (see server_test.go) with one
// deliberate difference: the mock plane is built with a REAL
// [mockplane.SpecSource] (specs.NewRepo(db, cfg)) instead of buildStack's
// nil. buildStack passes nil correctly for P0's own tests, which never
// attach a spec — but reusing it unchanged here would make routeTable never
// get built, so every mock-plane request in this file would 404 before ever
// reaching the route table, and the 501 assertion below would be
// unreachable. This function exists so that mistake cannot happen silently.
func buildStackWithSpecs(t *testing.T) (http.Handler, *config.Config) {
	t.Helper()
	cfg := testConfig(t)

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
	dispatcher := server.New(cfg, adminSrv.Handler(), mockPlane, log)
	handler := httpx.Chain(dispatcher, httpx.Recover(log), httpx.RequestLog(log), httpx.MaxBody(cfg.MaxBody))
	return handler, cfg
}

// widgetItem is one generated element of the /widgets array — the shape
// inlineSpec's schema declares (id, name, both required).
type widgetItem struct {
	ID   float64 `json:"id"`
	Name string  `json:"name"`
}

// TestP1a_ImportAttachRoute proves the full P1a+P1b stack end to end: log
// in, create a workspace, import a small inline OpenAPI document, attach it
// to the workspace, then hit the mock plane over the workspace host and
// assert its one operation now matches — answering 200 with a body that
// conforms to the operation's declared schema (P1b's response generator,
// replacing the 501 P1a used to answer here) — while an unrelated path
// still 404s exactly as P0 always answered it.
func TestP1a_ImportAttachRoute(t *testing.T) {
	t.Parallel()
	handler, _ := buildStackWithSpecs(t)

	cookie, csrf := login(t, handler, "p1a-admin")

	// 1. Create a workspace with no spec attached yet.
	createWSReq := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
		map[string]string{"name": "widgets-demo"}, cookie, csrf)
	createWSRec := do(handler, createWSReq)
	if createWSRec.Code != http.StatusCreated {
		t.Fatalf("create workspace: status = %d, want 201; body = %s", createWSRec.Code, createWSRec.Body)
	}
	var ws struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(createWSRec.Body.Bytes(), &ws); err != nil {
		t.Fatalf("decode create workspace response: %v", err)
	}

	// 2. Import the inline document as a new spec.
	importReq := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
		map[string]string{"name": "P1a smoke", "source": "upload", "document": inlineSpec}, cookie, csrf)
	importRec := do(handler, importReq)
	if importRec.Code != http.StatusCreated {
		t.Fatalf("import spec: status = %d, want 201; body = %s", importRec.Code, importRec.Body)
	}
	var imported struct {
		ID     int64 `json:"id"`
		Report struct {
			Operations int `json:"operations"`
			Degraded   int `json:"degraded"`
		} `json:"report"`
	}
	if err := json.Unmarshal(importRec.Body.Bytes(), &imported); err != nil {
		t.Fatalf("decode import spec response: %v", err)
	}
	if imported.Report.Operations != 1 || imported.Report.Degraded != 0 {
		t.Fatalf("import report = %+v, want exactly 1 clean operation", imported.Report)
	}

	// 3. Attach the imported spec to the workspace. editVersion: 1 -- A3/D10
	// requires it, and a freshly created workspace starts there (Repo.
	// Create bootstraps edit_version at 1, not the column's DEFAULT of 0).
	attachReq := jsonRequest(t, http.MethodPatch, "http://mocker.local/api/workspaces/"+strconv.FormatInt(ws.ID, 10),
		map[string]any{"specId": imported.ID, "editVersion": 1}, cookie, csrf)
	attachRec := do(handler, attachReq)
	if attachRec.Code != http.StatusOK {
		t.Fatalf("attach spec: status = %d, want 200; body = %s", attachRec.Code, attachRec.Body)
	}
	var attached struct {
		SpecID *int64 `json:"specId"`
	}
	if err := json.Unmarshal(attachRec.Body.Bytes(), &attached); err != nil {
		t.Fatalf("decode attach response: %v", err)
	}
	if attached.SpecID == nil || *attached.SpecID != imported.ID {
		t.Fatalf("attached workspace specId = %v, want %d", attached.SpecID, imported.ID)
	}

	mockHost := ws.Slug + ".mock.local"

	// 4. A path the imported document actually defines matches the route
	// table and answers 200 with a body conforming to the operation's
	// declared schema (P1b's response generator) — never the operations row
	// id, and never a raw, un-generated stand-in.
	t.Run("known operation answers a body matching its declared schema", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://"+mockHost+"/widgets", nil)
		rec := do(handler, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body)
		}
		if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "json") {
			t.Errorf("Content-Type = %q, want a JSON type", got)
		}

		var items []widgetItem
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatalf("decode body as a JSON array of widgets: %v; body = %s", err, rec.Body)
		}
		// DefaultSettings().ListSize is 5, and inlineSpec's array schema
		// declares no minItems/maxItems to override it — proves the list
		// contract (DESIGN §9) ran, not just that SOME array came back.
		if len(items) != 5 {
			t.Fatalf("got %d widgets, want 5 (workspace default listSize)", len(items))
		}
		for i, it := range items {
			if it.Name == "" {
				t.Errorf("item[%d].name is empty, want the required field populated", i)
			}
		}
		// Every item's id must be unique — the list contract's per-index
		// identity (DESIGN §9), not the same object repeated five times.
		seen := make(map[float64]bool, len(items))
		for i, it := range items {
			if seen[it.ID] {
				t.Errorf("item[%d].id = %v is a duplicate, want a unique id per row", i, it.ID)
			}
			seen[it.ID] = true
		}
	})

	// 5. A path the document never defines still 404s, exactly like P0's
	// bare Step 5 always did — attaching a spec must not turn every
	// unmatched path into something else.
	t.Run("unknown path still 404s", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://"+mockHost+"/does-not-exist", nil)
		rec := do(handler, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body)
		}
	})
}
