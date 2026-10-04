package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
	"testing"
)

func TestIncrementalCurrentnessUntouchedProofStaysExact(t *testing.T) {
	in, add := incrementalScopeFixture(t)
	a := add("retained", "service")
	prior := sourceCurrentness(a)
	scope, err := computeIncrementalScope(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	old := bytes.Clone(in.base.RawEvidence[a.EvidenceIDs[0]])
	current, err := incrementalRetainedCurrentness(in.session, in.base, a, prior, scope)
	if err != nil {
		t.Fatal(err)
	}
	if current.Own.Status != "current" || current.Own.ConfirmedSnapshotID != a.Freshness.ConfirmedSnapshotID || !slices.Contains(current.Own.Reasons, "unchanged_source_manifest") {
		t.Fatalf("currentness=%+v", current)
	}
	if !bytes.Equal(old, in.base.RawEvidence[a.EvidenceIDs[0]]) {
		t.Fatal("retained proof bytes changed")
	}
	if len(prior.Own.Reasons) != 0 {
		t.Fatal("base currentness mutated")
	}
}

func TestIncrementalCurrentnessDoesNotRefreshUnsafeProof(t *testing.T) {
	for _, name := range []string{"previously stale", "unverified", "unavailable", "metadata only", "affected dependency", "stale dependency", "changed file type"} {
		t.Run(name, func(t *testing.T) {
			in, add := incrementalScopeFixture(t)
			a := add("retained", "service")
			prior := sourceCurrentness(a)
			scope := &IncrementalAffectedScope{claims: map[string]bool{}}
			switch name {
			case "previously stale":
				prior.Own.Status = "stale"
			case "unverified":
				in.session.Manifest.Snapshot.Consistency = "unverified"
			case "unavailable":
				in.session.Manifest.Snapshot.Files = slices.Clone(in.session.Manifest.Snapshot.Files)
				in.session.Manifest.Snapshot.Files[0].AnalysisStatus = "unsupported"
			case "metadata only":
				in.base.LegacyProofBases = []LegacyProofBasis{{EvidenceID: a.EvidenceIDs[0], Support: "historical_metadata"}}
			case "affected dependency":
				target := add("target", "symbol")
				a.DependencyClaims = []SourceDependencyBinding{incrementalDependency(target, "/attributes/ref")}
				scope.claims[sourceAssertionKey(target)] = true
			case "stale dependency":
				prior.Dependency.Status = "stale"
			case "changed file type":
				in.session.Manifest.Snapshot.Files = slices.Clone(in.session.Manifest.Snapshot.Files)
				in.session.Manifest.Snapshot.Files[0].FileType = "sql"
			}
			current, err := incrementalRetainedCurrentness(in.session, in.base, a, prior, scope)
			if err != nil {
				t.Fatal(err)
			}
			if current.Own.Status == "current" {
				t.Fatalf("unsafe proof refreshed: %+v", current)
			}
		})
	}
}

func TestIncrementalCurrentnessPreservesStaleSelectedFacet(t *testing.T) {
	in, add := incrementalScopeFixture(t)
	a := add("column", "column")
	structural := structuralTestGraph(t, "relational")
	facets, _, err := relationalFacetObject("column", structuralNode(&structural, "column").Attributes)
	if err != nil {
		t.Fatal(err)
	}
	baseFacet, err := relationalObject(facets["sql"])
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]jsontext.Value{}
	for _, key := range []string{"stale", "fresh"} {
		fields := maps.Clone(baseFacet)
		freshness := a.Freshness
		if key == "stale" {
			freshness.Status = "stale"
			freshness.Reasons = []string{"not_reobserved"}
		}
		fields["sourceKind"] = relationalRaw(t, "sql")
		fields["sourceSnapshotId"] = relationalRaw(t, a.Freshness.ConfirmedSnapshotID)
		fields["evidenceIds"] = relationalRaw(t, a.EvidenceIDs)
		fields["freshness"] = relationalRaw(t, freshness)
		declared[key] = relationalRaw(t, fields)
	}
	a.Payload.Attributes = map[string]jsontext.Value{"facets": relationalRaw(t, declared)}
	in.base.Assertions[0] = a
	prior := populateSourceFields(a, sourceCurrentness(a), false, false, in.session.SnapshotID)
	current, err := incrementalRetainedCurrentness(in.session, in.base, a, prior, &IncrementalAffectedScope{claims: map[string]bool{}})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, field := range current.Fields {
		if field.Property.Kind != "relational_facet" || field.Property.Group != "nativeType" {
			continue
		}
		key := field.Property.FacetKey
		seen[key] = true
		want := "current"
		if key == "stale" {
			want = "stale"
		}
		if field.Own.Status != want || field.Own.ConfirmedSnapshotID != a.Freshness.ConfirmedSnapshotID {
			t.Fatalf("field=%+v", field)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("missing declared facets: %v", seen)
	}
	binding := incrementalDependency(a, "/attributes/sources/0/nodeId")
	binding.ValueContext = &LineageValueRef{Kind: "column", NodeID: a.RecordID, FacetKey: "stale"}
	claims := map[string]ProviderAssertion{sourceAssertionKey(a): a}
	currents := map[string]SourceClaimCurrentness{sourceAssertionKey(a): current}
	if !sourceDependencyUnsafe(binding, claims, currents) {
		t.Fatal("another fresh facet refreshed the selected stale mapping facet")
	}
	binding.ValueContext.FacetKey = "fresh"
	if sourceDependencyUnsafe(binding, claims, currents) {
		t.Fatal("current selected facet was marked stale by another facet")
	}
}

func TestIncrementalCurrentnessForeignBytesStayExact(t *testing.T) {
	in, add := incrementalScopeFixture(t)
	a := add("foreign", "service")
	a.Owner.ProviderNamespace = "foreign"
	prior := sourceCurrentness(a)
	raw, err := json.Marshal(prior)
	if err != nil {
		t.Fatal(err)
	}
	current, err := incrementalRetainedCurrentness(in.session, in.base, a, prior, &IncrementalAffectedScope{claims: map[string]bool{sourceAssertionKey(a): true}})
	if err != nil {
		t.Fatal(err)
	}
	next, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, next) {
		t.Fatalf("foreign currentness rewritten: %s", next)
	}
}

func TestIncrementalCurrentnessRejectsWrongSupportingProof(t *testing.T) {
	in, add := incrementalScopeFixture(t)
	a := add("retained", "service")
	var e Evidence
	if err := json.Unmarshal(in.base.RawEvidence[a.EvidenceIDs[0]], &e); err != nil {
		t.Fatal(err)
	}
	e.Source.ContentHash = incrementalFile("same.go", "b").ContentHash
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	in.base.RawEvidence[e.ID] = jsontext.Value(raw)
	if _, err := incrementalRetainedCurrentness(in.session, in.base, a, sourceCurrentness(a), &IncrementalAffectedScope{claims: map[string]bool{}}); err == nil {
		t.Fatal("invalid owned locator admitted")
	}
}
