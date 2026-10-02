package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"testing"
)

func lineageCommands(t *testing.T, s *ImportSession) []ImportCommand {
	cs := runtimeCommands(t, s)
	relationalCommand(cs, "input").Node.Attributes["inputs"] = relationalRaw(t, []any{map[string]any{"key": "opaque/~0", "name": "имя", "nativeType": known("string")}})
	api := lineageAPIAttrs(t)
	mapping := lineageMappingAttrs(t)
	mapping["sources"] = jsontext.Value(`[{"kind":"api_field","nodeKey":"request"}]`)
	mapping["destination"] = jsontext.Value(`{"kind":"port","nodeKey":"input","collection":"inputs","portKey":"opaque/~0"}`)
	for _, n := range []*ImportNode{{ExternalKey: "request", Kind: "api_field", Name: "request", ParentKey: new("http"), Attributes: api, EvidenceKeys: []string{"proof:request"}}, {ExternalKey: "mapping", Kind: "field_mapping", Name: "mapping", ParentKey: new("input"), Attributes: mapping, EvidenceKeys: []string{"proof:mapping"}}} {
		cs = append(cs, ImportCommand{Op: "upsert_node", Node: n}, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "contains:" + n.ExternalKey, Kind: "contains", FromKey: *n.ParentKey, ToKey: n.ExternalKey, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof:contains:" + n.ExternalKey}}})
		for _, typ := range []string{"node", "edge"} {
			key := n.ExternalKey
			if typ == "edge" {
				key = "contains:" + key
			}
			proof := *relationalCommand(cs, "proof:http").Evidence
			proof.ExternalKey = "proof:" + key
			proof.SubjectType = typ
			proof.SubjectKey = key
			cs = append(cs, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
		}
	}
	return cs
}
func lineageCommitted(t *testing.T) (*Repo, *ImportCommitResult, *ImportSession, map[string]string) {
	t.Helper()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, lineageProfileInput(runtimeInput(p), false))
	if err != nil {
		t.Fatal(err)
	}
	v, ids := stageRelational(t, r, p, s, lineageCommands(t, s), "lineage")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "commit")
	if err != nil {
		t.Fatal(err)
	}
	return r, out, s, ids
}
func TestLineageNestedResolutionAndOwnership(t *testing.T) {
	r, out, _, ids := lineageCommitted(t)
	n, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids["mapping"])
	if err != nil {
		t.Fatal(err)
	}
	a, err := decodeLineageMapping(n.Attributes)
	if err != nil {
		t.Fatal(err)
	}
	if a.Sources[0].NodeID != ids["request"] || a.Destination.NodeID != ids["input"] || a.Destination.PortKey != "opaque/~0" || a.Destination.Collection != "inputs" {
		t.Fatalf("resolved attrs %+v", a)
	}
	for key, want := range map[string]string{"mapping": LineageProfile, "request": LineageProfile, "input": RuntimeProfile, "http": GraphProfile} {
		n, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids[key])
		if err != nil || n.Ownership.Profile != want {
			t.Fatalf("%s ownership %+v %v", key, n, err)
		}
	}
	field, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids["request"])
	if err != nil {
		t.Fatal(err)
	}
	var fieldAttrs APIFieldAttributes
	if err := json.Unmarshal(relationalRaw(t, field.Attributes), &fieldAttrs); err != nil {
		t.Fatal(err)
	}
	if fieldAttrs.Selector.Path[0].Property != "имя/~\"" {
		t.Fatal("UTF-8/escaped property identity changed")
	}
	edges, err := r.QueryGraph(t.Context(), out.Project.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "edges", Kind: "contains"})
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range edges.Edges {
		if edge.To == ids["mapping"] || edge.To == ids["request"] {
			if edge.Ownership.Profile != LineageProfile {
				t.Fatal("lineage contains ownership drifted")
			}
			proofs, err := r.Evidence(t.Context(), out.Project.ID, out.Revision.ID, EvidenceQueryInput{SubjectID: edge.ID})
			if err != nil || len(proofs.Items) != 1 || proofs.Items[0].Ownership.Profile != LineageProfile {
				t.Fatalf("lineage evidence ownership %+v %v", proofs, err)
			}
		}
	}
	refs, err := sourceAttributeReferences("field_mapping", n.Attributes, false, true)
	if err != nil || len(refs) != 2 || refs[0].ID != ids["request"] {
		t.Fatalf("nested references %+v %v", refs, err)
	}
}
func TestLineageGraphRejectsInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]ImportCommand)
	}{
		{"missing port", func(cs []ImportCommand) {
			relationalCommand(cs, "input").Node.Attributes["inputs"] = jsontext.Value(`[]`)
		}},
		{"wrong collection", func(cs []ImportCommand) {
			relationalCommand(cs, "mapping").Node.Attributes["destination"] = jsontext.Value(`{"kind":"port","nodeKey":"input","collection":"results","portKey":"opaque/~0"}`)
		}},
		{"wrong reference kind", func(cs []ImportCommand) {
			relationalCommand(cs, "mapping").Node.Attributes["sources"] = jsontext.Value(`[{"kind":"api_field","nodeKey":"http"}]`)
		}},
		{"wrong mapping parent", func(cs []ImportCommand) {
			relationalCommand(cs, "mapping").Node.ParentKey = new("flow")
			relationalCommand(cs, "contains:mapping").Edge.FromKey = "flow"
		}},
		{"wrong field parent", func(cs []ImportCommand) {
			relationalCommand(cs, "request").Node.ParentKey = new("handler")
			relationalCommand(cs, "contains:request").Edge.FromKey = "handler"
		}},
		{"missing bounded proof", func(cs []ImportCommand) {
			relationalCommand(cs, "proof:request").Evidence.Source.StartLine = nil
			relationalCommand(cs, "proof:request").Evidence.Source.EndLine = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			s, err := r.BeginImport(t.Context(), p.ID, lineageProfileInput(runtimeInput(p), false))
			if err != nil {
				t.Fatal(err)
			}
			cs := lineageCommands(t, s)
			tc.change(cs)
			v, _ := stageRelational(t, r, p, s, cs, "invalid")
			if v.State == "ready" {
				t.Fatal("invalid lineage accepted")
			}
		})
	}
}
func TestLineageReconcilePortRemovalAndStaleness(t *testing.T) {
	for _, mode := range []string{"remove port", "rename port", "retained mapping", "refresh mapping", "whole chain"} {
		t.Run(mode, func(t *testing.T) {
			r, out, old, ids := lineageCommitted(t)
			p := &out.Project
			before, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids["mapping"])
			if err != nil {
				t.Fatal(err)
			}
			bytesBefore, _ := canonicalJSON(before)
			in := lineageProfileInput(runtimeRepeat(p, old.RepositoryID), false)
			in.GraphScope.Status = "partial"
			in.GraphScope.Gaps = []string{"Partial source"}
			for i := range in.Inventory {
				if in.Inventory[i].Category == "endpoints" {
					in.Inventory[i].Status = "partial"
					in.Inventory[i].Denominator = nil
					in.Inventory[i].KnownCount = 0
					in.Inventory[i].Gaps = []string{"Partial endpoints"}
				}
			}
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			cs := lineageCommands(t, s)
			switch mode {
			case "remove port":
				cs = relationalSelect(cs, "input", "proof:input")
				relationalCommand(cs, "input").Node.Attributes["inputs"] = jsontext.Value(`[]`)
			case "rename port":
				cs = relationalSelect(cs, "input", "proof:input")
				relationalCommand(cs, "input").Node.Attributes["inputs"] = jsontext.Value(`[{"key":"renamed","name":"имя","nativeType":{"status":"known","value":"string"}}]`)
			case "retained mapping":
				cs = relationalSelect(cs, "handler", "proof:handler")
			case "refresh mapping":
				cs = relationalSelect(cs, "mapping", "proof:mapping")
			}
			v, _ := stageRelational(t, r, p, s, cs, "repeat")
			if mode == "remove port" || mode == "rename port" {
				if v.State == "ready" {
					t.Fatal("dangling retained value accepted")
				}
				return
			}
			if v.State != "ready" {
				t.Fatal(v.Diagnostics)
			}
			next, err := commitFixture(t, r, p, s, v, "repeat-commit")
			if err != nil {
				t.Fatal(err)
			}
			n, err := r.Node(t.Context(), p.ID, next.Revision.ID, ids["mapping"])
			if err != nil {
				t.Fatal(err)
			}
			want := "stale"
			if mode == "whole chain" {
				want = "current"
			}
			if n.Freshness.Status != want {
				t.Fatalf("mapping freshness %+v", n.Freshness)
			}
			historical, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids["mapping"])
			if err != nil {
				t.Fatal(err)
			}
			after, _ := canonicalJSON(historical)
			if !slices.Equal(bytesBefore, after) {
				t.Fatal("history rewritten")
			}
		})
	}
}
