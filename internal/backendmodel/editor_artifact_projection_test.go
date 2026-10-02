package backendmodel

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/testkit"
)

const editorBackendRevision = "10000000-0000-4000-8000-000000000001"
const editorSourceID = "20000000-0000-4000-8000-000000000001"
const editorAPIDocument = `{"openapi":"3.1.0","info":{"title":"Historical API","version":"1"},"paths":{"/x":{"get":{"x-mocker-canvas-operation-id":"op","responses":{"200":{"description":"OK"}}}}},"x-mocker-state-diagrams":{"formatVersion":1,"diagrams":[{"id":"d","name":"Lifecycle","initialStateId":"a","states":[{"id":"a","name":"A","x":0,"y":0,"terminal":false},{"id":"b","name":"B","x":1,"y":0,"terminal":true}],"transitions":[{"id":"tr","name":"Finish","from":"a","to":"b","binding":{"method":"get","path":"/x"},"patchJSON":"{}","responseStatus":200}]}]},"x-mocker-response-rules":{"formatVersion":1,"rules":[{"id":"r","name":"Responses","binding":{"method":"get","path":"/x"},"nodes":[{"id":"start","type":"start","name":"Start","x":0,"y":0},{"id":"reply","type":"response","name":"Reply","x":1,"y":0,"response":{"status":200,"mediaType":"application/json","headers":[]}}],"edges":[{"id":"e","from":"start","port":"next","to":"reply"}]}]}}`

type editorProjectionFixture struct {
	request  *EditorArtifactRequest
	api      *editorTestAPI
	scenario *editorTestScenario
	state    RevisionState
	context  ArtifactContext
	pin      ArtifactPin
	apiPin   ArtifactPin
}

