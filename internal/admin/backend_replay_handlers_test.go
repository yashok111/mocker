package admin

import (
	"encoding/json/v2"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/backendreplay"
	"strings"
	"testing"
)

func TestBackendReplayReadAdmission(t *testing.T) {
	s := loopbackTestServer(t, nil)
	s.SetBackendReplay(backendreplay.NewService(backendreplay.NewRepo(s.db), s.backendRepo, nil))
	var project backendmodel.Project
	status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects", []byte(`{"name":"Replay","idempotencyKey":"replay-project"}`))
	if err != nil || status != 201 || json.Unmarshal(raw, &project) != nil {
		t.Fatal(status, string(raw), err)
	}
	base := "/api/backend-projects/" + project.ID + "/replay"
	for _, suffix := range []string{"targets", "template", "profiles", "packages", "runs"} {
		status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", base+"/"+suffix, nil)
		if err != nil || status != 200 {
			t.Fatal(suffix, status, string(raw), err)
		}
		validateBackendImportResponse(t, "GET", "/api/backend-projects/{id}/replay/"+suffix, raw)
	}
	for _, request := range []struct {
		path string
		body []byte
	}{{base + "/runs?latest=true", nil}, {base + "/runs", []byte(`{}`)}} {
		status, _, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", request.path, request.body)
		if err != nil || status != 400 {
			t.Fatal("open query/body accepted", status, err)
		}
	}
	for _, route := range s.routes() {
		if strings.Contains(route.pattern, "/replay/") && route.mcp != mcpAllow {
			t.Fatal("MCP missing", route.pattern)
		}
	}
}
