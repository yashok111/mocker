package mcp

import (
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/guide"
)

func TestGetGuideSelection(t *testing.T) {
	_, _, err := handleGetGuide(t.Context(), nil, GetGuideInput{GuideSetID: "unknown"})
	if err == nil || !strings.Contains(err.Error(), "unknown guide set") {
		t.Fatalf("unknown set error = %v", err)
	}
	_, out, err := handleGetGuide(t.Context(), nil, GetGuideInput{Topic: "backend-overview", GuideSetID: guide.CurrentGuideSetID()})
	if err != nil {
		t.Fatal(err)
	}
	if out.GuideSetID != guide.CurrentGuideSetID() || out.ManifestHash == "" || out.ContentHash == "" || out.WorkflowID != "mocker-backend-project" || out.WorkflowVersion != "2" {
		t.Fatalf("selected guide = %#v", out)
	}
	for _, topic := range []string{"overview", "tools", "shapes", "cookbook", "http", "design", "functions"} {
		_, legacy, err := handleGetGuide(t.Context(), nil, GetGuideInput{Topic: topic})
		if err != nil || legacy.Topic != topic || legacy.Markdown == "" || len(legacy.Topics) == 0 {
			t.Errorf("legacy %s: %#v, %v", topic, legacy, err)
		}
	}
}

func TestStandaloneImportReferencesThroughPinnedMCP(t *testing.T) {
	for _, topic := range []string{"backend-import", "backend-model", "backend-import-protocol", "backend-recovery", "backend-examples", "backend-profile-go-sql"} {
		out, message := callGuide(t, `{"topic":"`+topic+`","guideSetId":"`+guide.CurrentGuideSetID()+`"}`)
		if message != "" {
			t.Fatal(message)
		}
		wantHash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(out.Markdown)))
		if out.WorkflowID != "mocker-backend-import" || out.WorkflowVersion != "7" || out.GuideSetID != guide.CurrentGuideSetID() || out.ManifestHash != guide.CurrentGuideSetID() || out.ContentHash != wantHash {
			t.Fatalf("pinned topic %s: %#v", topic, out)
		}
	}
}

func TestObsoleteB03GuideSetDoesNotSubstitutePackagedImport(t *testing.T) {
	const previous = "sha256:78d12f5701030ccfbcccf4969ceae196b97d20f2fe4262e10d9897fbde6b8d7f"
	_, _, err := handleGetGuide(t.Context(), nil, GetGuideInput{Topic: "backend-import", GuideSetID: previous})
	if err == nil || !strings.Contains(err.Error(), "unknown guide set") {
		t.Fatalf("obsolete B0.3 procedure substituted: %v", err)
	}
}

