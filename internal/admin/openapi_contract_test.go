package admin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// specPath is api/openapi.json seen from this package's directory. It is the
// contract web/ generates its whole API client from (orval), so a drift
// between it and [Server.routes] ships a client that calls routes the server
// does not have — or leaves routes the server does have unreachable from the
// UI. Both directions are failures, and both are asserted below.
const specPath = "../../api/openapi.json"

// contractDoc is the sliver of OpenAPI this test needs. Deliberately NOT
// internal/openapi.Load: that package is mocker's importer for the documents
// USERS bring, and pointing it at mocker's own contract would couple a test
// of the admin plane to the behaviour of an unrelated subsystem — a warning
// or degradation added there would then fail this test for no reason of its
// own.
type contractDoc struct {
	OpenAPI string                                `json:"openapi"`
	Paths   map[string]map[string]json.RawMessage `json:"paths"`
}

// httpMethods are the keys of a Path Item Object that name an operation.
// "parameters" (and "summary"/"description"/"servers") are siblings of these
// and must not be mistaken for routes.
var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// loadContractRoutes returns every "METHOD /path" the contract declares.
func loadContractRoutes(t *testing.T) map[string]bool {
	t.Helper()

	raw, err := os.ReadFile(filepath.FromSlash(specPath))
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	var doc contractDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse contract: %v", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.1") {
		t.Fatalf("contract must stay OpenAPI 3.1 (orval and this test both read it as such); got %q", doc.OpenAPI)
	}

	out := make(map[string]bool)
	for path, item := range doc.Paths {
		for key := range item {
			if !httpMethods[key] {
				continue
			}
			out[strings.ToUpper(key)+" "+path] = true
		}
	}
	return out
}

// serverRoutes returns every pattern [Server.routes] registers. The receiver
// is a zero Server on purpose: building the table only takes method values,
// it never calls one, so no dependency has to be wired to ask this question.
func serverRoutes(t *testing.T) map[string]bool {
	t.Helper()

	out := make(map[string]bool)
	for _, rt := range (&Server{}).routes() {
		if out[rt.pattern] {
			t.Errorf("route registered twice: %q", rt.pattern)
		}
		out[rt.pattern] = true
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestOpenAPIContract_coversEveryRegisteredRoute is the drift guard in the
// direction that breaks the UI silently: a handler exists, the contract does
// not mention it, so orval never generates a client function for it and the
// route is unreachable from the app that is supposed to be its only caller.
func TestOpenAPIContract_coversEveryRegisteredRoute(t *testing.T) {
	contract := loadContractRoutes(t)

	var missing []string
	for _, pattern := range sortedKeys(serverRoutes(t)) {
		if !contract[pattern] {
			missing = append(missing, pattern)
		}
	}
	if len(missing) > 0 {
		t.Errorf("routes registered by internal/admin but absent from %s:\n  %s\n\nAdd them to the contract — web/ generates its entire client from it.",
			specPath, strings.Join(missing, "\n  "))
	}
}

// TestOpenAPIContract_declaresNoRouteTheServerLacks is the other direction,
// and the one that breaks at runtime rather than at build time: the contract
// promises a route, orval generates a typed function for it, and the call
// 404s (or 405s) against a server that never registered it.
func TestOpenAPIContract_declaresNoRouteTheServerLacks(t *testing.T) {
	registered := serverRoutes(t)

	var extra []string
	for _, pattern := range sortedKeys(loadContractRoutes(t)) {
		if !registered[pattern] {
			extra = append(extra, pattern)
		}
	}
	if len(extra) > 0 {
		t.Errorf("routes declared in %s that internal/admin does not register:\n  %s\n\nEither register the handler or drop it from the contract — a generated client would call it and get a 404.",
			specPath, strings.Join(extra, "\n  "))
	}
}

// TestOpenAPIContract_stateChangingRoutesRequireCSRF pins the one security
// property a generated client cannot infer on its own. Every state-changing
// route on this plane is behind [Server.enforceCSRF]; login alone is exempt
// from the token check because there is no session to take a token from yet.
// A new POST/PUT/PATCH/DELETE that forgets to declare csrfToken in the
// contract teaches the UI it may omit the header — and the request then fails
// at runtime with a 403 nobody predicted.
func TestOpenAPIContract_stateChangingRoutesRequireCSRF(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash(specPath))
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}

	// Decoded key-by-key rather than into one nested struct: a Path Item
	// Object's "parameters" sibling is an ARRAY, so a map whose value type is
	// the operation struct fails to unmarshal the whole document on it.
	var doc contractDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse contract: %v", err)
	}

	for path, item := range doc.Paths {
		for method, rawOp := range item {
			switch strings.ToUpper(method) {
			case "POST", "PUT", "PATCH", "DELETE":
			default:
				continue
			}
			if path == loginPath {
				continue // exempt from the token check, and only from that one
			}

			var op struct {
				Security []map[string][]string `json:"security"`
			}
			if err := json.Unmarshal(rawOp, &op); err != nil {
				t.Fatalf("parse %s %s: %v", strings.ToUpper(method), path, err)
			}

			var declaresCSRF bool
			for _, req := range op.Security {
				if _, ok := req["csrfToken"]; ok {
					declaresCSRF = true
				}
			}
			if !declaresCSRF {
				t.Errorf("%s %s changes state but does not declare the csrfToken security requirement; enforceCSRF will answer 403 and the generated client will not know why",
					strings.ToUpper(method), path)
			}
		}
	}
}

