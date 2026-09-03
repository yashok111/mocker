// Package specs_test's acceptance test proves P1a's import, operation index
// and route table against the 130-operation acceptance corpus
// (internal/testspec/testdata/acceptance.json — the structure-preserving,
// sanitised twin of a real internal API document), not a hand-built fixture. Every other test in this package exercises the
// code against minimalOAS30 or similarly small documents; this file exists
// because a $ref-heavy, dialect-quirky real API is the only thing that can
// catch "the indexer never resolves a $ref before checking for content" —
// 221 of the document's 419 response entries ARE a $ref, and every one of
// this file's hand-written sibling fixtures still passes even if that bug is
// present.
//
// The corpus comes from internal/testspec (embedded since 2026-09-03; until
// then the original document was read from a gitignored path and this whole
// file SKIPPED on a fresh clone). testspec.Bytes is exactly this file's own
// former acceptanceSpecBytes/repoRoot pair, moved there in P1b so every
// phase's acceptance test (internal/gen's included) shares one copy instead
// of growing a third near-duplicate.
//
// This file deliberately does NOT reuse repo_test.go's newTestDB/testConfig/
// newRepo helpers, even though they would fit: this package's other files
// are being written by a different agent in parallel with this one (see the
// phase's HARD RULE 1), so a symbol this file depends on could be renamed or
// removed out from under it mid-run. Its own acceptanceDB/acceptanceConfig
// below are intentional near-duplicates, kept local so this file's own
// correctness never depends on a concurrent edit elsewhere in the package.
package specs_test

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testspec"
)

// acceptanceSpecBytes is this file's own name for testspec.Bytes, kept as a
// one-line wrapper so every call site below reads the same as it always
// has.
func acceptanceSpecBytes(t *testing.T) []byte {
	t.Helper()
	return testspec.Bytes(t)
}

// acceptanceConfig is the TEST CONFIG trap list's minimum-safe Config, with
// MaxBody generous enough for a real ~340KB document.
func acceptanceConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		BaseDomain:     "mock.local",
		AdminHost:      "mocker.local",
		Routing:        config.RoutingHost,
		ReservedPrefix: "/__mocker",
		AuthMode:       config.AuthShared,
		DataDir:        t.TempDir(),
		MaxBody:        10 << 20,
		MaxResponse:    4 << 20,
		RuntimeCache:   32,
		Dev:            true,
	}
}

// acceptanceDB opens a fresh, migrated SQLite file under t.TempDir().
func acceptanceDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), t.TempDir()+"/mocker.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// acceptanceHTTPVerbs lists the path-item keys treated as HTTP operations. It
// is a fixed, independent copy of the OpenAPI operation-key set — NOT
// internal/specs's own unexported httpMethods list in index.go — precisely
// so this file's oracle (below) cannot share a bug with the code path it is
// meant to verify.
var acceptanceHTTPVerbs = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// oracleResolvesToContent independently reports whether node — a raw
// response entry, possibly a $ref — resolves to a response object exposing
// a non-empty "content" map. It calls [openapi.Resolver] directly rather
// than going through internal/specs's own indexResponses (the code under
// test in index.go), so a bug in THIS oracle and a bug in the real indexer
// cannot cancel each other out and hide from this test. An unresolved $ref
// is treated as no-content, matching the degrade-don't-fail posture the real
// indexer takes on the same failure (it never occurs in this document: every
// response $ref here resolves in one hop).
func oracleResolvesToContent(res *openapi.Resolver, node any) bool {
	resolved, err := res.ResolveNode(node)
	if err != nil {
		return false
	}
	obj, ok := resolved.(map[string]any)
	if !ok {
		return false
	}
	content, ok := obj["content"].(map[string]any)
	return ok && len(content) > 0
}

// buildContentOracle walks doc's raw paths object and records, for every
// (method, path, selector) response entry, whether it resolves to content.
// Keyed by the same three fields [specs.Response] stores (Selector is the
// selector exactly as written, e.g. "204" or "default"), so the main test
// can join a stored row straight back to this map.
func buildContentOracle(doc *openapi.Document, res *openapi.Resolver) map[[3]string]bool {
	paths, _ := doc.Root()["paths"].(map[string]any)

	oracle := make(map[[3]string]bool)
	for p, rawItem := range paths {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		for _, verb := range acceptanceHTTPVerbs {
			opRaw, present := item[verb]
			if !present {
				continue
			}
			opObj, ok := opRaw.(map[string]any)
			if !ok {
				continue
			}
			responses, ok := opObj["responses"].(map[string]any)
			if !ok {
				continue
			}
			for sel, node := range responses {
				oracle[[3]string{strings.ToUpper(verb), p, sel}] = oracleResolvesToContent(res, node)
			}
		}
	}
	return oracle
}

