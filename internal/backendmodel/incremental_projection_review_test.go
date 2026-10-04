package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"testing"
)

func TestIncrementalProjectionIncludesSharedConsumers(t *testing.T) {
	f := sourceReviewSharedValue(t, "column", "equal")
	consumerSession, batch := f.mapping(t, f.b)
	base := sourceReviewCommit(t, f.repo, f.project, consumerSession, batch.AcceptedVersion, "fixture")
	f.project = &base.Project
	graph, err := f.repo.ResolveSourceGraph(t.Context(), f.project.ID, f.project.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var target, parent, consumer ProviderAssertion
	for _, a := range graph.Assertions {
		if sourceAssertionRef(a) == f.a {
			target = a
		}
		if a.Payload.Kind == "field_mapping" && a.Owner.ProviderNamespace == "provider-c" {
			consumer = a
		}
	}
	for _, a := range graph.Assertions {
		if target.Payload.ParentID != nil && a.RecordID == *target.Payload.ParentID {
			parent = a
		}
	}
	in := source6Input(t, f.project)
	in.SourceScope = &SourceScope{Kind: "reconcile", RepositoryID: f.repository, ProviderNamespace: f.a.ProviderNamespace}
	in.SyncPolicy = IncrementalSourcePolicy
	in.ChangeManifest = &ChangeManifest{Scope: "affected-subgraph", Files: []ChangeManifestFile{}, AffectedRoots: []SourceSubjectRef{{RecordType: "node", ID: target.RecordID}}}
	in.IdempotencyKey = "selected-change"
	for _, partition := range graph.SourceVector.Partitions {
		if partition.ProviderNamespace == f.a.ProviderNamespace {
			in.Inventory = partition.Inventory
		}
	}
	session, err := f.repo.BeginImport(t.Context(), f.project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	facets, _, err := relationalFacetObject("column", target.Payload.Attributes)
	if err != nil {
		t.Fatal(err)
	}
	facet, err := relationalObject(facets["sql"])
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"sourceSnapshotId", "freshness", "evidenceIds"} {
		delete(facet, key)
	}
	facet["evidenceKeys"] = relationalRaw(t, []string{"changed-proof"})
	facet["nativeType"] = relationalRaw(t, known("text"))
	facet["typeFamily"] = relationalRaw(t, known("string"))
	attrs := replaceRelationalFacets("column", target.Payload.Attributes, map[string]jsontext.Value{"sql": relationalRaw(t, facet)})
	parentRef := sourceAssertionRef(parent)
	node := &ImportNode{ExternalKey: target.ExternalKey, Kind: "column", Name: target.Payload.Name, ParentRef: &ImportRecordRef{Base: &parentRef}, Attributes: attrs, EvidenceKeys: []string{"changed-proof"}}
	proof := *fixtureCommands(session)[1].Evidence
	proof.ExternalKey = "changed-proof"
	proof.SubjectKey = target.ExternalKey
	proof.Source.StartLine = new(int64(1))
	proof.Source.EndLine = new(int64(2))
	update := sendCommands(t, f.repo, f.project, session, 1, "update", ImportCommand{Op: "upsert_node", Node: node}, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
	out := sourceReviewCommit(t, f.repo, f.project, session, update.AcceptedVersion, "fixture")
	final, err := f.repo.ResolveSourceGraph(t.Context(), f.project.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale := false
	for _, current := range final.Currentness {
		if current.RecordID == consumer.RecordID && current.ProviderNamespace == consumer.Owner.ProviderNamespace {
			stale = current.Dependency.Status == "stale"
		}
	}
	if !stale {
		t.Fatal("fixture did not change consumer effective dependency")
	}
	var raw string
	if err = f.repo.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revision_decisions WHERE revision_id=?`, out.Revision.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var decisions struct {
		AffectedScope IncrementalAffectedScope `json:"affectedScope"`
	}
	if err = json.Unmarshal([]byte(raw), &decisions); err != nil {
		t.Fatal(err)
	}
	if !scopeContains(decisions.AffectedScope.ForeignDependencies, consumer) {
		t.Fatalf("stale effective consumer omitted from ForeignDependencies: %+v; gaps=%v", decisions.AffectedScope.ForeignDependencies, decisions.AffectedScope.Gaps)
	}
}

func TestIncrementalWireRejectsForbiddenNullManifest(t *testing.T) {
	for _, profile := range []string{"source5", "source6"} {
		t.Run(profile, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "null-manifest")
			in := eventsProfileInput(firstImportFixture(p), false)
			if profile == "source6" {
				in = source6Input(t, p)
			}
			raw, _ := json.Marshal(in)
			var object map[string]jsontext.Value
			if err := json.Unmarshal(raw, &object); err != nil {
				t.Fatal(err)
			}
			object["changeManifest"] = jsontext.Value(`null`)
			raw, _ = json.Marshal(object)
			var decoded BeginImportInput
			if err := json.Unmarshal(raw, &decoded, json.RejectUnknownMembers(true)); err != nil {
				return
			}
			if _, err := r.BeginImport(t.Context(), p.ID, decoded); err == nil {
				t.Fatal("whole/legacy accepted forbidden changeManifest:null")
			}
		})
	}
}

func TestIncrementalProjectionReadClosureKeepsWriteAuthorization(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		t.Run(fmt.Sprint(exhausted), func(t *testing.T) {
			owner := AssertionOwnership{RepositoryID: "repository", ProviderNamespace: "selected"}
			root := ProviderAssertion{RecordType: "node", RecordID: "root", Owner: owner}
			parent := ProviderAssertion{RecordType: "node", RecordID: "parent", Owner: owner, Payload: SourceAssertionPayload{Kind: "service"}}
			foreign := ProviderAssertion{RecordType: "node", RecordID: "consumer", Owner: AssertionOwnership{RepositoryID: "repository", ProviderNamespace: "foreign"}, Payload: SourceAssertionPayload{Kind: "field_mapping"}, DependencyClaims: []SourceDependencyBinding{{Site: "/parentId", Target: sourceAssertionRef(parent)}}}
			key := sourceAssertionKey(root)
			scope := &IncrementalAffectedScope{Affected: []SourceSubjectRef{{RecordType: "node", ID: "root"}}, contexts: map[incrementalVisit]bool{{claim: key}: true}, writable: map[string]bool{key: true}, claims: map[string]bool{key: true}, wholeClaims: map[string]bool{key: true}, dependentClaims: map[string]bool{}}
			if exhausted {
				for i := 1; i < MaxIncrementalSubjects; i++ {
					scope.contexts[incrementalVisit{claim: fmt.Sprint(i)}] = true
				}
			}
			p := &composedGraphPreparation{session: &ImportSession{RepositoryID: owner.RepositoryID, Manifest: SourceManifest{Provider: SourceProvider{Namespace: owner.ProviderNamespace}}}, candidate: &composedCandidate{Source: &SourceGraphSnapshot{Assertions: []ProviderAssertion{root, parent, foreign}}, IncrementalScope: scope}}
			err := p.includeIncrementalProjectionDependencies(t.Context(), map[string]bool{sourceAssertionKey(foreign): true})
			if exhausted {
				if err == nil {
					t.Fatal("projection closure bypassed the shared visit limit")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(scope.writable) != 1 || !scope.writable[key] || !scopeContains(scope.ValidationDependencies, parent) || !scopeContains(scope.ForeignDependencies, foreign) || scope.visitedCount < 3 {
				t.Fatalf("read closure changed write authority or omitted counted context: %+v", scope)
			}
		})
	}
}
