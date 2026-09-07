// respond_generated_test.go: generated mode's own serve-time gates — the
// dangerous-media-type refusal, a const recipe visible in the output, a JWT
// recipe producing a decodable token, a patched schema applying to the
// generated body, and a listSize override constraining a generated list.
// Split out of the former respond_test.go (package mockplane, not
// mockplane_test — see helpers_test.go's own comment for why).
package mockplane

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
)

func TestServeGenerated_GeneratedMode_DangerousResolvedMediaTypeRefused(t *testing.T) {
	route := router.Route{OpRowID: 1, Method: "GET", Path: "/page", CanonicalPath: "/page", SourceOrder: 1}
	variant := gen.ResponseVariant{
		OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "text/html",
		SchemaPtr: "#/paths/~1page/get/responses/200/content/text~1html/schema",
		OpPointer: "#/paths/~1page/get",
	}
	rt := fixtureRuntimeWithOverrides(t, dangerousHTMLDoc, []router.Route{route},
		map[int64][]gen.ResponseVariant{1: {variant}}, domain.DefaultSettings(), nil)
	m := mustMatch(t, rt, "GET", "/page")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/page", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "text/html" {
		t.Errorf("Content-Type = %q — a GENERATED body must be refused at a browser-executable resolved type exactly as a pinned one is", ct)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty: nothing the mock plane produces may be served under a type a browser executes", rec.Body.String())
	}
}

// TestServeGenerated_GeneratedMode_ConstRecipeVisibleInOutput proves mode
// "generated" (the default) threads the row's own compiled recipe set
// through to gen.Request.Recipes: a const recipe bound to "name" must win
// over whatever gen would otherwise have generated there.
func TestServeGenerated_GeneratedMode_ConstRecipeVisibleInOutput(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode: "generated",
					Recipes: map[string]recipes.Recipe{
						"name": {Kind: recipes.KindConst, Data: json.RawMessage(`"pinned-by-recipe"`)},
					},
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	if body.Name != "pinned-by-recipe" {
		t.Errorf("name = %q, want the const recipe's own value %q", body.Name, "pinned-by-recipe")
	}
}

// TestServeGenerated_GeneratedMode_JWTRecipeProducesDecodableToken is the
// phase's own acceptance criterion (DESIGN §19 "фронт логинится") proven at
// the serving layer: a jwt recipe answers a structurally valid compact JWS
// whose payload decodes, and whose "sub" is the SAME identity id the
// workspace's own Settings.Identity carries — DESIGN §10's "the token's sub
// and the id served by the profile endpoint come from ONE identity".
func TestServeGenerated_GeneratedMode_JWTRecipeProducesDecodableToken(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.Identity = domain.Identity{ID: "u-42", Name: "Ada"}
	settings.Auth = domain.AuthSettings{JWTTTLSec: 900, Alg: "HS256", SigningKey: "test-signing-key"}

	rows := map[string]*overrides.Row{
		overrides.OpKey("GET", "/session"): {
			Method: "GET", Path: "/session", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode: "generated",
					Recipes: map[string]recipes.Recipe{
						"token": {Kind: recipes.KindJWT},
					},
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, sessionSchemaDoc, []router.Route{sessionRoute()},
		map[int64][]gen.ResponseVariant{1: {sessionVariant()}}, settings, rows)
	m := mustMatch(t, rt, "GET", "/session")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/session", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	parts := strings.Split(body.Token, ".")
	if len(parts) != 3 {
		t.Fatalf("token = %q, want a 3-segment compact JWS", body.Token)
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("token payload does not decode as base64url: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		t.Fatalf("token payload is not valid JSON: %v (%s)", err, payloadJSON)
	}
	if claims["sub"] != "u-42" {
		t.Errorf("claims[sub] = %v, want %q — the token's sub and the identity's own id must be the SAME (DESIGN §10)", claims["sub"], "u-42")
	}
	if claims["name"] != "Ada" {
		t.Errorf("claims[name] = %v, want %q", claims["name"], "Ada")
	}
	exp, ok := claims["exp"].(float64)
	if !ok || exp <= 0 {
		t.Fatalf("claims[exp] = %v, want a positive epoch-SECONDS expiry", claims["exp"])
	}
	iat, ok := claims["iat"].(float64)
	if !ok {
		t.Fatalf("claims[iat] missing")
	}
	if got, want := exp-iat, float64(900); got != want {
		t.Errorf("exp-iat = %v, want %v (the recipe's own workspace-default ttl, JWTTTLSec)", got, want)
	}
}

// TestServeGenerated_GeneratedMode_PatchedSchemaAppliesToGeneratedBody is
// the split's own reason d'être for reading rt.lookupPatchedSchema inside
// the generator-request block (respond.go:250-280): a patched root, looked
// up by the ALREADY-CHOSEN variant's (OpRowID, Selector), must reach
// gen.Request.PatchedSchema and visibly change the generated body — proven
// here directly against rt.patchedSchemas rather than through op_overrides'
// own SchemaPatch/jsonpatch pipeline (internal/server's C6 test already
// covers that construction step; this file's own fixtures never call
// buildPatchedSchemas at all, so this branch had no cover in this package
// before this test).
func TestServeGenerated_GeneratedMode_PatchedSchemaAppliesToGeneratedBody(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	// itemVariant()'s Selector is "200" and its OpRowID is 1 — the exact
	// key lookupPatchedSchema reads inside serveGenerated once the variant
	// is chosen.
	rt.patchedSchemas = map[patchedSchemaKey]map[string]any{
		{opRowID: 1, selector: "200"}: {
			"type": "object",
			"properties": map[string]any{
				"id":    map[string]any{"type": "integer"},
				"name":  map[string]any{"type": "string"},
				"patch": map[string]any{"type": "string", "const": "from-schema-patch"},
			},
			"required": []any{"id", "name", "patch"},
		},
	}
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	if body["patch"] != "from-schema-patch" {
		t.Errorf(`body["patch"] = %v, want "from-schema-patch" — the patched root must reach gen.Body, not the unpatched spec schema; body=%s`, body["patch"], rec.Body)
	}
}

// TestServeGenerated_GeneratedMode_ListSizeOverrideConstrainsGeneratedList
// is the active twin of TestServeGenerated_OverrideOnFalse_IsInert's
// ListSize field: that test only proves ListSize does nothing when the row
// is switched off, never that it does something when the row is switched
// on. row.ListSize != nil (respond.go's withListSizeRecipe call inside the
// same overrideActive && !pinned block the patched-schema test above
// exercises) had no serveGenerated-level test that actually asserts the
// generated list length before this one.
func TestServeGenerated_GeneratedMode_ListSizeOverrideConstrainsGeneratedList(t *testing.T) {
	const listDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/items": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": { "id": { "type": "integer" } },
                    "required": ["id"]
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}`
	route := router.Route{OpRowID: 1, Method: "GET", Path: "/items", CanonicalPath: "/items", SourceOrder: 1}
	variant := gen.ResponseVariant{
		OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1items/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1items/get",
	}
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			ListSize: &overrides.ListSize{Min: 3, Max: 3},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, listDoc, []router.Route{route},
		map[int64][]gen.ResponseVariant{1: {variant}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	if len(list) != 3 {
		t.Errorf("len(list) = %d, want 3 — row.ListSize must reach the generator through withListSizeRecipe; body=%s", len(list), rec.Body)
	}
}