// TestAcceptance_RealDocument imports the acceptance corpus and checks every
// fact P1a's launch notes measured against it: format, operation count,
// path fidelity, base-path ambiguity, response defaulting, $ref-resolved
// media types, hash dedup, and the route table's static-over-param ordering
// on all eight real collisions the document contains.
func TestAcceptance_RealDocument(t *testing.T) {
	raw := acceptanceSpecBytes(t)

	db := acceptanceDB(t)
	repo := specs.NewRepo(db, acceptanceConfig(t))
	ctx := t.Context()

	res, err := repo.Import(ctx, specs.ImportInput{
		Name:     "Acceptance",
		Source:   "upload",
		Document: raw,
	})
	if err != nil {
		t.Fatalf("import acceptance document: %v", err)
	}
	specID := res.Spec.ID

	t.Run("report", func(t *testing.T) {
		if res.Report.Format != openapi.FormatOAS30 {
			t.Errorf("report format = %q, want %q", res.Report.Format, openapi.FormatOAS30)
		}
		if res.Report.Operations != 130 {
			t.Errorf("report.Operations = %d, want 130", res.Report.Operations)
		}
		if res.Report.Degraded != 0 {
			t.Errorf("report.Degraded = %d, want 0", res.Report.Degraded)
		}
		// The document has NO root servers[] (jq: .servers == null) and its
		// 110 path items disagree on their own servers[] path component (97
		// -> "", 9 -> "/api/v1", 4 declare an empty servers array and are
		// skipped) — verified with jq against the real file, not assumed.
		if res.Report.BasePathOrigin != openapi.BaseAmbiguous {
			t.Errorf("base path origin = %q, want %q", res.Report.BasePathOrigin, openapi.BaseAmbiguous)
		}
		if res.Report.BasePath != "" {
			t.Errorf("report base path = %q, want empty (ambiguous)", res.Report.BasePath)
		}
		if res.Spec.BasePath != "" {
			t.Errorf("stored spec base path = %q, want empty", res.Spec.BasePath)
		}
	})

	ops, err := repo.Operations(ctx, specID, 0, 0)
	if err != nil {
		t.Fatalf("list operations for spec %d: %v", specID, err)
	}
	if len(ops) != 130 {
		t.Fatalf("operations = %d, want 130", len(ops))
	}

	t.Run("operation_rows", func(t *testing.T) {
		// Decoded independently of openapi.Load/Document — a fresh
		// encoding/json pass over the raw bytes — so "byte-identical to its
		// paths[] key" is checked against the document itself, not against
		// whatever internal/openapi's own normalization pass produced.
		var rawDoc struct {
			Paths map[string]json.RawMessage `json:"paths"`
		}
		if err := json.Unmarshal(raw, &rawDoc); err != nil {
			t.Fatalf("independently decode acceptance document paths: %v", err)
		}

		for _, op := range ops {
			if op.ParseError != nil {
				t.Errorf("operation %s %s: parse_error = %q, want none", op.Method, op.Path, *op.ParseError)
			}
			if _, ok := rawDoc.Paths[op.Path]; !ok {
				t.Errorf("operation path %q is not byte-identical to any paths[] key in the document", op.Path)
			}
		}
	})

	// A second, independent Load of the same bytes, used only to build the
	// content-resolution oracle below — never the *specs.Repo's own
	// internally-loaded document, so this test's oracle shares no state with
	// the code path it verifies.
	doc, _, err := openapi.Load(raw)
	if err != nil {
		t.Fatalf("independently re-load acceptance document: %v", err)
	}
	oracle := buildContentOracle(doc, openapi.NewResolver(doc, openapi.DefaultRefBudget))

	t.Run("response_rows", func(t *testing.T) {
		var (
			csvCount        int
			noContentRows   int
			sawException200 bool
			feedbackChecked bool
		)

		for _, op := range ops {
			responses, err := repo.Responses(ctx, op.ID)
			if err != nil {
				t.Fatalf("responses for operation %d (%s %s): %v", op.ID, op.Method, op.Path, err)
			}

			defaults := 0
			for _, rr := range responses {
				if rr.IsDefault {
					defaults++
				}

				if rr.MediaType != nil && *rr.MediaType == "text/csv" {
					csvCount++
					if rr.SchemaPtr == nil {
						t.Errorf("%s %s response %q: text/csv row has a nil schema_ptr, want non-nil",
							op.Method, op.Path, rr.Selector)
					}
				}

				if rr.Selector == "" {
					// A synthesized fallback row (empty responses object).
					// Never happens in this document — every operation has a
					// real responses map — so there is nothing to join
					// against the oracle.
					continue
				}

				hasContent, found := oracle[[3]string{op.Method, op.Path, rr.Selector}]
				if !found {
					t.Fatalf("oracle has no entry for stored response %s %s %q — the DB has a row the document walk never produced",
						op.Method, op.Path, rr.Selector)
				}
				if hasContent && rr.MediaType == nil {
					t.Errorf("%s %s response %q: document resolves to response content but media_type is NULL",
						op.Method, op.Path, rr.Selector)
				}
				if !hasContent {
					noContentRows++
					switch {
					case op.Method == http.MethodDelete && op.Path == "/api/v1/requests/{requestId}" && rr.Selector == "200":
						sawException200 = true
					case rr.Selector == "204":
						// One of the 16 legitimately content-less 204s.
					default:
						// NOT keyed on "selector == 204": some 204 rows in
						// this document DO carry content (21 total 204
						// entries, only 16 of them empty), so a blanket
						// 204-allowance would hide a real regression on the
						// other 5. Anything else landing here is exactly
						// that kind of regression.
						t.Errorf("unexpected no-content response %s %s %q (status_origin=%s): not one of the documented 17 exceptions",
							op.Method, op.Path, rr.Selector, rr.StatusOrigin)
					}
				}
			}

			if defaults != 1 {
				t.Errorf("%s %s: %d is_default responses, want exactly 1", op.Method, op.Path, defaults)
			}

			if op.Method == http.MethodGet && op.Path == "/api/v1/feedback/section" {
				feedbackChecked = true
				if op.ParseError != nil {
					t.Errorf("GET /api/v1/feedback/section: parse_error = %q, want none (its schema tree reaches the recursive FeedbackSection schema)",
						*op.ParseError)
				}
			}
		}

		if csvCount != 1 {
			t.Errorf("text/csv response rows = %d, want exactly 1", csvCount)
		}
		if noContentRows != 17 {
			t.Errorf("no-content response rows = %d, want exactly 17 (16 x 204 + 1 x 200)", noContentRows)
		}
		if !sawException200 {
			t.Error("expected the one 200-with-no-content exception at DELETE /api/v1/requests/{requestId}, never saw it")
		}
		if !feedbackChecked {
			t.Error("GET /api/v1/feedback/section was not found among the indexed operations")
		}
	})

	t.Run("dedup", func(t *testing.T) {
		again, err := repo.Import(ctx, specs.ImportInput{
			Name:     "Acceptance (re-import)",
			Source:   "upload",
			Document: raw,
		})
		if !errors.Is(err, specs.ErrDuplicate) {
			t.Fatalf("re-import error = %v, want ErrDuplicate", err)
		}
		if again == nil || again.Spec == nil {
			t.Fatalf("re-import result = %+v, want the pre-existing spec", again)
		}
		if again.Spec.ID != specID {
			t.Errorf("re-import returned spec id %d, want the existing spec %d", again.Spec.ID, specID)
		}
	})

	t.Run("routes", func(t *testing.T) {
		routes, err := repo.Routes(ctx, specID)
		if err != nil {
			t.Fatalf("routes for spec %d: %v", specID, err)
		}
		if len(routes) != 130 {
			t.Fatalf("repo.Routes returned %d rows, want 130", len(routes))
		}

		table := router.Build(routes, "")
		if table.Len() != 130 {
			t.Errorf("table.Len() = %d, want 130", table.Len())
		}

		// router.Conflicts (by its own doc comment) only ever groups routes
		// with Custom set, and repo.Routes always builds Custom:false spec
		// routes (no custom endpoint exists before P2) — so this call is
		// trivially empty no matter what the document's canonical paths look
		// like. It is asserted because the task requires it, but it does NOT
		// prove this document's canonical paths are unique per method;
		// nothing in P1a proves that claim today.
		if conflicts := router.Conflicts(routes); len(conflicts) != 0 {
			t.Errorf("router.Conflicts = %v, want none", conflicts)
		}

		// The eight real static/{param} collisions DESIGN §8 rule 1 must
		// resolve in favor of the static route. request hits the static
		// sibling; paramReq/paramWant (where set) additionally check that
		// the {param} sibling still matches its own shape and decodes its
		// parameter correctly.
		type collision struct {
			name        string
			request     []string
			wantPath    string
			siblingPath string
			paramReq    []string
			paramWant   map[string]string
		}
		collisions := []collision{
			{
				name:        "badges/by-category",
				request:     []string{"api", "v1", "badges", "by-category"},
				wantPath:    "/api/v1/badges/by-category",
				siblingPath: "/api/v1/badges/{badgeId}",
				paramReq:    []string{"api", "v1", "badges", "badge-42"},
				paramWant:   map[string]string{"badgeId": "badge-42"},
			},
			{
				name:        "bulletins/count",
				request:     []string{"api", "v1", "bulletins", "count"},
				wantPath:    "/api/v1/bulletins/count",
				siblingPath: "/api/v1/bulletins/{bulletinId}",
				paramReq:    []string{"api", "v1", "bulletins", "bul-7"},
				paramWant:   map[string]string{"bulletinId": "bul-7"},
			},
			{
				name:        "bulletins/reminders-intervals",
				request:     []string{"api", "v1", "bulletins", "reminders-intervals"},
				wantPath:    "/api/v1/bulletins/reminders-intervals",
				siblingPath: "/api/v1/bulletins/{bulletinId}",
			},
			{
				name:        "tenants/requests",
				request:     []string{"api", "v1", "tenants", "requests"},
				wantPath:    "/api/v1/tenants/requests",
				siblingPath: "/api/v1/tenants/{tenantId}",
				paramReq:    []string{"api", "v1", "tenants", "tenant-9"},
				paramWant:   map[string]string{"tenantId": "tenant-9"},
			},
			{
				name:        "tenants/{tenantId}/cohorts/fiscal-years",
				request:     []string{"api", "v1", "tenants", "tenant-1", "cohorts", "fiscal-years"},
				wantPath:    "/api/v1/tenants/{tenantId}/cohorts/fiscal-years",
				siblingPath: "/api/v1/tenants/{tenantId}/cohorts/{cohortId}",
				paramReq:    []string{"api", "v1", "tenants", "tenant-1", "cohorts", "cohort-5"},
				paramWant:   map[string]string{"tenantId": "tenant-1", "cohortId": "cohort-5"},
			},
			{
				name:        "surveys/folders",
				request:     []string{"api", "v1", "surveys", "folders"},
				wantPath:    "/api/v1/surveys/folders",
				siblingPath: "/api/v1/surveys/{surveyId}",
				paramReq:    []string{"api", "v1", "surveys", "survey-3"},
				paramWant:   map[string]string{"surveyId": "survey-3"},
			},
			{
				name:        "surveys/themes",
				request:     []string{"api", "v1", "surveys", "themes"},
				wantPath:    "/api/v1/surveys/themes",
				siblingPath: "/api/v1/surveys/{surveyId}",
			},
			{
				name:        "surveys/cohorts",
				request:     []string{"api", "v1", "surveys", "cohorts"},
				wantPath:    "/api/v1/surveys/cohorts",
				siblingPath: "/api/v1/surveys/{surveyId}",
			},
		}

		for _, c := range collisions {
			t.Run(c.name, func(t *testing.T) {
				m, ok := table.Match("GET", c.request)
				if !ok {
					t.Fatalf("GET %v: no match, want the static route %q to win", c.request, c.wantPath)
				}
				if m.Route.Path != c.wantPath {
					t.Errorf("GET %v matched %q, want the static route %q (DESIGN §8 rule 1: static beats its {param} sibling %q)",
						c.request, m.Route.Path, c.wantPath, c.siblingPath)
				}

				if c.paramReq == nil {
					return
				}
				pm, ok := table.Match("GET", c.paramReq)
				if !ok {
					t.Fatalf("GET %v: no match, want the {param} sibling %q to still match its own shape", c.paramReq, c.siblingPath)
				}
				if pm.Route.Path != c.siblingPath {
					t.Errorf("GET %v matched %q, want the {param} sibling %q", c.paramReq, pm.Route.Path, c.siblingPath)
				}
				if !maps.Equal(pm.Params, c.paramWant) {
					t.Errorf("GET %v params = %v, want %v", c.paramReq, pm.Params, c.paramWant)
				}
			})
		}
	})
}
