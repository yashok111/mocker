package admin

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/testauth"
)

func TestBackendB41PublicRoutePolicies(t *testing.T) {
	want := map[string]checkpointPolicy{
		"GET /api/backend-projects/{id}/change-proposals":                                    cpRead,
		"POST /api/backend-projects/{id}/change-proposals":                                   cpAnotherLayer,
		"GET /api/backend-projects/{id}/change-proposals/{pid}":                              cpRead,
		"POST /api/backend-projects/{id}/change-proposals/{pid}/preview":                     cpNeverTouchesLayer,
		"POST /api/backend-projects/{id}/change-proposals/{pid}/commands":                    cpAnotherLayer,
		"POST /api/backend-projects/{id}/change-proposals/{pid}/restore":                     cpAnotherLayer,
		"GET /api/backend-projects/{id}/revisions/{rid}/assertions":                          cpRead,
		"GET /api/backend-projects/{id}/change-proposals/{pid}/revisions/{prid}/assertions":  cpRead,
		"GET /api/backend-projects/{id}/imports/{iid}/candidate/assertions":                  cpRead,
		"GET /api/backend-projects/{id}/change-proposals/{pid}/revisions/{prid}/nodes/{nid}": cpRead,
		"GET /api/backend-projects/{id}/change-proposals/{pid}/revisions/{prid}/evidence":    cpRead,
		"GET /api/backend-projects/{id}/change-proposals/{pid}/revisions/{prid}/coverage":    cpRead,
		"GET /api/backend-projects/{id}/imports/{iid}/candidate/nodes/{nid}":                 cpRead,
		"GET /api/backend-projects/{id}/imports/{iid}/candidate/evidence":                    cpRead,
		"GET /api/backend-projects/{id}/imports/{iid}/candidate/coverage":                    cpRead,
	}
	s := loopbackTestServer(t, nil)
	for _, route := range s.routes() {
		policy, ok := want[route.pattern]
		if !ok {
			continue
		}
		if route.checkpoint != policy || route.mcp != mcpAllow {
			t.Errorf("wrong route policy: %+v", route)
		}
		response := httptest.NewRecorder()
		route.handler(response, httptest.NewRequest(http.MethodGet, "/", nil))
		if response.Code != http.StatusUnauthorized {
			t.Errorf("route lacks auth: %s: %d", route.pattern, response.Code)
		}
		delete(want, route.pattern)
	}
	if len(want) != 0 {
		t.Fatalf("missing B4.1 public routes: %v", want)
	}
}

func TestBackendB41PublicMutationCSRF(t *testing.T) {
	s := loopbackTestServer(t, nil)
	handler := s.Handler()
	login := httptest.NewRequest(http.MethodPost, "http://mocker.local/api/auth/login", strings.NewReader(`{"name":"b41-public","password":"`+testauth.Password+`"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "http://mocker.local")
	signed := httptest.NewRecorder()
	handler.ServeHTTP(signed, login)
	if signed.Code != 200 {
		t.Fatalf("login %d %s", signed.Code, signed.Body)
	}
	var auth struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(signed.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	const id = "0197aaf9-5555-7000-8000-000000000001"
	base := "/api/backend-projects/" + id + "/change-proposals"
	for _, suffix := range []string{"", "/" + id + "/preview", "/" + id + "/commands", "/" + id + "/restore"} {
		for _, tc := range []struct {
			token, origin string
			want          int
		}{{"", "http://mocker.local", 403}, {"wrong", "http://mocker.local", 403}, {auth.CSRFToken, "http://attacker.local", 403}, {auth.CSRFToken, "http://mocker.local", 400}} {
			r := httptest.NewRequest(http.MethodPost, "http://mocker.local"+base+suffix, strings.NewReader(`{}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-CSRF-Token", tc.token)
			for _, cookie := range signed.Result().Cookies() {
				r.AddCookie(cookie)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("%s: %d %s", suffix, w.Code, w.Body)
			}
		}
	}
}

func TestBackendB41StrictPublicAdmission(t *testing.T) {
	s := loopbackTestServer(t, nil)
	const id = "0197aaf9-5555-7000-8000-000000000001"
	base := "/api/backend-projects/" + id
	pin := `"importCandidate":{"importId":"` + id + `","importVersion":1,"candidateHash":"` + strings.Repeat("a", 64) + `"}`
	for _, tc := range []struct{ path, fields string }{
		{"flow", `"view":"entrypoints"`},
		{"events", `"view":"jobs"`},
		{"lineage", `"seed":{"kind":"api_field","nodeId":"` + id + `"},"direction":"forward"`},
		{"database", `"datastoreId":"` + id + `","facetKey":"sql","recordType":"tables"`},
		{"api-artifacts", ``},
		{"artifacts", `"artifact":{"kind":"api_design","id":"1"},"view":"states"`},
	} {
		body := `{` + pin
		if tc.fields != "" {
			body += `,` + tc.fields
		}
		body += `}`
		b41Call(t, s, "POST", base+"/"+tc.path+"/query", body, 422, nil)
	}
	for _, query := range []string{"?limit=01", "?limit=1e0", "?limit=1&limit=2", "?cursor=" + strings.Repeat("x", 1025)} {
		b41Call(t, s, "GET", base+"/change-proposals"+query, nil, 400, nil)
	}
	b41Call(t, s, "GET", base+"/change-proposals", `{}`, 400, nil)
	for _, body := range []string{
		`null`, `{}`, `{"name":"X","baseRevisionId":"` + id + `","idempotencyKey":"key","name":"Y"}`,
		`{"name":"X","baseRevisionId":null,"idempotencyKey":"key"}`,
	} {
		b41Call(t, s, "POST", base+"/change-proposals", body, 400, nil)
	}
	for _, version := range []string{"0", "1.0", "1e0", "9223372036854775808", "null"} {
		body := `{"expectedVersion":` + version + `,"proposalRevisionId":"` + id + `","restoreRevisionId":"` + id + `","idempotencyKey":"key"}`
		b41Call(t, s, "POST", base+"/change-proposals/"+id+"/restore", body, 400, nil)
	}
}