// TestOpenAPIContract_declaresResetDataSchemas is clause 32's SCHEMA half
// (decisions.md mocker-p3b-resources D10) — the half this file did NOT
// already check before P3b: the two tests above compare routes and
// csrfToken, never schemas, so an operation whose response schema is
// undeclared reaches orval as an untyped result and fails only at
// `tsc --noEmit`, a bar this package's own suite never runs. reset-data's
// two wire shapes must be declared under exactly these two names — the
// SCREENS section imports them as `ResetDataRequest`/`ResetDataResult`
// verbatim (this slice's own naming choice, recorded in its summary).
func TestOpenAPIContract_declaresResetDataSchemas(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash(specPath))
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse contract: %v", err)
	}
	for _, name := range []string{"ResetDataRequest", "ResetDataResult"} {
		if _, ok := doc.Components.Schemas[name]; !ok {
			t.Errorf("components.schemas.%s is not declared in %s", name, specPath)
		}
	}
}

// TestMigrations_stillExactlyThreeFiles is clause 32's other new assertion,
// updated by P3h: P3b through P3g added no `0003_` migration (D2 R3 — every
// column those slices read or wrote existed since P0) and nothing anywhere
// else in this tree counted that before. P3h is the first slice since
// `0002_edit_version.sql` that genuinely needs a new column — entities has
// no base-scope key to widen — so `0003_base_scope.sql` is the first legitimate
// third file, and the pinned count moves from 2 to 3 for exactly that reason.
// A fourth file here means a later slice took a wrong turn, or that this test
// itself needs updating on purpose again — either way, a silent pass is the
// wrong outcome. P6a (decisions.md mocker-p6a-sse D20) is that update on
// purpose: `0004_traffic_autoincrement.sql` rebuilds `traffic` with
// AUTOINCREMENT so a cleared table never reissues an id a stream or poll
// cursor still points past — the first rebuild migration in this tree, and
// the count moves from 3 to 4 for exactly that reason — and P6b
// (mocker-p6b-sse-mock D2) to 5, `0005_custom_endpoints_stream.sql`, the
// kind/stream columns and their CHECK, the second rebuild — and A6
// (decisions.md mocker-a6-assets D1) to 6, `0006_assets.sql`, the first
// table this tree has added since P0 — and A15 (the 2026-09-03 audit) to
// 7, `0007_fk_indexes.sql`, seven plain CREATE INDEX over the foreign-key
// columns that had none (EXPLAIN showed `DELETE FROM resources` scanning
// three tables whole), ADD-only, no rebuild.
func TestMigrations_stillExactlyEightFiles(t *testing.T) {
	entries, err := os.ReadDir(filepath.FromSlash("../../internal/store/migrations"))
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var sqlFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			sqlFiles = append(sqlFiles, e.Name())
		}
	}
	if len(sqlFiles) != 8 {
		t.Errorf("internal/store/migrations/ holds %d .sql files (%v), want exactly 8", len(sqlFiles), sqlFiles)
	}
}

// --- clause 33(a), the api/openapi.json half (decisions.md
// mocker-p3b-resources D10/D14.2) ------------------------------------------

