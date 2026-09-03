package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/server"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/workspaces"
)

// c6InlineSpec carries three operations on purpose: POST /widgets is the
// one this file binds the non-conforming schemaPatch to, GET /widgets is
// "every other operation in the workspace" C6's own text requires still
// generate — a build that failed the whole runtime, rather than degrading
// one variant, would take this one down too — and GET /widgets/{id} is
// bound to c6ApplyFailingSchemaPatch below (round 2's repair addition, not
// part of §J's C6 text itself: it targets buildPatchedSchemas' Apply-error
// branch specifically, the one c6NonConformingSchemaPatch cannot reach).
// None of the three responses declares an example, so a failure here can
// only be the patch handling this check exists to prove, never C2's
// separate example-survival concern.
const c6InlineSpec = `{
  "openapi": "3.0.3",
  "info": { "title": "C6 legacy patch spec", "version": "1.0.0" },
  "paths": {
    "/widgets": {
      "post": {
        "operationId": "createWidget",
        "responses": {
          "201": {
            "description": "created",
            "content": {
              "application/json": {
                "schema": {
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
      },
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
    },
    "/widgets/{id}": {
      "get": {
        "operationId": "getWidget",
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
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
}`

// c6NonConformingSchemaPatch is valid JSON and invalid AS A PATCH — an
// object, where jsonpatch.Parse only ever accepts an array of operations —
// the exact shape overrides/repo_test.go:116's own fullVariant fixture
// already uses for the same reason (SchemaPatch is preserved-only there,
// never shape-checked). Bytes that are not valid JSON at all would fail
// jsonx.Unmarshal on the WHOLE responses blob at the row scan
// (overrides/repo.go:596-598) and 500 every route in the workspace for a
// reason that has nothing to do with this check; this shape decodes fine
// as a Responses map and fails only where jsonpatch.Parse looks at it.
const c6NonConformingSchemaPatch = `{"add":{"/properties/x":{}}}`

// c6ApplyFailingSchemaPatch is round 2's repair addition, closing a gap a
// reviewer filed against c6NonConformingSchemaPatch above: that fixture is a
// JSON OBJECT, so it fails jsonpatch.Parse on shape alone and never reaches
// jsonpatch.Apply at all — buildPatchedSchemas' Apply-error branch
// (internal/mockplane/overrides.go: `if aerr != nil { log.Error(...);
// continue }`) never runs, so a build that PROPAGATED an apply error instead
// of logging and skipping it (or panicked on one) would still pass every
// assertion below the non-conforming-patch subtest exercises. This fixture
// is a SYNTACTICALLY VALID RFC 6902 array — it parses cleanly — that fails
// only once jsonpatch.Apply has a document in hand: "remove" targets
// "/properties/missing", and c6InlineSpec's widget schema declares only
// "id" and "name", so applyToMap's opRemove case returns "no such property
// to remove" (internal/jsonpatch/apply.go:151-154). It is bound to GET
// /widgets/{id} — a THIRD operation, so it cannot interact with the
// non-conforming-patch subtest's own assertions on POST /widgets or the
// unrelated-operation subtest's on GET /widgets.
const c6ApplyFailingSchemaPatch = `[{"op":"remove","path":"/properties/missing"}]`

// buildC6Stack is [buildP1cStack] (p1c_test.go) with one difference: it
// also returns the [overrides.Repo] instance wired into the mock plane,
// because this check's whole point is writing through THAT repository
// directly rather than through the admin handler — the admin PUT-operation
// route runs an ingress gate (internal/admin, P2e's ingress door) that has
// nothing to do with what this check observes: a row that predates this
// slice, or was written by a future one this build cannot parse, sitting
// in storage already. buildP1cStack itself cannot be reused unchanged for
// that reason (it deliberately returns only the handler and config), and
// it is not this file's to edit (F-b owns one NEW file only), so this is
// its own copy of the same wiring, exactly as buildP1cStack was its own
// copy of buildStackWithSpecs before it.
func buildC6Stack(t *testing.T) (http.Handler, *overrides.Repo, *config.Config) {
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
	overridesRepo := overrides.NewRepo(db)
	log := testLogger()

	adminSrv := admin.New(cfg, sessions, ws, db, log)
	mockPlane := mockplane.New(cfg, ws, specsRepo, log)
	mockPlane.SetOverrides(overridesRepo)
	dispatcher := server.New(cfg, adminSrv.Handler(), mockPlane, log)
	handler := httpx.Chain(dispatcher, httpx.Recover(log), httpx.RequestLog(log), httpx.MaxBody(cfg.MaxBody))
	return handler, overridesRepo, cfg
}

