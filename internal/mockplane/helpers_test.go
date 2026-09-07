// respond_test.go was a WHITE-BOX test file (package mockplane, not
// mockplane_test): [chooseVariant], [acceptable], [wrapEnvelope],
// [clampedDelay]/[awaitDelay] and [setSafeHeader] are all unexported, and
// half of it was proving exactly what those private pieces do in isolation
// before ever going through HTTP. (ListFamily and DetailIDParam moved to
// [router] — P3a's D3 — so their own unit tests live in router_test.go now;
// this package still exercises them through [Plane.serveGenerated], see
// TestServeGenerated_DetailRouteMatchesListRow in respond_core_test.go.)
// The other half calls [Plane.serveGenerated] directly — bypassing
// ServeHTTP's workspace/CORS/preflight machinery entirely, already covered
// end to end by routes_test.go and plane_test.go in the sibling _test
// package — so these tests are squarely about respond.go's own contract:
// variant choice, negotiation, 204/205/HEAD/degraded suppression, the
// envelope, the delay, and the list-row/detail-card identity DESIGN §9
// promises.
//
// It grew to 3056 lines and was split by concern (mechanical moves only, no
// assertion changed) into respond_unit_test.go (the pure-unit tests named
// above), respond_core_test.go (serveGenerated's own basic contract),
// respond_pinned_test.go / respond_generated_test.go (the two response
// modes' own serve-time gates), respond_delay_test.go, respond_pause_test.go
// (P2a's park/release primitives and the handler around them) and
// respond_livestate_test.go (activeStatus/when/live-state precedence).
// helpers_test.go — this file — holds the harness every one of them draws
// on: respondTestPlane/fixtureRuntime/mustMatch and the fixture documents
// (itemSchemaDoc, widgetsFamilyDoc, …) most of those files share.
package mockplane

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// respondTestPlane builds a *Plane with a nil Source and nil SpecSource:
// every test below calls p.serveGenerated directly with a hand-built
// *runtime, so neither is ever consulted — only p.cfg (unused by
// serveGenerated itself, but required by New) and p.log (used for error
// logging) matter here.
func respondTestPlane() *Plane {
	return New(runtimeTestConfig(4<<20, 32), nil, nil, runtimeTestLogger())
}

// respondTestPlaneWithLiveState is respondTestPlane's P1c2 counterpart: a
// Plane with src wired via [Plane.SetLiveState], for the tests below that
// prove the live-state layer's own precedence over when[]/active_status —
// every other respond_test.go test deliberately leaves this nil, proving
// HARD RULE 6 the other way (a Plane that never calls SetLiveState serves
// exactly as it did before this phase).
func respondTestPlaneWithLiveState(src LiveStateSource) *Plane {
	p := respondTestPlane()
	p.SetLiveState(src)
	return p
}

func respondTestWorkspace() *workspaces.Workspace {
	return &workspaces.Workspace{ID: 1, Slug: "alex", Settings: domain.DefaultSettings()}
}

// mustResolver loads doc and builds a resolver over it the same way
// buildRuntime does (runtime.go), for tests that construct a *runtime by
// hand instead of going through SpecSource/runtimeFor.
func mustResolver(t *testing.T, doc string) *openapi.Resolver {
	t.Helper()
	d, _, err := openapi.Load([]byte(doc))
	if err != nil {
		t.Fatalf("openapi.Load: %v", err)
	}
	return openapi.NewResolver(d, openapi.DefaultRefBudget)
}

// fixtureRuntime builds a *runtime directly from a document, route table and
// variants map — the respond_test.go counterpart to runtime_test.go's
// widgetsSource()/runtimeFor path, skipping SpecSource entirely since these
// tests call serveGenerated (not runtimeFor) and need full control over
// settings per case.
func fixtureRuntime(t *testing.T, doc string, routes []router.Route, variants map[int64][]gen.ResponseVariant, settings domain.Settings) *runtime {
	t.Helper()
	return &runtime{
		table: router.Build(routes, settings.BasePath),
		gen: gen.New(mustResolver(t, doc), gen.Options{
			Seed:     settings.Seed,
			ListSize: settings.ListSize,
			NullRate: settings.NullRate,
			MaxBytes: 4 << 20,
			// Identity/Auth mirror buildRuntime's own step 4 (runtime.go):
			// without them here, an identity/jwt recipe test would see a
			// zero domain.Identity/domain.AuthSettings no matter what the
			// fixture's own `settings` carries, and would be proving
			// nothing about the real wiring.
			Identity: settings.Identity,
			Auth:     settings.Auth,
		}),
		variants: variants,
		settings: settings,
	}
}

