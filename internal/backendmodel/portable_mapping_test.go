package backendmodel

import (
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"
	"uuid"
)

func TestPortableTypedMappingPreservesTextAndScopesMembers(t *testing.T) {
	t.Parallel()
	r, base, draft := changeFixture(t)
	tx, err := r.db.R.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	source, err := r.ExportPortableSourceTx(t.Context(), tx, base.Project.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := r.ExportPortableProposalTx(t.Context(), tx, base.Project.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: draft.Proposal.ID, ProposalRevisionID: draft.Revision.ID}})
	if err != nil {
		t.Fatal(err)
	}
	oldNode := source.Nodes[0].ID
	source.Nodes[0].Name = oldNode
	a := DiagramVersion{Pin: DiagramPin{ID: uuid.NewV7().String(), Version: 1, ContentHash: fixtureHash}, ProjectID: base.Project.ID, Document: diagramTestDocument(base.Revision.ID), Provenance: DiagramProvenance{Format: "backend-diagram-provenance-v1", Action: "create", Elements: []DiagramElementProvenance{}}}
	b := a
	b.Pin.ID = uuid.NewV7().String()
	model := PortableModel{Project: base.Project, Target: BackendReadTarget{RevisionID: base.Revision.ID}, Sources: []PortableSource{*source}, Proposals: []PortableProposal{*proposal}, Diagrams: []DiagramVersion{a, b}, DiagramViews: []DiagramView{}, SavedViews: []SavedView{}, Annotations: []Annotation{}}
	ids, err := PortableModelIdentities(model)
	if err != nil {
		t.Fatal(err)
	}
	mapping := PortableRemap{OriginInstallationID: uuid.NewV7().String(), InstallationID: uuid.NewV7().String(), IDs: []PortableMapping{}, Artifacts: []PortableArtifactMapping{}}
	for _, id := range ids {
		mapping.IDs = append(mapping.IDs, PortableMapping{Origin: id, LocalID: uuid.NewV7().String()})
	}
	remapped, err := RemapPortableModel(model, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if remapped.Sources[0].Nodes[0].Name != oldNode || remapped.Sources[0].Nodes[0].ID == oldNode {
		t.Fatal("arbitrary text changed or typed identity not mapped")
	}
	if remapped.Diagrams[0].Document.Payload.PrimarySystemID == remapped.Diagrams[1].Document.Payload.PrimarySystemID {
		t.Fatal("fork member identity collapsed")
	}
	if model.Diagrams[0].Pin.ID != a.Pin.ID || model.Sources[0].Nodes[0].ID != oldNode {
		t.Fatal("mapper mutated source")
	}
	mapping.IDs = mapping.IDs[1:]
	if _, err := RemapPortableModel(model, mapping); err == nil {
		t.Fatal("incomplete map accepted")
	}
}

func TestPortableReferencePathsDoNotRewriteNativeText(t *testing.T) {
	old := uuid.NewV7().String()
	next := uuid.NewV7().String()
	raw := []byte(`{"nativeText":"` + old + `","nested":{"nodeId":"` + old + `"}}`)
	value, _ := json.Marshal(next)
	out, err := portableReplacePaths(raw, "", map[string]jsontext.Value{"/nested/nodeId": value})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		NativeText string `json:"nativeText"`
		Nested     struct {
			NodeID string `json:"nodeId"`
		} `json:"nested"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.NativeText != old || got.Nested.NodeID != next {
		t.Fatal(string(out))
	}
}
