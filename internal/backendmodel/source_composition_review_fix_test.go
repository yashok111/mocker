package backendmodel

import (
	"encoding/json/jsontext"
	"slices"
	"testing"
	"uuid"
)

// source6EmptyReconcile commits a whole-source reconcile of the given
// session's repository/provider that resubmits nothing, so every selected
// claim stays un-reobserved.
func source6EmptyReconcile(t *testing.T, r *Repo, p *Project, initial *ImportSession, key string) *ImportCommitResult {
	t.Helper()
	next := source6Input(t, p)
	next.IdempotencyKey = key
	next.SourceScope = &SourceScope{Kind: "reconcile", RepositoryID: initial.RepositoryID, ProviderNamespace: initial.Manifest.Provider.Namespace}
	session, err := r.BeginImport(t.Context(), p.ID, next)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := r.PreviewImport(t.Context(), p.ID, session.ID, PreviewImportInput{ExpectedImportVersion: session.Version, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || preview.State != "ready" {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	out, err := r.CommitImport(t.Context(), p.ID, session.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Review 2026-10-06, F50: a claim that stays un-reobserved across several
// whole-source imports must carry exactly one not_reobserved reason, not one
// more per import, and two consecutive revisions must not report a
// freshness change for it.
func TestSource6StaleReasonDoesNotGrowAcrossImports(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "stale-reason-growth")
	initial, first := commitSource6Fixture(t, r, p, source6Input(t, p))
	second := source6EmptyReconcile(t, r, &first.Project, initial, "stale-second")
	third := source6EmptyReconcile(t, r, &second.Project, initial, "stale-third")
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, third.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range graph.Currentness {
		if f.Own.Status != "stale" || !slices.Equal(f.Own.Reasons, []string{"not_reobserved"}) {
			t.Fatalf("claim %s own=%+v, want one not_reobserved", f.RecordID, f.Own)
		}
		for _, field := range f.Fields {
			if field.Own.Status == "stale" && !slices.Equal(field.Own.Reasons, []string{"not_reobserved"}) {
				t.Fatalf("field %+v own=%+v, want one not_reobserved", field.Property, field.Own)
			}
		}
	}
	comparison, err := r.CompareRevisions(t.Context(), p.ID, CompareRevisionsInput{FromRevisionID: second.Revision.ID, ToRevisionID: third.Revision.ID, ChangeKind: "freshness_changed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(comparison.Items) != 0 {
		t.Fatalf("unchanged stale claim reported %d freshness changes", len(comparison.Items))
	}
}

// Review 2026-10-06, F51: choosing the contender that has no facet K must
// leave no facet K behind — neither an empty one created on the way to the
// deleted leaf, nor a metadata-only shell once every group is deleted.
func TestSource6AbsentFacetSelectionPrunesFacet(t *testing.T) {
	t.Parallel()
	facet := jsontext.Value(`{"dialect":"postgresql","analysisStatus":"complete","gaps":[],"qualifiedName":"public.t","evidenceIds":["e"],"sourceSnapshotId":"s"}`)
	with := SourceAssertionPayload{RecordType: "node", Kind: "table", Name: "t", Attributes: replaceRelationalFacets("table", map[string]jsontext.Value{}, map[string]jsontext.Value{"sql": facet})}
	without := SourceAssertionPayload{RecordType: "node", Kind: "table", Name: "t", Attributes: replaceRelationalFacets("table", map[string]jsontext.Value{}, map[string]jsontext.Value{})}
	for name, payload := range map[string]SourceAssertionPayload{"present": with, "absent": without} {
		for _, group := range sourceFacetGroups("table") {
			var err error
			payload, err = ApplySourceProperty(payload, TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql", Group: group}, SourcePropertyValue{})
			if err != nil {
				t.Fatal(err)
			}
		}
		facets, _, err := relationalFacetObject("table", payload.Attributes)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := facets["sql"]; ok {
			t.Fatalf("%s: absent selection left facet sql=%s", name, facets["sql"])
		}
	}
}

// Review 2026-10-06, F52: an active resolution whose conflict disappeared
// (the claims now agree) does not fail the merge; the decision is unused
// and publishes no selection.
func TestSource6OrphanedResolutionIsIgnored(t *testing.T) {
	t.Parallel()
	payload := SourceAssertionPayload{RecordType: "node", Kind: "system", Name: "same", Attributes: map[string]jsontext.Value{}}
	claims := make([]ProviderAssertion, 0, 2)
	for _, namespace := range []string{"a", "b"} {
		claims = append(claims, ProviderAssertion{RecordType: "node", RecordID: "shared", AssertionHash: namespace, Owner: AssertionOwnership{RepositoryID: "repo", ProviderNamespace: namespace}, Payload: payload})
	}
	source := &SourceGraphSnapshot{SourceVector: &SourceVector{DocumentVersion: "source-vector-v1"}, Assertions: claims}
	candidate := &composedCandidate{Graph: &graphCandidate{}, Source: source}
	stale := SourceAssertionResolution{DecisionID: "d", RecordType: "node", ID: "shared", Property: TypedSourcePropertySelector{Kind: "name"}, ConflictHash: "old", Select: SourceAssertionSelection{RepositoryID: "repo", ProviderNamespace: "b", AssertionHash: "b"}, Reason: "earlier choice"}
	diagnostics, err := mergeSourceAssertions(&SourceGraphSnapshot{SourceVector: source.SourceVector}, candidate, []SourceDecision{{State: "active", Command: ImportCommand{Op: "resolve_assertion", Resolution: &stale}}})
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("orphaned resolution: diagnostics=%+v err=%v", diagnostics, err)
	}
	if len(candidate.Source.Selections) != 0 {
		t.Fatalf("unused resolution published a selection: %+v", candidate.Source.Selections)
	}
}

// Review 2026-10-06, F58: a single-provider import of more than half the
// advertised node or edge cap is within the cap; claims and effective records
// are each bounded by it, not their sum.
func TestSource6LimitsDoNotSumClaimsAndRecords(t *testing.T) {
	t.Parallel()
	check := func(nodes, edges int) error {
		g := &graphCandidate{Nodes: make([]Node, nodes), Edges: make([]Edge, edges)}
		source := &SourceGraphSnapshot{}
		for range nodes {
			source.Assertions = append(source.Assertions, ProviderAssertion{RecordType: "node"})
		}
		for range edges {
			source.Assertions = append(source.Assertions, ProviderAssertion{RecordType: "edge"})
		}
		p := &composedGraphPreparation{session: &ImportSession{}, candidate: &composedCandidate{Graph: g, Source: source}}
		return p.validateLimits(0)
	}
	if err := check(MaxRevisionNodes/2+1, MaxRevisionEdges/2+1); err != nil {
		t.Fatalf("advertised limits halved: %v", err)
	}
	assertFault(t, check(MaxRevisionNodes+1, 0), "backend_import_limit")
	assertFault(t, check(0, MaxRevisionEdges+1), "backend_import_limit")
}

// Review 2026-10-06, F195: claim_identity in an incremental-source-v1
// session (sync.md step 4) must not end in a changeManifest fault at
// preview after the batch was accepted.
func TestSource6IncrementalClaimIdentity(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "incremental-claim")
	a, first := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &first.Project
	in, target := source6SharedInput(t, r, p, a.RepositoryID)
	b, second := commitEmptySource6(t, r, p, in)
	p = &second.Project
	next := incrementalInput(t, p, b, emptyIncrementalChanges())
	next.Manifest.Provider.Namespace = b.Manifest.Provider.Namespace
	session, err := r.BeginImport(t.Context(), p.ID, next)
	if err != nil {
		t.Fatal(err)
	}
	claim := ImportCommand{Op: "claim_identity", ClaimIdentity: &SourceClaimIdentity{DecisionID: uuid.NewV7().String(), RecordType: "node", ExternalKey: "handler", Target: target, Reason: "same source object", EvidenceKeys: []string{"proof"}}}
	commands := append([]ImportCommand{claim}, fixtureCommands(session)...)
	hash, _ := ImportBatchHash(commands)
	batch, err := r.PutImportBatch(t.Context(), p.ID, session.ID, "claims", ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: hash, Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := r.PreviewImport(t.Context(), p.ID, session.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatalf("accepted claim_identity failed at preview: %v", err)
	}
	if preview.State != "ready" {
		t.Fatalf("preview=%+v", preview)
	}
	out, err := r.CommitImport(t.Context(), p.ID, session.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commit"})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	owners := []string{}
	for _, claim := range graph.Assertions {
		if claim.RecordID == target.ExpectedID {
			owners = append(owners, claim.Owner.ProviderNamespace)
		}
	}
	slices.Sort(owners)
	if !slices.Equal(owners, []string{a.Manifest.Provider.Namespace, b.Manifest.Provider.Namespace}) {
		t.Fatalf("shared identity owners=%v", owners)
	}
}

// Review 2026-10-06, F196: an evidence key a re-observed claim stopped
// listing is retired at commit, and evidence has no map_identity command —
// so a later upload of the same key must be accepted, not refused as a
// silent reuse.
func TestSource6DroppedEvidenceKeyCanReturn(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "evidence-key-return")
	initial, first := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &first.Project
	for i, key := range []string{"proof-2", "proof"} {
		in := source6Input(t, p)
		in.IdempotencyKey = "evidence-" + key
		in.SourceScope = &SourceScope{Kind: "reconcile", RepositoryID: initial.RepositoryID, ProviderNamespace: initial.Manifest.Provider.Namespace}
		session, err := r.BeginImport(t.Context(), p.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		commands := fixtureCommands(session)
		commands[0].Node.EvidenceKeys = []string{key}
		commands[1].Evidence.ExternalKey = key
		batch := sendCommands(t, r, p, session, session.Version, "evidence", commands...)
		out := sourceReviewCommit(t, r, p, session, batch.AcceptedVersion, "fixture")
		p = &out.Project
		if i == 1 {
			graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
			if err != nil || len(graph.Assertions) != 1 || len(graph.Assertions[0].EvidenceIDs) != 1 {
				t.Fatalf("graph=%+v err=%v", graph, err)
			}
		}
	}
}

// Review 2026-10-06, F57: a composed preview reports its changed files and
// staged map_identity decisions, in the counts and in the paged lists the
// review step reads.
func TestSource6PreviewListsSourceAndIdentityChanges(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "preview-changes")
	old, base := commitSource6Fixture(t, r, p, source6Input(t, p))
	p = &base.Project
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	changed := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	in := source6Input(t, p)
	in.SourceScope = &SourceScope{Kind: "reconcile", RepositoryID: old.RepositoryID, ProviderNamespace: old.Manifest.Provider.Namespace}
	in.IdempotencyKey = "changes"
	in.Manifest.Snapshot.Files[0].ContentHash = changed
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	b := sendCommands(t, r, p, s, 1, "map", ImportCommand{Op: "map_identity", Identity: &ImportIdentityMap{RecordType: "node", FromExternalKey: "handler", ToExternalKey: "renamed", ExpectedID: graph.Assertions[0].RecordID, Reason: "rename stable identity", EvidenceKeys: []string{"proof"}}})
	commands := fixtureCommands(s)
	commands[0].Node.ExternalKey = "renamed"
	commands[1].Evidence.SubjectKey = "renamed"
	commands[1].Evidence.Source.ContentHash = changed
	b = sendCommands(t, r, p, s, b.AcceptedVersion, "subject", commands...)
	preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || preview.State != "ready" {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	if preview.SourceChangeCount != 1 || preview.IdentityDecisionCount != 1 {
		t.Fatalf("sourceChangeCount=%d identityDecisionCount=%d", preview.SourceChangeCount, preview.IdentityDecisionCount)
	}
	sources, err := r.ImportChanges(t.Context(), p.ID, s.ID, ImportChangesInput{PreviewVersion: preview.Version, RecordType: "source"})
	if err != nil || len(sources.Items) != 1 || sources.Items[0].Source.Kind != "content_changed" || sources.Items[0].Source.Path != "main.go" {
		t.Fatalf("source changes=%+v err=%v", sources, err)
	}
	identities, err := r.ImportChanges(t.Context(), p.ID, s.ID, ImportChangesInput{PreviewVersion: preview.Version, RecordType: "identity"})
	if err != nil || len(identities.Items) != 1 || identities.Items[0].Identity.Command.ToExternalKey != "renamed" || !identities.Items[0].Identity.Resolved {
		t.Fatalf("identity decisions=%+v err=%v", identities, err)
	}
}

// Review 2026-10-06, F54: the dependency bindings of retained facets are
// appended in a fixed order, whatever the map iteration order is.
func TestSource6RetainedFacetBindingsAreDeterministic(t *testing.T) {
	t.Parallel()
	keys := []string{"a", "b", "c", "d", "e", "f"}
	facets := map[string]jsontext.Value{}
	old := ProviderAssertion{Payload: SourceAssertionPayload{RecordType: "node", Kind: "table"}}
	for _, key := range keys {
		facets[key] = jsontext.Value(`{}`)
		old.DependencyClaims = append(old.DependencyClaims, SourceDependencyBinding{Site: "/attributes/facets/" + key, Property: TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: key}})
	}
	old.Payload.Attributes = replaceRelationalFacets("table", map[string]jsontext.Value{}, facets)
	fresh := ProviderAssertion{Payload: SourceAssertionPayload{RecordType: "node", Kind: "table", Attributes: replaceRelationalFacets("table", map[string]jsontext.Value{}, map[string]jsontext.Value{})}}
	for range 20 {
		got := retainSourceFacets(fresh, old, map[string]bool{})
		if len(got.DependencyClaims) != len(keys) {
			t.Fatalf("retained %d bindings, want %d", len(got.DependencyClaims), len(keys))
		}
		for i, binding := range got.DependencyClaims {
			if binding.Property.FacetKey != keys[i] {
				t.Fatalf("retained bindings out of order: %+v", got.DependencyClaims)
			}
		}
	}
}
