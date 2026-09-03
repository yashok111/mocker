// mcp_resources_test.go is clause 34's own new fixture (decisions.md
// mocker-p3b-resources D10): every other test in this package drives a
// fakeCaller or scriptedCaller (mcp_test.go's own comment on why), and
// neither one ever opens a database — so "its effect is observed in the
// database" is unbuildable against either. This file builds ONE endpoint
// over a REAL *admin.Server on a REAL, migrated SQLite database instead —
// the same wiring internal/admin/loopback_test.go's own
// loopbackTestServer uses, re-created here rather than reused because that
// helper is unexported in a _test.go file this package cannot reach into.
// internal/admin does not import internal/mcp in production (checked, not
// assumed — routes_test.go's own comment makes the identical claim for the
// same edge), so importing it here in the other direction opens no cycle.
package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/workspaces"
)

// resourcesFixtureDoc is a small, hand-built OpenAPI document declaring one
// resource family — /widgets — an integer id, a bare-array 200 and a POST
// whose request body is the identical $ref as the item schema (the shape
// internal/resources' own computeWriteForm answers "bare" for), so
// confirming it takes over both GET routes and its POST/DELETE.
const resourcesFixtureDoc = `{
  "openapi": "3.0.3",
  "info": {"title": "mcp resources fixture", "version": "1.0.0"},
  "paths": {
    "/widgets": {
      "get": {
        "operationId": "listWidgets",
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"type": "array", "items": {"$ref": "#/components/schemas/Widget"}}
        }}}}
      },
      "post": {
        "operationId": "createWidget",
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Widget"}}}},
        "responses": {"201": {"description": "d", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/Widget"}
        }}}}
      }
    },
    "/widgets/{id}": {
      "get": {
        "operationId": "getWidget",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/Widget"}
        }}}}
      },
      "delete": {
        "operationId": "deleteWidget",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {"204": {"description": "d"}}
      }
    }
  },
  "components": {"schemas": {
    "Widget": {"type": "object", "properties": {"id": {"type": "integer"}, "name": {"type": "string"}}}
  }}
}`

// resourcesTestConfig is loopback_test.go's own loopbackTestConfig, spelled
// out again here for the identical reason that file gives for its own copy:
// it is unexported, in package admin, a different Go package this file
// cannot reach into.
func resourcesTestConfig(t *testing.T) *config.Config {
	t.Helper()
	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword(): %v", err)
	}
	return &config.Config{
		BaseDomain:          "mock.local",
		AdminHost:           "mocker.local",
		Routing:             config.RoutingHost,
		ReservedPrefix:      "/__mocker",
		AuthMode:            config.AuthShared,
		SharedPasswordHash:  hash,
		DataDir:             t.TempDir(),
		MaxBody:             10 << 20,
		MaxResponse:         4 << 20,
		TrafficMaxBody:      64 << 10,
		MaxEntities:         1000, // D11's own default (config.Load's, spelled out here since this fixture bypasses Load)
		RuntimeCache:        32,
		Dev:                 true,
		CheckpointRetention: 20,
	}
}

// newResourcesTestServer builds a fully wired *admin.Server over a fresh,
// migrated SQLite database at cfg.DBPath(). It implements Caller, so it is
// what every tool in this file's tests dispatches through — the SAME
// resourcesRepo/specsRepo an equivalent real deployment would build,
// pointed at the one database importResourcesFixtureSpec and
// insertResourcesTestWorkspace also write to directly.
func newResourcesTestServer(t *testing.T, cfg *config.Config) (*admin.Server, *store.DB) {
	t.Helper()
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
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return admin.New(cfg, sessions, ws, db, log), db
}

