package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/scenarioexport"
)

func asyncAPIExportTestServer(t *testing.T) *Server {
	t.Helper()
	raw, err := os.ReadFile("../designscenario/testdata/events/valid.json")
	if err != nil {
		t.Fatal(err)
	}
	var document designscenario.Document
	if err := jsonx.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	s := exportTestServer()
	s.designScenariosRepo = exportScenarioStub{revision: designscenario.Revision{
		RevisionSummary: designscenario.RevisionSummary{ID: 11, ScenarioID: 7, Version: 2, Hash: "event-snapshot"},
		Document:        document, FormDrafts: map[string]string{},
	}}
	return s
}

func TestAsyncAPIExportHTTPContractAndFailures(t *testing.T) {
	for _, tt := range []struct {
		name, format, contract, rid string
		auth                        bool
		status                      int
	}{
		{"producer JSON", "asyncapi-json", "ordersContract", "11", true, 200},
		{"consumer YAML", "asyncapi-yaml", "notificationsContract", "11", true, 200},
		{"unauthorized", "asyncapi-json", "ordersContract", "11", false, 401},
		{"missing revision", "asyncapi-json", "ordersContract", "12", true, 404},
		{"missing contract", "asyncapi-json", "absent", "11", true, 404},
		{"required contract", "asyncapi-json", "", "11", true, 400},
		{"wrong family", "openapi-json", "ordersContract", "11", true, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := asyncAPIExportTestServer(t)
			r := exportRequest(tt.format, tt.rid, tt.auth)
			query := r.URL.Query()
			query.Set("contractId", tt.contract)
			r.URL.RawQuery = query.Encode()
			w := httptest.NewRecorder()
			s.handleExportDesignScenario(w, r)
			if w.Code != tt.status {
				t.Fatalf("status=%d want=%d: %s", w.Code, tt.status, w.Body.String())
			}
			if tt.status != http.StatusOK {
				return
			}
			var artifact scenarioexport.Artifact
			if err := jsonx.Unmarshal(w.Body.Bytes(), &artifact); err != nil {
				t.Fatal(err)
			}
			if artifact.SourceHash != "event-snapshot" || artifact.RevisionID != 11 || !strings.Contains(artifact.Content, "orders.events") {
				t.Fatalf("wrong snapshot: %+v", artifact)
			}
		})
	}
}

func TestAsyncAPIExportHTTPReadinessAndArchive(t *testing.T) {
	s := asyncAPIExportTestServer(t)
	options := httptest.NewRecorder()
	s.handleDesignScenarioExportOptions(options, exportRequest("asyncapi-json", "11", true))
	if options.Code != http.StatusOK || !strings.Contains(options.Body.String(), `"asyncapi-json"`) || !strings.Contains(options.Body.String(), `"notificationsContract"`) {
		t.Fatalf("missing event options: %d %s", options.Code, options.Body.String())
	}
	archive := httptest.NewRecorder()
	s.handleExportDesignScenarioArchive(archive, archiveRequest(`{"items":[{"format":"asyncapi-json","contractId":"ordersContract"},{"format":"asyncapi-yaml","contractId":"notificationsContract"}]}`, "11", true))
	if archive.Code != http.StatusOK {
		t.Fatalf("event archive: %d %s", archive.Code, archive.Body.String())
	}
	stub := s.designScenariosRepo.(exportScenarioStub)
	stub.revision.Document.EventModel.Messages[0].PayloadSchemaID = ""
	s.designScenariosRepo = stub
	request := exportRequest("asyncapi-json", "11", true)
	request.URL.RawQuery = "contractId=ordersContract"
	blocked := httptest.NewRecorder()
	s.handleExportDesignScenario(blocked, request)
	if blocked.Code != http.StatusUnprocessableEntity || !strings.Contains(blocked.Body.String(), "event_payload_missing") {
		t.Fatalf("incomplete payload: %d %s", blocked.Code, blocked.Body.String())
	}
	s.cfg.MaxBody = 10
	limited := httptest.NewRecorder()
	s.handleExportDesignScenario(limited, request)
	if limited.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("diagnostic budget: %d %s", limited.Code, limited.Body.String())
	}
}

func TestAsyncAPIExportHTTPInvalidExample(t *testing.T) {
	s := asyncAPIExportTestServer(t)
	stub := s.designScenariosRepo.(exportScenarioStub)
	stub.revision.Document.EventModel.Messages[0].Examples[0].PayloadJSON = `{"orderId":123}`
	s.designScenariosRepo = stub
	request := exportRequest("asyncapi-json", "11", true)
	request.URL.RawQuery = "contractId=ordersContract"
	response := httptest.NewRecorder()
	s.handleExportDesignScenario(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "event_example_invalid") {
		t.Fatalf("invalid payload example: %d %s", response.Code, response.Body.String())
	}
}
