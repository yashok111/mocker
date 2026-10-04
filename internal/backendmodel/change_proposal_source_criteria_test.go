package backendmodel

import (
	"strings"
	"testing"
	"uuid"
)

func TestChangeProposalSourceCriterionTopLevelTypes(t *testing.T) {
	r, _, d := changeFixture(t)
	source, err := r.ResolveSourceGraph(t.Context(), d.Proposal.ProjectID, d.Revision.BaseRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	nodeID := source.State.Nodes[0].ID
	parent, child := uuid.NewV7().String(), uuid.NewV7().String()
	edge := changeContains(t, parent, child)
	d, _ = saveChange(t, r, d, "criterion-targets", changeCreateNode(t, parent, "service", nil, map[string]any{}), changeCreateNode(t, child, "module", new(parent), map[string]any{}), edge)
	for _, tc := range []struct {
		name, selector string
		present        bool
		value          any
	}{
		{"empty name", "name", true, ""}, {"null name", "name", true, nil}, {"missing name", "name", false, nil}, {"whitespace name", "name", true, " "}, {"long name", "name", true, strings.Repeat("n", 201)}, {"non-string name", "name", true, 42},
		{"non-uuid parent", "parent", true, "not-a-uuid"}, {"non-string parent", "parent", true, 42},
		{"missing endpoints", "edge_endpoints", false, nil}, {"null endpoints", "edge_endpoints", true, nil},
		{"missing from", "edge_endpoints", true, map[string]any{"to": child}}, {"missing to", "edge_endpoints", true, map[string]any{"from": parent}},
		{"null from", "edge_endpoints", true, map[string]any{"from": nil, "to": child}}, {"null to", "edge_endpoints", true, map[string]any{"from": parent, "to": nil}},
		{"invalid from", "edge_endpoints", true, map[string]any{"from": "bad", "to": child}}, {"invalid to", "edge_endpoints", true, map[string]any{"from": parent, "to": "bad"}},
		{"extra endpoint member", "edge_endpoints", true, map[string]any{"from": parent, "to": child, "cascade": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typ, id := "node", nodeID
			if tc.selector == "edge_endpoints" {
				typ, id = "edge", edge.ID
			}
			expected := map[string]any{"present": tc.present}
			if tc.present {
				expected["value"] = tc.value
			}
			command := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{map[string]any{"key": "expected", "kind": "field_equals", "required": true, "description": "Typed requirement", "recordType": typ, "id": id, "selector": map[string]any{"kind": "source", "source": map[string]any{"kind": tc.selector}}, "expected": expected}}})
			preview, err := r.PreviewChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{command}})
			if err != nil {
				t.Fatal(err)
			}
			if preview.CandidateHash != nil || preview.SemanticHash != nil || len(preview.Diagnostics) == 0 {
				t.Fatal("malformed expected value admitted")
			}
		})
	}
	for _, tc := range []struct {
		name, selector string
		value          any
	}{
		{"unmet name", "name", "A different valid name"}, {"nullable parent", "parent", nil}, {"unmet parent", "parent", uuid.NewV7().String()},
		{"unmet endpoints", "edge_endpoints", map[string]any{"from": uuid.NewV7().String(), "to": uuid.NewV7().String()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typ, id := "node", nodeID
			if tc.selector == "edge_endpoints" {
				typ, id = "edge", edge.ID
			}
			command := changeMapCommand(t, "set_criteria", map[string]any{"criteria": []any{map[string]any{"key": "expected", "kind": "field_equals", "required": true, "description": "Declared future requirement", "recordType": typ, "id": id, "selector": map[string]any{"kind": "source", "source": map[string]any{"kind": tc.selector}}, "expected": map[string]any{"present": true, "value": tc.value}}}})
			preview, err := r.PreviewChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{command}})
			if err != nil || preview.CandidateHash == nil || len(preview.Diagnostics) > 0 {
				t.Fatalf("well-typed declaration was evaluated as a current match: %+v %v", preview, err)
			}
		})
	}
}
