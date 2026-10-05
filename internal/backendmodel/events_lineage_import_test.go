package backendmodel

import (
	"encoding/json/jsontext"
	"slices"
	"testing"
)

func eventsLineageCommands(t *testing.T, s *ImportSession) []ImportCommand {
	t.Helper()
	cs := eventsCommands(t, s)
	relationalCommand(cs, "emit").Node.Attributes["outputs"] = relationalRaw(t, []any{map[string]any{"key": "serialized", "name": "serialized", "nativeType": known("string")}})
	producer := map[string]any{"kind": "event_field", "nodeKey": "field", "endpointKey": "emit", "routeKey": "emission"}
	consumer := map[string]any{"kind": "event_field", "nodeKey": "field", "endpointKey": "consumer", "routeKey": "delivery"}
	local := map[string]any{"kind": "port", "nodeKey": "emit", "collection": "outputs", "portKey": "serialized"}
	for _, x := range []struct {
		key, parent  string
		source, dest any
		transport    any
	}{{"serialize", "emit", local, producer, nil}, {"transport", "consumer", producer, consumer, map[string]any{"emitsEdgeKey": "emission", "deliveryEdgeKey": "delivery"}}} {
		a := map[string]any{"sources": []any{x.source}, "destination": x.dest, "transform": map[string]any{"kind": "copy", "description": "explicit field mapping", "redacted": false}, "analysisStatus": "complete", "gaps": []string{}}
		if x.transport != nil {
			a["transport"] = x.transport
		}
		cs = append(cs, ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: x.key, Kind: "field_mapping", Name: x.key, ParentKey: new(x.parent), Attributes: runtimeAttrs(t, a), EvidenceKeys: []string{"proof:" + x.key}}}, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "contains:" + x.key, Kind: "contains", FromKey: x.parent, ToKey: x.key, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof:contains:" + x.key}}})
		for _, key := range []string{x.key, "contains:" + x.key} {
			p := *relationalCommand(cs, "proof:field").Evidence
			p.ExternalKey = "proof:" + key
			p.SubjectKey = key
			p.SubjectType = "node"
			if key != "serialize" && key != "transport" {
				p.SubjectType = "edge"
			}
			cs = append(cs, ImportCommand{Op: "upsert_evidence", Evidence: &p})
		}
	}
	return cs
}
func TestEventsLineageImportExactRouteResolution(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, eventsInput(p))
	if err != nil {
		t.Fatal(err)
	}
	v, ids := stageRelational(t, r, p, s, eventsLineageCommands(t, s), "contextual")
	if v.State != "ready" {
		t.Fatalf("contextual import blocked: %+v", v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "contextual-commit")
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids["transport"])
	if err != nil {
		t.Fatal(err)
	}
	a, err := decodeLineageMappingForSchema(n.Attributes, EventsSchemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if a.Sources[0].RouteID != ids["emission"] || a.Destination.RouteID != ids["delivery"] || a.Transport.EmitsEdgeID != ids["emission"] || a.Sources[0].EndpointID != ids["emit"] {
		t.Fatalf("edge keys did not resolve to exact edges: %+v", a)
	}
	refs, err := sourceAttributeReferences(n.Kind, n.Attributes, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(refs, func(ref relationalReference) bool { return ref.RecordType == "edge" && ref.ID == ids["delivery"] }) {
		t.Fatal("route is not a typed edge dependency", refs)
	}
}

func TestEventsLineageQueryTransportWitnessAndStaleBoundary(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, eventsInput(p))
	if err != nil {
		t.Fatal(err)
	}
	v, ids := stageRelational(t, r, p, s, eventsLineageCommands(t, s), "contextual-query")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "contextual-query-commit")
	if err != nil {
		t.Fatal(err)
	}
	seed := LineageValueRef{Kind: "port", NodeID: ids["emit"], Collection: "outputs", PortKey: "serialized"}
	page, err := r.QueryLineage(t.Context(), p.ID, LineageQueryInput{RevisionID: out.Revision.ID, Seed: seed, Direction: "forward"})
	if err != nil {
		t.Fatalf("source5 query rejected: %v", err)
	}
	if page.Policy != "field-lineage-traversal-v2" || len(page.Items) != 2 || !slices.Equal(page.Items[1].WitnessMappingIDs, []string{ids["serialize"], ids["transport"]}) {
		t.Fatalf("missing contextual witness: %+v", page)
	}
	state, err := loadRevisionState(t.Context(), r.db.R, p.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range state.Evidence {
		if state.Evidence[i].SubjectID == ids["delivery"] {
			state.Evidence[i].Freshness = &AssertionFreshness{Status: "stale", Reasons: []string{"not_reobserved"}}
		}
	}
	blocked, err := projectLineage(t.Context(), state, LineageQueryInput{RevisionID: out.Revision.ID, Seed: seed, Direction: "forward"})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.Items) != 2 || blocked.Items[1].Expansion != "boundary" || !slices.Contains(blocked.Items[1].Reasons, "stale_evidence") {
		t.Fatalf("stale delivery proof crossed: %+v", blocked)
	}
}

