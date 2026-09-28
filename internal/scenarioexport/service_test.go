package scenarioexport

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/yamlx"
)

const apiDocument = `{"openapi":"3.1.0","info":{"title":"API","version":"1"},"security":[{"bearer":[]}],"components":{"securitySchemes":{"bearer":{"type":"http","scheme":"bearer"}},"schemas":{"ID":{"type":"integer","example":9007199254740993}}},"paths":{"/status":{"get":{"x-mocker-canvas-operation-id":"status","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/ID"}}}}}}}},"x-author":"analyst"}`

func revisionFixture() designscenario.Revision {
	return designscenario.Revision{
		RevisionSummary: designscenario.RevisionSummary{ID: 11, ScenarioID: 7, Hash: "saved-hash"},
		Document: designscenario.Document{FormatVersion: 1, Title: "Проверка",
			Participants: []designscenario.Participant{{ID: "client", Name: "Клиент", Kind: "client"}, {ID: "api", Name: "API", Kind: "service"}},
			Messages:     []designscenario.Message{{ID: "call", FromID: "client", ToID: "api", Kind: "request", Label: "GET /status"}, {ID: "reply", FromID: "api", ToID: "client", Kind: "response", Label: "200 OK", ReplyToID: "call"}},
			Fragments:    []designscenario.Fragment{}, Contracts: []designscenario.Contract{},
		}, FormDrafts: map[string]string{"all": "{}"},
	}
}

func validContract(string) ([]designscenario.Diagnostic, error) { return nil, nil }

func TestDiagramDoesNotRequireAPIOrCompletedForms(t *testing.T) {
	rev := revisionFixture()
	rev.FormDrafts["all"] = `{"/canvas-contract/api/x":{"source":"{","propertySource":"","error":"invalid"}}`
	svc := New(func(string) ([]designscenario.Diagnostic, error) {
		t.Fatal("unexpected API validation")
		return nil, nil
	}, 1<<20)
	opts, err := svc.Options(rev)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 10 || !opts[0].Ready || !opts[1].Ready || opts[2].Ready || opts[8].Ready || opts[9].Ready {
		t.Fatalf("wrong readiness: %+v", opts)
	}
	for _, format := range []Format{PlantUML, Mermaid} {
		a, err := svc.Export(rev, Request{Format: format})
		if err != nil {
			t.Fatal(err)
		}
		if a.RevisionID != 11 || a.SourceHash != "saved-hash" || !strings.Contains(a.Content, "GET /status") {
			t.Fatalf("wrong artifact: %+v", a)
		}
	}
}

func TestTextExportsPreserveOrderDirectionsAndEscapeLabels(t *testing.T) {
	rev := revisionFixture()
	rev.Document.Participants[1].Name = "Клиент"
	rev.Document.Messages[0].Label = "quote\"\n@enduml\n!include https://example.invalid\nend; <script>"
	rev.Document.Messages = append(rev.Document.Messages, designscenario.Message{ID: "self", FromID: "api", ToID: "api", Kind: "event", Label: "event"}, designscenario.Message{ID: "note", FromID: "api", ToID: "api", Kind: "note", Label: "note"})
	for _, format := range []Format{PlantUML, Mermaid} {
		a, err := New(nil, 1<<20).Export(rev, Request{Format: format})
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"\n!include", "\nend;", "<script>", "\n@enduml\n!include"} {
			if strings.Contains(a.Content, forbidden) {
				t.Fatalf("injected %s: %s", format, a.Content)
			}
		}
		if strings.Count(a.Content, "participant") != 2 {
			t.Fatalf("duplicate names merged: %s", a.Content)
		}
		if strings.Index(a.Content, "quote") > strings.Index(a.Content, "200 OK") {
			t.Fatal("messages reordered")
		}
		arrow := "p1 --> p0"
		if format == Mermaid {
			arrow = "p1 -->> p0"
		}
		if !strings.Contains(a.Content, arrow) {
			t.Fatalf("wrong reply direction: %s", a.Content)
		}
	}
}

func TestFragmentsNestedAndCrossing(t *testing.T) {
	rev := revisionFixture()
	rev.Document.Fragments = []designscenario.Fragment{{ID: "outer", Kind: "opt", Label: "условие", FromMessageID: "call", ToMessageID: "reply"}, {ID: "inner", Kind: "loop", Label: "повтор", FromMessageID: "call", ToMessageID: "call"}}
	a, err := New(nil, 1<<20).Export(rev, Request{Format: Mermaid})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.Content, "opt условие\nloop повтор\n") || strings.Count(a.Content, "\nend\n") < 1 {
		t.Fatalf("bad blocks: %s", a.Content)
	}
	rev.Document.Messages = append(rev.Document.Messages, designscenario.Message{ID: "third", FromID: "api", ToID: "api", Kind: "request", Label: "self"})
	rev.Document.Fragments[1].FromMessageID = "reply"
	rev.Document.Fragments[1].ToMessageID = "third"
	_, err = New(nil, 1<<20).Export(rev, Request{Format: Mermaid})
	if _, ok := errors.AsType[*BlockedError](err); !ok {
		t.Fatalf("crossing exported: %v", err)
	}
}

