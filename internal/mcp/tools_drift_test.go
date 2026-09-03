// tools_drift_test.go covers D8's P16 and P26 — the two acceptance
// properties this package owns for get_workspace_drift: the tool performs
// a REAL read through the real admin route (not some other route that
// happens to answer 200), and its PUBLISHED description names the three
// repair verbs paired with what each destroys.
package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
)

// driftAuthFixtureDoc declares one operation, POST /auth/login — imported
// first and overridden, then abandoned by a re-bind to
// driftWidgetsOnlyFixtureDoc, which declares no operation at all under
// that (method, path).
const driftAuthFixtureDoc = `{
  "openapi": "3.0.3",
  "info": {"title": "mcp drift fixture — auth", "version": "1.0.0"},
  "paths": {
    "/auth/login": {
      "post": {
        "operationId": "login",
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"type": "object", "properties": {"token": {"type": "string"}}}
        }}}}
      }
    }
  }
}`

const driftWidgetsOnlyFixtureDoc = `{
  "openapi": "3.0.3",
  "info": {"title": "mcp drift fixture — widgets", "version": "1.0.0"},
  "paths": {
    "/widgets": {
      "get": {
        "operationId": "listWidgets",
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"type": "array", "items": {"type": "object"}}
        }}}}
      }
    }
  }
}`

// importDriftFixtureSpec imports one of the two docs above through
// specs.Repo.Import directly, the same route importResourcesFixtureSpec
// uses and for the same reason: POST /api/specs is outside
// mcpAllowedRoutes, so no tool in this package could reach it anyway.
func importDriftFixtureSpec(t *testing.T, db *store.DB, cfg *config.Config, name, doc string) int64 {
	t.Helper()
	sr := specs.NewRepo(db, cfg)
	res, err := sr.Import(t.Context(), specs.ImportInput{Name: name, Source: "upload", Document: []byte(doc)})
	if err != nil {
		t.Fatalf("import fixture spec %q: %v", name, err)
	}
	return res.Spec.ID
}

// TestGetWorkspaceDriftTool_performsARealRead is D8 P16: the tool is
// pointed at GET /api/workspaces/{id}/resources instead of
// GET /api/workspaces/{id}/drift — a route that answers 200 for the same
// workspace but carries no override signal at all — and the expected red
// is this test's own assertion that the tool's own result names the
// orphaned override. It drives a REAL *admin.Server on a REAL, migrated
// SQLite database (newResourcesTestServer, mcp_resources_test.go), never
// this package's scriptedCaller fake, for the same reason
// TestRederiveSuggestionsTool_performsARealRederive does (P3f's own
// precedent, mcp_resources_test.go): the fake proves only the tool's JSON
// projection, never that the allowlist actually admits the route it calls.
func TestGetWorkspaceDriftTool_performsARealRead(t *testing.T) {
	t.Parallel()

	cfg := resourcesTestConfig(t)
	adminSrv, db := newResourcesTestServer(t, cfg)

	specA := importDriftFixtureSpec(t, db, cfg, "Auth", driftAuthFixtureDoc)
	wsID := insertResourcesTestWorkspace(t, db, "drift-tool-ws", specA, 3)

	// Pin an override on POST /auth/login directly (op_overrides' own
	// natural key is (workspace_id, method, path) — this bypasses
	// PUT .../operations/{opKey}, which mcpAllowedRoutes does carry, only
	// because it is one INSERT rather than a whole PUT body round trip).
	now := time.Now().Unix()
	if _, err := db.W.ExecContext(t.Context(), `
		INSERT INTO op_overrides (workspace_id, method, path, updated_at)
		VALUES (?, 'POST', '/auth/login', ?)`, wsID, now); err != nil {
		t.Fatalf("insert override: %v", err)
	}

	// Re-bind the workspace to a spec that declares no operation under
	// that (method, path) — the same event D3.1 names (a PATCH changing
	// spec_id), written directly for the identical reason
	// insertResourcesTestWorkspace writes workspaces directly: every
	// package that owns a table writes it directly in its own tests.
	specB := importDriftFixtureSpec(t, db, cfg, "Widgets Only", driftWidgetsOnlyFixtureDoc)
	if _, err := db.W.ExecContext(t.Context(), "UPDATE workspaces SET spec_id = ? WHERE id = ?", specB, wsID); err != nil {
		t.Fatalf("rebind workspace: %v", err)
	}

	lb := newLoopback(adminSrv)
	_, out, err := handleGetWorkspaceDrift(lb)(opsTestCtx(), nil, GetWorkspaceDriftInput{WorkspaceID: wsID})
	if err != nil {
		t.Fatalf("get_workspace_drift: %v", err)
	}
	if !out.HasDrift {
		t.Fatalf("get_workspace_drift HasDrift = false, want true")
	}
	if len(out.OrphanedOverrides) != 1 || out.OrphanedOverrides[0].Method != http.MethodPost || out.OrphanedOverrides[0].Path != "/auth/login" {
		t.Fatalf("get_workspace_drift OrphanedOverrides = %+v, want exactly one row naming POST /auth/login", out.OrphanedOverrides)
	}
	if len(out.OrphanedResources) != 0 {
		t.Errorf("get_workspace_drift OrphanedResources = %+v, want none (this fixture confirms no family)", out.OrphanedResources)
	}
	if len(out.ShadowedEndpoints) != 0 {
		t.Errorf("get_workspace_drift ShadowedEndpoints = %+v, want none (this fixture creates no custom endpoint)", out.ShadowedEndpoints)
	}
}

