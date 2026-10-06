package backendmodel

import (
	"database/sql"
	"errors"
	"testing"
	"uuid"
)

func portableOwnerFixture(t *testing.T) (*Repo, PortableModel, PortableRemap) {
	t.Helper()
	r, base, proposal := changeFixture(t)
	tx, err := r.db.R.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	sources := []PortableSource{}
	seen := map[string]bool{}
	var add func(string)
	add = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		s, err := r.ExportPortableSourceTx(t.Context(), tx, base.Project.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		deps, err := PortableRevisionDependencies(*s)
		if err != nil {
			t.Fatal(err)
		}
		for _, dep := range deps {
			add(dep)
		}
		sources = append(sources, *s)
	}
	add(base.Revision.ID)
	target := BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: proposal.Proposal.ID, ProposalRevisionID: proposal.Revision.ID}}
	p, err := r.ExportPortableProposalTx(t.Context(), tx, base.Project.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	model := PortableModel{Project: base.Project, Target: target, Sources: sources, Proposals: []PortableProposal{*p}, Diagrams: []DiagramVersion{}, DiagramViews: []DiagramView{}, SavedViews: []SavedView{}, Annotations: []Annotation{}}
	ids, err := PortableModelIdentities(model)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := r.InstallationID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	mapping := PortableRemap{OriginInstallationID: installation, InstallationID: installation, IDs: []PortableMapping{}, Artifacts: []PortableArtifactMapping{}}
	for _, id := range ids {
		mapping.IDs = append(mapping.IDs, PortableMapping{Origin: id, LocalID: uuid.NewV7().String()})
	}
	return r, model, mapping
}
func TestPortableOwnerSourceProposalAtomicImport(t *testing.T) {
	t.Parallel()
	r, model, mapping := portableOwnerFixture(t)
	for i := range model.Sources {
		if err := ValidatePortableSource(t.Context(), &model.Sources[i]); err != nil {
			t.Fatal(err)
		}
	}
	remapped, err := RemapPortableModel(model, mapping)
	if err != nil {
		t.Fatal(err)
	}
	fail := errors.New("abort all owner rows")
	var imported *PortableModel
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		var err error
		imported, err = r.ImportPortableModelTx(t.Context(), tx, *remapped, PortableImportOptions{Remap: mapping, OriginProjectID: model.Project.ID})
		if err != nil {
			return err
		}
		return fail
	})
	if !errors.Is(err, fail) {
		t.Fatal(err)
	}
	if _, err := r.Get(t.Context(), remapped.Project.ID); err == nil {
		t.Fatal("partial project escaped rollback")
	}
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		var err error
		imported, err = r.ImportPortableModelTx(t.Context(), tx, *remapped, PortableImportOptions{Remap: mapping, OriginProjectID: model.Project.ID})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := r.ResolveEffectiveGraph(t.Context(), imported.Project.ID, imported.Target)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.State.Nodes) != 1 || graph.State.Nodes[0].Name != "Handle" || graph.State.Nodes[0].ID == model.Sources[len(model.Sources)-1].Nodes[0].ID {
		t.Fatal("source/proposal semantics not retained")
	}
	if graph.Pins.ArtifactContextV3 == nil {
		t.Fatal("missing imported v3 context")
	}
}
