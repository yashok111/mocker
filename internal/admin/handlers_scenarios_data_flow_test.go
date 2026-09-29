package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/designscenario"
)

type dataFlowScenarioStub struct {
	designScenarioService
	reads *[]int64
}

func (s dataFlowScenarioStub) Detail(_ context.Context, id int64) (*designscenario.Detail, error) {
	if id != 7 {
		return nil, designscenario.ErrNotFound
	}
	return &designscenario.Detail{Scenario: designscenario.Scenario{ID: 7, Version: 2}, Draft: designscenario.Revision{RevisionSummary: designscenario.RevisionSummary{ID: 12}}}, nil
}
func (s dataFlowScenarioStub) Revision(_ context.Context, id, rid int64) (designscenario.Revision, error) {
	if id != 7 || (rid != 11 && rid != 12) {
		return designscenario.Revision{}, designscenario.ErrNotFound
	}
	*s.reads = append(*s.reads, rid)
	doc := designscenario.Document{FormatVersion: 1, Title: "test", Participants: []designscenario.Participant{}, Messages: []designscenario.Message{}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{}}
	doc.Messages = append(doc.Messages, designscenario.Message{ID: map[int64]string{11: "historical", 12: "current"}[rid], Kind: "request", Operation: &designscenario.OperationBinding{ContractID: "missing", OperationKey: "get"}})
	return designscenario.Revision{RevisionSummary: designscenario.RevisionSummary{ID: rid}, Document: doc}, nil
}

func TestDataFlowAnalysisReadsExactRevisionAndNeverWrites(t *testing.T) {
	t.Parallel()
	s := exportTestServer()
	reads := []int64{}
	s.designScenariosRepo = dataFlowScenarioStub{reads: &reads}
	for _, tt := range []struct {
		query string
		want  int64
	}{{"", 12}, {"?revisionId=11", 11}} {
		r := httptest.NewRequest(http.MethodGet, "/api/design-scenarios/7/data-flow"+tt.query, nil)
		r.SetPathValue("id", "7")
		r = r.WithContext(withAuthContext(r.Context(), nil, &auth.User{ID: 1}))
		w := httptest.NewRecorder()
		s.handleGetDesignScenarioDataFlow(w, r)
		if w.Code != 200 || len(reads) == 0 || reads[len(reads)-1] != tt.want {
			t.Fatalf("wrong revision: status=%d reads=%v body=%s", w.Code, reads, w.Body.String())
		}
	}
	body := `{"document":{"formatVersion":1,"title":"unsaved","participants":[],"messages":[],"fragments":[],"contracts":[]}}`
	r := httptest.NewRequest(http.MethodPost, "/api/design-scenarios/7/data-flow", strings.NewReader(body))
	r.SetPathValue("id", "7")
	r = r.WithContext(withAuthContext(r.Context(), nil, &auth.User{ID: 1}))
	w := httptest.NewRecorder()
	s.handleAnalyzeDesignScenarioDataFlow(w, r)
	// The stub has no write methods: any save would panic. It also rejects revision reads for the POST.
	if w.Code != 200 || len(reads) != 2 || !strings.Contains(w.Body.String(), `"diagnostics":`) {
		t.Fatalf("analysis: %d %s reads=%v", w.Code, w.Body.String(), reads)
	}
	detail, err := s.designScenariosRepo.Detail(context.Background(), 7)
	if err != nil || detail.Scenario.Version != 2 || detail.Draft.ID != 12 {
		t.Fatalf("analysis changed saved state: %+v %v", detail, err)
	}
}

func TestDataFlowAnalysisRejectsInvalidAndUnauthenticatedRequests(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		method, id, query, body string
		auth                    bool
		status                  int
	}{
		{"GET", "7", "?revisionId=0", "", true, 400}, {"GET", "7", "?revisionId=", "", true, 400}, {"GET", "7", "?revisionId=11&revisionId=12", "", true, 400},
		{"GET", "7", "?revisionId=99", "", true, 404}, {"GET", "9", "", "", true, 404}, {"GET", "7", "", "", false, 401},
		{"POST", "7", "", `{}`, true, 400}, {"POST", "7", "", `null`, true, 400}, {"POST", "7", "", `{"document":null}`, true, 400},
		{"POST", "7", "", `{"document":{},"unknown":true}`, true, 400}, {"POST", "7", "", `{`, true, 400},
		{"POST", "9", "", `{"document":{}}`, true, 404}, {"POST", "7", "", `{"document":{}}`, false, 401},
	} {
		t.Run(tt.method+tt.query+tt.body+tt.id, func(t *testing.T) {
			t.Parallel()
			s := exportTestServer()
			reads := []int64{}
			s.designScenariosRepo = dataFlowScenarioStub{reads: &reads}
			r := httptest.NewRequest(tt.method, "/api/design-scenarios/"+tt.id+"/data-flow"+tt.query, strings.NewReader(tt.body))
			r.SetPathValue("id", tt.id)
			if tt.auth {
				r = r.WithContext(withAuthContext(r.Context(), nil, &auth.User{ID: 1}))
			}
			w := httptest.NewRecorder()
			if tt.method == "GET" {
				s.handleGetDesignScenarioDataFlow(w, r)
			} else {
				s.handleAnalyzeDesignScenarioDataFlow(w, r)
			}
			if w.Code != tt.status {
				t.Fatalf("want %d got %d: %s", tt.status, w.Code, w.Body.String())
			}
		})
	}
}

func TestDataBindingDirectExecutionRequiresRun(t *testing.T) {
	t.Parallel()
	revision := designscenario.Revision{Document: designscenario.Document{Messages: []designscenario.Message{{ID: "get", Kind: "request", Operation: &designscenario.OperationBinding{ContractID: "api"}, Execution: &designscenario.StepExecution{Enabled: true, Bindings: []designscenario.DataBinding{{ID: "id"}}}}}, Contracts: []designscenario.Contract{{ID: "api", Mode: "linked", Source: &designscenario.ContractSource{DesignID: 1, RevisionID: 1}}}}}
	if _, err := executableContract(revision, "get", false); err == nil {
		t.Fatal("direct step accepted unresolved binding")
	}
	if _, err := executableContract(revision, "get", true); err != nil {
		t.Fatalf("runner resolved step rejected: %v", err)
	}
}

func TestDataFlowRoutesKeepReadOnlyCheckpointAndMCPAccess(t *testing.T) {
	t.Parallel()
	want := map[string]checkpointGroup{"GET /api/design-scenarios/{id}/data-flow": cpGroupRead, "POST /api/design-scenarios/{id}/data-flow": cpGroupNeverTouchesLayer}
	for _, route := range (&Server{}).routes() {
		group, ok := want[route.pattern]
		if !ok {
			continue
		}
		if route.checkpoint.group != group || route.mcp != mcpAllow {
			t.Errorf("wrong route policy for %s: %+v", route.pattern, route)
		}
		delete(want, route.pattern)
	}
	if len(want) > 0 {
		t.Fatalf("missing routes: %v", want)
	}
}