func TestStandaloneImportTopicDiscovery(t *testing.T) {
	handler := newTestEndpoint(t).Handler()
	response := doMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + testKey})
	var envelope struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				InputSchema struct {
					Properties map[string]struct {
						Description string `json:"description"`
					} `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	for _, tool := range envelope.Result.Tools {
		if tool.Name != "get_guide" {
			continue
		}
		for _, topic := range []string{"backend-import", "backend-model", "backend-import-protocol", "backend-recovery", "backend-examples", "backend-events"} {
			if !strings.Contains(tool.Description, topic) || !strings.Contains(tool.InputSchema.Properties["topic"].Description, topic) {
				t.Errorf("guide discovery omits %s", topic)
			}
		}
		return
	}
	t.Fatal("get_guide discovery tool missing")
}

func TestGetGuideSelectionThroughTransport(t *testing.T) {
	out, message := callGuide(t, `{"topic":"backend-overview","guideSetId":"`+guide.CurrentGuideSetID()+`"}`)
	if message != "" {
		t.Fatal(message)
	}
	if out.WorkflowID != "mocker-backend-project" || out.GuideSetID != guide.CurrentGuideSetID() || out.ContentHash == "" || out.ManifestHash == "" {
		t.Fatalf("transport lost selected identity: %#v", out)
	}
	_, message = callGuide(t, `{"topic":"overview","guideSetId":"old-server-set"}`)
	if !strings.Contains(message, "unknown guide set") {
		t.Fatalf("transport fell back on unknown set: %s", message)
	}
}

// Advertised backend workflow capabilities must have implemented MCP tools.
func TestGuideRequiredCapabilitiesHaveMCPTools(t *testing.T) {
	handler := newTestEndpoint(t).Handler()
	response := doMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + testKey})
	var envelope struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	inventory := map[string]bool{}
	for _, tool := range envelope.Result.Tools {
		inventory[tool.Name] = true
	}
	implementations := map[string][]string{
		"backend-projects":                {"list_backend_projects", "create_backend_project", "get_backend_project"},
		"backend-project-metadata":        {"apply_backend_project_commands"},
		"backend-revisions":               {"list_backend_revisions", "get_backend_revision"},
		"backend-graph-query":             {"query_backend_graph", "get_backend_node", "get_backend_evidence", "get_backend_coverage"},
		"backend-source-import":           {"begin_backend_import", "list_backend_imports", "get_backend_import", "put_backend_import_batch", "preview_backend_import", "commit_backend_import", "abort_backend_import"},
		"backend-source-reconcile":        {"begin_backend_import", "put_backend_import_batch", "preview_backend_import", "commit_backend_import", "get_backend_import_changes"},
		"backend-revision-compare":        {"compare_backend_revisions"},
		"backend-relational-import":       {"begin_backend_import", "put_backend_import_batch", "preview_backend_import", "commit_backend_import"},
		"backend-database-query":          {"query_backend_database"},
		"backend-database-er":             {"query_backend_database", "get_backend_node", "get_backend_evidence", "get_backend_coverage"},
		"backend-saved-views":             {"list_backend_saved_views", "create_backend_saved_view", "get_backend_saved_view", "save_backend_saved_view"},
		"backend-db-proposals":            {"list_backend_proposals", "create_backend_proposal", "get_backend_proposal"},
		"backend-db-typed-edits":          {"preview_backend_proposal_commands", "apply_backend_proposal_commands"},
		"backend-runtime-flow-import":     {"begin_backend_import", "put_backend_import_batch", "preview_backend_import", "commit_backend_import"},
		"backend-flow-query":              {"query_backend_flow"},
		"backend-data-access-query":       {"query_backend_flow"},
		"backend-field-lineage-import":    {"begin_backend_import", "put_backend_import_batch", "preview_backend_import", "commit_backend_import"},
		"backend-field-lineage-query":     {"query_backend_lineage"},
		"backend-events-import":           {"begin_backend_import", "put_backend_import_batch", "preview_backend_import", "commit_backend_import"},
		"backend-events-query":            {"query_backend_events"},
		"backend-editor-projections":      {"query_backend_artifacts", "preview_backend_artifact_pins", "apply_backend_artifact_pins", "get_design_scenario_artifact_snapshot"},
		"backend-api-artifact-pins":       {"query_backend_api_artifacts", "preview_backend_api_pins", "apply_backend_api_pins", "get_api_artifact_snapshot"},
		"backend-annotations":             {"apply_backend_project_commands", "list_backend_annotations"},
		"backend-source-sync":             {"begin_backend_import", "put_backend_import_batch", "preview_backend_import", "commit_backend_import", "get_backend_import_changes"},
		"backend-source-incremental-sync": {"begin_backend_import", "put_backend_import_batch", "preview_backend_import", "commit_backend_import"},
		"backend-source-assertions":       {"get_backend_assertions"},
		"backend-import-candidate":        {"query_backend_graph", "get_backend_node", "get_backend_evidence", "get_backend_coverage", "get_backend_assertions"},
		"backend-change-proposals":        {"list_backend_change_proposals", "create_backend_change_proposal", "get_backend_change_proposal", "restore_backend_change_proposal"},
		"backend-change-typed-edits":      {"preview_backend_change_proposal_commands", "apply_backend_change_proposal_commands"},
		"backend-representations":         {"put_backend_import_batch", "query_backend_graph", "get_backend_node", "query_backend_lineage"},
		"backend-saved-views-v2":          {"list_backend_saved_views", "create_backend_saved_view", "get_backend_saved_view", "save_backend_saved_view"},
		"backend-analysis-jobs":           {"start_backend_analysis", "list_backend_analysis", "get_backend_analysis", "get_backend_analysis_results", "cancel_backend_analysis", "retry_backend_analysis"},
		"backend-analysis-diff":           {"start_backend_analysis", "get_backend_analysis_results"},
		"backend-analysis-impact":         {"start_backend_analysis", "get_backend_analysis_results"},
		"backend-change-rebase":           {"preview_backend_change_proposal_rebase", "apply_backend_change_proposal_rebase"},
		"backend-change-ready":            {"apply_backend_change_proposal_lifecycle"},
		"backend-change-package":          {"start_backend_analysis", "get_backend_analysis_results"},
		"backend-conformance":             {"start_backend_analysis", "get_backend_analysis_results"},
		"backend-endpoint-review":         {"start_backend_analysis", "get_backend_analysis_results"},
		"backend-change-implemented":      {"apply_backend_change_proposal_lifecycle"},
		"backend-change-archive":          {"apply_backend_change_proposal_lifecycle"},
		"backend-change-unarchive":        {"apply_backend_change_proposal_lifecycle"},
	}
	if !inventory["get_backend_capabilities"] {
		t.Error("workflow discovery tool is missing")
	}
	for _, workflow := range guide.BackendWorkflows() {
		for _, capability := range workflow.RequiredCapabilities {
			names, ok := implementations[capability]
			if !ok {
				t.Errorf("capability %s has no implemented tool mapping", capability)
				continue
			}
			for _, name := range names {
				if !inventory[name] {
					t.Errorf("advertised capability %s lacks MCP tool %s", capability, name)
				}
			}
		}
	}
}

func TestObsoleteImportGuideSetDoesNotSubstituteV4(t *testing.T) {
	const previous = "sha256:6f352e4838720bf9447956681574dd16b64ef94f0d7a2700c512da5b9bc6c33f"
	_, _, err := handleGetGuide(t.Context(), nil, GetGuideInput{Topic: "backend-import", GuideSetID: previous})
	if err == nil || !strings.Contains(err.Error(), "unknown guide set") {
		t.Fatalf("obsolete pinned guide silently substituted: %v", err)
	}
	_, out, err := handleGetGuide(t.Context(), nil, GetGuideInput{Topic: "backend-import", GuideSetID: guide.CurrentGuideSetID()})
	if err != nil {
		t.Fatal(err)
	}
	if out.WorkflowVersion != "7" || out.WorkflowID != "mocker-backend-import" || out.GuideSetID != guide.CurrentGuideSetID() {
		t.Fatalf("current import guide identity: %#v", out)
	}
}