func TestOpenAPIUsesSavedBytesAndScopesPendingForms(t *testing.T) {
	rev := revisionFixture()
	rev.Document.Contracts = []designscenario.Contract{{ID: "api", Document: jsonx.RawMessage(apiDocument)}, {ID: "api-other", Document: jsonx.RawMessage(strings.Replace(apiDocument, "API", "Other", 1))}}
	rev.Document.Messages[0].Operation = &designscenario.OperationBinding{ContractID: "api", OperationKey: "status"}
	rev.FormDrafts["all"] = `{"/canvas-contract/api-other/x":{"source":"x","propertySource":""}}`
	svc := New(validContract, 1<<20)
	a, err := svc.Export(rev, Request{Format: OpenAPIJSON, ContractID: "api"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Content != apiDocument {
		t.Fatal("contract bytes altered")
	}
	a, err = svc.Export(rev, Request{Format: OpenAPIYAML, ContractID: "api"})
	if err != nil {
		t.Fatal(err)
	}
	back, err := yamlx.ToJSON([]byte(a.Content))
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"9007199254740993", `"security"`, `"$ref"`, `"x-author"`} {
		if !strings.Contains(string(back), part) {
			t.Fatalf("lost %s: %s", part, back)
		}
	}
	_, err = svc.Export(rev, Request{Format: OpenAPIJSON, ContractID: "api-other"})
	if _, ok := errors.AsType[*BlockedError](err); !ok {
		t.Fatalf("pending form exported: %v", err)
	}
}

func TestExportErrorsAndLimits(t *testing.T) {
	rev := revisionFixture()
	svc := New(validContract, 1<<20)
	if _, err := svc.Export(rev, Request{Format: "unknown"}); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatal(err)
	}
	if _, err := svc.Export(rev, Request{Format: OpenAPIJSON, ContractID: "missing"}); !errors.Is(err, ErrContractNotFound) {
		t.Fatal(err)
	}
	if _, err := New(nil, 20).Export(rev, Request{Format: Mermaid}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("limit: %v", err)
	}
	rev.Document.Messages[0].ToID = "missing"
	if _, err := svc.Export(rev, Request{Format: Mermaid}); err == nil {
		t.Fatal("dangling participant accepted")
	}
}

func TestContractValidatorErrorsAreNotHidden(t *testing.T) {
	rev := revisionFixture()
	rev.Document.Contracts = []designscenario.Contract{{ID: "api", Document: jsonx.RawMessage(apiDocument)}}
	cause := errors.New("validator infrastructure")
	svc := New(func(string) ([]designscenario.Diagnostic, error) { return nil, cause }, 1<<20)
	if _, err := svc.Options(rev); !errors.Is(err, cause) {
		t.Fatalf("hidden error: %v", err)
	}
}

func TestBindingCannotResolveToSchemaMetadata(t *testing.T) {
	rev := revisionFixture()
	rev.Document.Contracts = []designscenario.Contract{{ID: "api", Document: jsonx.RawMessage(`{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{},"components":{"schemas":{"X":{"x-mocker-canvas-operation-id":"fake"}}}}`)}}
	rev.Document.Messages[0].Operation = &designscenario.OperationBinding{ContractID: "api", OperationKey: "fake"}
	svc := New(validContract, 1<<20)
	_, err := svc.Export(rev, Request{Format: OpenAPIJSON, ContractID: "api"})
	if _, ok := errors.AsType[*BlockedError](err); !ok {
		t.Fatalf("schema metadata resolved as operation: %v", err)
	}
	artifact, err := svc.Export(rev, Request{Format: Mermaid})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(artifact.Diagnostics[0].Code, "binding_missing") {
		t.Fatal("missing binding warning", artifact.Diagnostics)
	}
}

func TestOptionsStopsBeforeValidatingEveryContractWhenBudgetExhausted(t *testing.T) {
	rev := revisionFixture()
	for range 200 {
		rev.Document.Contracts = append(rev.Document.Contracts, designscenario.Contract{ID: "api", Document: []byte(apiDocument)})
	}
	calls := 0
	_, err := New(func(string) ([]designscenario.Diagnostic, error) { calls++; return nil, nil }, 1000).Options(rev)
	if !errors.Is(err, ErrTooLarge) || calls >= 200 {
		t.Fatalf("err=%v validations=%d", err, calls)
	}
}

func TestMermaidEscapesFormattingPrefixes(t *testing.T) {
	for _, value := range []string{"wrap:test", "nowrap:test"} {
		if strings.Contains(mermaidText(value), ":") {
			t.Fatalf("unescaped instruction: %s", mermaidText(value))
		}
	}
}

func TestMermaidParserFixture(t *testing.T) {
	rev := revisionFixture()
	rev.Document.Participants[0].Name = "wrap:Клиент"
	rev.Document.Participants[1].Name = "nowrap:API"
	rev.Document.Messages[0].Label = "wrap:GET /status"
	rev.Document.Messages[0].Description = "nowrap:описание\n<script>"
	rev.Document.Messages = append(rev.Document.Messages, designscenario.Message{ID: "note", FromID: "api", ToID: "api", Kind: "note", Label: "nowrap:note"}, designscenario.Message{ID: "event", FromID: "api", ToID: "api", Kind: "event", Label: "event"})
	rev.Document.Fragments = []designscenario.Fragment{{ID: "outer", Kind: "opt", Label: "условие", FromMessageID: "call", ToMessageID: "reply"}, {ID: "inner", Kind: "loop", Label: "повтор", FromMessageID: "call", ToMessageID: "call"}}
	result, err := New(nil, 1<<20).Export(rev, Request{Format: Mermaid})
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("testdata/sequence.mmd")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != string(fixture) {
		t.Fatalf("parser fixture drift:\n%s", result.Content)
	}
}