func eventsLineageCommitted(t *testing.T) (*Repo, *ImportCommitResult, *ImportSession, map[string]string) {
	t.Helper()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, eventsInput(p))
	if err != nil {
		t.Fatal(err)
	}
	v, ids := stageRelational(t, r, p, s, eventsLineageCommands(t, s), "lineage")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "lineage-commit")
	if err != nil {
		t.Fatal(err)
	}
	return r, out, s, ids
}
func TestEventsLineageRetainedRouteStaleness(t *testing.T) {
	r, out, old, ids := eventsLineageCommitted(t)
	in := eventsProfileInput(runtimeRepeat(&out.Project, old.RepositoryID), false)
	in.IdempotencyKey = "lineage-repeat"
	in.GraphScope.Status = "partial"
	in.GraphScope.Gaps = []string{"delivery not reobserved"}
	s, err := r.BeginImport(t.Context(), out.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	cs := slices.DeleteFunc(eventsLineageCommands(t, s), func(c ImportCommand) bool {
		_, key, _ := commandAddress(c)
		return key == "delivery" || key == "proof:delivery"
	})
	v, _ := stageRelational(t, r, &out.Project, s, cs, "lineage-repeat")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	next, err := commitFixture(t, r, &out.Project, s, v, "lineage-repeat-commit")
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.Node(t.Context(), out.Project.ID, next.Revision.ID, ids["transport"])
	if err != nil {
		t.Fatal(err)
	}
	if n.Freshness.Status != "stale" || !slices.Contains(n.Freshness.Reasons, "dependency_stale") {
		t.Fatalf("reobserved mapping traverses retained stale edge: %+v", n.Freshness)
	}
	before, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids["transport"])
	if err != nil || before.Freshness.Status != "current" {
		t.Fatal("old pin mutated", before, err)
	}
	page, err := r.QueryLineage(t.Context(), out.Project.ID, LineageQueryInput{RevisionID: next.Revision.ID, Seed: LineageValueRef{Kind: "event_field", NodeID: ids["field"], EndpointID: ids["emit"], RouteID: ids["emission"]}, Direction: "forward"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Expansion != "boundary" {
		t.Fatalf("retained transport expanded: %+v", page)
	}
}
func TestEventsLineageRouteDeletionRequiresMappingClosure(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "referenced route", true: "explicit closure"}[closed], func(t *testing.T) {
			r, out, old, ids := eventsLineageCommitted(t)
			in := eventsProfileInput(runtimeRepeat(&out.Project, old.RepositoryID), false)
			in.IdempotencyKey = "delete-contextual"
			s, err := r.BeginImport(t.Context(), out.Project.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			keys := []string{"delivery"}
			if closed {
				keys = append(keys, "transport", "contains:transport")
			}
			cs := slices.DeleteFunc(eventsLineageCommands(t, s), func(c ImportCommand) bool {
				_, key, _ := commandAddress(c)
				return slices.Contains(keys, key) || slices.Contains(keys, keyWithoutProof(key))
			})
			for _, key := range keys {
				typ := "edge"
				if key == "transport" {
					typ = "node"
				}
				cs = append(cs, ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: typ, ExternalKey: key, ExpectedID: ids[key], Reason: "Removed declared delivery"}})
			}
			v, _ := stageRelational(t, r, &out.Project, s, cs, "delete-contextual")
			if !closed {
				if v.State == "ready" || !slices.ContainsFunc(v.Diagnostics, func(d ImportDiagnostic) bool { return d.Code == "backend_unsafe_deletion" }) {
					t.Fatal("referenced delivery deleted", v)
				}
				return
			}
			if v.State != "ready" {
				t.Fatal(v.Diagnostics)
			}
			next, err := commitFixture(t, r, &out.Project, s, v, "delete-contextual-commit")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Node(t.Context(), out.Project.ID, next.Revision.ID, ids["transport"]); err == nil {
				t.Fatal("mapping closure not deleted")
			}
		})
	}
}
func TestEventsLineageExactTupleRejectsCrossChannelAndWrongEndpoint(t *testing.T) {
	for _, mode := range []string{"consumer source", "route names", "channel mismatch", "foreign producer port"} {
		t.Run(mode, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			s, err := r.BeginImport(t.Context(), p.ID, eventsInput(p))
			if err != nil {
				t.Fatal(err)
			}
			cs := eventsLineageCommands(t, s)
			switch mode {
			case "consumer source":
				a := relationalCommand(cs, "transport").Node.Attributes
				a["sources"] = relationalRaw(t, []any{map[string]any{"kind": "event_field", "nodeKey": "field", "endpointKey": "consumer", "routeKey": "delivery"}})
			case "route names":
				a := relationalCommand(cs, "transport").Node.Attributes
				a["transport"] = relationalRaw(t, map[string]any{"emitsEdgeKey": "emit", "deliveryEdgeKey": "consumer"})
			case "channel mismatch":
				channel := *relationalCommand(cs, "channel").Node
				channel.ExternalKey = "channel2"
				channel.EvidenceKeys = []string{"proof:channel2"}
				contains := *relationalCommand(cs, "contains:channel").Edge
				contains.ExternalKey = "contains:channel2"
				contains.ToKey = "channel2"
				contains.EvidenceKeys = []string{"proof:contains:channel2"}
				cs = append(cs, ImportCommand{Op: "upsert_node", Node: &channel}, ImportCommand{Op: "upsert_edge", Edge: &contains})
				for _, key := range []string{"channel2", "contains:channel2"} {
					proof := *relationalCommand(cs, "proof:channel").Evidence
					proof.ExternalKey = "proof:" + key
					proof.SubjectKey = key
					proof.SubjectType = "node"
					if key != "channel2" {
						proof.SubjectType = "edge"
					}
					cs = append(cs, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
				}
				relationalCommand(cs, "emission").Edge.Attributes["channelKey"] = relationalRaw(t, "channel2")
			case "foreign producer port":
				relationalCommand(cs, "serialize").Node.Attributes["sources"] = relationalRaw(t, []any{map[string]any{"kind": "port", "nodeKey": "input", "collection": "outputs", "portKey": "status"}})
			}
			v, _ := stageRelational(t, r, p, s, cs, "wrong-context")
			if v.State == "ready" {
				t.Fatal("invalid contextual tuple admitted", mode)
			}
		})
	}
}

