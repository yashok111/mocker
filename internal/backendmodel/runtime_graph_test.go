package backendmodel

import (
	"encoding/json/jsontext"
	"testing"
)

func runtimeAdditionalProof(t *testing.T, cs []ImportCommand, c ImportCommand) []ImportCommand {
	t.Helper()
	typ, key, err := commandAddress(c)
	if err != nil {
		t.Fatal(err)
	}
	proof := *relationalCommand(cs, "proof:flow").Evidence
	proof.ExternalKey, proof.SubjectType, proof.SubjectKey = "proof:"+key, typ, key
	if c.Node != nil {
		c.Node.EvidenceKeys = []string{proof.ExternalKey}
	} else {
		c.Edge.EvidenceKeys = []string{proof.ExternalKey}
	}
	return append(cs, c, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
}

func runtimeTransactionCommands(t *testing.T, cs []ImportCommand) []ImportCommand {
	t.Helper()
	cs = runtimeAdditionalProof(t, cs, ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "tx", Kind: "transaction", Name: "Local SQL transaction", ParentKey: new("flow"), Attributes: runtimeAttrs(t, map[string]any{"analysisStatus": "complete", "gaps": []string{}, "datastoreKey": "database:orders", "connectionScope": known("tx"), "isolationLevel": unknown("Driver default is not resolved"), "boundaryStatus": "complete"})}})
	cs = runtimeAdditionalProof(t, cs, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "contains:tx", Kind: "contains", FromKey: "flow", ToKey: "tx", Attributes: map[string]jsontext.Value{}}})
	for _, key := range []string{"input", "query-step", "return"} {
		a := relationalCommand(cs, key).Node.Attributes
		a["transactionContext"] = relationalRaw(t, map[string]any{"status": "known", "transactionKey": "tx"})
	}
	relationalCommand(cs, "input").Node.Attributes["stepKind"] = relationalRaw(t, "transaction_begin")
	relationalCommand(cs, "return").Node.Attributes["stepKind"] = relationalRaw(t, "transaction_commit")
	cs = runtimeAdditionalProof(t, cs, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "tx:begin", Kind: "begins", FromKey: "input", ToKey: "tx", Attributes: map[string]jsontext.Value{}}})
	cs = runtimeAdditionalProof(t, cs, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "tx:commit", Kind: "commits", FromKey: "return", ToKey: "tx", Attributes: map[string]jsontext.Value{}}})
	a := runtimeStepAttrs(t, "transaction_rollback")
	a["transactionContext"] = relationalRaw(t, map[string]any{"status": "known", "transactionKey": "tx"})
	cs = runtimeAdditionalProof(t, cs, ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "rollback", Kind: "flow_step", Name: "Rollback", ParentKey: new("flow"), Attributes: a}})
	for _, e := range []*ImportEdge{{ExternalKey: "contains:rollback", Kind: "contains", FromKey: "flow", ToKey: "rollback", Attributes: map[string]jsontext.Value{}}, {ExternalKey: "tx:rollback", Kind: "rolls_back", FromKey: "rollback", ToKey: "tx", Attributes: map[string]jsontext.Value{}}, {ExternalKey: "query:error", Kind: "error", FromKey: "query-step", ToKey: "rollback", Attributes: runtimeAttrs(t, map[string]any{"label": "query failed", "outcome": "error"})}} {
		cs = runtimeAdditionalProof(t, cs, ImportCommand{Op: "upsert_edge", Edge: e})
	}
	relationalCommand(cs, "flow").Node.Attributes["exitStepKeys"] = relationalRaw(t, []string{"return", "rollback"})
	return cs
}

func TestRuntimeGraphTransactionAndAccessScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		valid  bool
		change func([]ImportCommand)
	}{
		{"exclusive commit and rollback", true, nil},
		{"wrong facet", false, func(cs []ImportCommand) {
			relationalCommand(cs, "query-read").Edge.Attributes["facetKey"] = relationalRaw(t, "missing")
		}},
		{"wrong datastore", false, func(cs []ImportCommand) {
			relationalCommand(cs, "query-read").Edge.Attributes["datastoreKey"] = relationalRaw(t, "handler")
		}},
		{"column cannot unknown", false, func(cs []ImportCommand) {
			a := relationalCommand(cs, "query-read").Edge.Attributes
			a["columnScope"] = relationalRaw(t, "unknown")
			a["scopeReason"] = relationalRaw(t, "Not individually analyzed")
		}},
		{"whole table not complete column scope", false, func(cs []ImportCommand) {
			e := relationalCommand(cs, "query-read").Edge
			e.ToKey = "table:orders"
			e.Attributes["columnScope"] = relationalRaw(t, "unknown")
			e.Attributes["scopeReason"] = relationalRaw(t, "Whole table")
		}},
		{"wrong boundary step", false, func(cs []ImportCommand) {
			relationalCommand(cs, "input").Node.Attributes["stepKind"] = relationalRaw(t, "input")
		}},
		{"wrong boundary context", false, func(cs []ImportCommand) {
			relationalCommand(cs, "input").Node.Attributes["transactionContext"] = relationalRaw(t, map[string]any{"status": "none", "reason": "Not resolved"})
		}},
		{"missing begin inventory", false, func(cs []ImportCommand) {
			e := relationalCommand(cs, "tx:begin").Edge
			e.Kind = "commits"
			relationalCommand(cs, "input").Node.Attributes["stepKind"] = relationalRaw(t, "transaction_commit")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			in := runtimeProfileInput(relationalFixtureInput(t, p, "sqlite", "v1"), false)
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			cs := runtimeTransactionCommands(t, runtimeRelationalCommands(t, p, s, "sqlite"))
			if tc.change != nil {
				tc.change(cs)
			}
			v, _ := stageRelational(t, r, p, s, cs, "scope")
			if (v.State == "ready") != tc.valid {
				t.Fatalf("candidate %+v", v)
			}
		})
	}
}

