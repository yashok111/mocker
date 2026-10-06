package admin

import (
	"encoding/json/v2"
	"fmt"
	"github.com/yashok111/mocker/internal/backendmaterialize"
	"github.com/yashok111/mocker/internal/backendmodel"
	"strings"
	"testing"
)

func TestMaterializationRESTRejectsOpenScope(t *testing.T) {
	s := loopbackTestServer(t, nil)
	for _, action := range []string{"preview", "apply"} {
		status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects/11111111-1111-4111-8111-111111111111/materializations/"+action, []byte(`{"profileVersion":"backend-http-draft-v1","unknown":true}`))
		if err != nil || status != 422 {
			t.Fatal(status, string(raw), err)
		}
	}
}

func TestMaterializationRESTPreviewApplyReplay(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, proposal, _, _ := b42Draft(t, s)
	target := backendmodel.BackendReadTarget{ChangeProposal: &backendmodel.ProposalReadTarget{ProposalID: proposal.Proposal.ID, ProposalRevisionID: proposal.Revision.ID}}
	graph, err := s.backendRepo.ResolveEffectiveGraph(t.Context(), p.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	in := backendmaterialize.PreviewInput{ProfileVersion: backendmaterialize.ProfileVersion, Target: target, TargetHash: graph.Pins.TargetHash, SourceScope: []string{graph.State.Nodes[0].ID}, Targets: []backendmaterialize.Target{{Key: "api", Kind: "api_design", Name: "Draft", Commands: []backendmaterialize.Command{{Type: "replace_api_document", APIDocument: `{"openapi":"3.1.0","info":{"title":"Draft","version":"1"},"paths":{}}`}}}}, Translations: []backendmaterialize.Translation{{SourceID: graph.State.Nodes[0].ID, TargetKey: "api", Selector: "", Reason: "Explicit draft"}}, ExcludedIDs: []string{}, Reason: "Draft only"}
	call := func(action string, value any) []byte {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := "/api/backend-projects/" + p.ID + "/materializations/" + action
		status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", path, raw)
		if err != nil || status != 200 {
			t.Fatal(status, string(body), err)
		}
		validateBackendImportResponse(t, "POST", path, body)
		return body
	}
	var preview backendmaterialize.Preview
	if err := json.Unmarshal(call("preview", in), &preview); err != nil {
		t.Fatal(err)
	}
	request := backendmaterialize.ApplyInput{PreviewInput: in, CandidateHash: preview.CandidateHash, IdempotencyKey: "exact"}
	first := call("apply", request)
	second := call("apply", request)
	if string(first) != string(second) {
		t.Fatal("receipt changed")
	}
}

func TestMaterializationIntegratedSVGExactView(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "SVG", IdempotencyKey: "svg"})
	if err != nil {
		t.Fatal(err)
	}
	doc := backendmodel.DiagramDocument{Format: backendmodel.DiagramDocumentVersion, Kind: "architecture", Target: backendmodel.BackendReadTarget{RevisionID: p.CurrentRevisionID}, Payload: backendmodel.ArchitecturePayload{PrimarySystemID: p.ID, Elements: []backendmodel.ArchitectureElement{{ID: p.ID, Label: "<script>label</script>", Role: "software_system", Origin: backendmodel.DiagramOrigin{Kind: "authored", Reason: "boundary"}, Refs: []backendmodel.DiagramRef{}}}, Links: []backendmodel.ArchitectureLink{}}}
	diagram, err := s.backendRepo.CreateDiagram(t.Context(), p.ID, backendmodel.DiagramCreateInput{Document: doc, IdempotencyKey: "diagram"})
	if err != nil {
		t.Fatal(err)
	}
	state := backendmodel.DiagramViewState{Diagram: diagram.Pin, Level: "context", RootID: p.ID, Origin: "all", Positions: []backendmodel.DiagramPosition{}, CollapsedIDs: []string{}}
	view, err := s.backendRepo.CreateDiagramView(t.Context(), p.ID, backendmodel.DiagramCreateViewInput{Name: "Exact SVG", State: state, IdempotencyKey: "view"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/backend-projects/%s/diagram-views/%s/versions/1/svg", p.ID, view.ID)
	read := func() []byte {
		t.Helper()
		status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", path, nil)
		if err != nil || status != 200 {
			t.Fatal(status, string(raw), err)
		}
		return raw
	}
	before := read()
	state.Positions = []backendmodel.DiagramPosition{{ID: p.ID, X: 400, Y: 300}}
	if _, err := s.backendRepo.SaveDiagramView(t.Context(), p.ID, view.ID, backendmodel.DiagramSaveViewInput{Name: "New head", State: state, ExpectedVersion: 1, IdempotencyKey: "save"}); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(read()) || strings.Contains(string(before), "<script>") {
		t.Fatal("SVG pin/escaping changed")
	}
}
