package designscenario

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestAnalyzeEventMapProjectsConsumerSpecificRoutes(t *testing.T) {
	t.Parallel()
	doc := Document{FormatVersion: 3, Participants: []Participant{{ID: "orders", Name: "Orders", Kind: "service"}, {ID: "billing", Name: "Billing", Kind: "service"}, {ID: "notice", Name: "Notice", Kind: "service"}}, EventModel: &EventModel{
		Servers:   []EventServer{{ID: "broker", Name: "Kafka"}},
		Channels:  []EventChannel{{ID: "primary", Name: "Orders", Address: "orders", ServerIDs: []string{"broker"}, MessageIDs: []string{"created"}}, {ID: "retry", Name: "Retry", Address: "orders.retry", ServerIDs: []string{"broker"}, MessageIDs: []string{"created"}}, {ID: "dlq", Name: "DLQ", Address: "orders.dlq", ServerIDs: []string{"broker"}, MessageIDs: []string{"created"}}},
		Messages:  []EventMessage{{ID: "created", Name: "Created", PayloadSchemaID: "payload", KeySchemaID: "key", HeadersSchemaID: "headers"}},
		Schemas:   []EventSchema{{ID: "payload", Name: "Payload", SchemaJSON: "{}"}, {ID: "key", Name: "Key", SchemaJSON: "{}"}, {ID: "headers", Name: "Headers", SchemaJSON: "{}"}},
		Contracts: []EventContract{{ID: "orders-contract", ParticipantID: "orders", Operations: []EventOperation{{ID: "produce", Name: "Produce", Action: "send", ChannelID: "primary", MessageID: "created"}}}, {ID: "billing-contract", ParticipantID: "billing", Operations: []EventOperation{{ID: "consume", Name: "Consume", Action: "receive", ChannelID: "primary", MessageID: "created", Kafka: &EventOperationKafka{GroupID: "billing-group"}, FailureRoutes: &EventFailureRoutes{RetryChannelID: "retry", DeadLetterChannelID: "dlq"}}}}, {ID: "notice-contract", ParticipantID: "notice", Operations: []EventOperation{{ID: "consume", Name: "Consume", Action: "receive", ChannelID: "primary", MessageID: "created", Kafka: &EventOperationKafka{GroupID: "notice-group"}}}}},
	}}
	a, err := AnalyzeEventMap(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Complete || a.Coverage.NodesReturned != len(a.Nodes) || a.Coverage.EdgesReturned != len(a.Edges) {
		t.Fatalf("coverage: %+v", a.Coverage)
	}
	count := func(kind string) int {
		return len(slices.DeleteFunc(slices.Clone(a.Edges), func(e EventMapEdge) bool { return e.Kind != kind }))
	}
	if count("send") != 1 || count("receive") != 2 || count("retry") != 1 || count("dead_letter") != 1 || count("payload") != 1 || count("key") != 1 || count("headers") != 1 {
		t.Fatalf("wrong topology: %+v", a.Edges)
	}
	for _, edge := range a.Edges {
		if edge.Kind == "retry" && edge.Source != "operation:billing-contract:consume" {
			t.Fatalf("retry belonged to wrong consumer: %+v", edge)
		}
	}
}

func TestAnalyzeEventMapNodeAndOutputLimits(t *testing.T) {
	t.Parallel()
	doc := Document{EventModel: &EventModel{Servers: make([]EventServer, MaxEventMapNodes+1)}}
	for i := range doc.EventModel.Servers {
		doc.EventModel.Servers[i] = EventServer{ID: fmt.Sprintf("s%05d", i), Name: fmt.Sprintf("%05d", i)}
	}
	a, err := AnalyzeEventMap(context.Background(), doc)
	if err != nil || a.Complete || len(a.Nodes) != MaxEventMapNodes || !slices.Contains(a.Coverage.TruncatedReasons, "nodes") {
		t.Fatalf("node cap: count=%d coverage=%+v err=%v", len(a.Nodes), a.Coverage, err)
	}
	doc.EventModel.Servers = doc.EventModel.Servers[:5000]
	for i := range doc.EventModel.Servers {
		doc.EventModel.Servers[i].Name = fmt.Sprintf("s%05d-%s", i, strings.Repeat("x", 900))
	}
	b, err := AnalyzeEventMap(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(b)
	if len(raw) > MaxEventMapBytes || b.Complete || !slices.Contains(b.Coverage.TruncatedReasons, "output") {
		t.Fatalf("output cap: bytes=%d coverage=%+v", len(raw), b.Coverage)
	}
}

func TestEventMapReadsSavedRevision(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	doc := validDocument("Map")
	doc.FormatVersion = 3
	doc.EventModel = &EventModel{Servers: []EventServer{}, Channels: []EventChannel{}, Messages: []EventMessage{}, Schemas: []EventSchema{}, Contracts: []EventContract{}}
	insertImpactScenario(t, repo, 1, rawImpactDocument(t, doc))
	report, err := repo.EventMap(t.Context(), 1, 0)
	if err != nil || report.ScenarioID != 1 || report.Version != 1 || report.RevisionID != 1 || report.Proposed || report.Nodes == nil || report.Edges == nil || report.Diagnostics == nil {
		t.Fatalf("saved map: %+v %v", report, err)
	}
	historical, err := repo.EventMap(t.Context(), 1, 1)
	if err != nil || historical.RevisionID != report.RevisionID {
		t.Fatalf("historical: %+v %v", historical, err)
	}
	_, err = repo.EventMap(t.Context(), 1, 2)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign revision: %v", err)
	}
}

func TestAnalyzeEventMapResolvesEmbeddedAliasesAndTransitions(t *testing.T) {
	t.Parallel()
	api := `{"openapi":"3.1.0","paths":{"/orders":{"$ref":"#/components/pathItems/shared","x-mocker-canvas-operation-ids":{"get":"order-key"}}},"components":{"pathItems":{"shared":{"get":{"responses":{"200":{"description":"OK"}}}}}},"x-mocker-state-diagrams":{"formatVersion":1,"diagrams":[{"id":"order","name":"Order","initialStateId":"new","states":[{"id":"new","name":"New","x":0,"y":0,"terminal":false},{"id":"done","name":"Done","x":1,"y":1,"terminal":true}],"transitions":[{"id":"created","name":"Created","from":"new","to":"done","binding":{"method":"get","path":"/orders"},"patchJSON":"{}","responseStatus":200}]}]}}`
	doc := Document{FormatVersion: 3, Participants: []Participant{{ID: "service", Name: "Service", Kind: "service"}}, Contracts: []Contract{{ID: "http", Name: "HTTP", Mode: "linked", Source: &ContractSource{DesignID: 7, RevisionID: 11}, Document: jsonx.RawMessage(api)}}, EventModel: &EventModel{Servers: []EventServer{}, Channels: []EventChannel{{ID: "topic", Address: "orders", ServerIDs: []string{}, MessageIDs: []string{"msg"}}}, Messages: []EventMessage{{ID: "msg", Name: "Event"}}, Schemas: []EventSchema{}, Contracts: []EventContract{{ID: "events", ParticipantID: "service", Operations: []EventOperation{{ID: "publish", Action: "send", ChannelID: "topic", MessageID: "msg", APILinks: []EventAPILink{{ContractID: "http", OperationKey: "order-key"}, {ContractID: "http", OperationKey: "missing"}}, StateLinks: []EventStateLink{{ContractID: "http", DiagramID: "order", TransitionID: "created"}, {ContractID: "http", DiagramID: "order", TransitionID: "missing"}}}}}}}}
	a, err := AnalyzeEventMap(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if a.Complete {
		t.Fatal("unresolved links should make map incomplete")
	}
	foundAPI, foundState := false, false
	for _, n := range a.Nodes {
		switch n.ID {
		case "api_operation:http:order-key":
			foundAPI = n.Locator.Pointer == "/contracts/0/document/components/pathItems/shared/get" && n.Locator.Path == "/orders" && n.Locator.PinnedRevisionID == 11
		case "state_transition:http:order:created":
			foundState = n.Locator.Pointer == "/contracts/0/document/x-mocker-state-diagrams/diagrams/0/transitions/0" && n.Locator.Method == "get" //nolint:usestdlibvars // lowercase OpenAPI path-item key; http.MethodGet is "GET"
		}
	}
	if !foundAPI || !foundState {
		raw, _ := json.Marshal(a.Nodes)
		t.Fatalf("link nodes: %s", raw)
	}
	for _, e := range a.Edges {
		if e.Kind == "api_link" && e.Target == "api_operation:http:missing" {
			t.Fatal("dangling API edge")
		}
		if e.Kind == "state_link" && e.Target == "state_transition:http:order:missing" {
			t.Fatal("dangling state edge")
		}
	}
	if !slices.ContainsFunc(a.Diagnostics, func(d EventMapDiagnostic) bool { return d.Code == "event_api_operation_unresolved" }) || !slices.ContainsFunc(a.Diagnostics, func(d EventMapDiagnostic) bool { return d.Code == "event_state_transition_missing" }) {
		t.Fatalf("missing diagnostics: %+v", a.Diagnostics)
	}
}

func TestAnalyzeEventMapCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := AnalyzeEventMap(ctx, Document{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestEventMapIDsDoNotCollideAcrossOpaqueTuples(t *testing.T) {
	if a, b := eventMapID("api_operation", "a:b", "c"), eventMapID("api_operation", "a", "b:c"); a == b {
		t.Fatalf("opaque tuple collision: %q", a)
	}
	if a, b := eventMapID("api_operation", "a%b", "c"), eventMapID("api_operation", "a%25b", "c"); a == b {
		t.Fatalf("percent escape collision: %q", a)
	}
}

func TestAnalyzeEventMapMissingMessageIsIncompleteWithoutDanglingEdge(t *testing.T) {
	doc := Document{EventModel: &EventModel{Servers: []EventServer{}, Channels: []EventChannel{{ID: "topic", Address: "topic", MessageIDs: []string{"missing"}}}, Messages: []EventMessage{}, Schemas: []EventSchema{}, Contracts: []EventContract{}}}
	a, err := AnalyzeEventMap(context.Background(), doc)
	if err != nil || a.Complete {
		t.Fatalf("missing message complete: %+v %v", a.Coverage, err)
	}
	if !slices.ContainsFunc(a.Diagnostics, func(d EventMapDiagnostic) bool { return d.Code == "event_message_missing" }) {
		t.Fatalf("no missing-message diagnostic: %+v", a.Diagnostics)
	}
	for _, e := range a.Edges {
		if e.Kind == "channel_message" {
			t.Fatalf("dangling edge: %+v", e)
		}
	}
}

func TestAnalyzeEventMapIncompleteOperationsDoNotInventReceiveEdges(t *testing.T) {
	doc := Document{Participants: []Participant{{ID: "service", Name: "Service"}}, EventModel: &EventModel{Servers: []EventServer{}, Channels: []EventChannel{{ID: "topic", MessageIDs: []string{"allowed"}}, {ID: "topic", MessageIDs: []string{"allowed"}}}, Messages: []EventMessage{{ID: "allowed"}, {ID: "other"}}, Schemas: []EventSchema{}, Contracts: []EventContract{{ID: "events", ParticipantID: "service", Operations: []EventOperation{{ID: "bad-action", Action: "invalid", ChannelID: "topic", MessageID: "allowed"}, {ID: "bad-message", Action: "receive", ChannelID: "topic", MessageID: "other"}}}}}}
	a, err := AnalyzeEventMap(context.Background(), doc)
	if err != nil || a.Complete {
		t.Fatalf("invalid draft appears complete: %+v %v", a.Coverage, err)
	}
	for _, code := range []string{"event_duplicate_id", "event_action_invalid", "event_operation_message_mismatch"} {
		if !slices.ContainsFunc(a.Diagnostics, func(d EventMapDiagnostic) bool { return d.Code == code }) {
			t.Fatalf("missing %s: %+v", code, a.Diagnostics)
		}
	}
	for _, edge := range a.Edges {
		if edge.Kind == "receive" && (edge.Target == "operation:events:bad-action" || edge.Target == "operation:events:bad-message") {
			t.Fatalf("invented receive edge: %+v", edge)
		}
		if edge.Source == "channel:topic" || edge.Target == "channel:topic" {
			t.Fatalf("edge attached to ambiguous channel: %+v", edge)
		}
	}
}

func TestAnalyzeEventMapCycleWarnsAndStableIDs(t *testing.T) {
	t.Parallel()
	doc := Document{FormatVersion: 3, Participants: []Participant{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}, EventModel: &EventModel{Servers: []EventServer{}, Channels: []EventChannel{{ID: "a", Address: "a", ServerIDs: []string{}, MessageIDs: []string{"m"}}, {ID: "b", Address: "b", ServerIDs: []string{}, MessageIDs: []string{"m"}}}, Messages: []EventMessage{{ID: "m", Name: "M"}}, Schemas: []EventSchema{}, Contracts: []EventContract{{ID: "a", ParticipantID: "a", Operations: []EventOperation{{ID: "receive", Action: "receive", ChannelID: "a", MessageID: "m", Kafka: &EventOperationKafka{GroupID: "g-a"}, FailureRoutes: &EventFailureRoutes{RetryChannelID: "b"}}}}, {ID: "b", ParticipantID: "b", Operations: []EventOperation{{ID: "receive", Action: "receive", ChannelID: "b", MessageID: "m", Kafka: &EventOperationKafka{GroupID: "g-b"}, FailureRoutes: &EventFailureRoutes{RetryChannelID: "a"}}}}}}}
	a, err := AnalyzeEventMap(context.Background(), doc)
	if err != nil || !a.Complete {
		t.Fatalf("cycle analysis: %+v %v", a.Coverage, err)
	}
	count := 0
	for _, d := range a.Diagnostics {
		if d.Code == "event_route_cycle" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("cycle warnings: %+v", a.Diagnostics)
	}
	reordered := doc
	reordered.Participants = slices.Clone(doc.Participants)
	slices.Reverse(reordered.Participants)
	m := *doc.EventModel
	m.Channels = slices.Clone(doc.EventModel.Channels)
	slices.Reverse(m.Channels)
	m.Contracts = slices.Clone(doc.EventModel.Contracts)
	slices.Reverse(m.Contracts)
	reordered.EventModel = &m
	b, err := AnalyzeEventMap(context.Background(), reordered)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(a EventMapAnalysis) ([]string, []string) {
		nodes := make([]string, 0, len(a.Nodes))
		edges := make([]string, 0, len(a.Edges))
		for _, n := range a.Nodes {
			nodes = append(nodes, n.ID)
		}
		for _, e := range a.Edges {
			edges = append(edges, e.ID)
		}
		return nodes, edges
	}
	an, ae := ids(a)
	bn, be := ids(b)
	if !slices.Equal(an, bn) || !slices.Equal(ae, be) {
		t.Fatalf("unstable semantic IDs: nodes=%v/%v edges=%v/%v", an, bn, ae, be)
	}
}
