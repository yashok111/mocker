package backendmodel

import (
	"database/sql"
	"errors"
	"testing"
	"uuid"
)

func TestPortableOwnerSource5LegacyProposalAndSavedViews(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "legacy-portable")
	in := eventsProfileInput(relationalFixtureInput(t, p, "sqlite", "v1"), false)
	session, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	preview, ids := stageRelational(t, r, p, session, relationalFixture(t, p, session, "sqlite", "v1"), "fixture")
	source, err := commitFixture(t, r, p, session, preview, "source5")
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := r.CreateProposal(t.Context(), p.ID, proposalCreateInput(source, ids, "legacy"))
	if err != nil {
		t.Fatal(err)
	}
	command := proposalNullable(ids, "require-user", false)
	_, err = r.ApplyProposal(t.Context(), p.ID, proposal.Proposal.ID, proposalApplyInput(t, r, proposal, "apply", command))
	if err != nil {
		t.Fatal(err)
	}
	proposal, err = r.GetProposal(t.Context(), p.ID, proposal.Proposal.ID, GetProposalInput{})
	if err != nil {
		t.Fatal(err)
	}
	target := BackendReadTarget{Proposal: &ProposalReadTarget{ProposalID: proposal.Proposal.ID, ProposalRevisionID: proposal.Revision.ID}}
	state := SavedViewState{Database: &SavedDatabaseViewState{Kind: "database", Scope: SavedDatabaseViewScope{DatastoreID: ids["database:orders"], FacetKey: "sql"}, Filters: SavedDatabaseViewFilters{Search: ""}, Positions: []SavedViewPosition{}, CollapsedGroupIDs: []string{}}}
	view, err := r.CreateSavedView(t.Context(), p.ID, CreateSavedViewInput{DocumentVersion: SavedViewDocumentVersion, Name: "DB", Target: target, State: state, IdempotencyKey: "view"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := r.db.R.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	sources := []PortableSource{}
	var add func(string)
	add = func(id string) {
		s, err := r.ExportPortableSourceTx(t.Context(), tx, p.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		if s.Revision.ParentRevisionID != nil {
			add(*s.Revision.ParentRevisionID)
		}
		sources = append(sources, *s)
	}
	add(source.Revision.ID)
	owner, err := r.ExportPortableProposalTx(t.Context(), tx, p.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	model := PortableModel{Project: source.Project, Target: target, Sources: sources, Proposals: []PortableProposal{*owner}, Diagrams: []DiagramVersion{}, DiagramViews: []DiagramView{}, SavedViews: []SavedView{*view}, Annotations: []Annotation{}}
	for i := range model.Sources {
		if err := ValidatePortableSource(t.Context(), &model.Sources[i]); err != nil {
			t.Fatal(err)
		}
	}
	identities, err := PortableModelIdentities(model)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := r.InstallationID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	mapping := PortableRemap{OriginInstallationID: installation, InstallationID: installation, IDs: []PortableMapping{}, Artifacts: []PortableArtifactMapping{}}
	for _, id := range identities {
		mapping.IDs = append(mapping.IDs, PortableMapping{Origin: id, LocalID: uuid.NewV7().String()})
	}
	mapped, err := RemapPortableModel(model, mapping)
	if err != nil {
		t.Fatal(err)
	}
	var imported *PortableModel
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		var err error
		imported, err = r.ImportPortableModelTx(t.Context(), tx, *mapped, PortableImportOptions{Remap: mapping, OriginProjectID: p.ID})
		return err
	})
	if err != nil {
		if f, ok := errors.AsType[*FaultError](err); ok {
			t.Fatalf("%v: %+v", err, f.Details)
		}
		t.Fatal(err)
	}
	graph, err := r.ResolveEffectiveGraph(t.Context(), imported.Project.ID, imported.Target)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.State.Nodes) != len(sources[len(sources)-1].Nodes) {
		t.Fatal("legacy source structure changed")
	}
	saved, err := r.GetSavedView(t.Context(), imported.Project.ID, imported.SavedViews[0].ID, GetSavedViewInput{Version: view.Version})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Pins.SemanticHash == view.Pins.SemanticHash || saved.State.Database.Scope.DatastoreID == view.State.Database.Scope.DatastoreID {
		t.Fatal("legacy view retained foreign IDs/hash")
	}
}
