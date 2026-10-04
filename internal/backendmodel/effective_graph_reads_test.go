package backendmodel

import (
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func TestEffectiveSourceGraphPinsAndDetachedBaseline(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "effective-source")
	_, base := commitSource6Fixture(t, r, p, source6Input(t, p))
	before := immutableBytes(t, r)
	graph, err := r.ResolveEffectiveGraph(t.Context(), p.ID, BackendReadTarget{RevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	if graph.State.Revision.ID != base.Revision.ID || graph.Pins.BaseRevisionID != base.Revision.ID || graph.Pins.EffectiveSemanticHash != base.Revision.SemanticHash || graph.Pins.StructuralSchemaVersion != "6" || graph.Pins.TargetHash == "" || graph.Source == nil || graph.Source.SourceVector == nil {
		t.Fatalf("bad effective source pins: %+v", graph)
	}
	graph.State.Nodes[0].Name = "changed"
	if graph.Source.State.Nodes[0].Name == "changed" {
		t.Fatal("effective state aliases immutable source")
	}
	for key, raw := range before {
		if immutableBytes(t, r)[key] != raw {
			t.Fatalf("read mutated %s", key)
		}
	}
}

func TestEffectiveFullDraftHistoryAndIntentProof(t *testing.T) {
	r, base, initial := changeFixture(t)
	id := lineageQueryID(900999)
	command := changeCommand(t, "create_node", `"id":"`+id+`","kind":"service","name":"Desired","parentId":null,"attributes":{}`)
	next, _ := saveChange(t, r, initial, "effective-desired", command)
	before := immutableBytes(t, r)
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: next.Proposal.ID, ProposalRevisionID: next.Revision.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if graph.State.Revision.ID != base.Revision.ID || graph.Pins.EffectiveSemanticHash != next.Revision.SemanticHash || len(graph.State.Nodes) != 2 || graph.Source == nil || len(graph.Source.Assertions) != 1 {
		t.Fatalf("full graph baseline/proof: %+v", graph)
	}
	intent := false
	for _, origin := range graph.Origins {
		if origin.SubjectID == id {
			intent = true
			if origin.Kind != "intent" || origin.CommandID != command.CommandID || len(origin.SourceClaims) != 0 || len(origin.EvidenceIDs) != 0 {
				t.Fatalf("intent gained proof: %+v", origin)
			}
		}
	}
	if !intent {
		t.Fatal("desired origins missing")
	}
	historical, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: initial.Proposal.ID, ProposalRevisionID: initial.Revision.ID}})
	if err != nil || len(historical.State.Nodes) != 1 || historical.Pins.TargetHash == graph.Pins.TargetHash {
		t.Fatalf("historical draft follows later head: %+v %v", historical, err)
	}
	for key, raw := range before {
		if immutableBytes(t, r)[key] != raw {
			t.Fatalf("read changed %s", key)
		}
	}
}

func TestEffectiveReadyCandidateRebuildPinsPurityAndInvalidation(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "staged-effective")
	session, err := r.BeginImport(t.Context(), p.ID, source6Input(t, p))
	if err != nil {
		t.Fatal(err)
	}
	batch := putFixture(t, r, p, session)
	preview, err := r.PreviewImport(t.Context(), p.ID, session.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || preview.State != "ready" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	target := BackendReadTarget{ImportCandidate: &ImportCandidateReadTarget{ImportID: session.ID, ImportVersion: preview.Version, CandidateHash: *preview.CandidateHash}}
	before := immutableBytes(t, r)
	graph, err := r.ResolveEffectiveGraph(t.Context(), p.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	if graph.State.Revision.ID != p.CurrentRevisionID || graph.Source.State.Revision.ID != p.CurrentRevisionID || graph.Pins.StructuralSchemaVersion != "6" || graph.Pins.ViewSchemaVersion != "import-candidate-v1" || len(graph.State.Nodes) != 1 || graph.Source.SourceVector == nil {
		t.Fatalf("staged pins/state: %+v", graph)
	}
	for key, raw := range before {
		if immutableBytes(t, r)[key] != raw {
			t.Fatalf("candidate read mutated %s", key)
		}
	}
	commands := fixtureCommands(session)
	commands[0].Node.Name = "New observed value"
	sendCommands(t, r, p, session, preview.Version, "invalidate", commands...)
	if _, err := r.ResolveEffectiveGraph(t.Context(), p.ID, target); err == nil {
		t.Fatal("old READY selector remained valid")
	}
	foreign := createProject(t, r, "foreign")
	if _, err := r.ResolveEffectiveGraph(t.Context(), foreign.ID, target); err == nil {
		t.Fatal("foreign import visible")
	}
}

func TestEffectiveGraphQueriesUseDesiredFiltersAndPins(t *testing.T) {
	r, base, initial := changeFixture(t)
	id := lineageQueryID(900998)
	command := changeCommand(t, "create_node", `"id":"`+id+`","kind":"service","name":"Desired service","parentId":null,"attributes":{}`)
	next, _ := saveChange(t, r, initial, "effective-filter", command)
	target := &ProposalReadTarget{ProposalID: next.Proposal.ID, ProposalRevisionID: next.Revision.ID}
	page, err := r.QueryGraph(t.Context(), base.Project.ID, GraphQueryInput{ChangeProposal: target, RecordType: "nodes", Kind: "service", Search: "desired"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Nodes) != 1 || page.Nodes[0].ID != id || page.Pins == nil || page.Target == nil || page.Pins.EffectiveSemanticHash != next.Revision.SemanticHash {
		t.Fatalf("desired filter/pins: %+v", page)
	}
	for _, origin := range page.Origins {
		if origin.SubjectID == id && origin.Kind != "intent" {
			t.Fatalf("created source origin: %+v", origin)
		}
	}
	old, err := r.QueryGraph(t.Context(), base.Project.ID, GraphQueryInput{ChangeProposal: &ProposalReadTarget{ProposalID: initial.Proposal.ID, ProposalRevisionID: initial.Revision.ID}, RecordType: "nodes", Kind: "service", Search: "desired"})
	if err != nil || len(old.Nodes) != 0 {
		t.Fatalf("historical desired search leaked future draft: %+v %v", old, err)
	}
}

func TestEffectiveBasicReadsShareBaselineAndIntentPins(t *testing.T) {
	r, base, initial := changeFixture(t)
	id := lineageQueryID(900997)
	command := changeCommand(t, "create_node", `"id":"`+id+`","kind":"service","name":"Desired","parentId":null,"attributes":{}`)
	next, _ := saveChange(t, r, initial, "effective-basic", command)
	target := BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: next.Proposal.ID, ProposalRevisionID: next.Revision.ID}}
	node, err := r.ReadNode(t.Context(), base.Project.ID, target, id)
	if err != nil {
		t.Fatal(err)
	}
	if node.Node == nil || node.Node.ID != id || node.Pins == nil || len(node.BaselineEvidence) != 0 {
		t.Fatalf("new node invented baseline: %+v", node)
	}
	evidence, err := r.ReadEvidence(t.Context(), base.Project.ID, target, EvidenceQueryInput{SubjectID: id})
	if err != nil || len(evidence.Items) != 0 || evidence.Pins == nil || evidence.Pins.TargetHash != node.Pins.TargetHash {
		t.Fatalf("intent evidence/pins: %+v %v", evidence, err)
	}
	coverage, err := r.ReadCoverage(t.Context(), base.Project.ID, target)
	if err != nil || coverage.Pins == nil || coverage.Pins.TargetHash != node.Pins.TargetHash || coverage.Source == nil {
		t.Fatalf("shared coverage pins: %+v %v", coverage, err)
	}
}

