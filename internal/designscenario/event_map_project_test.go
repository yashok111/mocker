package designscenario

import (
	"slices"
	"testing"
)

func TestAnalyzeEventMapProjectsOnlyEventContractParticipants(t *testing.T) {
	t.Parallel()
	doc := Document{
		FormatVersion: 3,
		Participants: []Participant{
			{ID: "broker", Name: "Kafka", Kind: "queue"},
			{ID: "app", Name: "Kafka", Kind: "service"},
			{ID: "unrelated", Name: "HTTP only", Kind: "service"},
			{ID: "empty", Name: "Draft application", Kind: "service"},
		},
		EventModel: &EventModel{
			Servers:  []EventServer{{ID: "kafkaA", Name: "Kafka"}, {ID: "kafkaB", Name: "Kafka"}},
			Channels: []EventChannel{{ID: "orders", Address: "orders", ServerIDs: []string{"kafkaA", "kafkaB"}, MessageIDs: []string{"created"}}},
			Messages: []EventMessage{{ID: "created", Name: "Created"}},
			Contracts: []EventContract{
				{ID: "events", ParticipantID: "app", Operations: []EventOperation{{ID: "publish", Action: "send", ChannelID: "orders", MessageID: "created"}}},
				{ID: "draft", ParticipantID: "empty", Operations: []EventOperation{}},
			},
		},
	}
	report, err := AnalyzeEventMap(t.Context(), doc)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"channel:orders", "message:created", "operation:events:publish", "participant:app", "participant:empty", "server:kafkaA", "server:kafkaB"}
	if len(report.Nodes) != len(wantIDs) {
		t.Fatalf("event map includes unrelated participants: %+v", report.Nodes)
	}
	for _, id := range wantIDs {
		if !slices.ContainsFunc(report.Nodes, func(node EventMapNode) bool { return node.ID == id }) {
			t.Fatalf("missing event entity %s", id)
		}
	}
	owner := report.Nodes[slices.IndexFunc(report.Nodes, func(node EventMapNode) bool { return node.ID == "participant:app" })]
	if owner.Locator.Pointer != "/participants/1" || owner.Label != "Kafka" {
		t.Errorf("owner locator/name changed: %+v", owner)
	}
	if !report.Complete || report.Coverage.NodesReturned != 7 || report.Coverage.EdgesReturned != 5 || len(report.Edges) != 5 || len(report.Coverage.TruncatedReasons) != 0 {
		t.Errorf("wrong coverage: %+v complete=%v edges=%+v", report.Coverage, report.Complete, report.Edges)
	}
}

func TestAnalyzeEventMapWithoutEventContractsOmitsScenarioParticipants(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		model *EventModel
	}{
		{name: "no event model"},
		{name: "empty event model", model: &EventModel{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			report, err := AnalyzeEventMap(t.Context(), Document{
				Participants: []Participant{{ID: "broker", Name: "Kafka", Kind: "queue"}},
				EventModel:   tt.model,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Nodes) != 0 || len(report.Edges) != 0 || report.Coverage.NodesReturned != 0 || !report.Complete {
				t.Fatalf("unrelated participants in empty event map: %+v", report)
			}
		})
	}
}

func TestAnalyzeEventMapKeepsDeclaredOwnerForInvalidOperations(t *testing.T) {
	t.Parallel()
	report, err := AnalyzeEventMap(t.Context(), Document{
		Participants: []Participant{{ID: "owner", Name: "Owner"}, {ID: "unrelated", Name: "Other"}},
		EventModel: &EventModel{Contracts: []EventContract{{
			ID: "events", ParticipantID: "owner",
			Operations: []EventOperation{{ID: "invalid", Action: "invalid", ChannelID: "missing", MessageID: "missing"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Nodes) != 2 || !slices.ContainsFunc(report.Nodes, func(node EventMapNode) bool { return node.ID == "participant:owner" }) {
		t.Errorf("declared owner must remain with its incomplete operation: %+v", report.Nodes)
	}
	if report.Complete || !slices.ContainsFunc(report.Diagnostics, func(d EventMapDiagnostic) bool { return d.Code == "event_operation_channel_missing" }) {
		t.Errorf("incomplete operation diagnostics changed: %+v", report)
	}
}

func TestAnalyzeEventMapMissingContractOwnerDoesNotInventParticipant(t *testing.T) {
	t.Parallel()
	report, err := AnalyzeEventMap(t.Context(), Document{
		Participants: []Participant{{ID: "unrelated", Name: "Kafka", Kind: "queue"}},
		EventModel: &EventModel{Contracts: []EventContract{{
			ID: "events", ParticipantID: "missing",
			Operations: []EventOperation{{ID: "publish", Action: "send"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Nodes) != 1 || report.Nodes[0].Kind != "operation" || len(report.Edges) != 0 || report.Complete {
		t.Errorf("unexpected projection for missing owner: %+v", report)
	}
	if !slices.ContainsFunc(report.Diagnostics, func(d EventMapDiagnostic) bool { return d.Code == "event_participant_missing" }) {
		t.Errorf("missing owner diagnostic lost: %+v", report.Diagnostics)
	}
}
