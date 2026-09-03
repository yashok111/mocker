// tools_entities_test.go covers list_resource_entities, A4's third tool
// (decisions.md mocker-a4-mcp-reach D1, D4). The route it wraps and the
// repository read behind it (internal/resources.Repo.ListFiltered) are
// exercised end to end by the section that owns internal/admin and
// internal/resources; this file covers what belongs to the ADAPTER layer —
// the JSON projection, the query it builds, and the one property D4's own
// "Addressing" clause names by name: the family segment is escaped BEFORE
// it reaches toolPath/renderParam, so a nested family's own "/" does not
// get read as extra path segments.
package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const resourceEntitiesFixture = `{
  "rows": [
    {"id":10,"entityKey":"1","scopeKey":"","baseScopeKey":"","data":{"id":1,"name":"gizmo"},"createdAt":"2026-08-20T12:00:00Z","updatedAt":"2026-08-20T12:00:00Z"},
    {"id":11,"entityKey":"2","scopeKey":"","baseScopeKey":"","data":{"id":2,"name":"widget"},"createdAt":"2026-08-20T12:01:00Z","updatedAt":"2026-08-20T12:01:00Z"}
  ],
  "lastId": 11
}`

// TestListResourceEntities_happyPath asserts the request (limit sent
// explicitly at its computed default, no after/scopeKey/baseScopeKey when
// none of them were passed) and every field of the projected rows against
// a fixture copied from resourceEntitiesView's own shape
// (resource_handlers.go).
func TestListResourceEntities_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: resourceEntitiesFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleListResourceEntities(lb)(opsTestCtx(), nil, ListResourceEntitiesInput{
		WorkspaceID: 7, RouteFamily: "/widgets",
	})
	if err != nil {
		t.Fatalf("handleListResourceEntities: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	if fc.calls[0].method != http.MethodGet {
		t.Errorf("method = %q, want GET", fc.calls[0].method)
	}
	if fc.calls[0].path != "/api/workspaces/7/resources/%2Fwidgets/entities?limit=100" {
		t.Errorf("call path = %q, want the escaped family with the default limit and no other query key", fc.calls[0].path)
	}

	if out.LastID != 11 {
		t.Errorf("LastID = %d, want 11", out.LastID)
	}
	if len(out.Rows) != 2 {
		t.Fatalf("len(Rows) = %d, want 2", len(out.Rows))
	}
	first := out.Rows[0]
	if first.ID != 10 || first.EntityKey != "1" || first.ScopeKey != "" || first.BaseScopeKey != "" {
		t.Errorf("Rows[0] = %+v, want id=10 entityKey=1 scopeKey= baseScopeKey=", first)
	}
	if string(first.Data) != `{"id":1,"name":"gizmo"}` {
		t.Errorf("Rows[0].Data = %s, want the raw fixture object, unmodified", first.Data)
	}
	if first.CreatedAt != "2026-08-20T12:00:00Z" || first.UpdatedAt != "2026-08-20T12:00:00Z" {
		t.Errorf("Rows[0] timestamps = %q/%q, want the fixture's own RFC3339 strings verbatim", first.CreatedAt, first.UpdatedAt)
	}
}

