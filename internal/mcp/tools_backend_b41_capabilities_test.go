package mcp

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func TestBackendB41CapabilityToolDescriptions(t *testing.T) {
	// The full descriptions: tools/list carries one sentence since F23.
	response := describedToolsList(t)
	var envelope struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	descriptions := map[string]string{}
	for _, tool := range envelope.Result.Tools {
		descriptions[tool.Name] = tool.Description
	}
	for name, terms := range map[string][]string{
		"get_backend_capabilities":       {"readTargetSupport", "changeProposalCommands", "proposalCommands"},
		"begin_backend_import":           {"composed", "sourceScope", "incremental-source-v1", "whole-source-v1"},
		"query_backend_graph":            {"changeProposal", "importCandidate"},
		"query_backend_database":         {"source2", "changeProposal", "unsupported"},
		"query_backend_flow":             {"source3", "changeProposal", "unsupported"},
		"query_backend_lineage":          {"source4", "changeProposal", "unsupported"},
		"query_backend_events":           {"source5", "changeProposal", "unsupported"},
		"query_backend_api_artifacts":    {"source1", "changeProposal", "unsupported"},
		"query_backend_artifacts":        {"source1", "changeProposal", "unsupported"},
		"create_backend_saved_view":      {"saved-view-v2", "saved-view-v1", "Flow", "Database"},
		"create_backend_change_proposal": {"source5/6", "proposal-graph-v1"},
		"list_backend_annotations":       {"100", "500"},
		"apply_backend_project_commands": {"100", "rename_project"},
	} {
		for _, term := range terms {
			if !strings.Contains(descriptions[name], term) {
				t.Errorf("%s description omits %q", name, term)
			}
		}
	}
	for _, name := range []string{"query_backend_flow", "query_backend_lineage", "query_backend_events"} {
		if strings.Contains(descriptions[name], "Proposal selectors are unsupported") {
			t.Errorf("%s still rejects the implemented full proposal target", name)
		}
	}
}