// TestC6_LegacyPatchDegradesGracefully is criterion C6 (P2e §J): a stored
// variant whose schemaPatch is valid JSON and invalid as a patch must
// degrade — the row scans, the bound operation generates without the
// patch, every OTHER operation in the workspace still generates, and a
// bundle Encode/Decode round trip of the workspace still succeeds. None of
// that is true the moment a patch check is added to
// overrides.ValidateVariant/ValidateResponses (A12's "single most
// dangerous edit in this slice"): that function runs on the row SCAN
// (overrides/repo.go:598-600), on op-override write, on custom-endpoint
// write and scan, and twice inside internal/bundle's Validate — reached
// from both Encode and Decode — so a strict gate placed there fails every
// one of the five assertions below at once, not just one of them.
//
// The write goes straight through overridesRepo.Put, never through the
// admin PUT-operation handler: internal/mockplane's every test serves
// through a fake OverrideSource that never runs a row scan at all, so this
// check could only ever fail here, against the real overrides.Repo over a
// real store.DB — see buildC6Stack's own comment.
//
// Round 2's repair addition, steps 2b and 5b: c6NonConformingSchemaPatch
// alone fails at jsonpatch.Parse and never reaches jsonpatch.Apply, so it
// cannot tell a correct Apply-error-tolerant build apart from one that
// propagates an Apply error or panics on it — a gap a reviewer filed against
// this file. c6ApplyFailingSchemaPatch closes it: it parses cleanly and
// fails only at Apply, against a THIRD operation so it cannot interact with
// §J's own C6 assertions above, which are left exactly as C6 (P2e §J) and
// decisions.md §5 pin them, fixture and all.
func TestC6_LegacyPatchDegradesGracefully(t *testing.T) {
	t.Parallel()
	handler, overridesRepo, _ := buildC6Stack(t)

	cookie, csrf := login(t, handler, "c6-admin")

	// 1. Create a workspace and attach c6InlineSpec to it — the ordinary,
	// admin-handler path; nothing about THIS step is what C6 observes.
	createWSReq := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
		map[string]string{"name": "c6-legacy-patch"}, cookie, csrf)
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

	importReq := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
		map[string]string{"name": "C6 legacy patch spec", "source": "upload", "document": c6InlineSpec}, cookie, csrf)
	importRec := do(handler, importReq)
	if importRec.Code != http.StatusCreated {
		t.Fatalf("import spec: status = %d, want 201; body = %s", importRec.Code, importRec.Body)
	}
	var imported struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(importRec.Body.Bytes(), &imported); err != nil {
		t.Fatalf("decode import spec response: %v", err)
	}

	// editVersion: 1 -- A3/D10 requires it, and this freshly created
	// workspace starts there (Repo.Create bootstraps edit_version at 1,
	// not the column's DEFAULT of 0).
	attachReq := jsonRequest(t, http.MethodPatch, "http://mocker.local/api/workspaces/"+strconv.FormatInt(ws.ID, 10),
		map[string]any{"specId": imported.ID, "editVersion": 1}, cookie, csrf)
	attachRec := do(handler, attachReq)
	if attachRec.Code != http.StatusOK {
		t.Fatalf("attach spec: status = %d, want 200; body = %s", attachRec.Code, attachRec.Body)
	}

	// 2. Write the non-conforming patch DIRECTLY through overrides.Repo —
	// never through the admin handler (that is the whole point of this
	// check: a row already sitting in storage, not one this build's own
	// ingress gate just accepted).
	key := overrides.OpKey(http.MethodPost, "/widgets")
	if _, _, err := overridesRepo.Put(t.Context(), ws.ID, key, func(row *overrides.Row) error {
		row.OverrideOn = true
		row.Responses = map[string]overrides.Variant{
			"201": {
				Mode:        "generated",
				SchemaPatch: []byte(c6NonConformingSchemaPatch),
			},
		}
		return nil
	}); err != nil {
		t.Fatalf("Put non-conforming override: %v — a tolerant WRITE gate must accept this row (D2: SchemaPatch is preserved-only in overrides.ValidateVariant)", err)
	}

	// 2b. Round 2's repair addition (see c6ApplyFailingSchemaPatch's own
	// comment): a SECOND row, on the third operation, carrying a patch that
	// parses fine and fails only at jsonpatch.Apply — the case
	// c6NonConformingSchemaPatch above cannot reach.
	detailKey := overrides.OpKey(http.MethodGet, "/widgets/{id}")
	if _, _, err := overridesRepo.Put(t.Context(), ws.ID, detailKey, func(row *overrides.Row) error {
		row.OverrideOn = true
		row.Responses = map[string]overrides.Variant{
			"200": {
				Mode:        "generated",
				SchemaPatch: []byte(c6ApplyFailingSchemaPatch),
			},
		}
		return nil
	}); err != nil {
		t.Fatalf("Put apply-failing override: %v — a tolerant WRITE gate must accept this row too", err)
	}

	// 3. "The row scans": read the workspace's overrides straight back
	// through the repository. This is the assertion a strict check placed
	// in overrides.ValidateVariant/ValidateResponses fails first — before
	// anything about serving or bundling is even reached.
	rows, err := overridesRepo.ForWorkspace(t.Context(), ws.ID)
	if err != nil {
		t.Fatalf("ForWorkspace (row scan) after storing a non-conforming schemaPatch: %v — "+
			"this is C6's core failure mode: a patch check added to "+
			"overrides.ValidateVariant/ValidateResponses fails the scan itself, which 500s "+
			"every route in the workspace (internal/mockplane/routes.go's runtimeFor)", err)
	}
	if _, ok := rows[key]; !ok {
		t.Fatalf("stored override %q is missing from ForWorkspace after Put", key)
	}

	mockHost := ws.Slug + ".mock.local"

	// 4. "The operation generates a body WITHOUT the patch": jsonpatch.Parse
	// fails on c6NonConformingSchemaPatch's shape, buildPatchedSchemas logs
	// once and skips it (internal/mockplane/overrides.go's own doc comment
	// on buildPatchedSchemas), and POST /widgets still answers 201 with an
	// ordinary generated body — never the /properties/x the patch would
	// have added had it been a valid array of operations.
	t.Run("bound operation degrades: generates unpatched", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://"+mockHost+"/widgets", nil)
		rec := do(handler, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /widgets: status = %d, want 201; body = %s", rec.Code, rec.Body)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v; body = %s", err, rec.Body)
		}
		if _, ok := body["id"]; !ok {
			t.Errorf("body missing required field %q, want an ordinary generated body: %s", "id", rec.Body)
		}
		if _, ok := body["name"]; !ok {
			t.Errorf("body missing required field %q, want an ordinary generated body: %s", "name", rec.Body)
		}
		if _, ok := body["x"]; ok {
			t.Errorf("body carries %q — the non-conforming patch's own field applied, want it dropped: %s", "x", rec.Body)
		}
	})

	// 5. "Every other operation in the workspace generates": GET /widgets
	// carries no override at all. A build that failed the runtime wholesale
	// — rather than degrading the one bound variant — would take this route
	// down with it (internal/mockplane/routes.go's runtimeFor 500s every
	// route in the workspace on a build error).
	t.Run("unrelated operation still generates", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://"+mockHost+"/widgets", nil)
		rec := do(handler, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /widgets: status = %d, want 200; body = %s", rec.Code, rec.Body)
		}
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatalf("decode body as a JSON array: %v; body = %s", err, rec.Body)
		}
		if len(items) == 0 {
			t.Errorf("GET /widgets returned no items, want the workspace's default list size")
		}
	})

	// 5b. Round 2's repair addition: the Apply-error branch specifically.
	// c6ApplyFailingSchemaPatch parses as a valid patch array and fails only
	// once jsonpatch.Apply resolves it against the widget schema (no
	// "missing" property to remove), which is buildPatchedSchemas' OTHER
	// failure branch (internal/mockplane/overrides.go's `if aerr != nil`) —
	// the one the non-conforming-JSON-object fixture above can never reach,
	// because that one fails at jsonpatch.Parse instead. A build that
	// propagated this apply error (instead of logging once and serving
	// unpatched) would fail the runtime for the workspace and this request
	// would come back 500, not 200; a build that panicked on it would be
	// caught by httpx.Recover and also answer something other than an
	// ordinary generated body.
	t.Run("bound operation with an apply-failing patch also degrades", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://"+mockHost+"/widgets/1", nil)
		rec := do(handler, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /widgets/1: status = %d, want 200; body = %s", rec.Code, rec.Body)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v; body = %s", err, rec.Body)
		}
		if _, ok := body["id"]; !ok {
			t.Errorf("body missing required field %q, want an ordinary generated body: %s", "id", rec.Body)
		}
		if _, ok := body["name"]; !ok {
			t.Errorf("body missing required field %q, want an ordinary generated body: %s", "name", rec.Body)
		}
	})

	// 6. "A bundle Encode of that workspace succeeds": a manual checkpoint
	// (internal/checkpoints.Repo.Create) reads every override row through
	// the SAME ForWorkspace call step 3 already exercised and then calls
	// bundle.Encode(b) directly (internal/checkpoints/repo.go's own
	// captureSnapshot) — the second of ValidateVariant's six call sites
	// A12 names, reached from a completely different door than the row
	// scan above.
	var checkpointID int64
	t.Run("bundle Encode: manual checkpoint capture succeeds", func(t *testing.T) {
		req := jsonRequest(t, http.MethodPost,
			"http://mocker.local/api/workspaces/"+strconv.FormatInt(ws.ID, 10)+"/checkpoints",
			map[string]string{"label": "c6 legacy patch"}, cookie, csrf)
		rec := do(handler, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create checkpoint (bundle.Encode): status = %d, want 201; body = %s", rec.Code, rec.Body)
		}
		var cp struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &cp); err != nil {
			t.Fatalf("decode checkpoint response: %v", err)
		}
		checkpointID = cp.ID
	})

	// 7. "A Decode of the result succeeds": rolling back to the checkpoint
	// just captured reads it back (internal/checkpoints.Repo.Get) and calls
	// bundle.Decode(doc) on the exact bytes step 6 wrote — proving the
	// non-conforming patch that degraded at serve time also round-trips
	// through the snapshot codec rather than losing the workspace's
	// history and undo the moment it is stored.
	t.Run("bundle Decode: rollback to that checkpoint succeeds", func(t *testing.T) {
		if checkpointID == 0 {
			t.Fatal("no checkpoint id captured — the Encode subtest above must run and pass first")
		}
		req := jsonRequest(t, http.MethodPost,
			"http://mocker.local/api/workspaces/"+strconv.FormatInt(ws.ID, 10)+"/rollback/"+strconv.FormatInt(checkpointID, 10),
			map[string]bool{"restoreData": false}, cookie, csrf)
		rec := do(handler, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("rollback (bundle.Decode): status = %d, want 200; body = %s", rec.Code, rec.Body)
		}
	})
}
