package admin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestScenarioTestSuggestionsReadOnlyRevisionAndValidation(t *testing.T) {
	s, scenario, _ := executionFixture(t, "/items")
	document := scenario.Draft.Document
	document.FormatVersion = 2
	id := document.Messages[0].ID
	document.Fragments = []designscenario.Fragment{{ID: "opt", Kind: "opt", FromMessageID: id, ToMessageID: id, Execution: &designscenario.FragmentExecution{Condition: &designscenario.ExecutionCondition{Variable: "flag", Operator: "exists"}}}}
	next, err := s.designScenariosRepo.Create(t.Context(), designscenario.CreateInput{Document: document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		query         string
		authenticated bool
		status        int
	}{
		{"", true, 200}, {fmt.Sprintf("?revisionId=%d", next.Draft.ID), true, 200},
		{fmt.Sprintf("?revisionId=%d", scenario.Draft.ID), true, 404},
		{"?revisionId=0", true, 400}, {"?revisionId=", true, 400}, {"?revisionId=1&revisionId=2", true, 400}, {"", false, 401},
	} {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/design-scenarios/%d/test-suggestions%s", next.Scenario.ID, tt.query), nil)
		req.SetPathValue("id", fmt.Sprint(next.Scenario.ID))
		if tt.authenticated {
			req = req.WithContext(withAuthContext(req.Context(), nil, &auth.User{ID: 1}))
		}
		w := httptest.NewRecorder()
		s.handleSuggestDesignScenarioTests(w, req)
		if w.Code != tt.status {
			t.Fatalf("%s: status %d want %d: %s", tt.query, w.Code, tt.status, w.Body.String())
		}
		if w.Code == 200 {
			var got designscenario.TestSuggestions
			if err := jsonx.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.RevisionID != next.Draft.ID || len(got.Cases) != 2 || got.RunCount != 0 {
				t.Fatalf("unexpected suggestions: %+v", got)
			}
		}
	}
	after, err := s.designScenariosRepo.Detail(t.Context(), next.Scenario.ID)
	if err != nil || after.Scenario.Version != next.Scenario.Version || after.Draft.ID != next.Draft.ID {
		t.Fatal("generation changed scenario")
	}
	runs, err := s.scenarioRuns.list(t.Context(), next.Scenario.ID)
	if err != nil || len(runs) != 0 {
		t.Fatal("generation executed scenario")
	}
}
func TestScenarioTestSuggestionsRouteIsReadOnly(t *testing.T) {
	for _, r := range (&Server{}).routes() {
		if r.pattern == "GET /api/design-scenarios/{id}/test-suggestions" {
			if r.checkpoint.group != cpGroupRead || r.mcp != mcpAllow {
				t.Fatalf("wrong policy: %+v", r)
			}
			return
		}
	}
	t.Fatal("route missing")
}