// TestListResourceEntities_escapesNestedFamilySlash is D4's own Addressing
// clause, the one this file exists to prove: a nested family's own
// route_family carries an internal "/" (a leading one on every family, an
// internal "/{}" one level deeper), and renderParam (routes.go) substitutes
// a string path param RAW. Escaping route_family HERE, before toolPath is
// ever called, is what keeps that internal "/" from being read as an extra
// path segment by the admin mux — the exact bug D4's own header comment
// warns copying opKeyFromPath would reproduce.
func TestListResourceEntities_escapesNestedFamilySlash(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"rows":[],"lastId":0}`}}}
	lb := newLoopback(fc)

	_, _, err := handleListResourceEntities(lb)(opsTestCtx(), nil, ListResourceEntitiesInput{
		WorkspaceID: 7, RouteFamily: "/orgs/{}/teams",
	})
	if err != nil {
		t.Fatalf("handleListResourceEntities: %v", err)
	}
	const want = "/api/workspaces/7/resources/%2Forgs%2F%7B%7D%2Fteams/entities?limit=100"
	if fc.calls[0].path != want {
		t.Errorf("call path = %q, want %q (the family escaped into ONE segment)", fc.calls[0].path, want)
	}
}

// TestListResourceEntities_scopeKeyPresentEmpty_vsAbsent is the
// nil-vs-pointer-to-empty distinction ListResourceEntitiesInput's own doc
// comment names: omitting ScopeKey must omit the query key entirely (any
// scope), while passing a pointer to "" must send scopeKey= explicitly (the
// family's own top-level scope) — the identical distinction the admin
// route's own parseResourceScopeFilter draws with url.Values.Has.
func TestListResourceEntities_scopeKeyPresentEmpty_vsAbsent(t *testing.T) {
	t.Parallel()

	t.Run("absent", func(t *testing.T) {
		t.Parallel()
		fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"rows":[],"lastId":0}`}}}
		_, _, err := handleListResourceEntities(newLoopback(fc))(opsTestCtx(), nil, ListResourceEntitiesInput{
			WorkspaceID: 7, RouteFamily: "/widgets",
		})
		if err != nil {
			t.Fatalf("handleListResourceEntities: %v", err)
		}
		if strings.Contains(fc.calls[0].path, "scopeKey") {
			t.Errorf("call path = %q, must not carry scopeKey when ScopeKey is nil", fc.calls[0].path)
		}
	})

	t.Run("present and empty", func(t *testing.T) {
		t.Parallel()
		empty := ""
		fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"rows":[],"lastId":0}`}}}
		_, _, err := handleListResourceEntities(newLoopback(fc))(opsTestCtx(), nil, ListResourceEntitiesInput{
			WorkspaceID: 7, RouteFamily: "/widgets", ScopeKey: &empty,
		})
		if err != nil {
			t.Fatalf("handleListResourceEntities: %v", err)
		}
		if !strings.Contains(fc.calls[0].path, "scopeKey=") {
			t.Errorf("call path = %q, want an explicit scopeKey= when ScopeKey points at \"\"", fc.calls[0].path)
		}
	})
}

// TestListResourceEntities_afterAndBaseScopeKeySentVerbatim covers the
// remaining query knobs together: after (only sent when > 0) and
// baseScopeKey (sent exactly like scopeKey — present, even empty, pins the
// axis).
func TestListResourceEntities_afterAndBaseScopeKeySentVerbatim(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"rows":[],"lastId":0}`}}}
	base := "42"
	_, _, err := handleListResourceEntities(newLoopback(fc))(opsTestCtx(), nil, ListResourceEntitiesInput{
		WorkspaceID: 7, RouteFamily: "/widgets", After: 9, Limit: 50, BaseScopeKey: &base,
	})
	if err != nil {
		t.Fatalf("handleListResourceEntities: %v", err)
	}
	path := fc.calls[0].path
	for _, want := range []string{"after=9", "limit=50", "baseScopeKey=42"} {
		if !strings.Contains(path, want) {
			t.Errorf("call path = %q, want it to contain %q", path, want)
		}
	}
}

// TestListResourceEntities_limitCapsAtMax mirrors list_traffic's own
// TestListTraffic_capsAtMaxLimit: a caller cannot widen the request past
// listResourceEntitiesMaxLimit.
func TestListResourceEntities_limitCapsAtMax(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"rows":[],"lastId":0}`}}}
	_, _, err := handleListResourceEntities(newLoopback(fc))(opsTestCtx(), nil, ListResourceEntitiesInput{
		WorkspaceID: 7, RouteFamily: "/widgets", Limit: 100000,
	})
	if err != nil {
		t.Fatalf("handleListResourceEntities: %v", err)
	}
	if !strings.Contains(fc.calls[0].path, "limit=500") {
		t.Errorf("call path = %q, want limit clamped to 500", fc.calls[0].path)
	}
}

// TestListResourceEntities_404IsToolError covers unknown_family, the one
// error taxonomy D4 collapses every cause into (never suggested, declined,
// or the workspace has no spec bound).
func TestListResourceEntities_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"unknown_family","message":"unknown route family"}}`},
	}}
	_, _, err := handleListResourceEntities(newLoopback(fc))(opsTestCtx(), nil, ListResourceEntitiesInput{
		WorkspaceID: 7, RouteFamily: "/nope",
	})
	if err == nil {
		t.Fatal("handleListResourceEntities returned no error for a 404")
	}
	if !strings.Contains(err.Error(), "unknown route family") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

// TestListResourceEntities_registeredWithHonestAnnotations is D4's own "no
// PUT, no POST" clause on the published surface: a real tools/list round
// trip, the same pattern TestProbeWorkspace_registeredWithHonestAnnotations
// uses, asserting list_resource_entities is present and read-only.
func TestListResourceEntities_registeredWithHonestAnnotations(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t}
	ep := New(fc, testKey, testConfig(), nil)
	h := ep.Handler()

	rec := doMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var env struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Annotations struct {
					ReadOnlyHint   bool `json:"readOnlyHint"`
					IdempotentHint bool `json:"idempotentHint"`
				} `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rec.Body.String())
	}

	var found bool
	for _, tool := range env.Result.Tools {
		if tool.Name != "list_resource_entities" {
			continue
		}
		found = true
		if !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint {
			t.Errorf("list_resource_entities annotations = %+v, want ReadOnlyHint and IdempotentHint both true", tool.Annotations)
		}
	}
	if !found {
		t.Fatal("list_resource_entities not found in tools/list")
	}
}

// TestListResourceEntitiesInput_hasNoConfirmSlugField: read-only, nothing
// destroyed, so the input schema must carry no confirmSlug field at all —
// unlike decide_resource's and reset_resource_data's own input structs.
func TestListResourceEntitiesInput_hasNoConfirmSlugField(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(ListResourceEntitiesInput{WorkspaceID: 7, RouteFamily: "/widgets"})
	if err != nil {
		t.Fatalf("marshal ListResourceEntitiesInput: %v", err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "confirmslug") {
		t.Errorf("ListResourceEntitiesInput carries a confirmSlug-shaped field: %s", raw)
	}
}
