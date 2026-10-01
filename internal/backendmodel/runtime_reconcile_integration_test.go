package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"testing"
)

func runtimeInput(p *Project) BeginImportInput {
	in := runtimeProfileInput(firstImportFixture(p), false)
	for i := range in.Inventory {
		if in.Inventory[i].Category == "files" || in.Inventory[i].Category == "endpoints" {
			in.Inventory[i].KnownCount = 1
			in.Inventory[i].Denominator = new(int64(1))
		}
	}
	return in
}

func runtimeCommands(t *testing.T, s *ImportSession) []ImportCommand {
	t.Helper()
	cs := []ImportCommand{}
	node := func(key, kind, parent string, attrs map[string]jsontext.Value) {
		n := &ImportNode{ExternalKey: key, Kind: kind, Name: key, Attributes: attrs, EvidenceKeys: []string{"proof:" + key}}
		if parent != "" {
			n.ParentKey = new(parent)
		}
		cs = append(cs, ImportCommand{Op: "upsert_node", Node: n})
	}
	edge := func(key, kind, from, to string, attrs map[string]jsontext.Value) {
		cs = append(cs, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: key, Kind: kind, FromKey: from, ToKey: to, Attributes: attrs, EvidenceKeys: []string{"proof:" + key}}})
	}
	node("http", "http_operation", "", runtimeAttrs(t, map[string]any{"method": "GET", "path": "/value"}))
	node("handler", "handler", "", runtimeAttrs(t, map[string]any{"language": "go"}))
	node("flow", "flow", "handler", runtimeAttrs(t, map[string]any{"analysisStatus": "complete", "gaps": []string{}, "entryStepKey": "input", "exitStepKeys": []string{"return"}, "exitStatus": "complete"}))
	node("input", "flow_step", "flow", runtimeStepAttrs(t, "input"))
	node("query-step", "flow_step", "flow", runtimeStepAttrs(t, "query"))
	node("return", "flow_step", "flow", runtimeStepAttrs(t, "return"))
	node("query", "query", "handler", runtimeAttrs(t, map[string]any{"analysisStatus": "complete", "gaps": []string{}, "dialect": "sqlite", "nativeDefinition": "SELECT 1;\n-- preserve \"quotes\" and tabs\t\n", "parameters": []any{}, "results": []any{map[string]any{"key": "value", "name": "value", "nativeType": known("int64")}}, "columnScope": "complete"}))
	edge("handles", "handles", "http", "handler", runtimeAttrs(t, map[string]any{}))
	for _, pair := range [][2]string{{"handler", "flow"}, {"flow", "input"}, {"flow", "query-step"}, {"flow", "return"}, {"handler", "query"}} {
		edge("contains:"+pair[1], "contains", pair[0], pair[1], runtimeAttrs(t, map[string]any{}))
	}
	edge("next:input", "next", "input", "query-step", runtimeAttrs(t, map[string]any{}))
	edge("next:query", "next", "query-step", "return", runtimeAttrs(t, map[string]any{}))
	edge("query-call", "calls", "query-step", "query", runtimeAttrs(t, map[string]any{}))
	for _, c := range slices.Clone(cs) {
		typ, key, _ := commandAddress(c)
		f := s.Manifest.Snapshot.Files[0]
		cs = append(cs, ImportCommand{Op: "upsert_evidence", Evidence: &ImportEvidence{ExternalKey: "proof:" + key, SubjectType: typ, SubjectKey: key, Method: "agent", Status: "explicit", Source: EvidenceSource{RepositoryID: s.RepositoryID, SnapshotID: s.SnapshotID, File: f.Path, ContentHash: f.ContentHash, StartLine: new(int64(1)), EndLine: new(int64(10))}}})
	}
	return cs
}

