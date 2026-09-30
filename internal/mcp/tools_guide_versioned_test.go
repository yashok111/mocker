package mcp

import (
	"encoding/json/v2"
	"github.com/yashok111/mocker/internal/guide"
	"strings"
	"testing"
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
	if out.GuideSetID != guide.CurrentGuideSetID() || out.ManifestHash == "" || out.ContentHash == "" || out.WorkflowID != "mocker-backend-project" || out.WorkflowVersion != "1" {
		t.Fatalf("selected guide = %#v", out)
	}
	for _, topic := range []string{"overview", "tools", "shapes", "cookbook", "http", "design", "functions"} {
		_, legacy, err := handleGetGuide(t.Context(), nil, GetGuideInput{Topic: topic})
		if err != nil || legacy.Topic != topic || legacy.Markdown == "" || len(legacy.Topics) == 0 {
			t.Errorf("legacy %s: %#v, %v", topic, legacy, err)
		}
	}
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
		"backend-projects":         {"list_backend_projects", "create_backend_project", "get_backend_project"},
		"backend-project-metadata": {"apply_backend_project_commands"},
		"backend-revisions":        {"list_backend_revisions", "get_backend_revision"},
		"backend-graph-query":      {"query_backend_graph", "get_backend_node", "get_backend_evidence", "get_backend_coverage"},
		"backend-source-import":    {"begin_backend_import", "list_backend_imports", "get_backend_import", "put_backend_import_batch", "preview_backend_import", "commit_backend_import", "abort_backend_import"},
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
