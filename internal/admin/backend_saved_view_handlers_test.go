package admin

import (
	"bytes"
	"encoding/json/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yashok111/mocker/api"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/testauth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBackendSavedViewPublicAuthenticationAndCSRF(t *testing.T) {
	s := loopbackTestServer(t, nil)
	source, ids := proposalTransportFixture(t, s, "sqlite")
	handler := s.Handler()
	base := "/api/backend-projects/" + source.Project.ID + "/saved-views"
	call := func(method, path string, in any, cookie *http.Cookie, token, origin string, want int) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if in != nil {
			var err error
			raw, err = json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, "http://mocker.local"+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if token != "" {
			req.Header.Set("X-CSRF-Token", token)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec
	}
	in := backendmodel.CreateSavedViewInput{Name: "Public", Target: backendmodel.BackendReadTarget{RevisionID: source.Revision.ID}, State: backendmodel.SavedViewState{Database: &backendmodel.SavedDatabaseViewState{Kind: "database", Scope: backendmodel.SavedDatabaseViewScope{DatastoreID: ids["database:orders"], FacetKey: "sql"}, Positions: []backendmodel.SavedViewPosition{}, CollapsedGroupIDs: []string{}}}, IdempotencyKey: "public-create"}
	for _, route := range []struct{ method, path string }{{"GET", base}, {"POST", base}, {"GET", base + "/" + source.Project.ID}, {"POST", base + "/" + source.Project.ID + "/save"}} {
		call(route.method, route.path, in, nil, "", "http://mocker.local", 401)
	}
	login := call("POST", "/api/auth/login", map[string]string{"name": "saved-view-public", "password": testauth.Password}, nil, "", "http://mocker.local", 200)
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing session cookie")
	}
	cookie := cookies[0]
	var auth struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &auth); err != nil || auth.CSRFToken == "" {
		t.Fatal("missing CSRF token", err)
	}
	for _, token := range []string{"", "wrong"} {
		call("POST", base, in, cookie, token, "http://mocker.local", 403)
	}
	call("POST", base, in, cookie, auth.CSRFToken, "http://attacker.local", 403)
	created := call("POST", base, in, cookie, auth.CSRFToken, "http://mocker.local", 200)
	var view backendmodel.SavedView
	if err := json.Unmarshal(created.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	path := base + "/" + view.ID
	call("GET", base, nil, cookie, "", "", 200)
	call("GET", path, nil, cookie, "", "", 200)
	save := backendmodel.SaveSavedViewInput{Name: "Public saved", State: in.State, ExpectedVersion: 1, IdempotencyKey: "public-save"}
	for _, token := range []string{"", "wrong"} {
		call("POST", path+"/save", save, cookie, token, "http://mocker.local", 403)
	}
	call("POST", path+"/save", save, cookie, auth.CSRFToken, "http://attacker.local", 403)
	saved := call("POST", path+"/save", save, cookie, auth.CSRFToken, "http://mocker.local", 200)
	if err := json.Unmarshal(saved.Body.Bytes(), &view); err != nil || view.Version != 2 {
		t.Fatal("refused requests changed version", view.Version, err)
	}
}

func TestBackendSavedViewRoutePolicy(t *testing.T) {
	want := map[string]checkpointPolicy{"GET /api/backend-projects/{id}/saved-views": cpRead, "POST /api/backend-projects/{id}/saved-views": cpAnotherLayer, "GET /api/backend-projects/{id}/saved-views/{vid}": cpRead, "POST /api/backend-projects/{id}/saved-views/{vid}/save": cpAnotherLayer}
	for _, r := range (&Server{}).routes() {
		if p, ok := want[r.pattern]; ok {
			if r.checkpoint != p || r.mcp != mcpAllow {
				t.Fatal(r)
			}
			delete(want, r.pattern)
		}
	}
	if len(want) != 0 {
		t.Fatal("missing routes", want)
	}
}
func TestBackendSavedViewRESTLifecycleAndStrictQueries(t *testing.T) {
	s := loopbackTestServer(t, nil)
	source, ids := proposalTransportFixture(t, s, "sqlite")
	base := "/api/backend-projects/" + source.Project.ID + "/saved-views"
	call := func(method, path string, in any, want int) []byte {
		t.Helper()
		var raw []byte
		if str, ok := in.(string); ok {
			raw = []byte(str)
		} else if in != nil {
			var err error
			raw, err = json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
		}
		status, b, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, raw)
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v", method, path, status, b, err)
		}
		if want == 200 {
			name := "BackendSavedView"
			if method == "GET" && strings.Split(path, "?")[0] == base {
				name = "BackendSavedViewPage"
			}
			validateSavedViewResponse(t, name, b)
		}
		return b
	}
	in := backendmodel.CreateSavedViewInput{Name: "Orders", Target: backendmodel.BackendReadTarget{RevisionID: source.Revision.ID}, State: backendmodel.SavedViewState{Database: &backendmodel.SavedDatabaseViewState{Kind: "database", Scope: backendmodel.SavedDatabaseViewScope{DatastoreID: ids["database:orders"], FacetKey: "sql"}, Filters: backendmodel.SavedDatabaseViewFilters{Search: ""}, Positions: []backendmodel.SavedViewPosition{}, CollapsedGroupIDs: []string{}}}, IdempotencyKey: "create"}
	created := call("POST", base, in, 200)
	var v backendmodel.SavedView
	if err := json.Unmarshal(created, &v); err != nil {
		t.Fatal(err)
	}
	path := base + "/" + v.ID
	call("GET", base+"?kind=database&limit=1", nil, 200)
	call("GET", path+"?version=1", nil, 200)
	save := backendmodel.SaveSavedViewInput{Name: "Second", State: in.State, ExpectedVersion: 1, IdempotencyKey: "save"}
	receipt := call("POST", path+"/save", save, 200)
	if replay := call("POST", path+"/save", save, 200); string(replay) != string(receipt) {
		t.Fatal("replay bytes changed")
	}
	call("GET", path+"?version=1", nil, 200)
	save.IdempotencyKey = "stale"
	call("POST", path+"/save", save, 409)
	for _, q := range []string{"?version=0", "?version=1.1", "?version=1&version=2", "?unknown=x", "?version=9223372036854775808"} {
		call("GET", path+q, nil, 400)
	}
	for _, q := range []string{"?kind=", "?kind=other", "?kind=flow&kind=database", "?limit=0", "?limit=101", "?unknown=x"} {
		call("GET", base+q, nil, 400)
	}
	call("GET", path+"?version=999", nil, 404)
	call("POST", base+"?x=1", in, 400)
	call("POST", path+"/save?x=1", save, 400)
	call("GET", path, `{}`, 400)
	call("POST", base, strings.Repeat(" ", backendmodel.MaxSavedViewBodyBytes+1), 413)
	bad := in
	dbState := *in.State.Database
	dbState.Scope.FacetKey = ""
	bad.State = backendmodel.SavedViewState{Database: &dbState}
	invalidResponse := call("POST", base, bad, 400)
	if !strings.Contains(string(invalidResponse), `"field":"state.scope.facetKey"`) {
		t.Fatal("strict decoder lost field path", string(invalidResponse))
	}
}

func validateSavedViewResponse(t *testing.T, name string, b []byte) {
	t.Helper()
	schema, err := api.BackendSchema(name)
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	uri := "https://mocker.invalid/" + name
	if err := compiler.AddResource(uri, schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(uri)
	if err != nil {
		t.Fatal(err)
	}
	dec := jsonx.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(out); err != nil {
		t.Fatalf("%s response violates schema: %v", name, err)
	}
}