// driftDescriptionVerbCosts pairs each of the three repair verbs D7 names
// with a substring of what its own sentence says that verb destroys — read
// straight out of the PUBLISHED description this test fetches through a
// live tools/list, not the source literal, so the assertion is over what
// an agent actually receives.
var driftDescriptionVerbCosts = []struct {
	verb  string
	costs []string
}{
	{"DELETE /api/workspaces/{id}/operations/{opKey}", []string{"pinned body", "recipes"}},
	{"DELETE /api/workspaces/{id}/endpoints/{eid}", []string{"authored body"}},
	{"POST /api/workspaces/{id}/resource-decisions", []string{"entity rows"}},
}

// TestGetWorkspaceDriftTool_descriptionNamesVerbsPairedWithCosts is D8 P26:
// the mutation this guards against is a bare one-line description, or a
// description that lists the three verbs in one sentence and their three
// costs in a separate one — a shape that would satisfy a substring check
// per verb and per cost individually without ever pairing them. This test
// asserts the pairing itself: for each verb, at least one of its own cost
// phrases appears in the SAME SENTENCE as the verb string (split on '.'),
// not merely somewhere in the whole description.
func TestGetWorkspaceDriftTool_descriptionNamesVerbsPairedWithCosts(t *testing.T) {
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
	var desc string
	var found bool
	for _, tool := range env.Result.Tools {
		if tool.Name == "get_workspace_drift" {
			desc = tool.Description
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tools/list has no get_workspace_drift entry")
	}

	sentences := strings.Split(desc, ".")
	for _, vc := range driftDescriptionVerbCosts {
		if !strings.Contains(desc, vc.verb) {
			t.Errorf("get_workspace_drift description does not name %q at all: %q", vc.verb, desc)
			continue
		}
		var sameSentenceCost bool
		for _, sentence := range sentences {
			if !strings.Contains(sentence, vc.verb) {
				continue
			}
			for _, cost := range vc.costs {
				if strings.Contains(sentence, cost) {
					sameSentenceCost = true
				}
			}
		}
		if !sameSentenceCost {
			t.Errorf("get_workspace_drift description names %q but no sentence containing it also names one of %v: %q", vc.verb, vc.costs, desc)
		}
	}
}