// fixtureRuntimeWithOverrides is fixtureRuntime's P1c counterpart: the same
// runtime, with op_overrides rows attached and compiled directly onto the
// unexported fields — this file is white-box (package mockplane), so it can
// do exactly what runtime_test.go's own fixtures do (e.g.
// TestLookupOverride_KeysOnPathNotCanonicalPath's `&runtime{overrides: ...}`)
// rather than standing up a real OverrideSource/SpecSource round trip just to
// exercise serveGenerated's application of them.
func fixtureRuntimeWithOverrides(t *testing.T, doc string, routes []router.Route, variants map[int64][]gen.ResponseVariant, settings domain.Settings, rows map[string]*overrides.Row) *runtime {
	t.Helper()
	rt := fixtureRuntime(t, doc, routes, variants, settings)
	rt.overrides = rows
	rt.recipeSets = buildRecipeSets(runtimeTestLogger(), "test", rows)
	return rt
}

func mustMatch(t *testing.T, rt *runtime, method, path string) *router.Match {
	t.Helper()
	m, ok := rt.table.Match(method, NormalizeSegments(path))
	if !ok {
		t.Fatalf("no match for %s %s in the fixture table", method, path)
	}
	return m
}

// blankDoc is used by every test below that never resolves a schema
// (SchemaPtr == "" on every variant it exercises) — status-selection,
// 204-suppression and no-variant tests care only about which HTTPStatus and
// how much (if any) body reaches the wire, not its content.
const blankDoc = `{"openapi":"3.0.3","info":{"title":"t","version":"1.0.0"},"paths":{}}`

// itemSchemaDoc declares GET /items -> 200 application/json object
// {id, name}, both required — the fixture every negotiation/envelope test
// below reuses.
const itemSchemaDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/items": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": { "id": { "type": "integer" }, "name": { "type": "string" } },
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

func itemVariant() gen.ResponseVariant {
	return gen.ResponseVariant{
		OpRowID:    1,
		Selector:   "200",
		HTTPStatus: 200,
		MediaType:  "application/json",
		SchemaPtr:  "#/paths/~1items/get/responses/200/content/application~1json/schema",
		OpPointer:  "#/paths/~1items/get",
	}
}

func itemRoute() router.Route {
	return router.Route{OpRowID: 1, Method: "GET", Path: "/items", CanonicalPath: "/items", SourceOrder: 1}
}

// declaredContent204Doc mirrors the real acceptance document's own measured
// spec bug (gen.go's Body doc comment): a 204 response that nonetheless
// declares application/json content with a real schema. [gen.Generator.Body]
// WILL generate a body for it — the point of this test is that serveGenerated
// suppresses it on the wire regardless, since 204/205 never have a body
// (DESIGN §9), full stop.
const declaredContent204Doc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/items": {
      "get": {
        "responses": {
          "204": {
            "description": "no content, but a schema anyway (spec bug, measured real)",
            "content": {
              "application/json": {
                "schema": { "type": "object", "properties": { "id": { "type": "integer" } } }
              }
            }
          }
        }
      }
    }
  }
}`

// widgetsFamilyDoc declares the pair DESIGN §9's list contract calls "list
// row == detail card": GET /widgets (a top-level array of {id, name}) and
// GET /widgets/{id} (the same item shape, singular).
const widgetsFamilyDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/widgets": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": { "id": { "type": "integer" }, "name": { "type": "string" } },
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
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": { "id": { "type": "integer" }, "name": { "type": "string" } },
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

// cohortsFamilyDoc mirrors the real acceptance-document route shape the
// finding names: GET /tenants/{tenantId}/cohorts (a list) and
// GET /tenants/{tenantId}/cohorts/{cohortId} (its detail route),
// where BOTH path parameters end in "Id" and so both look id-shaped by
// name alone.
const cohortsFamilyDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/tenants/{tenantId}/cohorts": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": { "id": { "type": "integer" }, "name": { "type": "string" } },
                    "required": ["id", "name"]
                  }
                }
              }
            }
          }
        }
      }
    },
    "/tenants/{tenantId}/cohorts/{cohortId}": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": { "id": { "type": "integer" }, "name": { "type": "string" } },
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

// unsatisfiableDoc's schema cannot be satisfied by any value
// (minimum > maximum): [gen.Generator.Body] returns [gen.ErrUnsatisfiable]
// for it, the fixture requirement 9 needs.
const unsatisfiableDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/bad": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": { "type": "integer", "minimum": 10, "maximum": 5 }
              }
            }
          }
        }
      }
    }
  }
}`

