package admin

import (
	"encoding/json/v2"
	"github.com/yashok111/mocker/internal/backendmodel"
	"testing"
)

func TestBackendLifecycleRESTAdmission(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Lifecycle", IdempotencyKey: "p"})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []any{map[string]any{"target": nil}, map[string]any{"target": map[string]any{"revisionId": p.CurrentRevisionID}, "stateDiagram": nil, "entity": nil, "stateFields": []any{}}, map[string]any{"unknown": true}} {
		raw, _ := json.Marshal(input)
		status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects/"+p.ID+"/diagrams/lifecycle/build", raw)
		if err != nil || status != 422 {
			t.Fatalf("%d %s %v", status, body, err)
		}
	}
}
