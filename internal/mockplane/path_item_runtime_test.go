package mockplane

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/specs"
)

func TestServeGenerated_InheritedPathItemsUseConcreteRoutesAndSourceExample(t *testing.T) {
	const raw = `{"openapi":"3.0.3","info":{"title":"T","version":"1"},"paths":{"/one/{id}":{"$ref":"#/components/pathItems/Shared"},"/two/{id}":{"$ref":"#/components/pathItems/Shared"}},"components":{"pathItems":{"Shared":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object"},"example":{"kind":"shared"}}}}}}}}}}`
	doc, report, err := openapi.Load([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	indexed, responses := specs.Index(doc, openapi.NewResolver(doc, openapi.DefaultRefBudget), report)
	if len(indexed) != 2 {
		t.Fatalf("indexed operations = %d", len(indexed))
	}
	routes := make([]router.Route, 0, len(indexed))
	variants := map[int64][]gen.ResponseVariant{}
	for i, operation := range indexed {
		id := int64(i + 1)
		routes = append(routes, router.Route{OpRowID: id, Method: operation.Method, Path: operation.Path, CanonicalPath: operation.CanonicalPath, SourceOrder: operation.SourceOrder})
		response := responses[i][0]
		variants[id] = []gen.ResponseVariant{{OpRowID: id, Selector: response.Selector, HTTPStatus: response.HTTPStatus, IsDefault: response.IsDefault, MediaType: *response.MediaType, SchemaPtr: *response.SchemaPtr, OpPointer: operation.Pointer}}
	}
	rt := fixtureRuntime(t, raw, routes, variants, domain.DefaultSettings())
	p := respondTestPlane()
	for _, path := range []string{"/one/7", "/two/9"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+path, nil)
		p.serveGenerated(rec, req, respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, path), resources.ScopeKey(""))
		if rec.Code != 200 || rec.Body.String() != `{"kind":"shared"}` {
			t.Errorf("GET %s = %d %q", path, rec.Code, rec.Body.String())
		}
	}
}
