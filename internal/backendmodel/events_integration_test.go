package backendmodel

import (
	"encoding/json/jsontext"
	"slices"
	"testing"
)

func eventsInput(p *Project) BeginImportInput { return eventsProfileInput(runtimeInput(p), false) }
func eventsCommands(t *testing.T, s *ImportSession) []ImportCommand {
	cs := runtimeCommands(t, s)
	extra := []ImportCommand{}
	node := func(key, kind, parent string, a map[string]jsontext.Value) {
		n := &ImportNode{ExternalKey: key, Kind: kind, Name: key, Attributes: a, EvidenceKeys: []string{"proof:" + key}}
		if parent != "" {
			n.ParentKey = new(parent)
		}
		extra = append(extra, ImportCommand{Op: "upsert_node", Node: n})
	}
	edge := func(key, kind, from, to string, a map[string]jsontext.Value) {
		extra = append(extra, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: key, Kind: kind, FromKey: from, ToKey: to, Attributes: a, EvidenceKeys: []string{"proof:" + key}}})
	}
	node("service", "service", "", runtimeAttrs(t, map[string]any{}))
	for _, kind := range []string{"channel", "message", "consumer", "job"} {
		node(kind, kind, "service", eventNodeAttrs(t, kind))
		edge("contains:"+kind, "contains", "service", kind, runtimeAttrs(t, map[string]any{}))
	}
	node("field", "event_field", "message", eventNodeAttrs(t, "event_field"))
	edge("contains:field", "contains", "message", "field", runtimeAttrs(t, map[string]any{}))
	node("emit", "flow_step", "flow", eventNodeAttrs(t, "flow_step"))
	edge("contains:emit", "contains", "flow", "emit", runtimeAttrs(t, map[string]any{}))
	relationalCommand(cs, "next:input").Edge.ToKey = "emit"
	edge("next:emit", "next", "emit", "query-step", runtimeAttrs(t, map[string]any{}))
	edge("emission", "emits", "emit", "message", runtimeAttrs(t, map[string]any{"channelKey": "channel", "deliveryStatus": "declared"}))
	edge("delivery", "delivered_to", "channel", "consumer", runtimeAttrs(t, map[string]any{"messageKey": "message", "condition": known("always"), "group": known("workers"), "deliveryStatus": "declared"}))
	edge("consumer-handles", "handles", "consumer", "handler", runtimeAttrs(t, map[string]any{}))
	edge("job-handles", "handles", "job", "handler", runtimeAttrs(t, map[string]any{}))
	for _, c := range slices.Clone(extra) {
		typ, key, _ := commandAddress(c)
		f := s.Manifest.Snapshot.Files[0]
		extra = append(extra, ImportCommand{Op: "upsert_evidence", Evidence: &ImportEvidence{ExternalKey: "proof:" + key, SubjectType: typ, SubjectKey: key, Method: "agent", Status: "explicit", Source: EvidenceSource{RepositoryID: s.RepositoryID, SnapshotID: s.SnapshotID, File: f.Path, ContentHash: f.ContentHash, StartLine: new(int64(1)), EndLine: new(int64(10))}}})
	}
	return append(cs, extra...)
}
func eventsCommitted(t *testing.T) (*Repo, *ImportCommitResult, *ImportSession, map[string]string) {
	t.Helper()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, eventsInput(p))
	if err != nil {
		t.Fatal(err)
	}
	v, ids := stageRelational(t, r, p, s, eventsCommands(t, s), "events")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "events-commit")
	if err != nil {
		t.Fatal(err)
	}
	return r, out, s, ids
}
func TestEventsImportCommitAndSavedFlow(t *testing.T) {
	r, out, _, ids := eventsCommitted(t)
	n, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids["emit"])
	if err != nil || n.Ownership.Profile != EventsProfile {
		t.Fatal(n, err)
	}
	for _, kind := range []string{"consumer", "job"} {
		pins, err := resolveSavedReferences(t.Context(), r.db.R, out.Project.ID, BackendReadTarget{RevisionID: out.Revision.ID}, SavedViewState{Flow: &SavedFlowViewState{Kind: "flow", Scope: SavedFlowViewScope{EntrypointID: ids[kind], FlowID: ids["flow"]}}})
		if err != nil || pins.RevisionID != out.Revision.ID {
			t.Fatal(pins, err)
		}
	}
	// Event-specific kind filters work through the existing graph read.
	page, err := r.QueryGraph(t.Context(), out.Project.ID, GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes", Kind: "channel"})
	if err != nil || len(page.Nodes) != 1 {
		t.Fatal(page, err)
	}
}
func TestEventsGraphRejectsWrongReferencesProofAndCompleteRoute(t *testing.T) {
	for _, mode := range []string{"missing channel", "wrong channel kind", "wrong parent", "duplicate selector", "proof without lines", "unknown delivery"} {
		t.Run(mode, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			s, err := r.BeginImport(t.Context(), p.ID, eventsInput(p))
			if err != nil {
				t.Fatal(err)
			}
			cs := eventsCommands(t, s)
			switch mode {
			case "missing channel":
				relationalCommand(cs, "emission").Edge.Attributes["channelKey"] = relationalRaw(t, "absent")
			case "wrong channel kind":
				relationalCommand(cs, "emission").Edge.Attributes["channelKey"] = relationalRaw(t, "message")
			case "wrong parent":
				relationalCommand(cs, "contains:field").Edge.FromKey = "service"
				relationalCommand(cs, "field").Node.ParentKey = new("service")
			case "duplicate selector":
				original := relationalCommand(cs, "field").Node
				cloned := *original
				cloned.ExternalKey = "field2"
				cloned.EvidenceKeys = []string{"proof:field2"}
				cs = append(cs, ImportCommand{Op: "upsert_node", Node: &cloned}, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "contains:field2", Kind: "contains", FromKey: "message", ToKey: "field2", Attributes: runtimeAttrs(t, map[string]any{}), EvidenceKeys: []string{"proof:contains:field2"}}})
				for _, key := range []string{"field2", "contains:field2"} {
					proof := *relationalCommand(cs, "proof:field").Evidence
					proof.ExternalKey = "proof:" + key
					proof.SubjectKey = key
					proof.SubjectType = "node"
					if key == "contains:field2" {
						proof.SubjectType = "edge"
					}
					cs = append(cs, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
				}
			case "proof without lines":
				relationalCommand(cs, "proof:delivery").Evidence.Source.StartLine = nil
				relationalCommand(cs, "proof:delivery").Evidence.Source.EndLine = nil
			case "unknown delivery":
				a := relationalCommand(cs, "delivery").Edge.Attributes
				a["deliveryStatus"] = relationalRaw(t, "unknown")
				a["deliveryReason"] = relationalRaw(t, "Dynamic delivery")
			}
			v, _ := stageRelational(t, r, p, s, cs, "bad")
			if v.State == "ready" || v.CandidateHash != nil {
				t.Fatal("invalid event graph ready", mode)
			}
		})
	}
}
func TestEventsPartialReimportRetainsStaleRoute(t *testing.T) {
	r, out, old, ids := eventsCommitted(t)
	p := &out.Project
	in := eventsProfileInput(runtimeRepeat(p, old.RepositoryID), false)
	in.GraphScope.Status = "partial"
	in.GraphScope.Gaps = []string{"Partial source observation"}
	in.IdempotencyKey = "repeat-events"
	for i := range in.Inventory {
		if in.Inventory[i].Category == "endpoints" {
			in.Inventory[i].Status = "partial"
			in.Inventory[i].KnownCount = 0
			in.Inventory[i].Denominator = nil
			in.Inventory[i].Gaps = []string{"Endpoints not reobserved"}
		}
	}
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	cs := eventsCommands(t, s)
	cs = relationalSelect(cs, "service", "proof:service")
	v, _ := stageRelational(t, r, p, s, cs, "partial")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	next, err := commitFixture(t, r, p, s, v, "partial-commit")
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadRevisionState(t.Context(), r.db.R, p.ID, next.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range state.Edges {
		if e.ID == ids["emission"] || e.ID == ids["delivery"] {
			if e.Freshness == nil || e.Freshness.Status != "stale" {
				t.Fatal("omitted route became current", e)
			}
		}
	}
	before, _ := r.Node(t.Context(), p.ID, out.Revision.ID, ids["channel"])
	after, _ := r.Node(t.Context(), p.ID, next.Revision.ID, ids["channel"])
	if before.Freshness.Status != "current" || after.Freshness.Status != "stale" {
		t.Fatal("pinned source mutated")
	}
}
func TestEventsDeletionRequiresNestedReferenceClosure(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "nested channel survives", true: "explicit closure"}[closed], func(t *testing.T) {
			r, out, old, ids := eventsCommitted(t)
			in := eventsProfileInput(runtimeRepeat(&out.Project, old.RepositoryID), false)
			in.IdempotencyKey = "delete-event-channel"
			s, err := r.BeginImport(t.Context(), out.Project.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			cs := eventsCommands(t, s)
			keys := []string{"channel", "contains:channel", "delivery"}
			if closed {
				keys = append(keys, "emission")
				relationalCommand(cs, "emit").Node.Attributes["stepKind"] = relationalRaw(t, "transform")
			}
			kept := slices.DeleteFunc(cs, func(c ImportCommand) bool {
				_, key, _ := commandAddress(c)
				return slices.Contains(keys, key) || slices.Contains(keys, keyWithoutProof(key))
			})
			for _, key := range keys {
				typ := "edge"
				if key == "channel" {
					typ = "node"
				}
				kept = append(kept, ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: typ, ExternalKey: key, ExpectedID: ids[key], Reason: "Removed configured route"}})
			}
			v, _ := stageRelational(t, r, &out.Project, s, kept, "delete-route")
			if !closed {
				if v.State == "ready" || !slices.ContainsFunc(v.Diagnostics, func(d ImportDiagnostic) bool { return d.Code == "backend_unsafe_deletion" }) {
					t.Fatal("dangling nested channel deleted", v)
				}
				return
			}
			if v.State != "ready" {
				t.Fatal(v.Diagnostics)
			}
			next, err := commitFixture(t, r, &out.Project, s, v, "delete-route-commit")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Node(t.Context(), out.Project.ID, next.Revision.ID, ids["channel"]); err == nil {
				t.Fatal("closure failed to delete channel")
			}
			if _, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids["channel"]); err != nil {
				t.Fatal("old revision changed", err)
			}
		})
	}
}
func keyWithoutProof(key string) string {
	if len(key) > 6 && key[:6] == "proof:" {
		return key[6:]
	}
	return ""
}