func TestRuntimeGraphSourceAndNativeReferences(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, runtimeInput(p))
	if err != nil {
		t.Fatal(err)
	}
	v, ids := stageRelational(t, r, p, s, runtimeCommands(t, s), "runtime")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "runtime-commit")
	if err != nil {
		t.Fatal(err)
	}
	flow, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids["flow"])
	if err != nil {
		t.Fatal(err)
	}
	if runtimeString(flow.Attributes["entryStepId"]) != ids["input"] || flow.Attributes["entryStepKey"] != nil || flow.Ownership.Profile != "runtime-flow-v1" {
		t.Fatalf("resolved runtime identity/ownership: %+v", flow)
	}
	query, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids["query"])
	if err != nil {
		t.Fatal(err)
	}
	if runtimeString(query.Attributes["nativeDefinition"]) != "SELECT 1;\n-- preserve \"quotes\" and tabs\t\n" {
		t.Fatal("native bytes changed")
	}
}

func TestRuntimeGraphRejectsInvalidStructure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]ImportCommand)
	}{
		{"entry wrong kind", func(cs []ImportCommand) {
			relationalCommand(cs, "flow").Node.Attributes["entryStepKey"] = relationalRaw(t, "handler")
		}},
		{"nonterminal exit", func(cs []ImportCommand) {
			relationalCommand(cs, "flow").Node.Attributes["exitStepKeys"] = relationalRaw(t, []string{"input"})
		}},
		{"missing terminal", func(cs []ImportCommand) {
			relationalCommand(cs, "flow").Node.Attributes["exitStepKeys"] = relationalRaw(t, []string{})
		}},
		{"wrong parent", func(cs []ImportCommand) {
			relationalCommand(cs, "input").Node.ParentKey = new("handler")
			relationalCommand(cs, "contains:input").Edge.FromKey = "handler"
		}},
		{"partial direct step", func(cs []ImportCommand) {
			a := relationalCommand(cs, "input").Node.Attributes
			a["analysisStatus"] = relationalRaw(t, "partial")
			a["gaps"] = relationalRaw(t, []string{"Missing branch"})
		}},
		{"query step wrong target", func(cs []ImportCommand) { relationalCommand(cs, "query-call").Edge.ToKey = "handler" }},
		{"proof without lines", func(cs []ImportCommand) {
			for _, c := range cs {
				if c.Evidence != nil && c.Evidence.SubjectKey == "flow" {
					c.Evidence.Source.StartLine, c.Evidence.Source.EndLine = nil, nil
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			s, err := r.BeginImport(t.Context(), p.ID, runtimeInput(p))
			if err != nil {
				t.Fatal(err)
			}
			cs := runtimeCommands(t, s)
			tc.change(cs)
			v, _ := stageRelational(t, r, p, s, cs, "bad")
			if v.State == "ready" || v.CandidateHash != nil {
				t.Fatal("invalid graph became ready")
			}
		})
	}
}

func runtimeCommitted(t *testing.T) (*Repo, *ImportCommitResult, *ImportSession, map[string]string) {
	t.Helper()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, runtimeInput(p))
	if err != nil {
		t.Fatal(err)
	}
	v, ids := stageRelational(t, r, p, s, runtimeCommands(t, s), "base")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "base-commit")
	if err != nil {
		t.Fatal(err)
	}
	return r, out, s, ids
}

func runtimeRepeat(p *Project, repository string) BeginImportInput {
	in := runtimeInput(p)
	in.Mode = "reconcile"
	in.RepositoryID = new(repository)
	in.GraphScope = &GraphScope{Profile: RuntimeProfile, Status: "complete", Gaps: []string{}}
	in.IdempotencyKey = "runtime-repeat"
	return in
}

