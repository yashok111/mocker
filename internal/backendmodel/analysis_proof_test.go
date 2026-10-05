package backendmodel

import (
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"strings"
	"testing"
	"uuid"
)

func TestAnalysisProofDesiredProperty(t *testing.T) {
	t.Parallel()
	r, base, d := changeFixture(t)
	id := uuid.NewV7().String()
	c := changeCommand(t, "create_node", fmt.Sprintf(`"id":%q,"kind":"service","name":"Desired","parentId":null,"attributes":{}`, id))
	d, _ = saveChange(t, r, d, "desired", c)
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for i := range graph.State.Nodes {
		if graph.State.Nodes[i].ID == id {
			graph.State.Nodes[i].Freshness = &AssertionFreshness{Status: "current"}
		}
	}
	proof, err := EffectivePropertyAnalysisProof(graph, ChangeRecordRef{RecordType: "node", ID: id}, EffectivePropertySelector{Kind: "source", Source: &TypedSourcePropertySelector{Kind: "name"}})
	if err != nil {
		t.Fatal(err)
	}
	if proof.Status != "desired" || proof.Boundary || len(proof.Assertions) != 0 {
		t.Fatalf("desired property misrepresented: %+v", proof)
	}
}
func TestAnalysisProofLegacySource5SupportAndStale(t *testing.T) {
	t.Parallel()
	r, base, ids := effectiveFiveRelationalFixture(t)
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	ref := ChangeRecordRef{RecordType: "edge", ID: ids["references:orders:user_fk"]}
	selector := EffectivePropertySelector{Kind: "source", Source: &TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql", Group: "updateAction"}}
	proof, err := EffectivePropertyAnalysisProof(graph, ref, selector)
	if err != nil {
		t.Fatal(err)
	}
	if proof.Status != "explicit" || proof.Boundary || len(proof.EvidenceIDs) == 0 {
		t.Fatalf("source5 typed support lost: %+v", proof)
	}
	for i := range graph.Source.State.Evidence {
		if slices.Contains(proof.EvidenceIDs, graph.Source.State.Evidence[i].ID) {
			graph.Source.State.Evidence[i].Status = "stale"
		}
	}
	proof, err = EffectivePropertyAnalysisProof(graph, ref, selector)
	if err != nil {
		t.Fatal(err)
	}
	if proof.Status != "stale" || !proof.Boundary {
		t.Fatalf("source5 stale support promoted: %+v", proof)
	}
}
func TestAnalysisProofLegacyDesiredNoOpKeepsIntent(t *testing.T) {
	t.Parallel()
	r, base, ids := effectiveFiveRelationalFixture(t)
	d, err := r.CreateProposal(t.Context(), base.Project.ID, proposalCreateInput(base, ids, "legacy-proof"))
	if err != nil {
		t.Fatal(err)
	}
	in := proposalApplyInput(t, r, d, "legacy-first", proposalNullable(ids, "legacy-command1", false))
	applied, err := r.ApplyProposal(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	d = &ProposalDetail{Proposal: applied.Proposal, Revision: applied.Revision}
	in = proposalApplyInput(t, r, d, "legacy-no-op", proposalNullable(ids, "legacy-command2", false))
	applied, err = r.ApplyProposal(t.Context(), base.Project.ID, d.Proposal.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{Proposal: &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: applied.Revision.ID}})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := EffectivePropertyAnalysisProof(graph, ChangeRecordRef{RecordType: "node", ID: ids["column:orders:user_id"]}, EffectivePropertySelector{Kind: "source", Source: &TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql", Group: "nullable"}})
	if err != nil {
		t.Fatal(err)
	}
	if proof.Status != "desired" || proof.Boundary || len(proof.EvidenceIDs) != 0 {
		t.Fatalf("legacy no-op intent borrowed source support: %+v", proof)
	}
	if len(graph.Origins) != 0 {
		t.Fatal("analysis adaptation changed historical public origins")
	}
}
func TestAnalysisProofSource6SelectedStaleUnresolvedAndLosingProvider(t *testing.T) {
	property := TypedSourcePropertySelector{Kind: "name"}
	selected := ProviderAssertion{RecordType: "node", RecordID: "id", AssertionHash: "selected", Owner: AssertionOwnership{RepositoryID: "repo", ProviderNamespace: "selected"}, Payload: SourceAssertionPayload{RecordType: "node", Kind: "system", Name: "selected"}, EvidenceIDs: []string{"selected-proof"}, Freshness: AssertionFreshness{Status: "current"}}
	losing := selected
	losing.AssertionHash = "losing"
	losing.Owner.ProviderNamespace = "losing"
	losing.Payload.Name = "losing"
	losing.EvidenceIDs = []string{"losing-proof"}
	source := &SourceGraphSnapshot{SourceVector: &SourceVector{DocumentVersion: "source-vector-v1"}, Assertions: []ProviderAssertion{losing, selected}, Selections: []SourceAssertionResolution{{RecordType: "node", ID: "id", Property: property, Select: SourceAssertionSelection{RepositoryID: "repo", ProviderNamespace: "selected", AssertionHash: "selected"}}}, RawEvidence: map[string]jsontext.Value{"selected-proof": jsontext.Value(`{"id":"selected-proof","status":"explicit"}`), "losing-proof": jsontext.Value(`{"id":"losing-proof","status":"stale"}`)}}
	current := sourceCurrentness(selected)
	current.Fields = []TypedFieldCurrentness{{Property: property, Own: selected.Freshness, Dependency: AssertionFreshness{Status: "stale", Reasons: []string{"dependency_changed"}}}}
	source.Currentness = []SourceClaimCurrentness{current}
	graph := &EffectiveGraphSnapshot{Source: source, State: RevisionState{Nodes: []Node{{ID: "id", Kind: "system", Name: "selected"}}}}
	ref := ChangeRecordRef{RecordType: "node", ID: "id"}
	selector := EffectivePropertySelector{Kind: "source", Source: new(property)}
	p, err := EffectivePropertyAnalysisProof(graph, ref, selector)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "stale" || !p.Boundary || !slices.Contains(p.Reasons, "dependency_changed") || slices.Contains(p.Reasons, "stale_evidence") || !slices.Equal(p.EvidenceIDs, []string{"selected-proof"}) || len(p.Assertions) != 1 || p.Assertions[0].ProviderNamespace != "selected" {
		t.Fatalf("selected source6 proof lost: %+v", p)
	}
	source.proofIndex = nil
	source.RawEvidence["selected-proof"] = jsontext.Value(`{"id":"selected-proof","status":"unresolved"}`)
	p, err = EffectivePropertyAnalysisProof(graph, ref, selector)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "unresolved" || !p.Boundary || !slices.Contains(p.Reasons, "unresolved_evidence") {
		t.Fatalf("unresolved selected claim promoted: %+v", p)
	}
}
func TestAnalysisProofProjectEffectiveSharesFailedOwnerBudgetAcrossSides(t *testing.T) {
	r, base, _ := changeFixture(t)
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	owner := &editorTestScenario{}
	request := NewEditorArtifactRequest(t.Context(), nil, owner)
	for side := range 2 {
		for i := range 10 {
			pin := ArtifactPin{Kind: "design_scenario", ID: fmt.Sprint(side*10 + i + 1), RevisionID: "1", ContentHash: strings.Repeat("a", 64)}
			g := *graph
			g.Pins = graph.Pins
			g.Pins.ArtifactPins = []ArtifactPin{pin}
			g.Pins.ArtifactContext = &ArtifactContext{DocumentVersion: EditorArtifactDocumentVersion, SourceContentHash: strings.Repeat("b", 64), SourceSemanticHash: base.Revision.SemanticHash}
			_, err = request.ProjectEffective(&g, ArtifactQueryInput{RevisionID: g.State.Revision.ID, Artifact: artifactKey(pin), View: "sequence"})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(owner.calls) != 20 {
		t.Fatalf("shared before/after budget used %d owner reads", len(owner.calls))
	}
	_, err = request.SnapshotPin(ArtifactKey{Kind: "design_scenario", ID: "21"}, "1")
	assertFault(t, err, "backend_artifact_work_limit")
	if len(owner.calls) != 20 {
		t.Fatal("twenty-first owner was read")
	}
}
func TestAnalysisProofValueSelectedFacetIgnoresStaleSibling(t *testing.T) {
	t.Parallel()
	r, base, ids := effectiveFiveRelationalFixture(t)
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	id := ids["column:orders:user_id"]
	for i := range graph.Source.State.Evidence {
		e := &graph.Source.State.Evidence[i]
		if e.SubjectID == id && e.PropertyPath != nil && strings.HasPrefix(*e.PropertyPath, "/attributes/facets/orm") {
			e.Status = "stale"
		}
	}
	p, err := EffectiveValueAnalysisProof(graph, LineageValueRef{Kind: "column", NodeID: id, FacetKey: "sql"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status == "stale" || p.Status == "unresolved" || p.Boundary || len(p.EvidenceIDs) == 0 {
		t.Fatalf("unrelated stale facet downgraded selected value: %+v", p)
	}
	sibling, err := EffectiveValueAnalysisProof(graph, LineageValueRef{Kind: "column", NodeID: id, FacetKey: "orm"})
	if err != nil {
		t.Fatal(err)
	}
	if sibling.Status != "stale" || !sibling.Boundary {
		t.Fatalf("selected stale facet lost boundary: %+v", sibling)
	}
}
func TestAnalysisProofEventValueRequiresExactContextualRoute(t *testing.T) {
	r, base, _, _ := eventsLineageCommitted(t)
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	var ref LineageValueRef
	for _, n := range graph.State.Nodes {
		if n.Kind != "field_mapping" {
			continue
		}
		a, err := decodeLineageMappingForSchema(n.Attributes, base.Revision.SchemaVersion)
		if err != nil {
			continue
		}
		for _, v := range append(slices.Clone(a.Sources), a.Destination) {
			if v.Kind == "event_field" {
				ref = v
				break
			}
		}
		if ref.Kind != "" {
			break
		}
	}
	if ref.Kind == "" {
		t.Fatal("fixture lacks contextual event value")
	}
	proof, err := EffectiveValueAnalysisProof(graph, ref)
	if err != nil {
		t.Fatal(err)
	}
	wrong := ref
	wrong.RouteID = uuid.NewV7().String()
	if _, err = EffectiveValueAnalysisProof(graph, wrong); err == nil {
		t.Fatal("contextual route fell back to same node")
	}
	routeSupport := false
	for i := range graph.Source.State.Evidence {
		e := &graph.Source.State.Evidence[i]
		if e.SubjectID == ref.RouteID {
			routeSupport = true
			if !slices.Contains(proof.EvidenceIDs, e.ID) {
				t.Fatal("exact route evidence omitted from contextual proof")
			}
			e.Status = "stale"
		}
	}
	if !routeSupport {
		t.Fatal("fixture route has no saved proof")
	}
	proof, err = EffectiveValueAnalysisProof(graph, ref)
	if err != nil {
		t.Fatal(err)
	}
	if proof.Status != "stale" || !proof.Boundary {
		t.Fatalf("stale contextual route lost boundary: %+v", proof)
	}
}
func TestAnalysisProofIntentIdentityUsesExistingOrigin(t *testing.T) {
	t.Parallel()
	r, base, d := changeFixture(t)
	id := uuid.NewV7().String()
	create := changeCommand(t, "create_node", fmt.Sprintf(`"id":%q,"kind":"service","name":"Desired","parentId":null,"attributes":{}`, id))
	identity := changeCommand(t, "map_identity", fmt.Sprintf(`"target":{"kind":"intent_identity","recordType":"node","id":%q},"expectedExternalKey":null,"newExternalKey":"planned-service"`, id))
	d, _ = saveChange(t, r, d, "identity-proof", create, identity)
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := EffectivePropertyAnalysisProof(graph, ChangeRecordRef{RecordType: "node", ID: id}, EffectivePropertySelector{Kind: "intent_identity", RecordType: "node", ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "desired" || p.Boundary || len(p.EvidenceIDs) != 0 {
		t.Fatalf("identity intent lost origin: %+v", p)
	}
}