func TestEventsLineageRetainedTransportUnknownRouteRemainsBoundary(t *testing.T) {
	r, out, old, ids := eventsLineageCommitted(t)
	in := eventsProfileInput(runtimeRepeat(&out.Project, old.RepositoryID), false)
	in.IdempotencyKey = "unknown-route-repeat"
	in.GraphScope.Status = "partial"
	in.GraphScope.Gaps = []string{"transport not reobserved"}
	s, err := r.BeginImport(t.Context(), out.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	cs := slices.DeleteFunc(eventsLineageCommands(t, s), func(c ImportCommand) bool {
		_, key, _ := commandAddress(c)
		return key == "transport" || key == "proof:transport" || key == "contains:transport" || key == "proof:contains:transport"
	})
	channel := relationalCommand(cs, "channel").Node.Attributes
	channel["analysisStatus"] = relationalRaw(t, "partial")
	channel["gaps"] = relationalRaw(t, []string{"delivery declaration became unknown"})
	delivery := relationalCommand(cs, "delivery").Edge.Attributes
	delivery["deliveryStatus"] = relationalRaw(t, "unknown")
	delivery["deliveryReason"] = relationalRaw(t, "dynamic subscription declaration")
	v, _ := stageRelational(t, r, &out.Project, s, cs, "unknown-route")
	if v.State != "ready" {
		t.Fatalf("retained old transport was rewritten or blocked: %+v", v.Diagnostics)
	}
	next, err := commitFixture(t, r, &out.Project, s, v, "unknown-route-commit")
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.QueryLineage(t.Context(), out.Project.ID, LineageQueryInput{RevisionID: next.Revision.ID, Seed: LineageValueRef{Kind: "event_field", NodeID: ids["field"], EndpointID: ids["emit"], RouteID: ids["emission"]}, Direction: "forward"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Expansion != "boundary" || !slices.Contains(page.Items[0].Reasons, "unknown_delivery") {
		t.Fatal("unknown retained transport expanded", page)
	}
}

func eventsLineageAddMapping(t *testing.T, cs []ImportCommand, key, parent string, a map[string]jsontext.Value) []ImportCommand {
	t.Helper()
	cs = append(cs, ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: key, Kind: "field_mapping", Name: key, ParentKey: new(parent), Attributes: a, EvidenceKeys: []string{"proof:" + key}}}, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "contains:" + key, Kind: "contains", FromKey: parent, ToKey: key, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof:contains:" + key}}})
	for _, k := range []string{key, "contains:" + key} {
		proof := *relationalCommand(cs, "proof:field").Evidence
		proof.ExternalKey = "proof:" + k
		proof.SubjectKey = k
		proof.SubjectType = "node"
		if k != key {
			proof.SubjectType = "edge"
		}
		cs = append(cs, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
	}
	return cs
}
func TestEventsLineageSameFieldTwoRoutesAndDeserialization(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, eventsInput(p))
	if err != nil {
		t.Fatal(err)
	}
	cs := eventsLineageCommands(t, s)
	delivery := *relationalCommand(cs, "delivery").Edge
	delivery.ExternalKey = "delivery2"
	delivery.EvidenceKeys = []string{"proof:delivery2"}
	cs = append(cs, ImportCommand{Op: "upsert_edge", Edge: &delivery})
	proof := *relationalCommand(cs, "proof:delivery").Evidence
	proof.ExternalKey = "proof:delivery2"
	proof.SubjectKey = "delivery2"
	cs = append(cs, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
	producer := map[string]any{"kind": "event_field", "nodeKey": "field", "endpointKey": "emit", "routeKey": "emission"}
	consumer2 := map[string]any{"kind": "event_field", "nodeKey": "field", "endpointKey": "consumer", "routeKey": "delivery2"}
	attrs := runtimeAttrs(t, map[string]any{"sources": []any{producer}, "destination": consumer2, "transport": map[string]any{"emitsEdgeKey": "emission", "deliveryEdgeKey": "delivery2"}, "transform": LineageTransform{Kind: "copy", Description: "second configured route"}, "analysisStatus": "complete", "gaps": []string{}})
	cs = eventsLineageAddMapping(t, cs, "transport2", "consumer", attrs)
	relationalCommand(cs, "input").Node.Attributes["inputs"] = relationalRaw(t, []any{map[string]any{"key": "decoded", "name": "decoded", "nativeType": known("string")}})
	attrs = runtimeAttrs(t, map[string]any{"sources": []any{consumer2}, "destination": map[string]any{"kind": "port", "nodeKey": "input", "collection": "inputs", "portKey": "decoded"}, "transform": LineageTransform{Kind: "copy", Description: "explicit deserialize"}, "analysisStatus": "complete", "gaps": []string{}})
	cs = eventsLineageAddMapping(t, cs, "deserialize2", "input", attrs)
	v, ids := stageRelational(t, r, p, s, cs, "two-routes")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "two-routes-commit")
	if err != nil {
		t.Fatal(err)
	}
	first := LineageValueRef{Kind: "event_field", NodeID: ids["field"], EndpointID: ids["consumer"], RouteID: ids["delivery"]}
	second := first
	second.RouteID = ids["delivery2"]
	page, err := r.QueryLineage(t.Context(), p.ID, LineageQueryInput{RevisionID: out.Revision.ID, Seed: second, Direction: "reverse"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Mapping.ID != ids["transport2"] || page.Items[0].Via != second {
		t.Fatalf("route equality bridged distinct addresses: %+v", page)
	}
	fwd, err := r.QueryLineage(t.Context(), p.ID, LineageQueryInput{RevisionID: out.Revision.ID, Seed: first, Direction: "forward"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fwd.Items) != 0 {
		t.Fatal("first route acquired second route deserializer", fwd)
	}
	fwd, err = r.QueryLineage(t.Context(), p.ID, LineageQueryInput{RevisionID: out.Revision.ID, Seed: second, Direction: "forward"})
	if err != nil || len(fwd.Items) != 1 || fwd.Items[0].Mapping.ID != ids["deserialize2"] || fwd.Items[0].Expansion != "expanded" {
		t.Fatal("explicit deserializer unavailable", fwd, err)
	}
}

func eventsLineageFacetProducerCommands(t *testing.T, s *ImportSession, omit bool) []ImportCommand {
	t.Helper()
	cs := eventsLineageTwoFacetCommands(t, s, omit)
	for _, x := range []struct{ key, kind, parent string }{{"event-service", "service", ""}, {"channel", "channel", "event-service"}, {"message", "message", "event-service"}, {"field", "event_field", "message"}, {"emit", "flow_step", "flow"}} {
		a := map[string]jsontext.Value{}
		if x.kind != "service" {
			a = eventNodeAttrs(t, x.kind)
		}
		if x.kind == "flow_step" {
			a["inputs"] = relationalRaw(t, []any{map[string]any{"key": "amount", "name": "amount", "nativeType": known("decimal")}})
		}
		node := &ImportNode{ExternalKey: x.key, Kind: x.kind, Name: x.key, Attributes: a, EvidenceKeys: []string{"proof:" + x.key}}
		if x.parent != "" {
			node.ParentKey = new(x.parent)
		}
		cs = append(cs, ImportCommand{Op: "upsert_node", Node: node})
		if x.parent != "" {
			cs = append(cs, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "contains:" + x.key, Kind: "contains", FromKey: x.parent, ToKey: x.key, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof:contains:" + x.key}}})
		}
	}
	cs = append(cs, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "emission", Kind: "emits", FromKey: "emit", ToKey: "message", Attributes: runtimeAttrs(t, map[string]any{"channelKey": "channel", "deliveryStatus": "declared"}), EvidenceKeys: []string{"proof:emission"}}}, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "next:emit", Kind: "next", FromKey: "input", ToKey: "emit", Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof:next:emit"}}})
	relationalCommand(cs, "flow").Node.Attributes["exitStepKeys"] = relationalRaw(t, []string{"emit"})
	for _, c := range slices.Clone(cs) {
		typ, key, _ := commandAddress(c)
		if typ != "node" && typ != "edge" {
			continue
		}
		if !slices.Contains([]string{"event-service", "channel", "message", "field", "emit", "contains:channel", "contains:message", "contains:field", "contains:emit", "emission", "next:emit"}, key) {
			continue
		}
		p := *relationalCommand(cs, "proof:m04-amount").Evidence
		p.ExternalKey = "proof:" + key
		p.SubjectKey = key
		p.SubjectType = typ
		p.PropertyPath = nil
		cs = append(cs, ImportCommand{Op: "upsert_evidence", Evidence: &p})
	}
	local := map[string]any{"kind": "port", "nodeKey": "emit", "collection": "inputs", "portKey": "amount"}
	for _, x := range []struct {
		key          string
		source, dest any
	}{{"bridge", map[string]any{"kind": "port", "nodeKey": "query", "collection": "results", "portKey": "amount"}, local}, {"serialization", local, map[string]any{"kind": "event_field", "nodeKey": "field", "endpointKey": "emit", "routeKey": "emission"}}} {

		cs = eventsLineageAddMapping(t, cs, x.key, "emit", runtimeAttrs(t, map[string]any{"sources": []any{x.source}, "destination": x.dest, "transform": LineageTransform{Kind: "copy", Description: "explicit copy"}, "analysisStatus": "complete", "gaps": []string{}}))
	}
	return cs
}
func TestEventsLineageSelectedFacetBoundaryBeforeEventWitness(t *testing.T) {
	for _, omit := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "retained selected proof"}[omit], func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			in := eventsProfileInput(lineageOrdersInput(t, p), false)
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			v, ids := stageRelational(t, r, p, s, eventsLineageFacetProducerCommands(t, s, false), "facet-event")
			if v.State != "ready" {
				t.Fatal(v.Diagnostics)
			}
			out, err := commitFixture(t, r, p, s, v, "facet-event-commit")
			if err != nil {
				t.Fatal(err)
			}
			in = eventsProfileInput(lineageOrdersInput(t, &out.Project), false)
			in.Mode = "reconcile"
			in.RepositoryID = new(s.RepositoryID)
			in.GraphScope = &GraphScope{Profile: EventsProfile, Status: "partial", Gaps: []string{"selected facet may be retained"}}
			in.IdempotencyKey = "facet-event-repeat"
			nextSession, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			v, _ = stageRelational(t, r, &out.Project, nextSession, eventsLineageFacetProducerCommands(t, nextSession, omit), "facet-event-repeat")
			if v.State != "ready" {
				t.Fatal(v.Diagnostics)
			}
			next, err := commitFixture(t, r, &out.Project, nextSession, v, "facet-event-repeat-commit")
			if err != nil {
				t.Fatal(err)
			}
			page, err := r.QueryLineage(t.Context(), p.ID, LineageQueryInput{RevisionID: next.Revision.ID, Seed: LineageValueRef{Kind: "column", NodeID: ids["column:orders:amount"], FacetKey: "sql"}, Direction: "forward", MaxDepth: 8, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			if omit {
				if len(page.Items) != 1 || page.Items[0].Mapping.ID != ids["m04-amount"] || page.Items[0].Expansion != "boundary" || !slices.Contains(page.Items[0].Reasons, "stale_evidence") {
					t.Fatal("stale selected facet reached event", page)
				}
				return
			}
			item := slices.IndexFunc(page.Items, func(i LineageItem) bool { return i.Mapping.ID == ids["serialization"] })
			if item < 0 || !slices.Equal(page.Items[item].WitnessMappingIDs, []string{ids["m04-amount"], ids["bridge"], ids["serialization"]}) {
				t.Fatal("explicit current facet-to-event witness missing", page)
			}
		})
	}
}
