package admin

import (
	"encoding/json/v2"
	bm "github.com/yashok111/mocker/internal/backendmodel"
	ob "github.com/yashok111/mocker/internal/backendobservations"
	"strings"
	"testing"
)

func TestBackendObservationReadAdmission(t *testing.T) {
	s := loopbackTestServer(t, nil)
	s.SetBackendObservations(&ob.Service{Repo: ob.NewRepo(s.db), Graphs: s.backendRepo})
	status, raw, e := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects", []byte(`{"name":"Observations","idempotencyKey":"ob-project"}`))
	var project bm.Project
	if e != nil || status != 201 || json.Unmarshal(raw, &project) != nil {
		t.Fatal(status, string(raw), e)
	}
	base := "/api/backend-projects/" + project.ID + "/observations"
	status, raw, e = s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", base, nil)
	if status != 200 || e != nil {
		t.Fatal(status, string(raw), e)
	}
	validateBackendImportResponse(t, "GET", "/api/backend-projects/{id}/observations", raw)
	for _, query := range []string{"?latest=true", "?limit=501", "?limit=1&limit=2"} {
		status, _, e = s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", base+query, nil)
		if e != nil || status != 400 {
			t.Fatal(query, status, e)
		}
	}
	for _, route := range s.routes() {
		if strings.Contains(route.pattern, "/observations") && route.mcp != mcpAllow {
			t.Fatal(route.pattern)
		}
	}
}
