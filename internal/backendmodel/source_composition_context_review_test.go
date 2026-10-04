package backendmodel

import (
	"encoding/json/jsontext"
	"strings"
	"testing"
)

func sourceContextProof(s *ImportSession, typ, key string) ImportCommand {
	e := *fixtureCommands(s)[1].Evidence
	e.ExternalKey, e.SubjectType, e.SubjectKey = "proof:"+key, typ, key
	e.Source.StartLine, e.Source.EndLine = new(int64(1)), new(int64(2))
	return ImportCommand{Op: "upsert_evidence", Evidence: &e}
}

func TestSource6ExactRuntimeAccessFacet(t *testing.T) {
	for _, kind := range []string{"reads", "writes", "deletes"} {
		for _, owner := range []string{"missing", "present"} {
			t.Run(kind+"/"+owner, func(t *testing.T) {
				f := sourceReviewSharedValue(t, "column", "borrow")
				graph, err := f.repo.ResolveSourceGraph(t.Context(), f.project.ID, f.project.CurrentRevisionID)
				if err != nil {
					t.Fatal(err)
				}
				var datastore BaseAssertionRef
				for _, a := range graph.Assertions {
					if a.Payload.Kind == "datastore" {
						datastore = sourceAssertionRef(a)
					}
				}
				target := f.a
				if owner == "present" {
					target = f.b
				}
				in := source6Input(t, f.project)
				in.SourceScope = &SourceScope{Kind: "add_provider", RepositoryID: f.repository}
				in.Manifest.Provider.Namespace, in.IdempotencyKey = "runtime-review", "runtime-review"
				s, err := f.repo.BeginImport(t.Context(), f.project.ID, in)
				if err != nil {
					t.Fatal(err)
				}
				commands := []ImportCommand{
					{Op: "upsert_node", Node: &ImportNode{ExternalKey: "handler", Kind: "handler", Name: "handler", Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof:handler"}}},
					sourceContextProof(s, "node", "handler"),
					{Op: "upsert_node", Node: &ImportNode{ExternalKey: "query", Kind: "query", Name: "query", ParentRef: &ImportRecordRef{LocalKey: "handler"}, Attributes: runtimeAttrs(t, map[string]any{"analysisStatus": "complete", "gaps": []string{}, "dialect": "postgresql", "nativeDefinition": "select 1", "parameters": []any{}, "results": []any{}, "columnScope": "complete"}), EvidenceKeys: []string{"proof:query"}}},
					sourceContextProof(s, "node", "query"),
					{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "parent", Kind: "contains", FromRef: &ImportRecordRef{LocalKey: "handler"}, ToRef: &ImportRecordRef{LocalKey: "query"}, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof:parent"}}},
					sourceContextProof(s, "edge", "parent"),
					{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "access", Kind: kind, FromRef: &ImportRecordRef{LocalKey: "query"}, ToRef: &ImportRecordRef{Base: &target}, Attributes: runtimeAttrs(t, map[string]any{"accessMode": map[string]string{"reads": "read", "writes": "update", "deletes": "delete"}[kind], "datastoreRef": ImportRecordRef{Base: &datastore}, "facetKey": "sql", "columnScope": "listed"}), EvidenceKeys: []string{"proof:access"}}},
					sourceContextProof(s, "edge", "access"),
				}
				batch := sendCommands(t, f.repo, f.project, s, 1, "runtime", commands...)
				preview, err := f.repo.PreviewImport(t.Context(), f.project.ID, s.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: f.project.CurrentRevisionID})
				if owner == "missing" {
					if err == nil && preview.State == "ready" {
						t.Fatal("runtime access borrowed a facet from a different provider")
					}
				} else if err != nil || preview.State != "ready" {
					t.Fatalf("exact owning facet rejected: %+v %v", preview, err)
				}
			})
		}
	}
}

func TestSource6ExactRelationalReferenceContext(t *testing.T) {
	for _, tc := range []struct {
		name, subject, target string
		change                func(*testing.T, *Node)
	}{
		{"constraint column parent", "fk", "column", func(_ *testing.T, n *Node) { n.ParentID = new(structuralID("elsewhere")) }},
		{"index column parent", "index", "column", func(_ *testing.T, n *Node) { n.ParentID = new(structuralID("elsewhere")) }},
		{"FK constraint facet", "references", "fk", func(t *testing.T, n *Node) {
			facets, _, _ := relationalFacetObject(n.Kind, n.Attributes)
			n.Attributes = replaceRelationalFacets(n.Kind, n.Attributes, map[string]jsontext.Value{"orm": facets["sql"]})
		}},
		{"FK constraint kind", "references", "fk", func(t *testing.T, n *Node) { structuralMutateFacet(t, n, "constraintKind", "unique") }},
		{"FK ordered columns", "references", "fk", func(t *testing.T, n *Node) {
			structuralMutateFacet(t, n, "columnIds", []string{structuralID("column2")})
		}},
		{"FK pair source parent", "references", "column", func(_ *testing.T, n *Node) { n.ParentID = new(structuralID("elsewhere")) }},
	} {
		for _, owner := range []string{"matching", "other-provider-only"} {
			t.Run(tc.name+"/"+owner, func(t *testing.T) {
				g := structuralTestGraph(t, "relational")
				if owner == "other-provider-only" {
					tc.change(t, structuralNode(&g, tc.target))
				}
				err := sourceReviewExactContext(t, g, structuralID(tc.subject))
				if owner == "matching" && err != nil {
					t.Fatal(err)
				}
				if owner != "matching" && err == nil {
					t.Fatal("context borrowed another provider's parent, facet or definition")
				}
			})
		}
	}
}

func sourceReviewExactContext(t *testing.T, graph SourceStructuralGraph, subjectID string) error {
	t.Helper()
	claims := map[string]ProviderAssertion{}
	add := func(id, typ string, payload SourceAssertionPayload) {
		if relationalSubject(payload.Kind, payload.Attributes, typ == "edge") {
			facets, _, _ := relationalFacetObject(payload.Kind, payload.Attributes)
			for key, raw := range facets {
				facet, _ := relationalObject(raw)
				facet["sourceKind"] = relationalRaw(t, "sql")
				facet["sourceSnapshotId"] = relationalRaw(t, structuralID("snapshot"))
				facet["freshness"] = relationalRaw(t, AssertionFreshness{Status: "current", ConfirmedSnapshotID: structuralID("snapshot"), Reasons: []string{}})
				facet["evidenceIds"] = relationalRaw(t, []string{structuralID("proof:" + id)})
				facets[key] = relationalRaw(t, facet)
			}
			payload.Attributes = replaceRelationalFacets(payload.Kind, payload.Attributes, facets)
		}
		claims[id] = ProviderAssertion{RecordType: typ, RecordID: id, ExternalKey: id, Owner: AssertionOwnership{RepositoryID: structuralID("repository"), ProviderNamespace: "bound-owner"}, AssertionHash: strings.Repeat("a", 64), Payload: payload}
	}
	for _, n := range graph.Nodes {
		add(n.ID, "node", sourceNodePayload(n))
	}
	for _, e := range graph.Edges {
		add(e.ID, "edge", sourceEdgePayload(e))
	}
	a, ok := claims[subjectID]
	if !ok {
		t.Fatal("missing context fixture subject")
	}
	refs, err := sourcePayloadReferenceSites(a.Payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		target, exists := claims[ref.ID]
		if !exists {
			t.Fatalf("missing context fixture target %s", ref.ID)
		}
		property := sourcePropertyForPointer(a.Payload, ref.Path)
		if property == nil {
			t.Fatalf("missing property at %s", ref.Path)
		}
		a.DependencyClaims = append(a.DependencyClaims, SourceDependencyBinding{Basis: "base", BaseRevisionID: structuralID("revision"), Site: ref.Path, Property: *property, Target: sourceAssertionRef(target)})
	}
	return validateSourceValueContexts(a, func(binding SourceDependencyBinding) (ProviderAssertion, error) {
		return claims[binding.Target.ExpectedID], nil
	})
}

func TestSource6ExactAdditionalReferenceContexts(t *testing.T) {
	for _, tc := range []struct {
		name, fixture, subject, target string
		change                         func(*testing.T, *Node)
	}{
		{"flow entry parent", "runtime", "flow", "step", func(_ *testing.T, n *Node) { n.ParentID = new(structuralID("elsewhere")) }},
		{"contains child parent", "runtime", "contains:step", "step", func(_ *testing.T, n *Node) { n.ParentID = new(structuralID("elsewhere")) }},
		{"emit endpoint kind", "events", "emission", "step", func(t *testing.T, n *Node) { n.Attributes["stepKind"] = relationalRaw(t, "return") }},
	} {
		for _, matching := range []bool{true, false} {
			t.Run(tc.name+"/"+map[bool]string{true: "matching", false: "other-provider-only"}[matching], func(t *testing.T) {
				g := structuralTestGraph(t, tc.fixture)
				if !matching {
					tc.change(t, structuralNode(&g, tc.target))
				}
				err := sourceReviewExactContext(t, g, structuralID(tc.subject))
				if matching && err != nil || !matching && err == nil {
					t.Fatalf("exact reference context: matching=%v error=%v", matching, err)
				}
			})
		}
	}
}

func TestSource6BareColumnReferenceDoesNotSelectFacet(t *testing.T) {
	for _, subject := range []string{"fk", "index", "references"} {
		t.Run(subject, func(t *testing.T) {
			g := structuralTestGraph(t, "relational")
			n := structuralNode(&g, "column")
			facets, _, _ := relationalFacetObject(n.Kind, n.Attributes)
			n.Attributes = replaceRelationalFacets(n.Kind, n.Attributes, map[string]jsontext.Value{"orm": facets["sql"]})
			if err := sourceReviewExactContext(t, g, structuralID(subject)); err != nil {
				t.Fatalf("ordinary column reference invented a selected facet: %v", err)
			}
		})
	}
}

func TestSource6RuntimeFacetCurrentness(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "stale selected field"}[stale], func(t *testing.T) {
			g := structuralTestGraph(t, "relational")
			n := structuralNode(&g, "column")
			target := ProviderAssertion{RecordType: "node", RecordID: n.ID, AssertionHash: "hash", Owner: AssertionOwnership{RepositoryID: "repo", ProviderNamespace: "provider"}, Payload: sourceNodePayload(*n), Freshness: AssertionFreshness{Status: "current"}}
			consumer := ProviderAssertion{RecordType: "edge", RecordID: structuralID("access"), Payload: SourceAssertionPayload{RecordType: "edge", Kind: "reads", Attributes: runtimeAttrs(t, map[string]any{"facetKey": "sql"})}, Freshness: AssertionFreshness{Status: "current"}}
			consumer.DependencyClaims = []SourceDependencyBinding{{Site: "/to", Target: sourceAssertionRef(target)}}
			current := populateSourceFields(target, sourceCurrentness(target), false, true, "")
			if stale {
				for i := range current.Fields {
					if current.Fields[i].Property.Kind == "relational_facet" {
						current.Fields[i].Own.Status = "stale"
					}
				}
			}
			graph := &SourceGraphSnapshot{Assertions: []ProviderAssertion{target, consumer}}
			out := sourceDependencyCurrentness(graph, map[string]SourceClaimCurrentness{sourceAssertionKey(target): current, sourceAssertionKey(consumer): sourceCurrentness(consumer)})
			if (out[1].Dependency.Status == "stale") != stale {
				t.Fatalf("selected runtime facet currentness was ignored: %+v", out[1])
			}
		})
	}
}
