package admin

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/backendreplay"
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
	}{{base + "/runs?latest=true", nil}, {base + "/runs", []byte(`{}`)},
		// The paged lists take exactly one optional `cursor` (review
		// 2026-10-06, F123/F124); nothing else, and only on those three.
		{base + "/runs?cursor=a&cursor=b", nil}, {base + "/runs?cursor=", nil}, {base + "/runs?cursor=x&latest=1", nil},
		{base + "/runs?cursor=" + project.ID, nil}, {base + "/profiles?cursor=x", nil}, {base + "/packages?cursor=" + project.ID + ":0", nil},
		{base + "/targets?cursor=x", nil}} {
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
