package admin

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestImpactHTTPReportsSavedFieldUsagesWithoutMutation(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	const document = `{"openapi":"3.1.0","info":{"title":"Fields","version":"1"},"paths":{"/source":{"get":{"x-mocker-canvas-operation-id":"source","responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"],"additionalProperties":false}}}}}}},"/target/{id}":{"get":{"x-mocker-canvas-operation-id":"target","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"integer"}}],"responses":{"200":{"description":"OK"}}}}}}`
	d, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Fields", Source: "ui", Document: document})
	if err != nil {
		t.Fatal(err)
	}
	// Normal UI/MCP serialization compacts embedded JSON. Formatting must not
	// downgrade an otherwise exact linked snapshot to hypothetical provenance.
	var embedded bytes.Buffer
	if err := jsonx.Compact(&embedded, []byte(d.Draft.Document)); err != nil {
		t.Fatal(err)
	}
	execution := func() *designscenario.StepExecution {
		return &designscenario.StepExecution{Enabled: true, PathParams: designscenario.ExecutionValues{}, Query: designscenario.ExecutionValues{}, Headers: designscenario.ExecutionValues{}, Assertions: []designscenario.ExecutionAssertion{}, Extract: []designscenario.ExecutionExtraction{}}
	}
	source, target := execution(), execution()
	source.Assertions = []designscenario.ExecutionAssertion{{Pointer: "/id", Equals: jsonx.RawMessage(`9007199254740993`)}}
	source.Extract = []designscenario.ExecutionExtraction{{Name: "id", Pointer: "/id"}}
	target.Bindings = []designscenario.DataBinding{{ID: "id", SourceMessageID: "source", SourcePointer: "/id", Target: designscenario.DataBindingTarget{Kind: "path", Name: "id"}}}
	scenario, err := s.designScenariosRepo.Create(t.Context(), designscenario.CreateInput{Source: "ui", Document: designscenario.Document{
		FormatVersion: 1, Title: "Field consumers", Fragments: []designscenario.Fragment{},
		Participants: []designscenario.Participant{{ID: "client", Name: "Client", Kind: "client"}, {ID: "api", Name: "API", Kind: "service"}},
		Contracts:    []designscenario.Contract{{ID: "api", Name: "API", Mode: "linked", Source: &designscenario.ContractSource{DesignID: d.Design.ID, RevisionID: d.Draft.ID, Version: 1}, Document: jsonx.RawMessage(embedded.Bytes())}},
		Messages: []designscenario.Message{
			{ID: "source", FromID: "client", ToID: "api", Kind: "request", Operation: &designscenario.OperationBinding{ContractID: "api", OperationKey: "source"}, Execution: source},
			{ID: "target", FromID: "client", ToID: "api", Kind: "request", Operation: &designscenario.OperationBinding{ContractID: "api", OperationKey: "target"}, Execution: target},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Changing only the source schema makes the existing integer path binding
	// incompatible, and asks for review of the saved assertion and extraction.
	candidate := strings.Replace(d.Draft.Document, `"type": "integer"`, `"type": "string"`, 1)
	if candidate == d.Draft.Document {
		t.Fatal("fixture did not change")
	}
	body, err := jsonx.Marshal(apidesign.ImpactInput{FromRevisionID: d.Draft.ID, Document: &candidate})
	if err != nil {
		t.Fatal(err)
	}
	status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", fmt.Sprintf("/api/designs/%d/impact", d.Design.ID), body)
	if err != nil || status != 200 {
		t.Fatalf("impact: %d %s %v", status, raw, err)
	}
	var report apidesign.ImpactReport
	if err := jsonx.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	byKind := map[string]apidesign.ImpactFieldImpact{}
	for _, finding := range report.FieldImpacts {
		byKind[finding.UsageKind] = finding
		if finding.Locator.ScenarioID != scenario.Scenario.ID || finding.Locator.ScenarioRevision != scenario.Draft.ID {
			t.Fatalf("wrong locator: %+v", finding)
		}
	}
	for _, kind := range []string{"binding_source", "assertion", "extract"} {
		finding, ok := byKind[kind]
		if !ok || finding.Before.Type != "integer" || finding.After.Type != "string" {
			t.Fatalf("missing changed field %s: %s", kind, raw)
		}
	}
	if byKind["binding_source"].Verdict != "broken" || byKind["binding_source"].UsagePointer != "/messages/1/execution/bindings/0/sourcePointer" {
		t.Fatalf("wrong binding verdict: %s", raw)
	}
	if report.Coverage.ScenariosScanned != 1 || report.Coverage.FieldImpactsReturned != len(report.FieldImpacts) || report.Coverage.FieldUsagesChecked < 4 {
		t.Fatalf("wrong coverage: %+v", report.Coverage)
	}
	readback, err := s.designScenariosRepo.Detail(t.Context(), scenario.Scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readback.Scenario.Version != scenario.Scenario.Version || readback.Draft.ID != scenario.Draft.ID || !bytes.Equal(readback.Draft.Document.Contracts[0].Document, scenario.Draft.Document.Contracts[0].Document) {
		t.Fatal("analysis mutated saved scenario")
	}
}
