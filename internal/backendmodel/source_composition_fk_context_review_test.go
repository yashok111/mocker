package backendmodel

import (
	"encoding/json/jsontext"
	"testing"
	"uuid"
)

func TestSource6ExactForeignKeyConstraintOwner(t *testing.T) {
	for _, owner := range []string{"missing", "present"} {
		t.Run(owner, func(t *testing.T) {
			// Both column providers agree on the selected facet, so this test
			// isolates ownership of the FK definition rather than column changes.
			f := sourceReviewSharedValue(t, "column", "equal")
			graph, err := f.repo.ResolveSourceGraph(t.Context(), f.project.ID, f.project.CurrentRevisionID)
			if err != nil {
				t.Fatal(err)
			}
			var table BaseAssertionRef
			dialect := ""
			for _, a := range graph.Assertions {
				if a.Payload.Kind == "table" {
					table = sourceAssertionRef(a)
					fs, _, _ := relationalFacetObject(a.Payload.Kind, a.Payload.Attributes)
					facet, _ := relationalObject(fs["sql"])
					dialect = runtimeString(facet["dialect"])
				}
			}
			constraints := map[string]BaseAssertionRef{}
			for _, namespace := range []string{"constraint-a", "constraint-b", "constraint-c"} {
				in := source6Input(t, f.project)
				in.SourceScope = &SourceScope{Kind: "add_provider", RepositoryID: f.repository}
				in.Manifest.Provider.Namespace, in.IdempotencyKey = namespace, "begin-"+namespace
				s, err := f.repo.BeginImport(t.Context(), f.project.ID, in)
				if err != nil {
					t.Fatal(err)
				}
				commands := []ImportCommand{}
				if namespace == "constraint-c" {
					target := constraints["constraint-a"]
					if owner == "present" {
						target = constraints["constraint-b"]
					}
					facet := map[string]any{"sourceKind": "sql", "dialect": dialect, "analysisStatus": "complete", "gaps": []string{}, "evidenceKeys": []string{"proof:reference"}, "columnPairs": []any{map[string]any{"fromColumnRef": ImportRecordRef{Base: &f.a}, "toColumnRef": ImportRecordRef{Base: &f.a}}}, "updateAction": known("no_action"), "deleteAction": known("no_action"), "matchType": known("simple")}
					edge := &ImportEdge{ExternalKey: "reference", Kind: "references", FromRef: &ImportRecordRef{Base: &target}, ToRef: &ImportRecordRef{Base: &table}, Attributes: runtimeAttrs(t, map[string]any{"facets": map[string]any{"sql": facet}}), EvidenceKeys: []string{"proof:reference"}}
					commands = append(commands, ImportCommand{Op: "upsert_edge", Edge: edge}, sourceContextProof(s, "edge", "reference"))
				} else {
					key := "orm"
					if namespace == "constraint-b" {
						key = "sql"
						commands = append(commands, ImportCommand{Op: "claim_identity", ClaimIdentity: &SourceClaimIdentity{DecisionID: uuid.NewV7().String(), RecordType: "node", ExternalKey: "constraint", Target: constraints["constraint-a"], Reason: "same constraint", EvidenceKeys: []string{"proof:constraint"}}})
					}
					facet := map[string]any{"sourceKind": key, "dialect": dialect, "analysisStatus": "complete", "gaps": []string{}, "evidenceKeys": []string{"proof:constraint"}, "constraintKind": "foreign_key", "columnRefs": []ImportRecordRef{{Base: &f.a}}, "expression": known(nil), "nativeDefinition": nil, "deferrable": known(false), "initiallyDeferred": known(false)}
					node := &ImportNode{ExternalKey: "constraint", Kind: "constraint", Name: "Shared FK", ParentRef: &ImportRecordRef{Base: &table}, Attributes: runtimeAttrs(t, map[string]any{"facets": map[string]any{key: facet}}), EvidenceKeys: []string{"proof:constraint"}}
					commands = append(commands, ImportCommand{Op: "upsert_node", Node: node}, sourceContextProof(s, "node", "constraint"))
					if namespace == "constraint-a" {
						edge := &ImportEdge{ExternalKey: "parent", Kind: "contains", FromRef: &ImportRecordRef{Base: &table}, ToRef: &ImportRecordRef{LocalKey: "constraint"}, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof:parent"}}
						commands = append(commands, ImportCommand{Op: "upsert_edge", Edge: edge}, sourceContextProof(s, "edge", "parent"))
					}
				}
				batch := sendCommands(t, f.repo, f.project, s, 1, "batch", commands...)
				if namespace == "constraint-c" {
					v, err := f.repo.PreviewImport(t.Context(), f.project.ID, s.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: f.project.CurrentRevisionID})
					if owner == "missing" && err == nil && v.State == "ready" {
						t.Fatal("FK edge borrowed a constraint facet from the other provider")
					}
					if owner == "present" && (err != nil || v.State != "ready") {
						t.Fatalf("exact FK owner rejected: %+v %v", v, err)
					}
					return
				}
				result := sourceReviewCommit(t, f.repo, f.project, s, batch.AcceptedVersion, "present")
				f.project = &result.Project
				graph, err = f.repo.ResolveSourceGraph(t.Context(), f.project.ID, f.project.CurrentRevisionID)
				if err != nil {
					t.Fatal(err)
				}
				for _, a := range graph.Assertions {
					if a.Owner.ProviderNamespace == namespace && a.Payload.Kind == "constraint" {
						constraints[namespace] = sourceAssertionRef(a)
					}
				}
			}
		})
	}
}
