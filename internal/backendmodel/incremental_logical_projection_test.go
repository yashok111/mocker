package backendmodel

import (
	"bytes"
	"encoding/json/v2"
	"slices"
	"testing"
	"uuid"
)

func TestIncrementalProjectionUpdatesLogicalCallerCurrentness(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "logical-projection")
	selected, first := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &first.Project
	in, target := source6SharedInput(t, r, p, selected.RepositoryID)
	shared, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	claim := ImportCommand{Op: "claim_identity", ClaimIdentity: &SourceClaimIdentity{DecisionID: uuid.NewV7().String(), RecordType: "node", ExternalKey: "handler", Target: target, Reason: "same target", EvidenceKeys: []string{"proof"}}}
	commands := append([]ImportCommand{claim}, fixtureCommands(shared)...)
	b := sendCommands(t, r, p, shared, 1, "shared", commands...)
	second := sourceReviewCommit(t, r, p, shared, b.AcceptedVersion, "fixture")
	p = &second.Project
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var targetB BaseAssertionRef
	for _, a := range graph.Assertions {
		if a.Owner.ProviderNamespace == "provider-b" {
			targetB = sourceAssertionRef(a)
		}
	}
	in = source6Input(t, p)
	in.SourceScope = &SourceScope{Kind: "add_provider", RepositoryID: selected.RepositoryID}
	in.Manifest.Provider.Namespace = "provider-c"
	in.IdempotencyKey = "caller-provider"
	consumerSession, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands = fixtureCommands(consumerSession)
	commands[0].Node.ExternalKey = "caller"
	commands[0].Node.Name = "Caller"
	commands[1].Evidence.SubjectKey = "caller"
	proof := *commands[1].Evidence
	proof.ExternalKey = "call-proof"
	proof.SubjectType = "edge"
	proof.SubjectKey = "call"
	proof.Source.StartLine = new(int64(1))
	proof.Source.EndLine = new(int64(2))
	commands = append(commands, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "call", Kind: "calls", FromRef: &ImportRecordRef{LocalKey: "caller"}, ToRef: &ImportRecordRef{Base: &targetB}, Attributes: commands[0].Node.Attributes, EvidenceKeys: []string{"call-proof"}}}, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
	b = sendCommands(t, r, p, consumerSession, 1, "caller", commands...)
	third := sourceReviewCommit(t, r, p, consumerSession, b.AcceptedVersion, "fixture")
	p = &third.Project
	graph, err = r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var caller, callEdge ProviderAssertion
	for _, a := range graph.Assertions {
		if a.Owner.ProviderNamespace == "provider-c" {
			if a.RecordType == "node" {
				caller = a
			} else {
				callEdge = a
			}
		}
	}
	changes := emptyIncrementalChanges()
	changes.AffectedRoots = []SourceSubjectRef{{RecordType: "node", ID: target.ExpectedID}}
	in = incrementalInput(t, p, selected, changes)
	session, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands = fixtureCommands(session)
	commands[0].Node.Name = "Changed target"
	b = sendCommands(t, r, p, session, 1, "changed", commands...)
	out := sourceReviewCommit(t, r, p, session, b.AcceptedVersion, "fixture")
	final, err := r.ResolveSourceGraph(t.Context(), p.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	edgeState, callerState := "", ""
	for _, state := range final.Currentness {
		if state.ProviderNamespace != "provider-c" {
			continue
		}
		if state.RecordID == callEdge.RecordID {
			edgeState = state.Dependency.Status
		}
		if state.RecordID == caller.RecordID {
			callerState = state.Dependency.Status
		}
	}
	if edgeState != "stale" {
		t.Fatalf("fixture needs projection boundary on call edge; got %s", edgeState)
	}
	var raw string
	if err = r.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revision_decisions_documents WHERE revision_id=?`, out.Revision.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var decisions struct {
		AffectedScope IncrementalAffectedScope `json:"affectedScope"`
	}
	if err = json.Unmarshal([]byte(raw), &decisions); err != nil {
		t.Fatal(err)
	}
	if !scopeContains(decisions.AffectedScope.ForeignDependencies, caller) {
		t.Fatal("fixture expected the caller in the expanded read scope")
	}
	if callerState != "stale" {
		t.Fatalf("projection-expanded explicit caller remains dependency=%s while its call edge is stale", callerState)
	}
	previous := sourceReadCurrentness(graph, caller)
	current := sourceReadCurrentness(final, caller)
	if current.Own.Status != "current" || current.Own.ConfirmedSnapshotID != previous.Own.ConfirmedSnapshotID || !slices.Contains(current.Dependency.Reasons, "dependency_changed") {
		t.Fatalf("wrong caller proof/currentness split: before=%+v after=%+v", previous, current)
	}
	if !bytes.Equal(graph.rawAssertions[sourceAssertionKey(caller)], final.rawAssertions[sourceAssertionKey(caller)]) {
		t.Fatal("logical caller assertion document was rewritten")
	}
	for _, id := range caller.EvidenceIDs {
		if !bytes.Equal(graph.RawEvidence[id], final.RawEvidence[id]) {
			t.Fatal("logical caller proof was rewritten")
		}
	}
	gap := "foreign_dependency_stale: " + caller.Owner.RepositoryID + "/" + caller.Owner.ProviderNamespace + "/node/" + caller.RecordID
	if !slices.Contains(decisions.AffectedScope.Gaps, gap) {
		t.Fatalf("caller dependency gap missing: %v", decisions.AffectedScope.Gaps)
	}
	if !scopeContains(decisions.AffectedScope.ForeignDependencies, callEdge) {
		t.Fatal("call edge lost from counted read scope")
	}
	if scopeContains(decisions.AffectedScope.Affected, caller) {
		t.Fatal("foreign caller acquired selected write scope")
	}

}

func TestIncrementalProjectionSelectedLogicalCallerRemainsReadOnly(t *testing.T) {
	selected := AssertionOwnership{RepositoryID: "repository", ProviderNamespace: "selected"}
	other := AssertionOwnership{RepositoryID: "repository", ProviderNamespace: "foreign"}
	root := ProviderAssertion{RecordType: "node", RecordID: "target", Owner: selected, Freshness: AssertionFreshness{Status: "current"}}
	target := root
	target.Owner = other
	caller := ProviderAssertion{RecordType: "node", RecordID: "caller", Owner: selected, Freshness: AssertionFreshness{Status: "current", ConfirmedSnapshotID: "original-proof"}}
	call := ProviderAssertion{RecordType: "edge", RecordID: "call", Owner: other, Payload: SourceAssertionPayload{Kind: "calls"}, Freshness: AssertionFreshness{Status: "current"}, DependencyClaims: []SourceDependencyBinding{{Site: "/from", Target: sourceAssertionRef(caller)}, {Site: "/to", Target: sourceAssertionRef(target)}}}
	rootKey := sourceAssertionKey(root)
	scope := &IncrementalAffectedScope{Affected: []SourceSubjectRef{{RecordType: "node", ID: root.RecordID}}, contexts: map[incrementalVisit]bool{{claim: rootKey}: true}, writable: map[string]bool{rootKey: true}, claims: map[string]bool{rootKey: true}, wholeClaims: map[string]bool{rootKey: true}, dependentClaims: map[string]bool{}}
	graph := &SourceGraphSnapshot{Assertions: []ProviderAssertion{root, target, caller, call}}
	p := &composedGraphPreparation{session: &ImportSession{RepositoryID: selected.RepositoryID, Manifest: SourceManifest{Provider: SourceProvider{Namespace: selected.ProviderNamespace}}}, candidate: &composedCandidate{Source: graph, IncrementalScope: scope}, claims: map[string]ProviderAssertion{}, currentness: map[string]SourceClaimCurrentness{}}
	for _, a := range graph.Assertions {
		p.claims[sourceAssertionKey(a)] = a
		p.currentness[sourceAssertionKey(a)] = sourceCurrentness(a)
	}
	changed := map[string]bool{sourceAssertionKey(call): true}
	if err := p.includeIncrementalProjectionDependencies(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	p.applyIncrementalDependencyGaps()
	current := p.currentness[sourceAssertionKey(caller)]
	if current.Own.Status != "current" || current.Own.ConfirmedSnapshotID != "original-proof" || current.Dependency.Status != "stale" || !slices.Contains(current.Dependency.Reasons, "dependency_changed") {
		t.Fatalf("selected read-only caller currentness: %+v", current)
	}
	if len(scope.writable) != 1 || !scope.writable[rootKey] || scopeContains(scope.Affected, caller) || !scopeContains(scope.ValidationDependencies, caller) || scope.visitedCount != 4 {
		t.Fatalf("logical closure changed authority or work accounting: %+v", scope)
	}
	if err := p.includeIncrementalProjectionDependencies(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	if scope.visitedCount != 4 || len(scope.writable) != 1 {
		t.Fatal("revisiting the same projection boundary changed accounting or authorization")
	}
}