func newEditorProjectionFixture(t *testing.T, edits ...func(*designscenario.Document)) *editorProjectionFixture {
	t.Helper()
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2_000_000}
	apiRepo := apidesign.NewRepo(db, cfg)
	repo := designscenario.NewRepo(db, cfg, apiRepo)
	apiDetail, err := apiRepo.Create(t.Context(), apidesign.CreateInput{Name: "API", Document: editorAPIDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	apiSnapshot, err := apiRepo.ArtifactSnapshot(t.Context(), apiDetail.Design.ID, apiDetail.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	doc := designscenario.Document{FormatVersion: 3, Title: "Exact scenario", Participants: []designscenario.Participant{{ID: "p", Name: "Service", Kind: "service"}, {ID: "client", Name: "Client", Kind: "client"}}, Messages: []designscenario.Message{{ID: "m2", FromID: "client", ToID: "p", Kind: "request", Label: "Second ID first", Operation: &designscenario.OperationBinding{ContractID: "linked", OperationKey: "op"}}, {ID: "m1", FromID: "client", ToID: "p", Kind: "request", Label: "First ID second", Operation: &designscenario.OperationBinding{ContractID: "copy", OperationKey: "op"}}}, Fragments: []designscenario.Fragment{{ID: "f", Kind: "opt", Label: "Optional", FromMessageID: "m2", ToMessageID: "m1"}}, Contracts: []designscenario.Contract{{ID: "linked", Mode: "linked", Document: []byte(apiSnapshot.IdentityDocument), Source: &designscenario.ContractSource{DesignID: apiSnapshot.DesignID, RevisionID: apiSnapshot.RevisionID, Version: apiSnapshot.Version}}, {ID: "copy", Mode: "copy", Document: []byte(strings.Replace(apiSnapshot.IdentityDocument, "Historical API", "Divergent copy", 1)), Source: &designscenario.ContractSource{DesignID: apiSnapshot.DesignID, RevisionID: apiSnapshot.RevisionID, Version: apiSnapshot.Version}}}, EventModel: &designscenario.EventModel{Servers: []designscenario.EventServer{{ID: "srv", Name: "Broker", Protocol: "kafka", Auth: "none"}}, Channels: []designscenario.EventChannel{{ID: "ch", Address: "topic", ServerIDs: []string{"srv"}, MessageIDs: []string{"em"}}}, Messages: []designscenario.EventMessage{{ID: "em", Name: "Created", Examples: []designscenario.EventExample{}, PayloadSchemaID: "schema"}}, Schemas: []designscenario.EventSchema{{ID: "schema", SchemaJSON: "{}"}}, Contracts: []designscenario.EventContract{{ID: "ec", ParticipantID: "p", Operations: []designscenario.EventOperation{{ID: "eo", Action: "send", ChannelID: "ch", MessageID: "em", APILinks: []designscenario.EventAPILink{{ContractID: "linked", OperationKey: "op"}, {ContractID: "copy", OperationKey: "op"}}, StateLinks: []designscenario.EventStateLink{{ContractID: "linked", DiagramID: "d", TransitionID: "tr"}}}}}}}}
	for _, edit := range edits {
		edit(&doc)
	}
	detail, err := repo.Create(t.Context(), designscenario.CreateInput{Document: doc, FormDrafts: map[string]string{"copy": "unfinished inert buffer"}, Source: "ui"})
	if err != nil {
		if ie, ok := errors.AsType[*designscenario.InvalidError](err); ok {
			t.Fatalf("fixture: %+v", ie.Diagnostics)
		}
		t.Fatal(err)
	}
	snapshot, err := repo.ArtifactSnapshot(t.Context(), detail.Scenario.ID, detail.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	api := &editorTestAPI{snapshots: map[string]*apidesign.ArtifactSnapshot{fmt.Sprintf("%d/%d", apiSnapshot.DesignID, apiSnapshot.RevisionID): apiSnapshot}, heads: map[int64]int64{apiSnapshot.DesignID: apiSnapshot.RevisionID + 99}}
	scenario := &editorTestScenario{snapshot: snapshot}
	pin := ArtifactPin{Kind: "design_scenario", ID: strconv.FormatInt(snapshot.ScenarioID, 10), RevisionID: strconv.FormatInt(snapshot.RevisionID, 10), ContentHash: snapshot.ContentHash}
	apiPin := ArtifactPin{Kind: "api_design", ID: strconv.FormatInt(apiSnapshot.DesignID, 10), RevisionID: strconv.FormatInt(apiSnapshot.RevisionID, 10), ContentHash: apiSnapshot.ContentHash}
	return &editorProjectionFixture{request: NewEditorArtifactRequest(t.Context(), api, scenario), api: api, scenario: scenario, pin: pin, apiPin: apiPin, state: RevisionState{Revision: Revision{ID: editorBackendRevision, SemanticHash: strings.Repeat("a", 64), SourceSnapshotIDs: []string{editorSourceID}, ArtifactPins: []ArtifactPin{pin}}, Nodes: []Node{{ID: editorSourceID}}}, context: ArtifactContext{DocumentVersion: EditorArtifactDocumentVersion, SourceContentHash: strings.Repeat("b", 64), SourceSemanticHash: strings.Repeat("c", 64), APIBindings: []APIArtifactBinding{}, EditorBindings: []EditorBinding{}}}
}
func (f *editorProjectionFixture) query(view, embedded string) ArtifactQueryInput {
	return ArtifactQueryInput{RevisionID: editorBackendRevision, Artifact: ArtifactKey{f.pin.Kind, f.pin.ID}, View: view, EmbeddedContractID: embedded}
}
func TestEditorArtifactNineSelectorsExactOwner(t *testing.T) {
	t.Parallel()
	f := newEditorProjectionFixture(t)
	for _, s := range []EditorSelector{{Kind: "participant", ParticipantID: "p"}, {Kind: "sequence_message", MessageID: "m1"}, {Kind: "event_operation", ContractID: "ec", OperationID: "eo"}, {Kind: "event_channel", ChannelID: "ch"}, {Kind: "event_message", MessageID: "em"}, {Kind: "state_diagram", DiagramID: "d", EmbeddedContractID: "copy"}, {Kind: "state_transition", DiagramID: "d", TransitionID: "tr", EmbeddedContractID: "linked"}, {Kind: "response_rule", RuleID: "r", EmbeddedContractID: "copy"}, {Kind: "response_node", RuleID: "r", NodeID: "reply", EmbeddedContractID: "linked"}} {
		t.Run(s.Kind, func(t *testing.T) {
			object, err := f.request.ResolveObject(f.pin, s)
			if err != nil {
				t.Fatal(err)
			}
			if object.Selector != s || object.Locator.Pin != f.pin || object.ObjectHash == "" || object.Label == "" || object.Data.Kind != s.Kind {
				t.Fatalf("wrong exact object: %+v", object)
			}
			if s.EmbeddedContractID == "linked" && object.Locator.Embedded.OriginStatus != "verified" {
				t.Fatal(object.Locator.Embedded)
			}
		})
	}
	object, err := f.request.ResolveObject(f.pin, EditorSelector{Kind: "participant", ParticipantID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	// Independent literal owner object/hash input, never produced by projector.
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte(`{"domain":"backend-editor-object-v1","object":{"description":"","id":"p","kind":"service","name":"Service"}}`)))
	if object.ObjectHash != expected || object.Locator.Owner.Pointer != "/participants/0" {
		t.Fatalf("hash/pointer=%s %s expected=%s", object.ObjectHash, object.Locator.Owner.Pointer, expected)
	}
	for _, s := range []EditorSelector{{Kind: "participant", ParticipantID: "Service"}, {Kind: "sequence_message", MessageID: "missing"}, {Kind: "event_operation", ContractID: "copy", OperationID: "eo"}, {Kind: "state_diagram", DiagramID: "d"}} {
		if _, err := f.request.ResolveObject(f.pin, s); err == nil {
			t.Fatal("inferred or missing selection accepted", s)
		}
	}
	object.Data.Participant.Name = "caller edit"
	again, _ := f.request.ResolveObject(f.pin, object.Selector)
	if again.Data.Participant.Name != "Service" {
		t.Fatal("shared typed mutation")
	}
	if len(f.scenario.calls) != 1 || len(f.api.calls) != 1 {
		t.Fatalf("snapshot reuse: %v %v", f.scenario.calls, f.api.calls)
	}
	// The API owner supports the four state/rule selector variants directly.
	for _, s := range []EditorSelector{{Kind: "state_diagram", DiagramID: "d"}, {Kind: "state_transition", DiagramID: "d", TransitionID: "tr"}, {Kind: "response_rule", RuleID: "r"}, {Kind: "response_node", RuleID: "r", NodeID: "reply"}} {
		if _, err := f.request.ResolveObject(f.apiPin, s); err != nil {
			t.Fatal(err)
		}
	}
}
func TestEditorProjectionFourViewsPagingAndIsolation(t *testing.T) {
	t.Parallel()
	f := newEditorProjectionFixture(t)
	for _, tc := range []struct {
		view, embedded string
		count          int
	}{{"sequence", "", 5}, {"states", "copy", 4}, {"response_rules", "linked", 4}, {"event_model", "", 0}} {
		t.Run(tc.view, func(t *testing.T) {
			in := f.query(tc.view, tc.embedded)
			page, err := f.request.Project(&f.state, f.context, in)
			if err != nil {
				t.Fatal(err)
			}
			if !page.Complete || !page.BindingsComplete || page.SelectedPin != f.pin || page.SourceSnapshotIDs[0] != editorSourceID || page.Resolution.Status != "resolved" {
				t.Fatalf("page %+v", page)
			}
			if tc.count > 0 && len(page.Items) != tc.count {
				t.Fatalf("items=%d expected=%d", len(page.Items), tc.count)
			}
			if tc.view == "sequence" {
				if page.Items[2].Data.SequenceMessage.ID != "m2" || page.Items[3].Data.SequenceMessage.ID != "m1" || page.Items[2].Locator.Owner.FragmentID != "f" {
					t.Fatal("authored message ordering/context lost")
				}
			}
			if tc.view == "event_model" {
				nodes, edges := 0, 0
				ids := map[string]bool{}
				aux := map[string]bool{}
				for _, item := range page.Items {
					if ids[item.ID] {
						t.Fatal("row ID collision", item.ID)
					}
					ids[item.ID] = true
					if item.Data.EventNode != nil {
						nodes++
						aux[item.Data.EventNode.Kind] = true
						if slices.Contains([]string{"server", "schema", "api_operation"}, item.Data.EventNode.Kind) && item.BindingSelector != nil {
							t.Fatal("auxiliary binding union widened")
						}
					}
					if item.Data.EventEdge != nil {
						edges++
						if item.BindingSelector != nil {
							t.Fatal("edge binding union widened")
						}
					}
				}
				if nodes != page.Coverage.NodesReturned || edges != page.Coverage.EdgesReturned || !aux["server"] || !aux["schema"] || !aux["api_operation"] {
					t.Fatalf("event coverage %+v kinds=%v", page.Coverage, aux)
				}
			}
		})
	}
	in := f.query("sequence", "")
	in.Limit = 2
	first, err := f.request.Project(&f.state, f.context, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" || len(first.NextCursor) > 256 {
		t.Fatal("paging", first)
	}
	in.Cursor = first.NextCursor
	second, err := f.request.Project(&f.state, f.context, in)
	if err != nil || second.Items[0].Data.SequenceMessage.ID != "m2" {
		t.Fatalf("page2 %+v %v", second, err)
	}
	mismatch := in
	mismatch.Limit = 3
	if _, err := f.request.Project(&f.state, f.context, mismatch); err == nil {
		t.Fatal("limit replay accepted")
	}
	mismatch = in
	mismatch.View = "event_model"
	if _, err := f.request.Project(&f.state, f.context, mismatch); err == nil {
		t.Fatal("view replay accepted")
	}
	all, err := f.request.Project(&f.state, f.context, f.query("sequence", ""))
	if err != nil {
		t.Fatal(err)
	}
	if first.Items[0].ID != all.Items[0].ID {
		t.Fatal("row IDs depend on page limit")
	}
	first.Pins[0].ContentHash = "mutated"
	first.Items[0].Data.Participant.Name = "mutated"
	all2, _ := f.request.Project(&f.state, f.context, f.query("sequence", ""))
	if all2.Pins[0] != f.pin || all2.Items[0].Data.Participant.Name != "Service" {
		t.Fatal("page alias mutated immutable scope")
	}
}
func TestEditorProjectionFrozenBrokenOrphanRoster(t *testing.T) {
	t.Parallel()
	f := newEditorProjectionFixture(t)
	binding := EditorBinding{ArtifactKind: f.pin.Kind, ArtifactID: f.pin.ID, Selector: EditorSelector{Kind: "sequence_message", MessageID: "missing"}, SourceNodeIDs: []string{editorSourceID}, SourceLabels: []string{"Frozen label"}, ObjectHash: strings.Repeat("e", 64), LastKnownLabel: "Saved missing message", Origin: "manual", Reason: "Explicit saved association"}
	f.context.EditorBindings = []EditorBinding{binding}
	f.state.Nodes = nil
	f.scenario.snapshot = nil
	page, err := f.request.Project(&f.state, f.context, f.query("sequence", ""))
	if err != nil {
		t.Fatal(err)
	}
	if page.Resolution.Status != "unavailable" || page.Complete || !page.BindingsComplete || len(page.EditorBindings) != 1 || page.EditorBindings[0].LastKnownLabel != "Saved missing message" || len(page.Items) != 0 {
		t.Fatal("lost frozen broken roster", page)
	}
	page.EditorBindings[0].SourceLabels[0] = "caller edit"
	if f.context.EditorBindings[0].SourceLabels[0] != "Frozen label" {
		t.Fatal("binding roster alias")
	}
}
func TestEditorRequestCancellationAndConcurrentReuse(t *testing.T) {
	t.Parallel()
	f := newEditorProjectionFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	r := NewEditorArtifactRequest(ctx, f.api, f.scenario)
	cancel()
	if _, err := r.SnapshotPin(ArtifactKey{f.pin.Kind, f.pin.ID}, f.pin.RevisionID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(f.scenario.calls) != 0 {
		t.Fatal("cancelled scope read owner")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.request.ResolveObject(f.pin, EditorSelector{Kind: "participant", ParticipantID: "p"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(f.scenario.calls) != 1 {
		t.Fatal(f.scenario.calls)
	}
}
