package admin

import (
	"encoding/json/v2"
	"os"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendInteractionsREST(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Interactions", IdempotencyKey: "p"})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/backend-projects/" + p.ID
	raw, err := os.ReadFile("../backendmodel/testdata/diagrams/interaction.json")
	if err != nil {
		t.Fatal(err)
	}
	var d backendmodel.DiagramDocument
	if err = json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	d.Target = backendmodel.BackendReadTarget{RevisionID: p.CurrentRevisionID}
	call := func(path string, input any, want int) []byte {
		t.Helper()
		b, _ := json.Marshal(input)
		status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", base+path, b)
		if err != nil || status != want {
			t.Fatalf("%s: %d %s %v", path, status, body, err)
		}
		return body
	}
	created := call("/diagrams", backendmodel.DiagramCreateInput{Document: d, IdempotencyKey: "create"}, 200)
	var v backendmodel.DiagramVersion
	if err = json.Unmarshal(created, &v); err != nil {
		t.Fatal(err)
	}
	call("/diagrams/query", backendmodel.DiagramQueryInput{Pin: v.Pin, Section: "links", Origin: "all", Limit: 100}, 200)
	call("/diagrams/interactions/build", map[string]any{"target": d.Target, "entrypointId": p.ID, "maxSteps": 0}, 422)
	call("/diagrams/interactions/build", map[string]any{"target": d.Target, "entrypointId": p.ID, "unknown": true}, 422)
	call("/diagrams/interactions/build", map[string]any{"target": nil, "entrypointId": p.ID}, 422)
}

func TestBackendInteractionsUnsupportedScopeCode(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Unsupported target", IdempotencyKey: "p"})
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"target": map[string]any{"proposal": map[string]any{"proposalId": p.ID, "proposalRevisionId": p.CurrentRevisionID}}, "entrypointId": p.ID, "maxSteps": 1}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects/"+p.ID+"/diagrams/interactions/build", raw)
	if err != nil || status != 422 {
		t.Fatalf("%d %s %v", status, body, err)
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err = json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Code != "backend_unsupported_scope" {
		t.Fatalf("scope failure lost domain code: %s", body)
	}
}
