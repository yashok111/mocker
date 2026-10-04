package backendmodel

import (
	"encoding/json/jsontext"
	"testing"
)

func runtimeSource6Commands(t *testing.T, s *ImportSession, boundedEdges bool) []ImportCommand {
	t.Helper()
	graph := structuralTestGraph(t, "runtime")
	commands := make([]ImportCommand, 0, 2*(len(graph.Nodes)+len(graph.Edges)))
	proof := func(typ, key string, bounded bool) ImportCommand {
		e := *fixtureCommands(s)[1].Evidence
		e.ExternalKey = "proof:" + key
		e.SubjectType = typ
		e.SubjectKey = key
		if bounded {
			e.Source.StartLine = new(int64(1))
			e.Source.EndLine = new(int64(3))
		}
		return ImportCommand{Op: "upsert_evidence", Evidence: &e}
	}
	for _, n := range graph.Nodes {
		attrs := n.Attributes
		if n.Kind == "flow" {
			attrs = map[string]jsontext.Value{}
			for k, v := range n.Attributes {
				attrs[k] = v
			}
			attrs["entryStepRef"] = relationalRaw(t, ImportRecordRef{LocalKey: structuralID("step")})
			attrs["exitStepRefs"] = relationalRaw(t, []ImportRecordRef{{LocalKey: structuralID("step")}})
			delete(attrs, "entryStepId")
			delete(attrs, "exitStepIds")
		}
		node := &ImportNode{ExternalKey: n.ID, Kind: n.Kind, Name: n.Name, Attributes: attrs, EvidenceKeys: []string{"proof:" + n.ID}}
		if n.ParentID != nil {
			node.ParentRef = &ImportRecordRef{LocalKey: *n.ParentID}
		}
		commands = append(commands, ImportCommand{Op: "upsert_node", Node: node}, proof("node", n.ID, true))
	}
	for _, e := range graph.Edges {
		commands = append(commands, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: e.ID, Kind: e.Kind, FromRef: &ImportRecordRef{LocalKey: e.From}, ToRef: &ImportRecordRef{LocalKey: e.To}, Attributes: e.Attributes, EvidenceKeys: []string{"proof:" + e.ID}}}, proof("edge", e.ID, boundedEdges))
	}
	return commands
}

func TestSource6RuntimeRelationsRequireBoundedProof(t *testing.T) {
	for _, bounded := range []bool{false, true} {
		name := "missing"
		if bounded {
			name = "bounded"
		}
		t.Run(name, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, name)
			s, err := r.BeginImport(t.Context(), p.ID, source6Input(t, p))
			if err != nil {
				t.Fatal(err)
			}
			b := sendCommands(t, r, p, s, 1, "runtime", runtimeSource6Commands(t, s, bounded)...)
			preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
			if bounded {
				if err != nil || preview.State != "ready" {
					t.Fatalf("bounded graph=%+v err=%v", preview, err)
				}
			} else if err == nil && preview.State == "ready" {
				t.Fatal("runtime contains relation bypassed physical proof bounds as foundation claim")
			}
		})
	}
}

