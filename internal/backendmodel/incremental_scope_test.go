package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"testing"
	"uuid"
)

func incrementalScopeFixture(t *testing.T) (incrementalScopeInput, func(string, string) ProviderAssertion) {
	t.Helper()
	s := &ImportSession{RepositoryID: uuid.NewV7().String(), SnapshotID: uuid.NewV7().String(), Manifest: SourceManifest{Provider: SourceProvider{Namespace: "selected"}, Snapshot: SnapshotManifest{Consistency: "verified", Files: []ManifestFile{incrementalFile("same.go", "a")}}}}
	snapshot := SourceSnapshot{ID: uuid.NewV7().String(), RepositoryID: s.RepositoryID, Provider: s.Manifest.Provider, SnapshotManifest: s.Manifest.Snapshot}
	s.BasePartition = &SourcePartition{RepositoryID: s.RepositoryID, ProviderNamespace: "selected", SnapshotID: snapshot.ID}
	graph := &SourceGraphSnapshot{SourceVector: &SourceVector{Snapshots: []SourceSnapshot{snapshot}, Partitions: []SourcePartition{*s.BasePartition}}, RawEvidence: map[string]jsontext.Value{}}
	add := func(key, kind string) ProviderAssertion {
		a := ProviderAssertion{RecordType: "node", RecordID: uuid.NewV7().String(), ExternalKey: key, Owner: AssertionOwnership{RepositoryID: s.RepositoryID, ProviderNamespace: "selected", Profile: GraphProfile}, Payload: SourceAssertionPayload{RecordType: "node", Kind: kind, Name: key, Attributes: map[string]jsontext.Value{}}, EvidenceIDs: []string{uuid.NewV7().String()}, Freshness: AssertionFreshness{Status: "current", ConfirmedSnapshotID: snapshot.ID, Reasons: []string{}}, DependencyClaims: []SourceDependencyBinding{}, FieldCurrentness: []TypedFieldCurrentness{}}
		e := Evidence{ID: a.EvidenceIDs[0], ExternalKey: key + "-proof", SubjectID: a.RecordID, Method: "ast", Status: "explicit", Ownership: new(a.Owner), Freshness: new(a.Freshness), Source: EvidenceSource{RepositoryID: s.RepositoryID, SnapshotID: snapshot.ID, File: "same.go", ContentHash: incrementalFile("same.go", "a").ContentHash}}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		graph.RawEvidence[e.ID] = raw
		graph.Assertions = append(graph.Assertions, a)
		return a
	}
	return incrementalScopeInput{base: graph, session: s, changes: ChangeManifest{Scope: "affected-subgraph", Files: []ChangeManifestFile{}, AffectedRoots: []SourceSubjectRef{}}, ids: map[string]string{}}, add
}

func incrementalDependency(target ProviderAssertion, site string) SourceDependencyBinding {
	return SourceDependencyBinding{Target: sourceAssertionRef(target), Site: site}
}
func scopeContains(refs []SourceSubjectRef, a ProviderAssertion) bool {
	return slices.Contains(refs, SourceSubjectRef{RecordType: a.RecordType, ID: a.RecordID})
}