func TestEffectiveCandidateRepreviewAbortCommitRestartAndCursor(t *testing.T) {
	for _, action := range []string{"preview", "abort", "commit", "restart"} {
		t.Run(action, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "candidate-action")
			session, err := r.BeginImport(t.Context(), p.ID, source6Input(t, p))
			if err != nil {
				t.Fatal(err)
			}
			batch := putFixture(t, r, p, session)
			preview, err := r.PreviewImport(t.Context(), p.ID, session.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
			if err != nil {
				t.Fatal(err)
			}
			target := BackendReadTarget{ImportCandidate: &ImportCandidateReadTarget{ImportID: session.ID, ImportVersion: preview.Version, CandidateHash: *preview.CandidateHash}}
			graph, err := r.ResolveEffectiveGraph(t.Context(), p.ID, target)
			if err != nil {
				t.Fatal(err)
			}
			before := immutableBytes(t, r)
			page, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{ImportCandidate: target.ImportCandidate, RecordType: "nodes"})
			if err != nil || page.Pins == nil || page.Pins.TargetHash != graph.Pins.TargetHash || len(page.Nodes) != 1 {
				t.Fatalf("candidate graph: %+v %v", page, err)
			}
			evidence, err := r.ReadEvidence(t.Context(), p.ID, target, EvidenceQueryInput{})
			if err != nil || len(evidence.Items) != 1 || evidence.Pins.TargetHash != graph.Pins.TargetHash {
				t.Fatalf("candidate proof: %+v %v", evidence, err)
			}
			assertions, err := r.ReadAssertions(t.Context(), p.ID, target, SourceAssertionsQuery{})
			if err != nil || assertions.Basis != "candidate" || len(assertions.Items) != 1 {
				t.Fatalf("candidate claims: %+v %v", assertions, err)
			}
			for key, raw := range before {
				if immutableBytes(t, r)[key] != raw {
					t.Fatalf("candidate read changed %s", key)
				}
			}
			switch action {
			case "preview":
				_, err = r.PreviewImport(t.Context(), p.ID, session.ID, PreviewImportInput{ExpectedImportVersion: preview.Version, BaseRevisionID: p.CurrentRevisionID})
			case "abort":
				_, err = r.AbortImport(t.Context(), p.ID, session.ID, AbortImportInput{ExpectedImportVersion: preview.Version, IdempotencyKey: "abort"})
			case "commit":
				_, err = r.CommitImport(t.Context(), p.ID, session.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commit"})
			case "restart":
				path := r.db.Path()
				if err := r.db.Close(); err != nil {
					t.Fatal(err)
				}
				db, e := store.Open(t.Context(), path)
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { _ = db.Close() })
				r = NewRepo(db)
			}
			if err != nil {
				t.Fatal(err)
			}
			current, err := r.ResolveEffectiveGraph(t.Context(), p.ID, target)
			if action == "restart" {
				if err != nil || current.Pins.TargetHash != graph.Pins.TargetHash {
					t.Fatalf("restart changed candidate: %+v %v", current, err)
				}
			} else if err == nil {
				t.Fatalf("%s did not invalidate old candidate", action)
			}
		})
	}
}