func TestSource6ReferencesBindFinalCandidateAndExactBase(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "dependency")
	s, err := r.BeginImport(t.Context(), p.ID, source6Input(t, p))
	if err != nil {
		t.Fatal(err)
	}
	b := sendCommands(t, r, p, s, 1, "runtime", runtimeSource6Commands(t, s, true)...)
	first := commitStaged(t, r, p, s, b.AcceptedVersion, "base")
	p = &first.Project
	old, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var target BaseAssertionRef
	for _, a := range old.Assertions {
		if a.Payload.Kind == "flow_step" {
			target = sourceAssertionRef(a)
		}
	}
	in := source6Input(t, p)
	in.SourceScope = &SourceScope{Kind: "reconcile", RepositoryID: s.RepositoryID, ProviderNamespace: s.Manifest.Provider.Namespace}
	in.IdempotencyKey = "reconcile"
	next, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands := runtimeSource6Commands(t, next, true)
	for i := range commands {
		if n := commands[i].Node; n != nil {
			switch n.Kind {
			case "flow_step":
				n.Name = "changed step"
			case "flow":
				n.Attributes["entryStepRef"] = relationalRaw(t, ImportRecordRef{Base: &target})
			}
		}
	}
	b = sendCommands(t, r, p, next, 1, "changed", commands...)
	out := commitStaged(t, r, p, next, b.AcceptedVersion, "changed")
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	var flow, step ProviderAssertion
	for _, a := range graph.Assertions {
		if a.Payload.Kind == "flow" {
			flow = a
		}
		if a.Payload.Kind == "flow_step" {
			step = a
		}
	}
	if step.AssertionHash == target.AssertionHash {
		t.Fatal("changed own assertion kept old intrinsic hash")
	}
	for _, dependency := range flow.DependencyClaims {
		switch dependency.Site {
		case "/attributes/entryStepId":
			if dependency.Basis != "base" || dependency.BaseRevisionID != first.Revision.ID || dependency.Target.AssertionHash != target.AssertionHash {
				t.Fatal("explicit base pin silently advanced")
			}
		case "/attributes/exitStepIds/0":
			if dependency.Basis != "candidate" || dependency.BaseRevisionID != "" || dependency.Target.AssertionHash != step.AssertionHash {
				t.Fatal("local ref did not bind final candidate")
			}
		}
	}
	found := false
	for _, f := range graph.Currentness {
		if f.RecordID == flow.RecordID && f.Dependency.Status == "stale" {
			found = true
		}
	}
	if !found {
		t.Fatal("changed exact base dependency was reported current")
	}
}

func TestSource6UnselectedFacetKeepsPriorCurrentness(t *testing.T) {
	a := ProviderAssertion{RecordType: "node", RecordID: sourceContractNode, Owner: AssertionOwnership{RepositoryID: sourceContractRepo, ProviderNamespace: "a"}, Payload: SourceAssertionPayload{RecordType: "node", Kind: "column", Name: "id", Attributes: runtimeAttrs(t, map[string]any{"facets": map[string]any{"sql": map[string]any{"nativeType": known("integer"), "sourceSnapshotId": sourceContractSnapshot, "freshness": AssertionFreshness{Status: "current", ConfirmedSnapshotID: sourceContractSnapshot, Reasons: []string{}}}}})}, Freshness: AssertionFreshness{Status: "current", ConfirmedSnapshotID: sourceContractOther, Reasons: []string{}}}
	f := sourceCurrentness(a)
	property := TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql", Group: "nativeType"}
	f.Fields = []TypedFieldCurrentness{{Property: property, Own: AssertionFreshness{Status: "stale", ConfirmedSnapshotID: sourceContractSnapshot, Reasons: []string{"not_reobserved"}}, Dependency: AssertionFreshness{Status: "current", Reasons: []string{}}}}
	next := populateSourceFields(a, f, false, false, sourceContractOther)
	for _, field := range next.Fields {
		if field.Property == property {
			if field.Own.Status != "stale" || field.Own.ConfirmedSnapshotID != sourceContractSnapshot {
				t.Fatalf("unselected prior facet refreshed: %+v", field)
			}
			return
		}
	}
	t.Fatal("selected facet projection lost")
}

func TestSource6UnresolvedCoveragePartial(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "unresolved")
	s, err := r.BeginImport(t.Context(), p.ID, source6Input(t, p))
	if err != nil {
		t.Fatal(err)
	}
	b := sendCommands(t, r, p, s, 1, "unresolved", ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "unknown", Kind: "unresolved_target", Name: "unknown", Attributes: runtimeAttrs(t, map[string]any{"expectedKind": "handler", "reason": "missing body", "searchScope": "fixture"}), EvidenceKeys: []string{}}})
	out := commitStaged(t, r, p, s, b.AcceptedVersion, "unresolved")
	if out.Revision.Coverage.Status != "partial" {
		t.Fatal("unresolved source target was reported complete")
	}
}
