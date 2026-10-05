package backendmodel

import "testing"

func TestInteractionsBudgetFrontier(t *testing.T) {
	state := runtimeQueryFixture(t)
	runtimeQueryAddEdge(t, state, 116, "branch", 5, 7, map[string]any{"label": "rejected"})
	before, _ := requestDigest(state)
	g := &EffectiveGraphSnapshot{State: *state, Pins: EffectiveGraphPins{BaseRevisionID: state.Revision.ID, TargetHash: state.Revision.SemanticHash}}
	in := DiagramInteractionBuildInput{Target: BackendReadTarget{RevisionID: state.Revision.ID}, EntrypointID: runtimeQueryID(1), MaxSteps: 1}
	candidate, err := buildInteractions(t.Context(), g, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !candidate.Truncated || len(candidate.Gaps) == 0 || len(candidate.Document.Interactions.Steps) > 1 {
		t.Fatalf("missing cut frontier: %+v", candidate)
	}
	after, _ := requestDigest(state)
	if before != after {
		t.Fatal("builder mutated input")
	}
	in.MaxSteps = 200
	full, err := buildInteractions(t.Context(), g, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if full.Truncated {
		t.Fatal("small fixture truncated")
	}
	if len(full.Document.Interactions.Steps) < 2 || len(full.Document.Interactions.Branches) != 2 {
		t.Fatalf("branches lost: %+v", full.Document.Interactions)
	}
	for _, order := range full.Document.Interactions.Order {
		for _, a := range full.Document.Interactions.Steps {
			for _, b := range full.Document.Interactions.Steps {
				if a.ID == order.From && b.ID == order.To && len(a.BranchPath) > 0 && len(b.BranchPath) > 0 && a.BranchPath[0] != b.BranchPath[0] {
					t.Fatal("alternatives linearized")
				}
			}
		}
	}
}

func TestInteractionsBuildUnresolvedSend(t *testing.T) {
	s := eventsRuntimeState(t)
	s.Nodes[3].Attributes = runtimeQueryAttrs(t, map[string]any{"stepKind": "emit", "analysisStatus": "complete", "gaps": []string{}})
	runtimeQueryAddNode(t, s, 30, "message", 0, nil)
	runtimeQueryAddEdge(t, s, 130, "emits", 4, 30, map[string]any{"channelId": runtimeQueryID(31), "deliveryStatus": "declared"})
	g := &EffectiveGraphSnapshot{State: *s, Pins: EffectiveGraphPins{BaseRevisionID: s.Revision.ID, TargetHash: s.Revision.SemanticHash}}
	out, err := buildInteractions(t.Context(), g, DiagramInteractionBuildInput{Target: BackendReadTarget{RevisionID: s.Revision.ID}, EntrypointID: runtimeQueryID(1), MaxSteps: 200}, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, step := range out.Document.Interactions.Steps {
		if step.Kind == "send" {
			found = true
			if step.To != "" {
				t.Fatal("message identity fabricated a receiver")
			}
		}
		if step.Kind == "receive" {
			t.Fatal("fabricated receive")
		}
	}
	if !found {
		t.Fatal("send lost")
	}
}

func TestInteractionsBuildControlOrder(t *testing.T) {
	s := runtimeQueryFixture(t)
	runtimeQueryAddEdge(t, s, 120, "next", 6, 7, nil)
	g := &EffectiveGraphSnapshot{State: *s, Pins: EffectiveGraphPins{BaseRevisionID: s.Revision.ID, TargetHash: s.Revision.SemanticHash}}
	out, err := buildInteractions(t.Context(), g, DiagramInteractionBuildInput{Target: BackendReadTarget{RevisionID: s.Revision.ID}, EntrypointID: runtimeQueryID(1), MaxSteps: 200}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Document.Interactions.Steps) != 2 || len(out.Document.Interactions.Order) != 1 {
		t.Fatalf("explicit order lost: %+v", out.Document.Interactions)
	}
}

func TestInteractionsBuildCandidateReadPurity(t *testing.T) {
	r, db := testRepo(t)
	project := createProject(t, r, "builder-purity")
	s := runtimeQueryFixture(t)
	runtimeQueryAddRelationalFacets(t, s)
	s.Revision.ProjectID = project.ID
	s.Revision.ParentRevisionID = new(project.CurrentRevisionID)
	runtimeQueryPersist(t, r, s)
	before := diagramTableCounts(t, db)
	in := DiagramInteractionBuildInput{Target: BackendReadTarget{RevisionID: s.Revision.ID}, EntrypointID: runtimeQueryID(1), MaxSteps: 200}
	candidate, err := r.BuildInteractions(t.Context(), project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if before != diagramTableCounts(t, db) {
		t.Fatal("read-only builder wrote rows")
	}
	saved, err := r.CreateDiagram(t.Context(), project.ID, DiagramCreateInput{Document: candidate.Document, IdempotencyKey: "accept"})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := requestDigest(candidate.Document)
	if saved.Pin.ContentHash != hash || saved.TargetHash != candidate.TargetHash {
		t.Fatal("candidate changed at create")
	}
}

func TestInteractionsFrontierSurvivesSave(t *testing.T) {
	r, _ := testRepo(t)
	project := createProject(t, r, "frontier-persistence")
	s := runtimeQueryFixture(t)
	runtimeQueryAddRelationalFacets(t, s)
	runtimeQueryAddEdge(t, s, 116, "branch", 5, 7, map[string]any{"label": "rejected"})
	s.Revision.ProjectID = project.ID
	s.Revision.ParentRevisionID = new(project.CurrentRevisionID)
	runtimeQueryPersist(t, r, s)
	candidate, err := r.BuildInteractions(t.Context(), project.ID, DiagramInteractionBuildInput{Target: BackendReadTarget{RevisionID: s.Revision.ID}, EntrypointID: runtimeQueryID(1), MaxSteps: 1})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := r.CreateDiagram(t.Context(), project.ID, DiagramCreateInput{Document: candidate.Document, IdempotencyKey: "save"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range saved.Gaps {
		if g.Code == "interaction_boundary" {
			found = true
		}
	}
	if !found {
		t.Fatal("accepted candidate lost its cut frontier")
	}
}

func TestInteractionsMergeDoesNotChooseFirstAlternative(t *testing.T) {
	s := runtimeQueryFixture(t)
	runtimeQueryAddEdge(t, s, 116, "branch", 5, 7, map[string]any{"label": "rejected"})
	runtimeQueryAddNode(t, s, 21, "flow_step", 3, map[string]any{"stepKind": "call"})
	runtimeQueryAddEdge(t, s, 121, "next", 6, 21, nil)
	runtimeQueryAddEdge(t, s, 122, "next", 7, 21, nil)
	runtimeQueryAddEdge(t, s, 123, "calls", 21, 8, nil)
	g := &EffectiveGraphSnapshot{State: *s, Pins: EffectiveGraphPins{BaseRevisionID: s.Revision.ID, TargetHash: s.Revision.SemanticHash}}
	out, err := buildInteractions(t.Context(), g, DiagramInteractionBuildInput{Target: BackendReadTarget{RevisionID: s.Revision.ID}, EntrypointID: runtimeQueryID(1), MaxSteps: 200}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range out.Document.Interactions.Steps {
		for _, ref := range step.Refs {
			if ref.ID == runtimeQueryID(123) && step.Kind != "boundary" {
				t.Fatal("unsupported merge assigned to first alternative")
			}
		}
	}
	found := false
	for _, step := range out.Document.Interactions.Steps {
		if step.Kind == "boundary" {
			found = true
		}
	}
	if !found {
		t.Fatal("merge boundary not retained in document")
	}
}

func TestInteractionsSharedCalleeDoesNotChooseFirstAlternative(t *testing.T) {
	s := runtimeQueryFixture(t)
	runtimeQueryAddEdge(t, s, 116, "branch", 5, 7, map[string]any{"label": "rejected"})
	runtimeQueryAddNode(t, s, 20, "handler", 0, nil)
	runtimeQueryAddNode(t, s, 21, "flow", 20, map[string]any{"entryStepId": runtimeQueryID(22)})
	runtimeQueryAddNode(t, s, 22, "flow_step", 21, map[string]any{"stepKind": "call"})
	for i := range s.Edges {
		if s.Edges[i].ID == runtimeQueryID(112) || s.Edges[i].ID == runtimeQueryID(114) {
			s.Edges[i].To = runtimeQueryID(20)
		}
	}
	runtimeQueryAddEdge(t, s, 121, "contains", 20, 21, nil)
	runtimeQueryAddEdge(t, s, 122, "contains", 21, 22, nil)
	runtimeQueryAddEdge(t, s, 123, "calls", 22, 8, nil)
	g := &EffectiveGraphSnapshot{State: *s, Pins: EffectiveGraphPins{BaseRevisionID: s.Revision.ID, TargetHash: s.Revision.SemanticHash}}
	out, err := buildInteractions(t.Context(), g, DiagramInteractionBuildInput{Target: BackendReadTarget{RevisionID: s.Revision.ID}, EntrypointID: runtimeQueryID(1), MaxSteps: 200}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range out.Document.Interactions.Steps {
		for _, ref := range step.Refs {
			if ref.ID == runtimeQueryID(123) && step.Kind != "boundary" {
				t.Fatal("shared callee expanded under arbitrary alternative")
			}
		}
	}
}

func TestInteractionsVisitedBudgetFrontier(t *testing.T) {
	s := runtimeQueryFixture(t)
	s.Evidence = make([]Evidence, 250001)
	g := &EffectiveGraphSnapshot{State: *s, Pins: EffectiveGraphPins{BaseRevisionID: s.Revision.ID, TargetHash: s.Revision.SemanticHash}}
	out, err := buildInteractions(t.Context(), g, DiagramInteractionBuildInput{Target: BackendReadTarget{RevisionID: s.Revision.ID}, EntrypointID: runtimeQueryID(1), MaxSteps: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated || out.VisitedObjects > 250000 || len(out.Document.Interactions.Steps) != 1 || out.Document.Interactions.Steps[0].Kind != "boundary" {
		t.Fatal("visited budget did not retain boundary")
	}
}

func TestInteractionsBuilderRejectsUnsupportedSourceScope(t *testing.T) {
	s := runtimeQueryFixture(t)
	s.Revision.SchemaVersion = "1"
	g := &EffectiveGraphSnapshot{State: *s, Pins: EffectiveGraphPins{BaseRevisionID: s.Revision.ID}}
	_, err := buildInteractions(t.Context(), g, DiagramInteractionBuildInput{Target: BackendReadTarget{RevisionID: s.Revision.ID}, EntrypointID: runtimeQueryID(1), MaxSteps: 200}, nil)
	assertFault(t, err, "backend_unsupported_scope")
}

func TestInteractionsBuildFullTargetEvidenceDeduplication(t *testing.T) {
	s := runtimeQueryFixture(t)
	g := &EffectiveGraphSnapshot{State: *s, Pins: EffectiveGraphPins{BaseRevisionID: s.Revision.ID, TargetHash: s.Revision.SemanticHash}}
	for _, e := range s.Evidence {
		g.BaselineEvidence = append(g.BaselineEvidence, EffectiveEvidenceBasis{RevisionID: s.Revision.ID, SubjectID: e.SubjectID, EvidenceID: e.ID})
	}
	target := BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: runtimeQueryID(800), ProposalRevisionID: runtimeQueryID(801)}}
	candidate, err := buildInteractions(t.Context(), g, DiagramInteractionBuildInput{Target: target, EntrypointID: runtimeQueryID(1), MaxSteps: 200}, nil)
	if err != nil {
		t.Fatal(err)
	}
	proofs := 0
	for _, row := range diagramBases(candidate.Document) {
		proofs += len(row.origin.Evidence)
	}
	if proofs == 0 {
		t.Fatal("deduplication discarded all source proof")
	}
}