func TestRuntimeReconcileStableIdentityAndRetainedProof(t *testing.T) {
	for _, reassert := range []bool{false, true} {
		t.Run(map[bool]string{false: "omission stays stale", true: "reassert stays same identity"}[reassert], func(t *testing.T) {
			r, out, old, ids := runtimeCommitted(t)
			p := &out.Project
			in := runtimeRepeat(p, old.RepositoryID)
			if !reassert {
				in.GraphScope.Status = "partial"
				in.GraphScope.Gaps = []string{"Only handler reobserved"}
				for i := range in.Inventory {
					if in.Inventory[i].Category == "endpoints" {
						in.Inventory[i].Status = "partial"
						in.Inventory[i].KnownCount = 0
						in.Inventory[i].Denominator = nil
						in.Inventory[i].Gaps = []string{"Endpoints not reobserved"}
					}
				}
			}
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			cs := runtimeCommands(t, s)
			if !reassert {
				cs = relationalSelect(cs, "handler", "proof:handler")
			}
			v, newIDs := stageRelational(t, r, p, s, cs, "repeat")
			if v.State != "ready" {
				t.Fatal(v.Diagnostics)
			}
			next, err := commitFixture(t, r, p, s, v, "repeat-commit")
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"flow", "input", "query-step", "return", "query"} {
				n, err := r.Node(t.Context(), p.ID, next.Revision.ID, ids[key])
				if err != nil {
					t.Fatal(err)
				}
				want := "stale"
				if reassert {
					want = "current"
					if newIDs[key] != ids[key] {
						t.Fatal("reimport changed UUID")
					}
				}
				if n.Freshness.Status != want {
					t.Fatalf("%s freshness %+v", key, n.Freshness)
				}
			}
			proof, err := r.Evidence(t.Context(), p.ID, out.Revision.ID, EvidenceQueryInput{SubjectID: ids["query"]})
			if err != nil || len(proof.Items) != 1 || proof.Items[0].Source.SnapshotID != old.SnapshotID {
				t.Fatalf("historical proof changed %+v %v", proof, err)
			}
		})
	}
}

func TestRuntimeIdentityMappedFlowBeforeAllocation(t *testing.T) {
	r, out, old, ids := runtimeCommitted(t)
	p := &out.Project
	s, err := r.BeginImport(t.Context(), p.ID, runtimeRepeat(p, old.RepositoryID))
	if err != nil {
		t.Fatal(err)
	}
	b := sendCommands(t, r, p, s, s.Version, "map", ImportCommand{Op: "map_identity", Identity: &ImportIdentityMap{RecordType: "node", FromExternalKey: "flow", ToExternalKey: "renamed-flow", ExpectedID: ids["flow"], Reason: "Source symbol renamed", EvidenceKeys: []string{"proof:flow"}}})
	s.Version = b.AcceptedVersion
	cs := runtimeCommands(t, s)
	for _, c := range cs {
		if c.Node != nil {
			if c.Node.ExternalKey == "flow" {
				c.Node.ExternalKey = "renamed-flow"
			}
			if c.Node.ParentKey != nil && *c.Node.ParentKey == "flow" {
				c.Node.ParentKey = new("renamed-flow")
			}
		}
		if c.Edge != nil {
			if c.Edge.FromKey == "flow" {
				c.Edge.FromKey = "renamed-flow"
			}
			if c.Edge.ToKey == "flow" {
				c.Edge.ToKey = "renamed-flow"
			}
		}
		if c.Evidence != nil && c.Evidence.SubjectKey == "flow" {
			c.Evidence.SubjectKey = "renamed-flow"
		}
	}
	v, newIDs := stageRelational(t, r, p, s, cs, "mapped")
	if v.State != "ready" || newIDs["renamed-flow"] != ids["flow"] {
		t.Fatalf("mapped candidate %+v %+v", v, newIDs)
	}
	next, err := commitFixture(t, r, p, s, v, "mapped-commit")
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.Node(t.Context(), p.ID, next.Revision.ID, ids["input"])
	if err != nil || n.ParentID == nil || *n.ParentID != ids["flow"] {
		t.Fatalf("mapped nested reference %+v %v", n, err)
	}
}