func TestIncrementalScopeExistingUpdateNeedsExplicitRoot(t *testing.T) {
	in, add := incrementalScopeFixture(t)
	a := add("untouched", "service")
	in.staged = []ImportCommand{{Op: "upsert_node", Node: &ImportNode{ExternalKey: a.ExternalKey, Kind: "service"}}}
	in.ids["node\x00"+a.ExternalKey] = a.RecordID
	if _, err := computeIncrementalScope(t.Context(), in); err == nil {
		t.Fatal("existing staged update self-authorized")
	}
	in.changes.AffectedRoots = []SourceSubjectRef{{RecordType: "node", ID: a.RecordID}}
	scope, err := computeIncrementalScope(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !scopeContains(scope.Affected, a) {
		t.Fatal("explicit root not affected")
	}
}

func TestIncrementalScopeCallerAndForeignRead(t *testing.T) {
	in, add := incrementalScopeFixture(t)
	target := add("query", "symbol")
	caller := add("caller", "symbol")
	unrelated := add("other", "symbol")
	foreign := add("foreign", "symbol")
	foreign.Owner.ProviderNamespace = "foreign"
	edge := add("call", "calls")
	edge.RecordType = "edge"
	edge.Payload.RecordType = "edge"
	edge.DependencyClaims = []SourceDependencyBinding{incrementalDependency(caller, "/from"), incrementalDependency(target, "/to")}
	foreign.DependencyClaims = []SourceDependencyBinding{incrementalDependency(target, "/attributes/ref")}
	in.base.Assertions = []ProviderAssertion{target, caller, unrelated, foreign, edge}
	in.changes.AffectedRoots = []SourceSubjectRef{{RecordType: "node", ID: target.RecordID}}
	scope, err := computeIncrementalScope(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !scopeContains(scope.Affected, target) || !scopeContains(scope.Affected, caller) || !scopeContains(scope.Affected, edge) || scopeContains(scope.Affected, unrelated) {
		t.Fatalf("affected=%+v", scope.Affected)
	}
	if !scopeContains(scope.ForeignDependencies, foreign) || scopeContains(scope.Affected, foreign) {
		t.Fatalf("foreign write/read separation=%+v", scope)
	}
	if scope.UntouchedCount != 1 {
		t.Fatalf("untouched=%d", scope.UntouchedCount)
	}
	in.changes.AffectedRoots = append(in.changes.AffectedRoots, SourceSubjectRef{RecordType: "node", ID: foreign.RecordID})
	if _, err := computeIncrementalScope(t.Context(), in); err == nil {
		t.Fatal("foreign root authorized")
	}
}

func TestIncrementalScopeOwnerDoesNotPullDisconnectedSibling(t *testing.T) {
	in, add := incrementalScopeFixture(t)
	service := add("service", "service")
	left := add("left", "flow")
	right := add("right", "flow")
	left.DependencyClaims = []SourceDependencyBinding{incrementalDependency(service, "/parentId")}
	right.DependencyClaims = []SourceDependencyBinding{incrementalDependency(service, "/parentId")}
	in.base.Assertions = []ProviderAssertion{service, left, right}
	in.changes.AffectedRoots = []SourceSubjectRef{{RecordType: "node", ID: left.RecordID}}
	scope, err := computeIncrementalScope(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !scopeContains(scope.ValidationDependencies, service) || scopeContains(scope.Affected, service) || scopeContains(scope.Affected, right) {
		t.Fatalf("sibling scope=%+v", scope.Affected)
	}
}

func TestIncrementalScopeChangedAvailabilityAndNewIdentity(t *testing.T) {
	in, add := incrementalScopeFixture(t)
	a := add("known", "service")
	in.session.Manifest.Snapshot.Files = slices.Clone(in.session.Manifest.Snapshot.Files)
	in.session.Manifest.Snapshot.Files[0].AnalysisStatus = "unsupported"
	id := uuid.NewV7().String()
	in.staged = []ImportCommand{{Op: "upsert_node", Node: &ImportNode{ExternalKey: "new", Kind: "service", Attributes: map[string]jsontext.Value{}}}}
	in.ids["node\x00new"] = id
	scope, err := computeIncrementalScope(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !scopeContains(scope.Affected, a) || !slices.Contains(scope.Affected, SourceSubjectRef{RecordType: "node", ID: id}) {
		t.Fatalf("scope=%+v", scope)
	}
	if !slices.Equal(scope.AvailabilityChanges, []string{"same.go"}) {
		t.Fatalf("availability=%v", scope.AvailabilityChanges)
	}
}

func TestIncrementalScopeCancellation(t *testing.T) {
	in, _ := incrementalScopeFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := computeIncrementalScope(ctx, in); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestIncrementalClosureLimit(t *testing.T) {
	for _, count := range []int{100000, 100001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			in, _ := incrementalScopeFixture(t)
			in.base.Assertions = make([]ProviderAssertion, count)
			for i := range count {
				a := ProviderAssertion{RecordType: "node", RecordID: fmt.Sprintf("%08x-0000-4000-8000-000000000000", i), ExternalKey: fmt.Sprint(i), Owner: AssertionOwnership{RepositoryID: in.session.RepositoryID, ProviderNamespace: "selected"}, Payload: SourceAssertionPayload{RecordType: "node", Kind: "service"}}
				if i > 0 {
					a.DependencyClaims = []SourceDependencyBinding{incrementalDependency(in.base.Assertions[0], "/parentId")}
				}
				in.base.Assertions[i] = a
			}
			in.changes.AffectedRoots = []SourceSubjectRef{{RecordType: "node", ID: in.base.Assertions[0].RecordID}}
			scope, err := computeIncrementalScope(t.Context(), in)
			if count == 100000 {
				if err != nil {
					t.Fatal(err)
				}
				if len(scope.Affected) != count {
					t.Fatalf("count=%d", len(scope.Affected))
				}
			} else if err == nil {
				t.Fatal("overflow accepted")
			}
		})
	}
}

func TestIncrementalScopeEventRoutesKeepSeparateContexts(t *testing.T) {
	in, add := incrementalScopeFixture(t)
	field := add("field", "event_field")
	mapping := add("mapping", "field_mapping")
	first := incrementalDependency(field, "/attributes/sources/0/nodeId")
	first.ValueContext = &LineageValueRef{Kind: "event_field", NodeID: field.RecordID, EndpointID: uuid.NewV7().String(), RouteID: uuid.NewV7().String()}
	second := first
	second.Site = "/attributes/sources/1/nodeId"
	second.ValueContext = new(*first.ValueContext)
	second.ValueContext.RouteID = uuid.NewV7().String()
	mapping.DependencyClaims = []SourceDependencyBinding{first, second}
	in.base.Assertions = []ProviderAssertion{field, mapping}
	in.changes.AffectedRoots = []SourceSubjectRef{{RecordType: "node", ID: field.RecordID}}
	scope, err := computeIncrementalScope(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(scope.Affected) != 2 || scope.visitedCount < 5 {
		t.Fatalf("contexts collapsed: affected=%d visits=%d", len(scope.Affected), scope.visitedCount)
	}
}

func TestIncrementalClosureLimitCountsContextMultiplication(t *testing.T) {
	in, add := incrementalScopeFixture(t)
	field := add("field", "event_field")
	mapping := add("mapping", "field_mapping")
	mapping.DependencyClaims = make([]SourceDependencyBinding, 100000)
	for i := range mapping.DependencyClaims {
		binding := incrementalDependency(field, fmt.Sprintf("/attributes/sources/%d/nodeId", i))
		binding.ValueContext = &LineageValueRef{Kind: "event_field", NodeID: field.RecordID, EndpointID: "endpoint", RouteID: fmt.Sprint(i)}
		mapping.DependencyClaims[i] = binding
	}
	in.base.Assertions = []ProviderAssertion{field, mapping}
	in.changes.AffectedRoots = []SourceSubjectRef{{RecordType: "node", ID: field.RecordID}}
	if _, err := computeIncrementalScope(t.Context(), in); err == nil {
		t.Fatal("context multiplication bypassed visited-work limit")
	}
}

func TestIncrementalScopeNewEdgeIncludesExplicitDependencies(t *testing.T) {
	in, add := incrementalScopeFixture(t)
	from := add("from", "service")
	to := add("to", "service")
	in.ids["node\x00from"] = from.RecordID
	in.ids["node\x00to"] = to.RecordID
	id := uuid.NewV7().String()
	in.ids["edge\x00call"] = id
	in.staged = []ImportCommand{{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "call", Kind: "calls", FromRef: &ImportRecordRef{LocalKey: "from"}, ToRef: &ImportRecordRef{LocalKey: "to"}, Attributes: map[string]jsontext.Value{}}}}
	scope, err := computeIncrementalScope(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !scopeContains(scope.Affected, from) || !scopeContains(scope.Affected, to) {
		t.Fatalf("new edge lost endpoint dependencies: %+v", scope.Affected)
	}
}

func TestIncrementalScopeContainsOwnerDoesNotPullSibling(t *testing.T) {
	in, add := incrementalScopeFixture(t)
	service := add("service", "service")
	left := add("left", "flow")
	right := add("right", "flow")
	links := make([]ProviderAssertion, 0, 2)
	for _, child := range []ProviderAssertion{left, right} {
		edge := add("contains-"+child.ExternalKey, "contains")
		edge.RecordType = "edge"
		edge.Payload.RecordType = "edge"
		edge.DependencyClaims = []SourceDependencyBinding{incrementalDependency(service, "/from"), incrementalDependency(child, "/to")}
		links = append(links, edge)
	}
	in.base.Assertions = append([]ProviderAssertion{service, left, right}, links...)
	in.changes.AffectedRoots = []SourceSubjectRef{{RecordType: "node", ID: left.RecordID}}
	scope, err := computeIncrementalScope(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if scopeContains(scope.Affected, right) {
		t.Fatalf("contains parent pulled unrelated sibling: %+v", scope.Affected)
	}
}

func TestIncrementalScopeEventRouteDoesNotAuthorizeOtherRouteMapping(t *testing.T) {
	in, add := incrementalScopeFixture(t)
	field := add("field", "event_field")
	routeA := add("route-a", "event_route")
	routeB := add("route-b", "event_route")
	mappingA := add("map-a", "field_mapping")
	mappingB := add("map-b", "field_mapping")
	for _, item := range []struct {
		mapping *ProviderAssertion
		route   ProviderAssertion
	}{{mapping: &mappingA, route: routeA}, {mapping: &mappingB, route: routeB}} {
		value := &LineageValueRef{Kind: "event_field", NodeID: field.RecordID, EndpointID: "endpoint", RouteID: item.route.RecordID}
		fieldBinding := incrementalDependency(field, "/attributes/sources/0/nodeId")
		fieldBinding.ValueContext = value
		routeBinding := incrementalDependency(item.route, "/attributes/sources/0/routeId")
		routeBinding.ValueContext = value
		item.mapping.DependencyClaims = []SourceDependencyBinding{fieldBinding, routeBinding}
	}
	in.base.Assertions = []ProviderAssertion{field, routeA, routeB, mappingA, mappingB}
	in.changes.AffectedRoots = []SourceSubjectRef{{RecordType: "node", ID: routeA.RecordID}}
	scope, err := computeIncrementalScope(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !scopeContains(scope.Affected, mappingA) || scopeContains(scope.Affected, mappingB) {
		t.Fatalf("contextual route spread=%+v", scope.Affected)
	}
	current, err := incrementalRetainedCurrentness(in.session, in.base, mappingB, sourceCurrentness(mappingB), scope)
	if err != nil {
		t.Fatal(err)
	}
	if current.Own.Status != "current" {
		t.Fatal("unrelated route mapping lost unchanged own proof through shared field context")
	}
}

func TestIncrementalScopeValidationOwnerDoesNotAuthorizeExistingWrite(t *testing.T) {
	in, add := incrementalScopeFixture(t)
	owner := add("owner", "service")
	child := add("child", "flow")
	child.DependencyClaims = []SourceDependencyBinding{incrementalDependency(owner, "/parentId")}
	in.base.Assertions = []ProviderAssertion{owner, child}
	in.changes.AffectedRoots = []SourceSubjectRef{{RecordType: "node", ID: child.RecordID}}
	in.ids["node\x00owner"] = owner.RecordID
	in.staged = []ImportCommand{{Op: "upsert_node", Node: &ImportNode{ExternalKey: "owner", Kind: "service", Attributes: map[string]jsontext.Value{}}}}
	if _, err := computeIncrementalScope(t.Context(), in); err == nil {
		t.Fatal("structural validation owner granted unrelated write permission")
	}
	in.changes.AffectedRoots = append(in.changes.AffectedRoots, SourceSubjectRef{RecordType: "node", ID: owner.RecordID})
	if _, err := computeIncrementalScope(t.Context(), in); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalScopeEvidenceDeletionAndResolutionNeedOwnedRoot(t *testing.T) {
	for _, decision := range []string{"evidence", "resolution"} {
		t.Run(decision, func(t *testing.T) {
			in, add := incrementalScopeFixture(t)
			a := add("untouched", "service")
			if decision == "evidence" {
				in.staged = []ImportCommand{{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: "evidence", ExternalKey: "untouched-proof", ExpectedID: a.EvidenceIDs[0]}}}
			} else {
				in.staged = []ImportCommand{{Op: "resolve_assertion", Resolution: &SourceAssertionResolution{RecordType: a.RecordType, ID: a.RecordID}}}
			}
			if _, err := computeIncrementalScope(t.Context(), in); err == nil {
				t.Fatal("decision outside selected affected scope self-authorized")
			}
			in.changes.AffectedRoots = []SourceSubjectRef{{RecordType: a.RecordType, ID: a.RecordID}}
			if _, err := computeIncrementalScope(t.Context(), in); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIncrementalScopeRejectsRetainedSnapshotAsActiveBase(t *testing.T) {
	in, _ := incrementalScopeFixture(t)
	retained := in.base.SourceVector.Snapshots[0]
	retained.ID = uuid.NewV7().String()
	retained.Role = "retained_provenance"
	in.base.SourceVector.Snapshots = append(in.base.SourceVector.Snapshots, retained)
	pinned := *in.session.BasePartition
	pinned.SnapshotID = retained.ID
	in.session.BasePartition = &pinned
	if _, err := computeIncrementalScope(t.Context(), in); err == nil {
		t.Fatal("retained provenance substituted for exact active selected base")
	}
}
