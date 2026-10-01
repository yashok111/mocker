package admin

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/guide"
)

func TestBackendRoutesRequireAuthentication(t *testing.T) {
	s := loopbackTestServer(t, nil)
	for _, route := range s.routes() {
		if !strings.Contains(route.pattern, "/api/backend-projects") {
			continue
		}
		t.Run(route.pattern, func(t *testing.T) {
			method, path, _ := strings.Cut(route.pattern, " ")
			rec := httptest.NewRecorder()
			route.handler(rec, httptest.NewRequest(method, path, nil))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated request got %d: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestBackendCapabilitiesAdvertiseUsableWorkflow(t *testing.T) {
	s := loopbackTestServer(t, nil)
	status, data, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", "/api/backend-projects/capabilities", nil)
	if err != nil || status != 200 {
		t.Fatalf("capabilities: %d %s %v", status, data, err)
	}
	validateBackendImportResponse(t, "GET", "/api/backend-projects/capabilities", data)
	var caps struct {
		Features   []string         `json:"features"`
		Workflows  []guide.Workflow `json:"workflowVersions"`
		GuideSetID string           `json:"guideSetId"`
		Limits     struct {
			MaxBodyBytes int64 `json:"maxBodyBytes"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(data, &caps); err != nil {
		t.Fatal(err)
	}
	if caps.GuideSetID != guide.CurrentGuideSetID() || len(caps.Workflows) == 0 || caps.Limits.MaxBodyBytes != s.cfg.MaxBody {
		t.Fatalf("discovery mismatch: %s", data)
	}
	for _, workflow := range caps.Workflows {
		if _, ok := guide.Topic(workflow.Entrypoint); !ok {
			t.Fatalf("unavailable workflow entrypoint: %s", workflow.Entrypoint)
		}
		for _, required := range workflow.RequiredCapabilities {
			found := false
			for _, feature := range caps.Features {
				if required == feature {
					found = true
				}
			}
			if !found {
				t.Fatalf("workflow requires unavailable feature %q", required)
			}
		}
	}
}

func TestBackendProjectRoutes(t *testing.T) {
	s := loopbackTestServer(t, nil)
	call := func(method, path, input string, want int) []byte {
		t.Helper()
		status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, []byte(input))
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v; want %d", method, path, status, body, err, want)
		}
		return body
	}
	body := call("POST", "/api/backend-projects", `{"name":"Orders","idempotencyKey":"create"}`, 201)
	var p backendmodel.Project
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	if p.Version != 1 || !backendmodel.ValidID(p.ID) {
		t.Fatalf("project: %s", body)
	}
	base := "/api/backend-projects/" + p.ID
	call("GET", base, "", 200)
	call("GET", base+"/revisions", "", 200)
	rev := call("GET", base+"/revisions/"+p.CurrentRevisionID, "", 200)
	if !strings.Contains(string(rev), `"denominator":null`) {
		t.Fatalf("unknown coverage lost: %s", rev)
	}
	input := `{"expectedVersion":1,"idempotencyKey":"rename","commands":[{"type":"rename_project","name":"Shipping"}]}`
	first := call("POST", base+"/commands", input, 200)
	replay := call("POST", base+"/commands", input, 200)
	if string(first) != string(replay) {
		t.Fatalf("receipt replay changed: %s vs %s", first, replay)
	}
	conflict := call("POST", base+"/commands", strings.Replace(input, `"rename"`, `"other"`, 1), 409)
	if !strings.Contains(string(conflict), `"currentVersion":2`) || !strings.Contains(string(conflict), `"retryable":false`) {
		t.Fatalf("conflict recovery missing: %s", conflict)
	}
	if initial := call("POST", "/api/backend-projects", `{"name":"Orders","idempotencyKey":"create"}`, 201); string(initial) != string(body) {
		t.Fatal("creation replay changed")
	}
	call("GET", "/api/backend-projects?limit=1", "", 200)
	call("GET", "/api/backend-projects?limit=zero", "", 400)
	call("GET", "/api/backend-projects?limit=0", "", 400)
	call("GET", "/api/backend-projects?limit=101", "", 400)
	call("GET", "/api/backend-projects?limit=1&limit=2", "", 400)
	call("GET", "/api/backend-projects?unknown=true", "", 400)
	call("GET", "/api/backend-projects/missing", "", 404)
	for _, input := range []string{`null`, `{"name":"n","idempotencyKey":"x","unexpected":true}`, `{"name":"n","idempotencyKey":"x"} {}`, `{"name":"a","name":"b","idempotencyKey":"x"}`} {
		call("POST", "/api/backend-projects", input, 400)
	}
	s.cfg.MaxBody = 30
	call("POST", "/api/backend-projects", fmt.Sprintf(`{"name":"%s","idempotencyKey":"large"}`, strings.Repeat("x", 40)), http.StatusRequestEntityTooLarge)
}

func TestBackendVersionRemainsExactOverREST(t *testing.T) {
	s := loopbackTestServer(t, nil)
	status, data, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects", []byte(`{"name":"Exact","idempotencyKey":"precise"}`))
	if status != 201 || err != nil {
		t.Fatalf("create: %d %s %v", status, data, err)
	}
	var p backendmodel.Project
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.W.ExecContext(t.Context(), "UPDATE backend_projects SET version=? WHERE id=?", int64(9007199254740993), p.ID); err != nil {
		t.Fatal(err)
	}
	status, data, err = s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects/"+p.ID+"/commands", []byte(`{"expectedVersion":9007199254740993,"idempotencyKey":"precise-rename","commands":[{"type":"rename_project","name":"Still exact"}]}`))
	if err != nil || status != 200 || !strings.Contains(string(data), `"version":9007199254740994`) {
		t.Fatalf("rounded version: %d %s %v", status, data, err)
	}
}

func TestBackendCapabilitiesExposeReconciliationContract(t *testing.T) {
	s := loopbackTestServer(t, nil)
	status, data, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", "/api/backend-projects/capabilities", nil)
	if err != nil || status != 200 {
		t.Fatalf("capabilities: %d %s %v", status, data, err)
	}
	var got struct {
		ImportModes           []string `json:"importModes"`
		ImportCommands        []string `json:"importCommands"`
		ReconciliationProfile struct {
			Version string `json:"version"`
			Profile string `json:"profile"`
			Scope   string `json:"scope"`
		} `json:"reconciliationProfile"`
		ComparisonVersion int64 `json:"comparisonVersion"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.ImportModes, ",") != "initial,reconcile" || strings.Join(got.ImportCommands, ",") != "upsert_node,upsert_edge,upsert_evidence,remove,map_identity,delete_assertion" || got.ReconciliationProfile.Version != "1" || got.ReconciliationProfile.Profile != backendmodel.GraphProfile || got.ReconciliationProfile.Scope != "whole-repository" || got.ComparisonVersion != 1 {
		t.Fatalf("missing reconciliation contract: %s", data)
	}
}