// csvDoc declares a text/csv 200 response for GET /export — a non-JSON
// media type this document has exactly one real example of (DESIGN §9's
// "для не-JSON — плейсхолдер по типу").
const csvDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/export": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "text/csv": {
                "schema": { "type": "string" }
              }
            }
          }
        }
      }
    }
  }
}`

// itemOverrideKey is the op_overrides key every fixture below binds a row
// under: itemRoute/itemVariant's own GET /items, exactly as
// overrides.OpKey(route.Method, route.Path) computes it in production.
func itemOverrideKey() string { return overrides.OpKey("GET", "/items") }

// TestServeGenerated_GeneratedMode_DangerousResolvedMediaTypeRefused is the
// sibling of the test above, and the one that used to pass while the plane was
// vulnerable. That gate read `pinned && dangerousResolvedMediaType(...)`, which
// is the same shape of qualifier the admin write gate carried as
// `Mode == "pinned" &&` — and it left the cheapest route wide open: a GENERATED
// body needs no override row at all. An imported document that declares
// text/html on a response whose example carries a <script> was served verbatim,
// status 200, straight out of gen.finalize (which returns a text/* string raw).
// The document is operator-supplied through POST /api/specs, so this was an
// admin write path to a same-origin script in MOCKER_ROUTING=path.
//
// Note there is NO overrides row here at all — that is the entire point.
//
// The document below declares a text/html response WITH a schema on purpose.
// The first version of this test used the blank fixture, whose generated body
// is empty for want of a schema — so "the body is empty" held whether the gate
// fired or not, and the test passed against the vulnerable build. A guard whose
// test cannot fail is the thing this whole file exists to prevent.
const dangerousHTMLDoc = `{
	"openapi": "3.0.3",
	"info": { "title": "t", "version": "1.0.0" },
	"paths": {
		"/page": {
			"get": {
				"responses": {
					"200": {
						"description": "ok",
						"content": {
							"text/html": {
								"schema": { "type": "string", "example": "<script>alert(document.cookie)</script>" }
							}
						}
					}
				}
			}
		}
	}
}`

// sessionSchemaDoc/sessionVariant/sessionRoute are the jwt-recipe fixture:
// GET /session -> 200 application/json {token: string} — separate from
// itemSchemaDoc because a jwt recipe needs a string-typed leaf to bind to,
// and item's own schema has none.
const sessionSchemaDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/session": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": { "token": { "type": "string" } },
                  "required": ["token"]
                }
              }
            }
          }
        }
      }
    }
  }
}`

func sessionVariant() gen.ResponseVariant {
	return gen.ResponseVariant{
		OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1session/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1session/get",
	}
}

func sessionRoute() router.Route {
	return router.Route{OpRowID: 1, Method: "GET", Path: "/session", CanonicalPath: "/session", SourceOrder: 1}
}

// planeP2aPause is this file's own tiny fixture builder for the pause
// tests below: a real [livestate.Store] with exactly one pause directive
// set on GET /items, and the [livestate.Effect] Apply hands back for it —
// [livestate.Store.Apply] itself never blocks (reserveParkLocked only
// increments a counter), so building this needs no goroutine of its own.
// Prefixed planeP2a, not just "pause...", so it cannot collide with a test
// helper another agent adds to this same package in this same run.
func planeP2aPause(t *testing.T, workspaceID int64) (*livestate.Store, livestate.Effect) {
	t.Helper()
	store := livestate.NewStore(0, nil)
	if err := store.Set(workspaceID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/items"}, Action: livestate.ActionPause,
	}); err != nil {
		t.Fatalf("store.Set(pause): %v", err)
	}
	eff := store.Apply(workspaceID, "GET", "/items")
	if !eff.Pause {
		t.Fatalf("Apply.Pause = false, want true")
	}
	return store, eff
}

// failingResponseWriter is an http.ResponseWriter whose Write always fails,
// for the one branch httptest.ResponseRecorder can never reach on its own:
// respond.go's final w.Write(body) error path (the Debug "write generated
// response" log). Header/WriteHeader delegate to a real ResponseRecorder so
// status/headers stay observable; only the body write is made to fail.
type failingResponseWriter struct {
	*httptest.ResponseRecorder
}

func (w *failingResponseWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("failingResponseWriter: simulated write failure")
}

// recordingHandler is a minimal slog.Handler that captures every record it
// is handed, guarded by a mutex because slog does not promise its caller
// runs handlers serially. It always reports Enabled — including Debug,
// which slog's own handlers filter out by default — so an absent record
// proves the call site never logged, never that the level was cut upstream.
// This is the only way to tell "checked the error and logged it" apart from
// "ignored it": status/headers/body are byte-identical either way, since
// Header()/WriteHeader() have already committed by the time Write runs.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

// debugRecord returns the first Debug-level record with the given message,
// or nil if none was emitted.
func (h *recordingHandler) debugRecord(msg string) *slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.records {
		if h.records[i].Level == slog.LevelDebug && h.records[i].Message == msg {
			rec := h.records[i]
			return &rec
		}
	}
	return nil
}
