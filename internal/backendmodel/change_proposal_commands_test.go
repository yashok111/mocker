package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"testing"
	"uuid"
)

func TestChangeProposalEveryCommandSchemaRejectsMissingMixedAndNull(t *testing.T) {
	id, other := uuid.NewV7().String(), uuid.NewV7().String()
	step := map[string]any{"analysisStatus": "complete", "gaps": []string{}, "stepKind": "input", "inputs": []any{}, "outputs": []any{}, "transactionContext": map[string]any{"status": "none", "reason": "No transaction"}, "nativeText": "opaque"}
	constraint := changeDesiredFacet(map[string]any{"constraintKind": "unique", "columnIds": []string{id}, "expression": known(nil), "nativeDefinition": nil, "deferrable": known(false), "initiallyDeferred": known(false)})
	index := changeDesiredFacet(map[string]any{"terms": []any{map[string]any{"columnId": id, "direction": "asc", "nulls": "last"}}, "unique": known(false), "predicate": known(nil), "method": known(nil), "nativeDefinition": nil})
	cases := []struct {
		typ, fields string
		payload     map[string]any
	}{
		{"create_node", "id kind name parentId attributes", map[string]any{"id": id, "kind": "service", "name": "X", "parentId": nil, "attributes": map[string]any{}}},
		{"update_node", "id update", map[string]any{"id": id, "update": map[string]any{"kind": "service", "group": "attributes", "attributes": map[string]any{}}}},
		{"rename", "recordType id name", map[string]any{"recordType": "edge", "id": id, "name": "Desired label"}},
		{"remove_node", "id", map[string]any{"id": id}}, {"remove_edge", "id", map[string]any{"id": id}},
		{"upsert_edge", "id kind from to attributes", map[string]any{"id": id, "kind": "contains", "from": id, "to": other, "attributes": map[string]any{}}},
		{"alter_column", "columnId facetKey change", map[string]any{"columnId": id, "facetKey": "sql", "change": map[string]any{"group": "nullable", "nullable": known(false)}}},
		{"alter_constraint", "action constraintId facetKey name tableId definition", map[string]any{"action": "create", "constraintId": id, "facetKey": "sql", "name": "Unique", "tableId": other, "definition": constraint}},
		{"alter_constraint", "action constraintId facetKey definition", map[string]any{"action": "update", "constraintId": id, "facetKey": "sql", "definition": constraint}},
		{"alter_constraint", "action constraintId facetKey", map[string]any{"action": "remove", "constraintId": id, "facetKey": "sql"}},
		{"alter_index", "action indexId facetKey name tableId definition", map[string]any{"action": "create", "indexId": id, "facetKey": "sql", "name": "Index", "tableId": other, "definition": index}},
		{"alter_index", "action indexId facetKey definition", map[string]any{"action": "update", "indexId": id, "facetKey": "sql", "definition": index}},
		{"alter_index", "action indexId facetKey", map[string]any{"action": "remove", "indexId": id, "facetKey": "sql"}},
		{"edit_flow_step", "stepId attributes", map[string]any{"stepId": id, "attributes": step}},
		{"edit_branch", "edgeId kind from to attributes", map[string]any{"edgeId": id, "kind": "next", "from": id, "to": other, "attributes": map[string]any{}}},
		{"set_field_mapping", "mappingId parentId sources destination transform analysisStatus gaps", map[string]any{"mappingId": id, "parentId": other, "sources": []any{map[string]any{"kind": "representation_field", "nodeId": id}}, "destination": map[string]any{"kind": "representation_field", "nodeId": other}, "transform": map[string]any{"kind": "copy", "description": "copy", "redacted": false}, "analysisStatus": "complete", "gaps": []string{}}},
		{"set_artifact_pin", "artifact revisionId editorBindings", map[string]any{"artifact": map[string]any{"kind": "design_scenario", "id": "1"}, "revisionId": "1", "editorBindings": []any{}}},
		{"remove_artifact_pin", "artifact", map[string]any{"artifact": map[string]any{"kind": "design_scenario", "id": "1"}}},
		{"map_identity", "target expectedExternalKey newExternalKey", map[string]any{"target": map[string]any{"kind": "intent_identity", "recordType": "node", "id": id}, "expectedExternalKey": nil, "newExternalKey": "planned"}},
		{"set_criteria", "criteria", map[string]any{"criteria": []any{}}},
	}
	for _, tt := range cases {
		t.Run(tt.typ, func(t *testing.T) {
			c := changeMapCommand(t, tt.typ, tt.payload)
			raw, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			var original map[string]jsontext.Value
			if err = json.Unmarshal(raw, &original); err != nil {
				t.Fatal(err)
			}
			for _, field := range strings.Fields("type commandId reason " + tt.fields) {
				for _, mode := range []string{"missing", "null"} {
					if mode == "null" && (field == "parentId" || field == "expectedExternalKey") {
						continue
					}
					m := map[string]jsontext.Value{}
					for k, v := range original {
						m[k] = v
					}
					if mode == "missing" {
						delete(m, field)
					} else {
						m[field] = jsontext.Value(`null`)
					}
					body, _ := json.Marshal(m)
					var decoded ChangeProposalCommand
					if json.Unmarshal(body, &decoded) == nil {
						t.Fatalf("accepted %s %s: %s", mode, field, body)
					}
				}
			}
			original["cascade"] = jsontext.Value(`true`)
			body, _ := json.Marshal(original)
			var decoded ChangeProposalCommand
			if json.Unmarshal(body, &decoded) == nil {
				t.Fatal("accepted mixed payload")
			}
		})
	}
}

func TestChangeProposalGenericSpecializedEquivalence(t *testing.T) {
	r, d, ids := changeRelationalFixture(t)
	specific := changeMapCommand(t, "alter_column", map[string]any{"columnId": ids["column"], "facetKey": "sql", "change": map[string]any{"group": "nullable", "nullable": known(false)}})
	var attributes map[string]jsontext.Value
	for _, n := range changeReadSnapshot(t, r, d).Nodes {
		if n.ID == ids["column"] {
			raw, _ := json.Marshal(n.Attributes)
			if err := json.Unmarshal(raw, &attributes); err != nil {
				t.Fatal(err)
			}
		}
	}
	var facets map[string]map[string]jsontext.Value
	if err := json.Unmarshal(attributes["facets"], &facets); err != nil {
		t.Fatal(err)
	}
	facets["sql"]["nullable"] = jsontext.Value(`{"status":"known","value":false}`)
	attributes["facets"], _ = json.Marshal(facets)
	generic := changeMapCommand(t, "update_node", map[string]any{"id": ids["column"], "update": map[string]any{"kind": "column", "group": "attributes", "attributes": attributes}})
	hashes := make([]string, 0, 2)
	candidates := make([]string, 0, 2)
	for _, c := range []ChangeProposalCommand{specific, generic} {
		preview, err := r.PreviewChangeProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []ChangeProposalCommand{c}})
		if err != nil || preview.SemanticHash == nil {
			t.Fatalf("equivalent preview %+v %v", preview, err)
		}
		hashes = append(hashes, *preview.SemanticHash)
		candidates = append(candidates, *preview.CandidateHash)
	}
	if hashes[0] != hashes[1] || candidates[0] == candidates[1] {
		t.Fatal("generic and specialized schemas disagree on semantics")
	}
}
