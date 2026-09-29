package admin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestResourceMapHTTPRequiresSession(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/designs/1/resource-map", ""},
		{"POST", "/api/designs/1/resource-map/preview", `{}`},
		{"POST", "/api/designs/1/resource-map/commands", `{"expectedVersion":1,"commands":[]}`},
	} {
		req := httptest.NewRequest(tc.method, "http://mocker.local"+tc.path, strings.NewReader(tc.body))
		if tc.method == "POST" {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "http://mocker.local")
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without session: %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestResourceMapHTTPPreviewAndAtomicCommands(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	ctx := t.Context()
	src := loopbackTestSrc()
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		status, out, err := s.CallAsMCP(ctx, src, method, path, []byte(body))
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v", method, path, status, out, err)
		}
		return out
	}
	raw := call("POST", "/api/designs", `{"name":"Orders","document":"{\"openapi\":\"3.1.0\",\"info\":{\"title\":\"Orders\",\"version\":\"1\"},\"paths\":{\"/orders\":{\"get\":{\"responses\":{\"200\":{\"description\":\"OK\"}}}}},\"x-big\":9007199254740993}"}`, 201)
	var detail apidesign.Detail
	if err := jsonx.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/designs/%d/resource-map", detail.Design.ID)
	preview := call("POST", base+"/preview", `{"commands":[{"kind":"upsert_resource","resource":{"id":"custom","name":"Custom","service":"billing","description":"","operationKeys":[],"x":1,"y":2}}]}`, 200)
	if len(preview) == 0 {
		t.Fatal("empty preview")
	}
	var current struct {
		Version         int64 `json:"version"`
		ScenarioUsages  []any `json:"scenarioUsages"`
		UsagesTruncated bool  `json:"usagesTruncated"`
	}
	if err := jsonx.Unmarshal(call("GET", base, "", 200), &current); err != nil {
		t.Fatal(err)
	}
	if current.Version != 1 || current.ScenarioUsages == nil || current.UsagesTruncated {
		t.Fatalf("preview wrote or wrong usages: %+v", current)
	}
	call("POST", base+"/commands", `{"expectedVersion":1,"commands":[{"kind":"upsert_resource","resource":{"id":"custom","name":"Custom","service":"billing","description":"","operationKeys":[],"x":1,"y":2}}]}`, 200)
	call("POST", base+"/commands", `{"expectedVersion":1,"commands":[{"kind":"remove_resource","resourceId":"custom"}]}`, 409)
	call("POST", base+"/commands", `{"expectedVersion":2,"commands":[{"kind":"remove_resource","resourceId":"custom"},{"kind":"unknown"}]}`, 400)
	call("POST", base+"/commands", `{"expectedVersion":2,"commands":[]}`, 400)
	call("POST", base+"/commands", `{"commands":[{"kind":"remove_resource","resourceId":"custom"}]}`, 400)
	call("POST", base+"/preview", `{"commands":[{"kind":"move_resource","resourceId":"custom","x":1}]}`, 400)
	if err := jsonx.Unmarshal(call("GET", base, "", 200), &current); err != nil {
		t.Fatal(err)
	}
	if current.Version != 2 {
		t.Fatalf("failed batch changed version: %d", current.Version)
	}
}
