package mcp

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/guide"
)

// TestListBackendAnalysisKindsMatchTheContract pins review 2026-10-06,
// F10/F28/F131: the hand-written list_backend_analysis kind enum had six
// values while the route and api/openapi.json accept eight, so an agent could
// not filter for the scenario_measurement or scenario_comparison jobs it
// started; the adapter refused before any request.
func TestListBackendAnalysisKindsMatchTheContract(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]jsontext.Value `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var get struct {
		Parameters []struct {
			Name   string `json:"name"`
			Schema struct {
				Enum []string `json:"enum"`
			} `json:"schema"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(doc.Paths["/api/backend-projects/{id}/analyses"]["get"], &get); err != nil {
		t.Fatal(err)
	}
	var contract []string
	for _, p := range get.Parameters {
		if p.Name == "kind" {
			contract = p.Schema.Enum
		}
	}
	if len(contract) != 8 {
		t.Fatalf("contract kind enum = %v", contract)
	}
	for _, kind := range contract {
		calls := &recordingCaller{status: 200, body: []byte(`{"items":[],"nextCursor":""}`)}
		if _, msg := callTool(t, calls, "list_backend_analysis", `{"projectId":"`+backendTestID+`","kind":"`+kind+`"}`); msg != "" || !strings.Contains(calls.path, "kind="+kind) {
			t.Errorf("kind %s: %q %s", kind, msg, calls.path)
		}
	}
}

// TestResolveDiagramScopeDescribesARead pins review 2026-10-06, F31/F132:
// resolve_backend_diagram_scope was registered in the replay loop and
// published the replay text (allowReset consent, queued work, mocked
// payment) although it is a pure scope read.
func TestResolveDiagramScopeDescribesARead(t *testing.T) {
	response := describedToolsList(t)
	var env struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	for _, tool := range env.Result.Tools {
		if tool.Name == "resolve_backend_diagram_scope" {
			if strings.Contains(tool.Description, "allowReset") || !strings.Contains(tool.Description, "Pure read") {
				t.Fatalf("description = %q", tool.Description)
			}
			return
		}
	}
	t.Fatal("resolve_backend_diagram_scope is not published")
}

// TestGetGuideAdvertisesEveryTopic pins review 2026-10-06, F134: the topic
// description was a hand-copied list that stopped at backend-replay, while
// guide.Topics() serves forty, so backend-portable, -verify, -measurements
// and seven more were missing from the input schema an agent reads.
func TestGetGuideAdvertisesEveryTopic(t *testing.T) {
	response := describedToolsList(t)
	var env struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Properties map[string]struct {
						Description string `json:"description"`
					} `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	for _, tool := range env.Result.Tools {
		if tool.Name != "get_guide" {
			continue
		}
		description := tool.InputSchema.Properties["topic"].Description
		for _, topic := range guide.Topics() {
			if !strings.Contains(description, topic) {
				t.Errorf("topic %s missing from %q", topic, description)
			}
		}
		return
	}
	t.Fatal("get_guide is not published")
}

// TestBackendArrayResultIsAnObject pins review 2026-10-06, F24: the four
// replay list routes answer a bare JSON array, and the adapter put it into
// structuredContent unchanged. MCP defines structuredContent as an object,
// so a spec-strict client (the TypeScript SDK) rejected the whole result and
// the documented replay flow broke at step 1. The text content keeps the
// exact REST body.
func TestBackendArrayResultIsAnObject(t *testing.T) {
	for _, name := range []string{"list_backend_replay_targets", "list_backend_replay_profiles", "list_backend_replay_packages", "list_backend_replay_runs"} {
		t.Run(name, func(t *testing.T) {
			body := `[{"id":"test","version":1}]`
			structured, msg := callTool(t, &recordingCaller{status: 200, body: []byte(body)}, name, `{"projectId":"`+backendTestID+`"}`)
			if msg != "" {
				t.Fatal(msg)
			}
			var wrapped struct {
				Items []map[string]any `json:"items"`
			}
			if !strings.HasPrefix(strings.TrimSpace(string(structured)), "{") || json.Unmarshal(structured, &wrapped) != nil || len(wrapped.Items) != 1 {
				t.Fatalf("structuredContent = %s, want {\"items\":[...]}", structured)
			}
		})
	}
}
