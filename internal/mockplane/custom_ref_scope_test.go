package mockplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
)

// A custom endpoint has no resource takeover, but its ref recipes still read
// the workspace's resources. The base scope must survive that serving branch.
func TestServeCustom_RefUsesRequestBaseScope(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.BasePath = "/tenants/{tenantId}"
	settings.BasePathValues = []string{"7", "8"}
	rt := fixtureRuntime(t, orderDoc, orderRoutes(), orderVariants(), settings)
	rt.resources = map[string]*resources.Resource{"/subjects": subjectsResource(42)}
	store := &fakeEntityStore{listFn: func(_ context.Context, id int64, base, scope resources.ScopeKey) ([]resources.Entity, error) {
		if id != 42 || scope != "" {
			t.Fatalf("unexpected ref lookup: resource=%d routeScope=%q", id, scope)
		}
		switch base {
		case "7":
			return []resources.Entity{entityRow(1, "701", `{"id":701}`)}, nil
		case "8":
			return []resources.Entity{entityRow(2, "801", `{"id":801}`)}, nil
		default:
			return nil, nil
		}
	}}
	p := refTestPlane(store)
	row := &customep.Row{
		ID: 1, WorkspaceID: 1, Method: "GET", Path: "/order", CanonicalPath: "/order",
		OverrideOn: true, ActiveStatus: 200,
		Responses: map[string]overrides.Variant{"200": {
			Schema: []byte(`{"type":"object","required":["subjectId"],"properties":{"subjectId":{"type":"integer"}}}`),
			Recipes: map[string]recipes.Recipe{
				"subjectId": {Kind: recipes.KindRef, Data: refRecipeData(t, "/subjects", "id", "set-null")},
			},
		}},
	}
	rt.custom = map[int64]*customep.Row{1: row}
	rt.customInline = buildCustomInline(p.log, "alex", rt.custom, rt.resolver)
	rt.table = router.Build([]router.Route{{
		Method: "GET", Path: "/order", CanonicalPath: "/order", Custom: true, CustomRowID: 1,
	}}, settings.BasePath)
	ws := respondTestWorkspace()
	ws.Settings = settings

	for _, tt := range []struct {
		base resources.ScopeKey
		want string
	}{
		{base: "7", want: `{"subjectId":701}`},
		{base: "8", want: `{"subjectId":801}`},
		{base: "9", want: `{"subjectId":null}`},
	} {
		t.Run(string(tt.base), func(t *testing.T) {
			path := "/tenants/" + string(tt.base) + "/order"
			match := mustMatch(t, rt, http.MethodGet, path)
			req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+path, nil)
			rec := httptest.NewRecorder()
			p.serveCustom(rec, req, ws, rt, match, tt.base)
			if rec.Code != http.StatusOK || rec.Body.String() != tt.want {
				t.Fatalf("GET %s: status=%d body=%s; want 200 %s", path, rec.Code, rec.Body, tt.want)
			}
		})
	}
}