// importResourcesFixtureSpec imports resourcesFixtureDoc, deriving its one
// resource_suggestions row in the same transaction as its operations,
// exactly as a real upload would — the identical move
// internal/resources/repo_test.go's own importFixtureSpec makes, using the
// exported specs.Repo.Import directly rather than driving the admin HTTP
// route: POST /api/specs is deliberately outside mcpAllowedRoutes
// (loopback.go's own D12 list), so no tool in this package could reach it
// even if this fixture wanted to.
func importResourcesFixtureSpec(t *testing.T, db *store.DB, cfg *config.Config) int64 {
	t.Helper()
	sr := specs.NewRepo(db, cfg)
	res, err := sr.Import(t.Context(), specs.ImportInput{Name: "fixture", Source: "upload", Document: []byte(resourcesFixtureDoc)})
	if err != nil {
		t.Fatalf("import fixture spec: %v", err)
	}
	return res.Spec.ID
}

// insertResourcesTestWorkspace writes a workspaces row directly, the same
// pattern internal/resources/repo_test.go's own insertWorkspace uses (and
// internal/checkpoints', internal/scenarios' and internal/specs' own test
// helpers before it): every package that owns a table writes it directly in
// its own tests rather than only ever exercising it through a caller three
// layers up.
func insertResourcesTestWorkspace(t *testing.T, db *store.DB, slug string, specID int64, listSize int) int64 {
	t.Helper()
	settings := domain.Settings{Seed: 1, ListSize: listSize}
	settingsJSON, err := settings.MarshalJSONStable()
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	now := time.Now().Unix()
	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO workspaces (slug, name, spec_id, revision, settings, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?, ?)`,
		slug, slug, specID, string(settingsJSON), now, now)
	if err != nil {
		t.Fatalf("insert workspace %q: %v", slug, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	return id
}

// entityRowCount reads entities directly off the database this test's
// tools just wrote to (or read from) through CallAsMCP — the "observed in
// the database" half clause 34 asks for on the two WRITE tools.
func entityRowCount(t *testing.T, db *store.DB, resourceID int64) int {
	t.Helper()
	var n int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities WHERE resource_id = ?", resourceID).Scan(&n); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	return n
}

func resourceRowCount(t *testing.T, db *store.DB, workspaceID int64) int {
	t.Helper()
	var n int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM resources WHERE workspace_id = ?", workspaceID).Scan(&n); err != nil {
		t.Fatalf("count resources: %v", err)
	}
	return n
}

// TestResourceTools_wholeLifecycleThroughRealStore is clause 34: each of
// the four new tools reaches its route through CallAsMCP over a real
// *admin.Server on a real store, and what is observed is the DATABASE, not
// the tool's own return value — for the two WRITES (decide_resource,
// reset_resource_data) the effect is read back out of it directly by SQL,
// for the two READS (list_resource_suggestions, list_resources) the rows
// returned are asserted to be the rows the database holds at that moment.
func TestResourceTools_wholeLifecycleThroughRealStore(t *testing.T) {
	t.Parallel()

	cfg := resourcesTestConfig(t)
	adminSrv, db := newResourcesTestServer(t, cfg)
	specID := importResourcesFixtureSpec(t, db, cfg)
	const wsSlug = "widgets-ws"
	const seedCount = 3
	wsID := insertResourcesTestWorkspace(t, db, wsSlug, specID, seedCount)

	lb := newLoopback(adminSrv)
	ctx := opsTestCtx()

	// ---- list_resource_suggestions: READ, rows are what the database holds ----
	_, sugOut, err := handleListResourceSuggestions(lb)(ctx, nil, ListResourceSuggestionsInput{SpecID: specID})
	if err != nil {
		t.Fatalf("list_resource_suggestions: %v", err)
	}
	if len(sugOut.Suggestions) != 1 || sugOut.Suggestions[0].RouteFamily != "/widgets" {
		t.Fatalf("list_resource_suggestions = %+v, want exactly one /widgets suggestion", sugOut.Suggestions)
	}

	// ---- list_resources before any decision: READ, the row is undecided ----
	_, beforeOut, err := handleListResources(lb)(ctx, nil, ListResourcesInput{WorkspaceID: wsID})
	if err != nil {
		t.Fatalf("list_resources (before): %v", err)
	}
	if len(beforeOut.Families) != 1 || beforeOut.Families[0].Decision != nil {
		t.Fatalf("list_resources (before) = %+v, want one undecided /widgets row", beforeOut.Families)
	}

	// ---- decide_resource confirm: WRITE, observed by reading entities back out of the database ----
	_, confirmOut, err := handleDecideResource(lb)(ctx, nil, DecideResourceInput{
		WorkspaceID: wsID, RouteFamily: "/widgets", State: "confirmed",
	})
	if err != nil {
		t.Fatalf("decide_resource confirm: %v", err)
	}
	if confirmOut.Family.Decision == nil || *confirmOut.Family.Decision != "confirmed" {
		t.Fatalf("decide_resource confirm: Decision = %+v, want \"confirmed\"", confirmOut.Family.Decision)
	}
	if confirmOut.Family.ResourceID == nil {
		t.Fatal("decide_resource confirm: ResourceID is nil")
	}
	resourceID := *confirmOut.Family.ResourceID
	if got := entityRowCount(t, db, resourceID); got != seedCount {
		t.Fatalf("entities in the database after confirm = %d, want %d (seed_count)", got, seedCount)
	}

	// ---- reset_resource_data clear: WRITE, observed directly in the database ----
	_, clearOut, err := handleResetResourceData(lb)(ctx, nil, ResetResourceDataInput{
		WorkspaceID: wsID, Mode: "clear", ConfirmSlug: wsSlug,
	})
	if err != nil {
		t.Fatalf("reset_resource_data clear: %v", err)
	}
	if !clearOut.Changed || clearOut.Deleted != seedCount {
		t.Fatalf("reset_resource_data clear = %+v, want changed=true deleted=%d", clearOut, seedCount)
	}
	if got := entityRowCount(t, db, resourceID); got != 0 {
		t.Fatalf("entities in the database after clear = %d, want 0", got)
	}
	if got := resourceRowCount(t, db, wsID); got != 1 {
		t.Fatalf("resources rows in the database after clear = %d, want 1 (clear never deletes the resource itself)", got)
	}

	// ---- reset_resource_data reseed: WRITE, repopulates, observed directly in the database ----
	_, reseedOut, err := handleResetResourceData(lb)(ctx, nil, ResetResourceDataInput{
		WorkspaceID: wsID, Mode: "reseed", ConfirmSlug: wsSlug,
	})
	if err != nil {
		t.Fatalf("reset_resource_data reseed: %v", err)
	}
	if !reseedOut.Changed {
		t.Fatalf("reset_resource_data reseed = %+v, want changed=true", reseedOut)
	}
	if got := entityRowCount(t, db, resourceID); got != seedCount {
		t.Fatalf("entities in the database after reseed = %d, want %d", got, seedCount)
	}

	// ---- list_resources after reseed: READ, count matches the database ----
	_, afterOut, err := handleListResources(lb)(ctx, nil, ListResourcesInput{WorkspaceID: wsID})
	if err != nil {
		t.Fatalf("list_resources (after): %v", err)
	}
	if len(afterOut.Families) != 1 || afterOut.Families[0].EntityCount == nil || *afterOut.Families[0].EntityCount != seedCount {
		t.Fatalf("list_resources (after) = %+v, want one confirmed row with entityCount=%d", afterOut.Families, seedCount)
	}

	// ---- decide_resource decline of a confirmed family requires confirmSlug ----
	if _, _, err := handleDecideResource(lb)(ctx, nil, DecideResourceInput{
		WorkspaceID: wsID, RouteFamily: "/widgets", State: "declined",
	}); err == nil {
		t.Fatal("decide_resource decline without confirmSlug succeeded, want a refusal")
	}
	if got := resourceRowCount(t, db, wsID); got != 1 {
		t.Fatalf("resources rows after a refused decline = %d, want 1 (nothing changed)", got)
	}

	// ---- decide_resource decline with the right slug: WRITE, observed directly in the database ----
	_, declineOut, err := handleDecideResource(lb)(ctx, nil, DecideResourceInput{
		WorkspaceID: wsID, RouteFamily: "/widgets", State: "declined", ConfirmSlug: wsSlug,
	})
	if err != nil {
		t.Fatalf("decide_resource decline: %v", err)
	}
	if declineOut.Family.Decision == nil || *declineOut.Family.Decision != "declined" {
		t.Fatalf("decide_resource decline: Decision = %+v, want \"declined\"", declineOut.Family.Decision)
	}
	if got := resourceRowCount(t, db, wsID); got != 0 {
		t.Fatalf("resources rows after decline = %d, want 0 (declining a confirmed family deletes its row)", got)
	}
	if got := entityRowCount(t, db, resourceID); got != 0 {
		t.Fatalf("entities in the database after decline = %d, want 0 (cascade)", got)
	}
}

// TestToolsList_hasFortySixToolsAndIrreversibilityWarning is the other
// half of clause 34: tools/list returns 46 tools (A4's probe_workspace and
// list_resource_entities on top of P4a's 44, decisions.md
// mocker-a4-mcp-reach D1, D9), and reset_resource_data's PUBLISHED
// description carries the irreversibility warning D7 requires. This does
// not need the real-store fixture above — a description is static
// regardless of what Caller registerTools was handed — so it drives
// newTestEndpoint's fakeCaller, the same as every other tools/list test in
// this package.
func TestToolsList_hasFortyEightToolsAndIrreversibilityWarning(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()
	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	var env struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rec.Body.String())
	}
	if len(env.Result.Tools) != 63 {
		t.Errorf("tools/list returned %d tools, want 63", len(env.Result.Tools))
	}
	var found bool
	for _, tool := range env.Result.Tools {
		if tool.Name != "reset_resource_data" {
			continue
		}
		found = true
		if !strings.Contains(strings.ToLower(tool.Description), "irreversible") {
			t.Errorf("reset_resource_data's description does not carry the irreversibility warning: %q", tool.Description)
		}
	}
	if !found {
		t.Error("tools/list did not include reset_resource_data")
	}
}

// mcpForbiddenPhrasesHard mirrors internal/admin/openapi_contract_test.go's
// forbiddenPhrasesHard — D14.2's own comment names the MCP half over
// assembled tools/list as this section's job, the sibling openapi test's
// comment explicitly disclaiming it ("the internal/mcp half ... belongs to
// the next section"). The phrases are the same class: claims a checkpoint
// this slice falsifies. Screened over the ASSEMBLED description text (the
// JSON tools/list actually returns), not over .go source — tools_history.go
// splits some of these phrases across a `+` string-concatenation boundary,
// so a source grep would find what a real MCP client never sees, and miss
// what it does.
var mcpForbiddenPhrasesHard = []string{
	"no such data to restore",
	"what the attached spec alone would serve",
}

// TestToolsList_hardForbiddenPhrasesDoNotSurvive is clause 33(b)'s MCP
// half: no PUBLISHED tool description may still carry a phrase D14.2 named
// as falsified by this slice (a checkpoint now carries resources and
// resource_decisions, restored UPSERT-only; reset_overrides never touches
// a confirmed resource either way). A match here means a stale claim ships
// to every MCP client's tools/list response.
func TestToolsList_hardForbiddenPhrasesDoNotSurvive(t *testing.T) {
	t.Parallel()
	h := newTestEndpoint(t).Handler()
	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	var env struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rec.Body.String())
	}
	for _, phrase := range mcpForbiddenPhrasesHard {
		for _, tool := range env.Result.Tools {
			if strings.Contains(tool.Description, phrase) {
				t.Errorf("forbidden phrase %q survives in %s's description: %q", phrase, tool.Name, tool.Description)
			}
		}
	}
}

// TestRederiveSuggestionsTool_performsARealRederive is decisions.md
// mocker-p3f-rederive §D9 P22: the MCP tool performs a REAL rederive over
// POST /api/specs/{id}/rederive, not the suggestion LISTING route — the
// mutation this property is written against is pointing the tool at
// list_resource_suggestions' own GET instead, which would answer without
// ever writing a new generation. The fixture narrows the SAME
// resourcesFixtureDoc's generation 1 directly (decisions.md §D9's own
// "no production seam is added to make derivation configurable" rule,
// deleting /widgets' own row rather than the deriver ever refusing to find
// it), so a genuine call to Rederive is what brings it back.
func TestRederiveSuggestionsTool_performsARealRederive(t *testing.T) {
	t.Parallel()

	cfg := resourcesTestConfig(t)
	adminSrv, db := newResourcesTestServer(t, cfg)
	specID := importResourcesFixtureSpec(t, db, cfg)

	// Narrow generation 1 by dropping /widgets, but leave ONE row behind
	// (a decoy the current derivation does not produce either) rather than
	// emptying the generation outright: an empty resource_suggestions
	// population makes [specs.Repo.EnsureSuggestions]'s own lazy backfill
	// fire on the very next read (D4.5's own unconditional branch) and
	// silently restore /widgets before this test's rederive call ever
	// runs — the row this fixture needs deleted stays deleted only while
	// suggestionsExist still sees something for the spec.
	if _, err := db.W.ExecContext(t.Context(),
		"DELETE FROM resource_suggestions WHERE spec_id = ? AND route_family = ?", specID, "/widgets"); err != nil {
		t.Fatalf("narrow generation 1: %v", err)
	}
	if _, err := db.W.ExecContext(t.Context(), `
		INSERT INTO resource_suggestions (spec_id, gen, route_family, name, id_field, entity_schema, wrapper, confidence)
		VALUES (?, 1, '/decoy-p22', 'Decoy', 'id', '{"type":"object"}', '{"arrayKey":null,"countKey":null}', 1.0)`,
		specID); err != nil {
		t.Fatalf("seed decoy suggestion row: %v", err)
	}

	lb := newLoopback(adminSrv)
	ctx := opsTestCtx()

	// Before: the listing no longer names /widgets — narrowing actually
	// took, not a no-op fixture (it still names the decoy: this reader has
	// no gen predicate of its own to fail differently).
	_, before, err := handleListResourceSuggestions(lb)(ctx, nil, ListResourceSuggestionsInput{SpecID: specID})
	if err != nil {
		t.Fatalf("list_resource_suggestions (before): %v", err)
	}
	for _, s := range before.Suggestions {
		if s.RouteFamily == "/widgets" {
			t.Fatalf("list_resource_suggestions (before) = %+v, want no /widgets row (generation 1 was narrowed)", before.Suggestions)
		}
	}

	_, out, err := handleRederiveSuggestions(lb)(ctx, nil, RederiveSuggestionsInput{SpecID: specID})
	if err != nil {
		t.Fatalf("rederive_suggestions: %v", err)
	}
	if !out.Changed {
		t.Fatalf("rederive_suggestions Changed = false, want true (a generation was written)")
	}
	if out.Generation != 2 {
		t.Errorf("rederive_suggestions Generation = %d, want 2", out.Generation)
	}
	if !slices.Contains(out.Added, "/widgets") {
		t.Errorf("rederive_suggestions Added = %v, want it to name /widgets", out.Added)
	}
	if !slices.Contains(out.Removed, "/decoy-p22") {
		t.Errorf("rederive_suggestions Removed = %v, want it to name /decoy-p22", out.Removed)
	}

	// After: the listing reads back the family the rederive just brought
	// back — the tool actually wrote a generation, not merely reported one.
	_, after, err := handleListResourceSuggestions(lb)(ctx, nil, ListResourceSuggestionsInput{SpecID: specID})
	if err != nil {
		t.Fatalf("list_resource_suggestions (after): %v", err)
	}
	if len(after.Suggestions) != 1 || after.Suggestions[0].RouteFamily != "/widgets" {
		t.Fatalf("list_resource_suggestions (after) = %+v, want exactly one /widgets suggestion", after.Suggestions)
	}
}
