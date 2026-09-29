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

type eventMapScenarioStub struct {
	designScenarioService
	reads *[]int64
}

func (s eventMapScenarioStub) Detail(_ context.Context, id int64) (*designscenario.Detail, error) {
	if id != 7 {
		return nil, designscenario.ErrNotFound
	}
	return &designscenario.Detail{Scenario: designscenario.Scenario{ID: 7, Version: 2}, Draft: designscenario.Revision{RevisionSummary: designscenario.RevisionSummary{ID: 12}}}, nil
}
func (s eventMapScenarioStub) EventMap(_ context.Context, id, rid int64) (designscenario.EventMapReport, error) {
	if rid == 0 {
		rid = 12
	}
	if id != 7 || (rid != 11 && rid != 12) {
		return designscenario.EventMapReport{}, designscenario.ErrNotFound
	}
	*s.reads = append(*s.reads, rid)
	return designscenario.EventMapReport{ScenarioID: id, Version: 2, RevisionID: rid, EventMapAnalysis: designscenario.EventMapAnalysis{Nodes: []designscenario.EventMapNode{}, Edges: []designscenario.EventMapEdge{}, Diagnostics: []designscenario.EventMapDiagnostic{}, Complete: true}}, nil
}

func TestEventMapAnalysisReadsExactRevisionAndNeverWrites(t *testing.T) {
	t.Parallel()
	s := exportTestServer()
	reads := []int64{}
	s.designScenariosRepo = eventMapScenarioStub{reads: &reads}
	for _, tt := range []struct {
		query string
		want  int64
	}{{"", 12}, {"?revisionId=11", 11}} {
		r := httptest.NewRequest(http.MethodGet, "/api/design-scenarios/7/event-map"+tt.query, nil)
		r.SetPathValue("id", "7")
		r = r.WithContext(withAuthContext(r.Context(), nil, &auth.User{ID: 1}))
		w := httptest.NewRecorder()
		s.handleGetDesignScenarioEventMap(w, r)
		if w.Code != 200 || len(reads) == 0 || reads[len(reads)-1] != tt.want {
			t.Fatalf("wrong revision: status=%d reads=%v body=%s", w.Code, reads, w.Body.String())
		}
	}
	body := `{"document":{"formatVersion":1,"title":"unsaved","participants":[],"messages":[],"fragments":[],"contracts":[]}}`
	r := httptest.NewRequest(http.MethodPost, "/api/design-scenarios/7/event-map", strings.NewReader(body))
	r.SetPathValue("id", "7")
	r = r.WithContext(withAuthContext(r.Context(), nil, &auth.User{ID: 1}))
	w := httptest.NewRecorder()
	s.handleAnalyzeDesignScenarioEventMap(w, r)
	// The stub has no write methods: any save would panic. It also rejects revision reads for the POST.
	if w.Code != 200 || len(reads) != 2 || (!strings.Contains(w.Body.String(), `"proposed":true`) || strings.Contains(w.Body.String(), `"revisionId":`)) {
		t.Fatalf("analysis: %d %s reads=%v", w.Code, w.Body.String(), reads)
	}
	detail, err := s.designScenariosRepo.Detail(context.Background(), 7)
	if err != nil || detail.Scenario.Version != 2 || detail.Draft.ID != 12 {
		t.Fatalf("analysis changed saved state: %+v %v", detail, err)
	}
}

func TestEventMapAnalysisRejectsInvalidAndUnauthenticatedRequests(t *testing.T) {
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
			s.designScenariosRepo = eventMapScenarioStub{reads: &reads}
			r := httptest.NewRequest(tt.method, "/api/design-scenarios/"+tt.id+"/event-map"+tt.query, strings.NewReader(tt.body))
			r.SetPathValue("id", tt.id)
			if tt.auth {
				r = r.WithContext(withAuthContext(r.Context(), nil, &auth.User{ID: 1}))
			}
			w := httptest.NewRecorder()
			if tt.method == "GET" {
				s.handleGetDesignScenarioEventMap(w, r)
			} else {
				s.handleAnalyzeDesignScenarioEventMap(w, r)
			}
			if w.Code != tt.status {
				t.Fatalf("want %d got %d: %s", tt.status, w.Code, w.Body.String())
			}
		})
	}
}

func TestEventMapRoutesKeepReadOnlyCheckpointAndMCPAccess(t *testing.T) {
	t.Parallel()
	want := map[string]checkpointGroup{"GET /api/design-scenarios/{id}/event-map": cpGroupRead, "POST /api/design-scenarios/{id}/event-map": cpGroupNeverTouchesLayer}
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