func TestRuntimeGraphCyclesAndRecursiveCalls(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		t.Run(map[bool]string{false: "control cycle", true: "recursive handler"}[recursive], func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			s, err := r.BeginImport(t.Context(), p.ID, runtimeInput(p))
			if err != nil {
				t.Fatal(err)
			}
			cs := runtimeCommands(t, s)
			if recursive {
				relationalCommand(cs, "query-step").Node.Attributes = runtimeStepAttrs(t, "call")
				relationalCommand(cs, "query-call").Edge.ToKey = "handler"
			} else {
				cs = runtimeAdditionalProof(t, cs, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "cycle", Kind: "next", FromKey: "return", ToKey: "input", Attributes: map[string]jsontext.Value{}}})
				relationalCommand(cs, "flow").Node.Attributes["exitStepKeys"] = relationalRaw(t, []string{})
			}
			v, _ := stageRelational(t, r, p, s, cs, "cycle")
			if v.State != "ready" {
				t.Fatal(v.Diagnostics)
			}
		})
	}
}

func TestRuntimeGraphCrossFlowAndUnresolvedDispatch(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{false: "cross flow", true: "partial dispatch"}[valid], func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			s, err := r.BeginImport(t.Context(), p.ID, runtimeInput(p))
			if err != nil {
				t.Fatal(err)
			}
			cs := runtimeCommands(t, s)
			if valid {
				a := runtimeStepAttrs(t, "call")
				a["analysisStatus"] = relationalRaw(t, "partial")
				a["gaps"] = relationalRaw(t, []string{"Dynamic dispatch"})
				a["dispatchStatus"] = relationalRaw(t, "partial")
				a["dispatchReason"] = relationalRaw(t, "Runtime callback not statically resolved")
				relationalCommand(cs, "query-step").Node.Attributes = a
				f := relationalCommand(cs, "flow").Node.Attributes
				f["analysisStatus"] = relationalRaw(t, "partial")
				f["gaps"] = relationalRaw(t, []string{"Dynamic call remainder"})
				cs = runtimeAdditionalProof(t, cs, ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "unresolved", Kind: "unresolved_target", Name: "Dynamic callback", Attributes: runtimeAttrs(t, map[string]any{"expectedKind": "handler", "reason": "Dynamic callback", "searchScope": "Imported repository Go symbols"})}})
				cs = runtimeAdditionalProof(t, cs, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "call:remainder", Kind: "calls", FromKey: "query-step", ToKey: "unresolved", Attributes: map[string]jsontext.Value{}}})
			} else {
				cs = runtimeAdditionalProof(t, cs, ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "handler2", Kind: "handler", Name: "Other handler", Attributes: map[string]jsontext.Value{}}})
				cs = runtimeAdditionalProof(t, cs, ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "flow2", Kind: "flow", Name: "Other flow", ParentKey: new("handler2"), Attributes: runtimeAttrs(t, map[string]any{"analysisStatus": "complete", "gaps": []string{}, "entryStepKey": "return2", "exitStepKeys": []string{"return2"}, "exitStatus": "complete"})}})
				cs = runtimeAdditionalProof(t, cs, ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "return2", Kind: "flow_step", Name: "Other exit", ParentKey: new("flow2"), Attributes: runtimeStepAttrs(t, "return")}})
				for _, pair := range [][2]string{{"handler2", "flow2"}, {"flow2", "return2"}} {
					cs = runtimeAdditionalProof(t, cs, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "contains:" + pair[1], Kind: "contains", FromKey: pair[0], ToKey: pair[1], Attributes: map[string]jsontext.Value{}}})
				}
				relationalCommand(cs, "next:query").Edge.ToKey = "return2"
			}
			v, _ := stageRelational(t, r, p, s, cs, "dispatch")
			if (v.State == "ready") != valid {
				t.Fatal(v.Diagnostics)
			}
		})
	}
}
