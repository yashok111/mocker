package admin

import (
	"encoding/json/v2"
	"os"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendBusinessMapRESTAdmission(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Business", IdempotencyKey: "project"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../backendmodel/testdata/diagrams/business_map.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["target"] = map[string]any{"revisionId": p.CurrentRevisionID}
	call := func(input any, want int) []byte {
		t.Helper()
		b, _ := json.Marshal(input)
		status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects/"+p.ID+"/diagrams", b)
		if err != nil || status != want {
			t.Fatalf("%d %s %v", status, body, err)
		}
		return body
	}
	input := map[string]any{"document": doc, "idempotencyKey": "valid"}
	body := call(input, 200)
	var v backendmodel.DiagramVersion
	if err = json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	if v.Document.Kind != "business_map" || len(v.Gaps) == 0 {
		t.Fatal("lost map or gaps")
	}
	input["idempotencyKey"] = "bad"
	payload := doc["payload"].(map[string]any)
	payload["unknown"] = true
	call(input, 422)
	delete(payload, "unknown")
	payload["elements"] = nil
	call(input, 422)
	status, b, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", "/api/backend-projects/"+p.ID+"/diagrams?kind=business_map", nil)
	if err != nil || status != 200 {
		t.Fatalf("list: %d %s %v", status, b, err)
	}
}
