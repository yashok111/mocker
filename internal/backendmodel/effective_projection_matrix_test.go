package backendmodel

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"testing"
)

func effectiveRepresentationFixture(t *testing.T) (*Repo, *ImportCommitResult, map[string]string) {
	t.Helper()
	r, _ := testRepo(t)
	p := createProject(t, r, "effective-projection-chain")
	input := source6Input(t, p)
	for i := range input.Inventory {
		if input.Inventory[i].Category == "endpoints" || input.Inventory[i].Category == "datastores" {
			input.Inventory[i].KnownCount = 1
			input.Inventory[i].Denominator = new(int64(1))
		}
	}
	session, err := r.BeginImport(t.Context(), p.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	batch := sendCommands(t, r, p, session, session.Version, "chain", representationChainCommands(t, session, representationChain(t))...)
	base := commitStaged(t, r, p, session, batch.AcceptedVersion, "commit-chain")
	source, err := r.ResolveSourceGraph(t.Context(), p.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, a := range source.Assertions {
		ids[a.ExternalKey] = a.RecordID
	}
	return r, base, ids
}
func TestEffectiveLineageIntentCallbackAndExactPins(t *testing.T) {
	t.Parallel()
	r, base, ids := effectiveRepresentationFixture(t)
	draft, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Desired mapping", BaseRevisionID: base.Revision.ID, IdempotencyKey: "desired"})
	if err != nil {
		t.Fatal(err)
	}
	mappingID := ids["00000000-0000-4000-8000-000000000053"]
	command := changeMapCommand(t, "set_field_mapping", map[string]any{"mappingId": mappingID, "parentId": ids["00000000-0000-4000-8000-000000000020"], "sources": []LineageValueRef{{Kind: "representation_field", NodeID: ids["00000000-0000-4000-8000-000000000021"]}}, "destination": LineageValueRef{Kind: "api_field", NodeID: ids["00000000-0000-4000-8000-000000000031"]}, "transform": LineageTransform{Kind: "copy", Description: "Desired serializer"}, "analysisStatus": "complete", "gaps": []string{}, "description": "Proposed behavior"})
	next, _ := saveChange(t, r, draft, "desired-mapping", command)
	page, err := r.QueryLineage(t.Context(), base.Project.ID, LineageQueryInput{ChangeProposal: &ProposalReadTarget{ProposalID: next.Proposal.ID, ProposalRevisionID: next.Revision.ID}, Seed: LineageValueRef{Kind: "representation_field", NodeID: ids["00000000-0000-4000-8000-000000000021"]}, Direction: "forward"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Pins == nil || page.Pins.EffectiveSemanticHash != next.Revision.SemanticHash || len(page.Items) != 1 || page.Items[0].Expansion != "expanded" || page.Items[0].Status != "desired" {
		t.Fatalf("desired mapping mistaken for missing/stale proof: %+v", page)
	}
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: next.Proposal.ID, ProposalRevisionID: next.Revision.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range graph.Origins {
		if origin.SubjectID == mappingID && origin.Selector.Source != nil && origin.Selector.Source.Kind == "attributes" && origin.Selector.Source.Group == "transform" && (origin.Kind != "intent" || len(origin.EvidenceIDs) != 0 || len(origin.SourceClaims) != 0) {
			t.Fatalf("intent origin gained source proof: %+v", origin)
		}
	}
}

func TestEffectiveProjectionPinsAndPropertyOriginsMatrix(t *testing.T) {
	t.Parallel()
	r, base, ids := effectiveRepresentationFixture(t)
	draft, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Desired nullable", BaseRevisionID: base.Revision.ID, IdempotencyKey: "matrix"})
	if err != nil {
		t.Fatal(err)
	}
	column := ids["00000000-0000-4000-8000-000000000043"]
	command := changeMapCommand(t, "alter_column", map[string]any{"columnId": column, "facetKey": "sql", "change": map[string]any{"group": "nullable", "nullable": map[string]any{"status": "known", "value": true}}})
	next, _ := saveChange(t, r, draft, "matrix-nullable", command)
	target := BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: next.Proposal.ID, ProposalRevisionID: next.Revision.ID}}
	node, err := r.ReadNode(t.Context(), base.Project.ID, target, column)
	if err != nil {
		t.Fatal(err)
	}
	intent, retained := false, false
	for _, origin := range node.Origins {
		if origin.Selector.Source == nil {
			continue
		}
		switch origin.Selector.Source.Group {
		case "nullable":
			intent = origin.Kind == "intent" && len(origin.EvidenceIDs) == 0
		case "nativeType":
			retained = origin.Kind == "source" && len(origin.EvidenceIDs) > 0
		}
	}
	if !intent || !retained {
		t.Fatalf("property origin support conflated: %+v", node.Origins)
	}
	database, err := r.QueryDatabase(t.Context(), base.Project.ID, DatabaseQueryInput{ChangeProposal: target.ChangeProposal, DatastoreID: ids["00000000-0000-4000-8000-000000000040"], FacetKey: "sql", RecordType: "tables"})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := r.QueryFlow(t.Context(), base.Project.ID, FlowQueryInput{ChangeProposal: target.ChangeProposal, View: "entrypoints"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := r.QueryEvents(t.Context(), base.Project.ID, EventsQueryInput{ChangeProposal: target.ChangeProposal, View: "service_calls"})
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := r.ReadCoverage(t.Context(), base.Project.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	assertions, err := r.ReadAssertions(t.Context(), base.Project.ID, target, SourceAssertionsQuery{ID: column})
	if err != nil {
		t.Fatal(err)
	}
	for _, pins := range []*EffectiveGraphPins{database.Pins, flow.Pins, events.Pins, coverage.Pins, new(assertions.Pins)} {
		if pins == nil || pins.TargetHash != node.Pins.TargetHash || pins.BaseRevisionID != base.Revision.ID || pins.EffectiveSemanticHash != next.Revision.SemanticHash {
			t.Fatalf("projection target drift: %+v", pins)
		}
	}
	if len(assertions.Items) != 1 || assertions.Basis != "baseline" {
		t.Fatalf("intent replaced baseline assertion: %+v", assertions)
	}
	saved, err := r.CreateSavedView(t.Context(), base.Project.ID, CreateSavedViewInput{DocumentVersion: SavedViewV2DocumentVersion, Name: "Desired database", Target: target, State: SavedViewState{Database: &SavedDatabaseViewState{Kind: "database", Scope: SavedDatabaseViewScope{DatastoreID: ids["00000000-0000-4000-8000-000000000040"], FacetKey: "sql"}, Filters: SavedDatabaseViewFilters{}, Positions: []SavedViewPosition{}, CollapsedGroupIDs: []string{}}}, IdempotencyKey: "save-matrix"})
	if err != nil || saved.Pins.Effective.TargetHash != node.Pins.TargetHash {
		t.Fatalf("saved target graph mismatch: %+v %v", saved, err)
	}
	before, err := json.Marshal([]any{database, flow, events}, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	later := changeMapCommand(t, "alter_column", map[string]any{"columnId": column, "facetKey": "sql", "change": map[string]any{"group": "nullable", "nullable": map[string]any{"status": "known", "value": false}}})
	_, _ = saveChange(t, r, next, "matrix-later-head", later)
	database, err = r.QueryDatabase(t.Context(), base.Project.ID, DatabaseQueryInput{ChangeProposal: target.ChangeProposal, DatastoreID: ids["00000000-0000-4000-8000-000000000040"], FacetKey: "sql", RecordType: "tables"})
	if err != nil {
		t.Fatal(err)
	}
	flow, err = r.QueryFlow(t.Context(), base.Project.ID, FlowQueryInput{ChangeProposal: target.ChangeProposal, View: "entrypoints"})
	if err != nil {
		t.Fatal(err)
	}
	events, err = r.QueryEvents(t.Context(), base.Project.ID, EventsQueryInput{ChangeProposal: target.ChangeProposal, View: "service_calls"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal([]any{database, flow, events}, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("historical specialized projections changed after proposal head advanced")
	}

}

func TestEffectiveFullSource5Structural6KeepsLegacyProofAndArtifactPins(t *testing.T) {
	t.Parallel()
	service, old, ids, scenario := artifactServiceFixture(t)
	base, _ := upgradeEventsArtifactFixture(t, service, old)
	draft, err := service.repo.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Source5 full6", BaseRevisionID: base.Revision.ID, IdempotencyKey: "source5-full"})
	if err != nil {
		t.Fatal(err)
	}
	target := BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: draft.Proposal.ID, ProposalRevisionID: draft.Revision.ID}}
	graph, err := service.repo.ResolveEffectiveGraph(t.Context(), base.Project.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Pins.StructuralSchemaVersion != "6" || graph.State.Revision.SchemaVersion != "5" || graph.Source.SourceVector != nil {
		t.Fatalf("source5 proof became composed: %+v", graph.Pins)
	}
	proof, err := effectiveRecordProof(graph, "node", ids["http"], nil)
	if err != nil || proof.status == "stale" && proof.reasons["currentness_missing"] {
		t.Fatalf("source5 dispatched composed missing sidecar: %+v %v", proof, err)
	}
	if _, err := service.repo.ReadAssertions(t.Context(), base.Project.ID, target, SourceAssertionsQuery{}); err == nil {
		t.Fatal("full source5 invented provider claim documents")
	}
	command := changeMapCommand(t, "set_artifact_pin", map[string]any{"artifact": map[string]any{"kind": "design_scenario", "id": fmt.Sprint(scenario.Scenario.ID)}, "revisionId": fmt.Sprint(scenario.Draft.ID), "editorBindings": []any{map[string]any{"selector": map[string]any{"kind": "participant", "participantId": "p"}, "sourceNodeIds": []string{ids["http"]}}}})
	next, _ := saveChange(t, service.repo, draft, "desired-scenario", command)
	page, err := service.Query(t.Context(), base.Project.ID, ArtifactQueryInput{ChangeProposal: &ProposalReadTarget{ProposalID: next.Proposal.ID, ProposalRevisionID: next.Revision.ID}, Artifact: ArtifactKey{Kind: "design_scenario", ID: fmt.Sprint(scenario.Scenario.ID)}, View: "sequence"})
	if err != nil {
		t.Fatal(err)
	}
	if page.EffectivePins == nil || page.EffectivePins.EffectiveSemanticHash != next.Revision.SemanticHash || page.RevisionID != base.Revision.ID || len(page.Pins) != 1 {
		t.Fatalf("artifact projection used baseline pins: %+v", page)
	}
}