func TestRuntimeDeletionNestedReferenceAndFullClosure(t *testing.T) {
	for _, key := range []string{"input", "return", "all"} {
		t.Run(key, func(t *testing.T) {
			r, out, old, ids := runtimeCommitted(t)
			p := &out.Project
			s, err := r.BeginImport(t.Context(), p.ID, runtimeRepeat(p, old.RepositoryID))
			if err != nil {
				t.Fatal(err)
			}
			cs := runtimeCommands(t, s)
			deletions := []ImportCommand{}
			for _, c := range cs {
				typ, k, _ := commandAddress(c)
				remove := false
				if key == "all" {
					remove = typ != "evidence" && k != "http" && k != "handler" && k != "handles"
				} else {
					remove = c.Node != nil && k == key || c.Edge != nil && (c.Edge.FromKey == key || c.Edge.ToKey == key)
				}
				if remove {
					deletions = append(deletions, ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: typ, ExternalKey: k, ExpectedID: ids[k], Reason: "Removed from source"}})
				}
			}
			kept := relationalSelect(cs, "http", "handler", "handles", "proof:http", "proof:handler", "proof:handles")
			cs = append(kept, deletions...)
			v, _ := stageRelational(t, r, p, s, cs, "delete")
			if key != "all" {
				if v.State == "ready" || !slices.ContainsFunc(v.Diagnostics, func(d ImportDiagnostic) bool { return d.Code == "backend_unsafe_deletion" }) {
					t.Fatalf("dangling nested reference accepted %+v", v)
				}
				return
			}
			if v.State != "ready" {
				t.Fatal(v.Diagnostics)
			}
			next, err := commitFixture(t, r, p, s, v, "delete-commit")
			if err != nil {
				t.Fatal(err)
			}
			graph, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: next.Revision.ID, RecordType: "nodes"})
			if err != nil || len(graph.Nodes) != 2 {
				t.Fatalf("closure graph %+v %v", graph, err)
			}
			if _, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids["flow"]); err != nil {
				t.Fatal("deletion erased old flow", err)
			}
		})
	}
}

func TestRuntimeReferencesAllNestedModes(t *testing.T) {
	for _, tc := range []struct {
		kind string
		edge bool
		a    map[string]jsontext.Value
		want []string
	}{
		{"flow", false, runtimeAttrs(t, map[string]any{"analysisStatus": "complete", "gaps": []string{}, "entryStepKey": "entry", "exitStepKeys": []string{"exit"}, "exitStatus": "complete"}), []string{"entry", "exit"}},
		{"flow_step", false, func() map[string]jsontext.Value {
			a := runtimeStepAttrs(t, "input")
			a["transactionContext"] = relationalRaw(t, map[string]any{"status": "known", "transactionKey": "tx"})
			return a
		}(), []string{"tx"}},
		{"transaction", false, runtimeAttrs(t, map[string]any{"analysisStatus": "complete", "gaps": []string{}, "datastoreKey": "db", "connectionScope": known("tx"), "isolationLevel": known("serializable"), "boundaryStatus": "complete"}), []string{"db"}},
		{"reads", true, runtimeAttrs(t, map[string]any{"accessMode": "read", "datastoreKey": "db", "facetKey": "sql", "columnScope": "listed"}), []string{"db"}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			ids := map[string]string{}
			for i, k := range tc.want {
				ids[k] = []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}[i]
			}
			resolved, err := resolveRuntimeAttributes(tc.kind, tc.a, tc.edge, func(_, key, _ string) string { return ids[key] })
			if err != nil {
				t.Fatal(err)
			}
			refs, err := runtimeReferences(tc.kind, resolved, tc.edge, true)
			if err != nil || len(refs) != len(tc.want) {
				t.Fatalf("references %+v %v", refs, err)
			}
			for _, key := range tc.want {
				if !sourceActiveReferenceTo(tc.kind, resolved, tc.edge, ids[key]) {
					t.Fatal("nested deletion safety omitted", key)
				}
			}
			if err := validateRuntimeAttributes(tc.kind, resolved, tc.edge, false); err == nil {
				t.Fatal("UUID fields accepted as import shortcut")
			}
			raw, err := json.Marshal(resolved)
			if err != nil || len(raw) == 0 {
				t.Fatal(err)
			}
		})
	}
}