// collectDescriptions walks raw (an already-decoded any) and returns every
// string value found under a "description" key, anywhere in the document —
// paths, operations, responses, parameters, schemas, tags, all of it. This
// is what makes the screen below run over the CANONICAL population D14.2
// asks for, not a hand-picked subset of it.
func collectDescriptions(raw any, out *[]string) {
	switch v := raw.(type) {
	case map[string]any:
		for key, val := range v {
			if key == "description" {
				if s, ok := val.(string); ok {
					*out = append(*out, s)
				}
			}
			collectDescriptions(val, out)
		}
	case []any:
		for _, e := range v {
			collectDescriptions(e, out)
		}
	}
}

func loadAllDescriptions(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(specPath))
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse contract: %v", err)
	}
	var out []string
	collectDescriptions(doc, &out)
	return out
}

// forbiddenPhrasesHard is D14.2's four HARD phrases: the ones this slice
// makes FALSE and which must not survive ANYWHERE in api/openapi.json's
// canonical description population. Two of the eleven phrases D14.2 names
// collapse into one check here (`entity/resource data in this build` and
// `resource/entity data` both assert "no entity data in this build", the
// warrant P3b falsifies at both of its openapi.json sites) — the other two
// are `no such data to restore` (which no site in this document ever used,
// screened here rather than skipped, per D14.2's own "record which
// descriptions it read and dismissed") and `op_overrides and
// custom_endpoints` as a claim about what a ROLLBACK RESTORES.
// `resource/entity restore is a later phase` is a fifth: a fourth site
// (rollbackWorkspace's 400 response, api/openapi.json:5287) used this
// wording instead of `resource/entity data`, so it slipped past the first
// four phrases and past D14.2's own three-site enumeration; caught by
// review rather than by this list, and added here so a future stale
// variant is caught mechanically.
var forbiddenPhrasesHard = []string{
	"entity/resource data in this build",
	"resource/entity data",
	"no such data to restore",
	"op_overrides and custom_endpoints",
	"resource/entity restore is a later phase",
}

// TestOpenAPIContract_hardForbiddenPhrasesDoNotSurvive is clause 33(a)'s
// HARD assertion, the api/openapi.json half (the internal/mcp half over
// assembled tools/list belongs to the next section). A match here is a
// description this slice was supposed to falsify and did not.
func TestOpenAPIContract_hardForbiddenPhrasesDoNotSurvive(t *testing.T) {
	descriptions := loadAllDescriptions(t)
	for _, phrase := range forbiddenPhrasesHard {
		for _, d := range descriptions {
			if strings.Contains(d, phrase) {
				t.Errorf("forbidden phrase %q survives in a description: %q", phrase, d)
			}
		}
	}
}

// forbiddenPhrasesExploratory is D14.2's remaining seven phrases — the
// SCREEN, not a check: it decides nothing on its own (two of the seven,
// "only settings" and "what the attached spec alone would serve", would go
// red against otherwise-correct descriptions if asserted hard), and exists
// so a human reading this test's own log output sees every match D14.2
// asks the run to record, rather than a silent pass that never looked.
var forbiddenPhrasesExploratory = []string{
	"overrides and custom endpoints",
	"op_overrides rows and custom_endpoints rows",
	"settings/op_overrides/custom_endpoints",
	"settings, edits and endpoints",
	"only settings",
	"settings+overrides+endpoints",
	"what the attached spec alone would serve",
}

// TestOpenAPIContract_screenExploratoryPhrases is clause 33(b): an
// exploratory run, logged rather than asserted, over the seven phrases
// forbiddenPhrasesHard does not cover. Every match is read and recorded
// here — as of this slice, "overrides and custom endpoints" is the one
// live match, in rollbackWorkspace's own description, and it is an
// ACCURATE statement of what that route restores (P3b's UPSERT-only
// resource restore is described separately, right beside it), not a false
// claim this slice needs to falsify.
func TestOpenAPIContract_screenExploratoryPhrases(t *testing.T) {
	descriptions := loadAllDescriptions(t)
	for _, phrase := range forbiddenPhrasesExploratory {
		for _, d := range descriptions {
			if strings.Contains(d, phrase) {
				t.Logf("exploratory phrase %q found (not a failure — read and dismissed): %q", phrase, d)
			}
		}
	}
}
