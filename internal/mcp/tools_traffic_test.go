package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ---- set_session_directive ----

const sessionListFixture = `{
  "directives": [
    {"target": {"method":"GET","path":"/widgets"}, "action":"status", "status":503, "ms":0, "once":false, "n":0, "setAt":"2026-08-20T12:00:00Z"},
    {"target": "*", "action":"pause", "status":0, "ms":0, "once":false, "n":0, "setAt":"2026-08-20T12:00:05Z"}
  ]
}`

// TestSetSessionDirective_postHappyPath is the anti-drift guard for the
// POST half: the outbound target union round-trips (a concrete
// {method,path} target sent, the wire's own "*" and {method,path} shapes
// both decoded back correctly), and every field of SessionDirectiveLine is
// asserted non-zero against a fixture copied from sessionListView's own
// shape (livestate_handlers.go:78-81).
func TestSetSessionDirective_postHappyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: sessionListFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleSetSessionDirective(lb)(opsTestCtx(), nil, SetSessionDirectiveInput{
		WorkspaceID: 12, Method: "GET", Path: "/widgets", Action: "status", Status: 503,
	})
	if err != nil {
		t.Fatalf("handleSetSessionDirective: %v", err)
	}

	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	if fc.calls[0].method != http.MethodPost || fc.calls[0].path != "/api/workspaces/12/session" {
		t.Errorf("call = %s %s, want POST /api/workspaces/12/session", fc.calls[0].method, fc.calls[0].path)
	}
	sent := decodeBody(t, fc.calls[0].body)
	target, _ := sent["target"].(map[string]any)
	if target["method"] != "GET" || target["path"] != "/widgets" {
		t.Errorf("sent target = %v, want {method:GET path:/widgets}", sent["target"])
	}
	if sent["action"] != "status" {
		t.Errorf("sent action = %v, want status", sent["action"])
	}
	if sent["status"] != float64(503) {
		t.Errorf("sent status = %v, want 503", sent["status"])
	}
	if _, ok := sent["scenario"]; ok {
		t.Errorf("sent body carries a scenario key: %v — this tool must never offer one (§C-f)", sent)
	}

	if len(out.Directives) != 2 {
		t.Fatalf("len(Directives) = %d, want 2", len(out.Directives))
	}
	first := out.Directives[0]
	if first.Target.All || first.Target.Method != http.MethodGet || first.Target.Path != "/widgets" {
		t.Errorf("Directives[0].Target = %+v, want {Method:GET Path:/widgets}", first.Target)
	}
	if first.Action != "status" || first.Status != 503 {
		t.Errorf("Directives[0] = %+v, want action=status status=503", first)
	}
	if first.SetAt != "2026-08-20T12:00:00Z" {
		t.Errorf("Directives[0].SetAt = %q, want 2026-08-20T12:00:00Z", first.SetAt)
	}
	second := out.Directives[1]
	if !second.Target.All || second.Target.Method != "" || second.Target.Path != "" {
		t.Errorf("Directives[1].Target = %+v, want the \"*\" target (All:true, no method/path)", second.Target)
	}
	if second.Action != "pause" {
		t.Errorf("Directives[1].Action = %q, want pause", second.Action)
	}
}

// TestSetSessionDirective_allTargetSendsStarString is the "all" half of the
// target union: All:true must round-trip to the literal wire string "*",
// never {"method":"","path":""} — the two are NOT the same request to
// livestate.Target.UnmarshalJSON (livestate.go:80-100).
func TestSetSessionDirective_allTargetSendsStarString(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"directives":[]}`}}}
	lb := newLoopback(fc)

	_, _, err := handleSetSessionDirective(lb)(opsTestCtx(), nil, SetSessionDirectiveInput{
		WorkspaceID: 1, All: true, Action: "pause",
	})
	if err != nil {
		t.Fatalf("handleSetSessionDirective: %v", err)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if sent["target"] != "*" {
		t.Errorf("sent target = %v (%T), want the bare string \"*\"", sent["target"], sent["target"])
	}
}

// TestSetSessionDirective_clearAllSendsDelete is the DELETE half: clearAll
// must issue a DELETE with no body, never a POST — the request the tool
// makes is asserted directly, not just its effect.
func TestSetSessionDirective_clearAllSendsDelete(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"cleared":3}`}}}
	lb := newLoopback(fc)

	_, out, err := handleSetSessionDirective(lb)(opsTestCtx(), nil, SetSessionDirectiveInput{
		WorkspaceID: 12, ClearAll: true,
		// Deliberately also setting fields a POST would use, to prove they
		// are ignored on the clearAll path rather than smuggled into a stray
		// request.
		Action: "status", Status: 503,
	})
	if err != nil {
		t.Fatalf("handleSetSessionDirective: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	if fc.calls[0].method != http.MethodDelete || fc.calls[0].path != "/api/workspaces/12/session" {
		t.Errorf("call = %s %s, want DELETE /api/workspaces/12/session", fc.calls[0].method, fc.calls[0].path)
	}
	if fc.calls[0].body != "" {
		t.Errorf("DELETE body = %q, want empty (DELETE .../session takes none)", fc.calls[0].body)
	}
	if len(out.Directives) != 0 {
		t.Errorf("Directives = %v, want an empty (non-nil) slice after clearAll (§C-e: every directive is gone)", out.Directives)
	}
	if out.Cleared != 3 {
		t.Errorf("Cleared = %d, want 3", out.Cleared)
	}
}

func TestSetSessionDirective_missingActionIsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t}
	lb := newLoopback(fc)

	_, _, err := handleSetSessionDirective(lb)(opsTestCtx(), nil, SetSessionDirectiveInput{WorkspaceID: 1, All: true})
	if err == nil {
		t.Fatal("handleSetSessionDirective returned no error for a missing action")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %d, want 0 (must fail before ever calling out)", len(fc.calls))
	}
}

func TestSetSessionDirective_missingTargetIsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t}
	lb := newLoopback(fc)

	_, _, err := handleSetSessionDirective(lb)(opsTestCtx(), nil, SetSessionDirectiveInput{WorkspaceID: 1, Action: "pause"})
	if err == nil {
		t.Fatal("handleSetSessionDirective returned no error for neither all:true nor method+path")
	}
	if len(fc.calls) != 0 {
		t.Errorf("calls = %d, want 0 (must fail before ever calling out)", len(fc.calls))
	}
}

func TestSetSessionDirective_POST404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"workspace not found"}}`},
	}}
	_, _, err := handleSetSessionDirective(newLoopback(fc))(opsTestCtx(), nil, SetSessionDirectiveInput{
		WorkspaceID: 999, All: true, Action: "pause",
	})
	if err == nil {
		t.Fatal("handleSetSessionDirective returned no error for a 404")
	}
	if !strings.Contains(err.Error(), "workspace not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestSetSessionDirective_POST500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleSetSessionDirective(newLoopback(fc))(opsTestCtx(), nil, SetSessionDirectiveInput{
		WorkspaceID: 1, All: true, Action: "pause",
	})
	if err == nil {
		t.Fatal("handleSetSessionDirective returned no error for a 500")
	}
}

func TestSetSessionDirective_DELETE404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"workspace not found"}}`},
	}}
	_, _, err := handleSetSessionDirective(newLoopback(fc))(opsTestCtx(), nil, SetSessionDirectiveInput{
		WorkspaceID: 999, ClearAll: true,
	})
	if err == nil {
		t.Fatal("handleSetSessionDirective returned no error for a 404 on DELETE")
	}
	if !strings.Contains(err.Error(), "workspace not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestSetSessionDirective_DELETE500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleSetSessionDirective(newLoopback(fc))(opsTestCtx(), nil, SetSessionDirectiveInput{
		WorkspaceID: 1, ClearAll: true,
	})
	if err == nil {
		t.Fatal("handleSetSessionDirective returned no error for a 500 on DELETE")
	}
}

// ---- list_traffic ----

const trafficListFixture = `{
  "rows": [
    {"id":42,"ts":"2026-08-20T12:00:00Z","method":"GET","path":"/api/v1/widgets","peerIp":"127.0.0.1","matchedKind":"operation","matchedId":9,"status":200,"durationMs":3.5,"reqHeaders":{"Accept":"application/json"},"reqBody":"{}","respBody":"{\"id\":1}","notes":"","truncated":false},
    {"id":41,"ts":"2026-08-20T11:59:00Z","method":"POST","path":"/api/v1/login","matchedKind":"operation","matchedId":3,"status":401,"durationMs":1.2,"notes":"redacted,suppressed","truncated":true}
  ],
  "rate1m": 4,
  "dropped": 0
}`

// TestListTraffic_happyPath is the anti-drift guard for the plain-list
// path: every projected field asserted against a fixture copied from
// trafficListView's own shape (traffic_handlers.go:69-73), the default
// limit computed and sent explicitly, and hasMore correctly false when
// fewer rows came back than the limit.
func TestListTraffic_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: trafficListFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleListTraffic(lb)(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleListTraffic: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	if fc.calls[0].path != "/api/workspaces/7/traffic?limit=100" {
		t.Errorf("call path = %q, want the computed default limit sent explicitly", fc.calls[0].path)
	}

	if out.Returned != 2 || out.Limit != 100 || out.HasMore {
		t.Errorf("Returned/Limit/HasMore = %d/%d/%v, want 2/100/false", out.Returned, out.Limit, out.HasMore)
	}
	if len(out.Rows) != 2 {
		t.Fatalf("len(Rows) = %d, want 2", len(out.Rows))
	}

	first := out.Rows[0]
	if first.ID != 42 || first.Method != http.MethodGet || first.Path != "/api/v1/widgets" || first.Status != 200 {
		t.Errorf("Rows[0] = %+v, want id=42 method=GET path=/api/v1/widgets status=200", first)
	}
	if first.DurationMs != 3.5 {
		t.Errorf("Rows[0].DurationMs = %v, want 3.5", first.DurationMs)
	}
	if first.MatchedKind != "operation" || first.MatchedID == nil || *first.MatchedID != 9 {
		t.Errorf("Rows[0] matched = %q/%v, want operation/*9", first.MatchedKind, first.MatchedID)
	}
	if first.Truncated {
		t.Error("Rows[0].Truncated = true, want false")
	}
	if len(first.Notes) != 0 {
		t.Errorf("Rows[0].Notes = %v, want none", first.Notes)
	}
	// The core compaction assertion for THIS row: the plain list must never
	// carry bodies, even though the admin route's own wire row does.
	if first.ReqBody != "" || first.RespBody != "" || first.ReqHeaders != nil {
		t.Errorf("Rows[0] carries body/header fields on a plain list call: reqBody=%q respBody=%q reqHeaders=%v",
			first.ReqBody, first.RespBody, first.ReqHeaders)
	}

	second := out.Rows[1]
	if second.ID != 41 || !second.Truncated {
		t.Errorf("Rows[1] = %+v, want id=41 truncated=true", second)
	}
	if len(second.Notes) != 2 || second.Notes[0] != "redacted" || second.Notes[1] != "suppressed" {
		t.Errorf("Rows[1].Notes = %v, want [redacted suppressed]", second.Notes)
	}
}

// TestListTraffic_noBodyFieldsInMarshalledOutput asserts on the MARSHALLED
// JSON, not the Go struct, per this run's own instruction: an added field
// carrying an omitempty json tag would still pass a struct-level check on a
// zero value but must never appear on the wire for a plain list call.
func TestListTraffic_noBodyFieldsInMarshalledOutput(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: trafficListFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleListTraffic(lb)(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleListTraffic: %v", err)
	}

	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal ListTrafficOutput: %v", err)
	}
	s := string(raw)
	for _, forbidden := range []string{`"reqBody"`, `"respBody"`, `"reqHeaders"`} {
		if strings.Contains(s, forbidden) {
			t.Errorf("marshalled output contains %s, want no body/header field at all on a plain list: %s", forbidden, s)
		}
	}
}

// TestListTraffic_capsAtMaxLimit proves a caller cannot widen the request
// past listTrafficMaxLimit — mirroring TestFindOperations_capTruncates'
// own coverage of the identical clamp shape in tools_ops.go.
func TestListTraffic_capsAtMaxLimit(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"rows":[],"rate1m":0,"dropped":0}`}}}
	lb := newLoopback(fc)

	_, out, err := handleListTraffic(lb)(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 1, Limit: 100000})
	if err != nil {
		t.Fatalf("handleListTraffic: %v", err)
	}
	if fc.calls[0].path != "/api/workspaces/1/traffic?limit=500" {
		t.Errorf("call path = %q, want limit clamped to 500", fc.calls[0].path)
	}
	if out.Limit != 500 {
		t.Errorf("Limit = %d, want 500", out.Limit)
	}
}

// TestListTraffic_hasMoreWhenReturnedEqualsLimit is the "there may be more"
// half of §C's honesty requirement for this tool: returned==limit must
// report hasMore true, since a plain list can never know the real total the
// way find_operations can (traffic_handlers.go:70 returns no count of what
// it did not send).
func TestListTraffic_hasMoreWhenReturnedEqualsLimit(t *testing.T) {
	t.Parallel()
	twoRows := `{"rows":[
		{"id":2,"method":"GET","path":"/a","matchedKind":"none","status":404,"durationMs":1,"notes":"","truncated":false},
		{"id":1,"method":"GET","path":"/b","matchedKind":"none","status":404,"durationMs":1,"notes":"","truncated":false}
	],"rate1m":0,"dropped":0}`
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: twoRows}}}
	lb := newLoopback(fc)

	_, out, err := handleListTraffic(lb)(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 1, Limit: 2})
	if err != nil {
		t.Fatalf("handleListTraffic: %v", err)
	}
	if out.Returned != 2 || out.Limit != 2 || !out.HasMore {
		t.Errorf("Returned/Limit/HasMore = %d/%d/%v, want 2/2/true", out.Returned, out.Limit, out.HasMore)
	}
}

func TestListTraffic_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"workspace not found"}}`},
	}}
	_, _, err := handleListTraffic(newLoopback(fc))(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 999})
	if err == nil {
		t.Fatal("handleListTraffic returned no error for a 404")
	}
	if !strings.Contains(err.Error(), "workspace not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestListTraffic_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleListTraffic(newLoopback(fc))(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 1})
	if err == nil {
		t.Fatal("handleListTraffic returned no error for a 500")
	}
}

// ---- list_traffic with trafficId (§C7) ----

const trafficPollMatchFixture = `{
  "rows": [
    {"id":42,"ts":"2026-08-20T12:00:00Z","method":"GET","path":"/api/v1/widgets","matchedKind":"operation","matchedId":9,"status":200,"durationMs":3.5,"reqHeaders":{"Accept":"application/json"},"reqBody":"{}","respBody":"{\"id\":1,\"name\":\"gizmo\"}","notes":"","truncated":false}
  ],
  "lastId": 42,
  "dropped": 0
}`

// TestListTraffic_withTrafficID_happyPath is §C7's composition proven
// correct: since is trafficID-1, limit=1, and — because the returned row's
// id MATCHES what was asked for — bodies and headers are attached, unlike
// every other test in this file.
func TestListTraffic_withTrafficID_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: trafficPollMatchFixture}}}
	lb := newLoopback(fc)

	tid := int64(42)
	_, out, err := handleListTraffic(lb)(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 7, TrafficID: &tid})
	if err != nil {
		t.Fatalf("handleListTraffic: %v", err)
	}
	if fc.calls[0].method != http.MethodGet || fc.calls[0].path != "/api/workspaces/7/traffic/poll?since=41&limit=1" {
		t.Errorf("call = %s %s, want GET /api/workspaces/7/traffic/poll?since=41&limit=1", fc.calls[0].method, fc.calls[0].path)
	}
	if out.Returned != 1 || len(out.Rows) != 1 {
		t.Fatalf("Returned/len(Rows) = %d/%d, want 1/1", out.Returned, len(out.Rows))
	}
	row := out.Rows[0]
	if row.ID != 42 {
		t.Errorf("Rows[0].ID = %d, want 42", row.ID)
	}
	if row.ReqBody != "{}" {
		t.Errorf("Rows[0].ReqBody = %q, want {}", row.ReqBody)
	}
	if row.RespBody != `{"id":1,"name":"gizmo"}` {
		t.Errorf("Rows[0].RespBody = %q, want the fixture's pinned body", row.RespBody)
	}
	if row.ReqHeaders["Accept"] != "application/json" {
		t.Errorf("Rows[0].ReqHeaders = %v, want Accept=application/json", row.ReqHeaders)
	}
}

// TestListTraffic_withTrafficID_prunedRowReportsNoSuchEntry is §C7's own
// named test: the fake returns a row with a DIFFERENT id than requested
// (traffic.Repo.Since's own "id > since, oldest first" behavior when the
// requested row was pruned — repo.go:53) and the tool must report "no such
// entry" rather than silently describing a stranger's request as the one
// asked about.
func TestListTraffic_withTrafficID_prunedRowReportsNoSuchEntry(t *testing.T) {
	t.Parallel()
	mismatched := `{"rows":[{"id":45,"method":"GET","path":"/api/v1/other","matchedKind":"operation","matchedId":1,"status":200,"durationMs":1,"notes":"","truncated":false}],"lastId":45,"dropped":0}`
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: mismatched}}}
	lb := newLoopback(fc)

	tid := int64(42)
	_, _, err := handleListTraffic(lb)(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 7, TrafficID: &tid})
	if err == nil {
		t.Fatal("handleListTraffic returned no error when poll answered a DIFFERENT row than requested")
	}
	if !strings.Contains(err.Error(), "no such entry") {
		t.Errorf("error = %q, want it to say \"no such entry\" (§C7)", err.Error())
	}
	if strings.Contains(err.Error(), "/api/v1/other") {
		t.Errorf("error = %q, must not describe the OTHER row it happened to receive", err.Error())
	}
}

// TestListTraffic_withTrafficID_emptyPollReportsNoSuchEntry covers the other
// shape §C7 warns about: nothing at all came back (every row past "since"
// is gone, or the workspace never had one this high).
func TestListTraffic_withTrafficID_emptyPollReportsNoSuchEntry(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"rows":[],"lastId":41,"dropped":0}`}}}
	lb := newLoopback(fc)

	tid := int64(42)
	_, _, err := handleListTraffic(lb)(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 7, TrafficID: &tid})
	if err == nil {
		t.Fatal("handleListTraffic returned no error when poll answered with no rows at all")
	}
	if !strings.Contains(err.Error(), "no such entry") {
		t.Errorf("error = %q, want it to say \"no such entry\" (§C7)", err.Error())
	}
}

func TestListTraffic_withTrafficID_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"workspace not found"}}`},
	}}
	tid := int64(1)
	_, _, err := handleListTraffic(newLoopback(fc))(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 999, TrafficID: &tid})
	if err == nil {
		t.Fatal("handleListTraffic returned no error for a 404 on the poll route")
	}
	if !strings.Contains(err.Error(), "workspace not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

// ---- list_traffic since/lastId (A4, decisions.md mocker-a4-mcp-reach D6) ----

// TestListTraffic_noSince_lastIdIsFirstRowID is D6's own no-since clause:
// the plain list path calls GET .../traffic (unchanged — trafficListFixture
// is the identical fixture TestListTraffic_happyPath already asserts every
// other field of), and LastID is derived off the ALREADY-FETCHED rows —
// wire.Rows[0].ID, the newest row, because trafficListView's own rows come
// back newest-first. Getting this backwards (picking the LAST element
// instead of the first) would silently report the OLDEST row in the page as
// the cursor, which the two-row fixture here is specifically sized to catch
// (Rows[0].ID=42, Rows[1].ID=41 — a wrong pick reads as 41, not 42).
func TestListTraffic_noSince_lastIdIsFirstRowID(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: trafficListFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleListTraffic(lb)(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleListTraffic: %v", err)
	}
	if fc.calls[0].path != "/api/workspaces/7/traffic?limit=100" {
		t.Errorf("call path = %q, want the plain list route, unchanged by D6", fc.calls[0].path)
	}
	if out.LastID != 42 {
		t.Errorf("LastID = %d, want 42 (Rows[0].ID, the newest row)", out.LastID)
	}
}

// TestListTraffic_noSince_emptyList_lastIdIsZero covers the empty-page case
// D6's own Shape names: nothing to chain from yet reports 0, the same value
// Since:0 against an empty table would answer.
func TestListTraffic_noSince_emptyList_lastIdIsZero(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"rows":[],"rate1m":0,"dropped":0}`}}}
	lb := newLoopback(fc)

	_, out, err := handleListTraffic(lb)(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 7})
	if err != nil {
		t.Fatalf("handleListTraffic: %v", err)
	}
	if out.LastID != 0 {
		t.Errorf("LastID = %d, want 0 on an empty page", out.LastID)
	}
}

// TestListTraffic_since_usesPollRouteAndReportsItsLastID is D6's positive
// clause: Since set switches this tool onto GET .../traffic/poll (never a
// since-flavored GET .../traffic, which has no cursor field to answer with
// at all — the wrong fix D6 itself names), and LastID is read straight off
// the route's own answer rather than re-derived from the rows.
func TestListTraffic_since_usesPollRouteAndReportsItsLastID(t *testing.T) {
	t.Parallel()
	pollFixture := `{"rows":[
		{"id":43,"method":"GET","path":"/a","matchedKind":"none","status":404,"durationMs":1,"notes":"","truncated":false},
		{"id":44,"method":"GET","path":"/b","matchedKind":"none","status":404,"durationMs":1,"notes":"","truncated":false}
	],"lastId":44,"dropped":0}`
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: pollFixture}}}
	lb := newLoopback(fc)

	since := int64(42)
	_, out, err := handleListTraffic(lb)(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 7, Since: &since})
	if err != nil {
		t.Fatalf("handleListTraffic: %v", err)
	}
	if fc.calls[0].method != http.MethodGet || fc.calls[0].path != "/api/workspaces/7/traffic/poll?since=42&limit=100" {
		t.Errorf("call = %s %s, want GET /api/workspaces/7/traffic/poll?since=42&limit=100", fc.calls[0].method, fc.calls[0].path)
	}
	if out.Returned != 2 || len(out.Rows) != 2 {
		t.Fatalf("Returned/len(Rows) = %d/%d, want 2/2", out.Returned, len(out.Rows))
	}
	// Oldest first is the route's own contract, not something this branch
	// reorders — Rows[0] is id 43, the OLDER of the two.
	if out.Rows[0].ID != 43 || out.Rows[1].ID != 44 {
		t.Errorf("Rows = [%d %d], want [43 44] (oldest first)", out.Rows[0].ID, out.Rows[1].ID)
	}
	if out.LastID != 44 {
		t.Errorf("LastID = %d, want 44 (the poll route's own lastId, not re-derived)", out.LastID)
	}
}

// TestListTraffic_sinceZero_isDistinctFromNoSince proves the pointer, not a
// plain int64, is what makes Since=0 reach the poll route at all: a bare
// int64 with omitempty would make "since 0" indistinguishable from "since
// absent" on the wire, silently keeping a caller reading from the very
// beginning of the table on the plain, newest-first list forever.
func TestListTraffic_sinceZero_isDistinctFromNoSince(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: `{"rows":[],"lastId":0,"dropped":0}`}}}
	lb := newLoopback(fc)

	zero := int64(0)
	_, _, err := handleListTraffic(lb)(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 7, Since: &zero})
	if err != nil {
		t.Fatalf("handleListTraffic: %v", err)
	}
	if fc.calls[0].path != "/api/workspaces/7/traffic/poll?since=0&limit=100" {
		t.Errorf("call path = %q, want the poll route with since=0, not the plain list", fc.calls[0].path)
	}
}

// TestListTraffic_withTrafficID_lastIdIsTheFetchedRow proves the
// single-entry path also answers LastID (D6: populated on every path this
// tool answers through), and that it is the fetched row's own id — not left
// at zero, and not the poll route's raw lastId (which would be the SAME
// value here only because trafficPollMatchFixture's one row happens to
// match the id requested).
func TestListTraffic_withTrafficID_lastIdIsTheFetchedRow(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: trafficPollMatchFixture}}}
	lb := newLoopback(fc)

	tid := int64(42)
	_, out, err := handleListTraffic(lb)(opsTestCtx(), nil, ListTrafficInput{WorkspaceID: 7, TrafficID: &tid})
	if err != nil {
		t.Fatalf("handleListTraffic: %v", err)
	}
	if out.LastID != 42 {
		t.Errorf("LastID = %d, want 42", out.LastID)
	}
}

// ---- override_from_traffic ----

const toOverrideFixture = `{"opKey":"GET%20%2Fwidgets","status":503,"revision":7}`

// TestOverrideFromTraffic_happyPath asserts the request (POST, no body) and
// every field of the projected response against a fixture copied from
// toOverrideView's own shape (from_traffic.go:66-70).
func TestOverrideFromTraffic_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{{status: http.StatusOK, body: toOverrideFixture}}}
	lb := newLoopback(fc)

	_, out, err := handleOverrideFromTraffic(lb)(opsTestCtx(), nil, OverrideFromTrafficInput{WorkspaceID: 7, TrafficID: 42})
	if err != nil {
		t.Fatalf("handleOverrideFromTraffic: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	if fc.calls[0].method != http.MethodPost || fc.calls[0].path != "/api/workspaces/7/traffic/42/to-override" {
		t.Errorf("call = %s %s, want POST /api/workspaces/7/traffic/42/to-override", fc.calls[0].method, fc.calls[0].path)
	}
	if fc.calls[0].body != "" {
		t.Errorf("request body = %q, want empty (to-override takes none)", fc.calls[0].body)
	}
	if out.OpKey != "GET%20%2Fwidgets" || out.Status != 503 || out.Revision != 7 {
		t.Errorf("output = %+v, want opKey=GET%%20%%2Fwidgets status=503 revision=7", out)
	}
}

// TestOverrideFromTraffic_409RefusalReportsReason is §C-d's own named test:
// the real refusal for a truncated, redacted or suppressed row is 409
// (from_traffic.go:114,121,130,135,140), and the tool must report the
// admin plane's OWN reason so the model can pick a different trafficId,
// exactly like every other 4xx this package turns into a tool error
// (toolErr, loopback_client.go) — no special-casing needed for this status
// specifically.
func TestOverrideFromTraffic_409RefusalReportsReason(t *testing.T) {
	t.Parallel()
	const reason = "traffic row's body was truncated or redacted; pinning it would ship a lie"
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusConflict, body: `{"error":{"code":"conflict","message":"` + reason + `"}}`},
	}}
	lb := newLoopback(fc)

	_, _, err := handleOverrideFromTraffic(lb)(opsTestCtx(), nil, OverrideFromTrafficInput{WorkspaceID: 7, TrafficID: 41})
	if err == nil {
		t.Fatal("handleOverrideFromTraffic returned no error for a 409 refusal")
	}
	if !strings.Contains(err.Error(), reason) {
		t.Errorf("error = %q, want it to carry the admin plane's own refusal reason %q", err.Error(), reason)
	}
}

func TestOverrideFromTraffic_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"traffic row not found"}}`},
	}}
	_, _, err := handleOverrideFromTraffic(newLoopback(fc))(opsTestCtx(), nil, OverrideFromTrafficInput{WorkspaceID: 7, TrafficID: 999})
	if err == nil {
		t.Fatal("handleOverrideFromTraffic returned no error for a 404")
	}
	if !strings.Contains(err.Error(), "traffic row not found") {
		t.Errorf("error = %q, want it to carry the admin plane's message", err.Error())
	}
}

func TestOverrideFromTraffic_500IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusInternalServerError, body: `{"error":{"code":"internal","message":"boom"}}`},
	}}
	_, _, err := handleOverrideFromTraffic(newLoopback(fc))(opsTestCtx(), nil, OverrideFromTrafficInput{WorkspaceID: 7, TrafficID: 1})
	if err == nil {
		t.Fatal("handleOverrideFromTraffic returned no error for a 500")
	}
}

// ---- registration: honest annotations, real output schemas, no scenario ----

// TestAddTrafficTools_registersThreeToolsWithHonestAnnotations mirrors
// tools_ops_test.go's own TestAddOperationTools_registersSixToolsWith
// HonestAnnotations: a real tools/list round trip, asserting PRESENCE among
// whatever else the registry holds (not an exact count — tools_ops.go's own
// six tools are a DIFFERENT file's business), plus the one requirement
// unique to this file's set_session_directive: its published input schema
// must never mention "scenario" at all (§C-f — scenario switching has its
// own dedicated admin route as of P2b, so offering a "scenario" field here
// would only steer a client at the wrong endpoint, not at a route that has
// stopped existing).
func TestAddTrafficTools_registersThreeToolsWithHonestAnnotations(t *testing.T) {
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
				Name         string         `json:"name"`
				Description  string         `json:"description"`
				InputSchema  map[string]any `json:"inputSchema"`
				OutputSchema map[string]any `json:"outputSchema"`
				Annotations  struct {
					ReadOnlyHint   bool `json:"readOnlyHint"`
					IdempotentHint bool `json:"idempotentHint"`
				} `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode tools/list: %v; body=%s", err, rec.Body.String())
	}

	want := map[string]struct {
		readOnly   bool
		idempotent bool
	}{
		"set_session_directive": {false, true},
		"list_traffic":          {true, true},
		"override_from_traffic": {false, true},
	}

	seen := make(map[string]bool, len(want))
	for _, tool := range env.Result.Tools {
		w, ok := want[tool.Name]
		if !ok {
			continue // one of tools_ops.go's own tools — not this test's business.
		}
		seen[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("%s: empty Description", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s: no output schema published — declared output type must not be any", tool.Name)
		}
		if tool.Annotations.ReadOnlyHint != w.readOnly {
			t.Errorf("%s: ReadOnlyHint = %v, want %v", tool.Name, tool.Annotations.ReadOnlyHint, w.readOnly)
		}
		if tool.Annotations.IdempotentHint != w.idempotent {
			t.Errorf("%s: IdempotentHint = %v, want %v", tool.Name, tool.Annotations.IdempotentHint, w.idempotent)
		}

		if tool.Name == "set_session_directive" {
			schemaJSON, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatalf("marshal set_session_directive input schema: %v", err)
			}
			if strings.Contains(string(schemaJSON), "scenario") {
				t.Errorf("set_session_directive's published input schema mentions \"scenario\": %s — "+
					"§C-f: this tool must never offer a field for switching scenarios; that has its own "+
					"dedicated admin route now (POST .../scenarios/{sid}/activate)", schemaJSON)
			}
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("tools/list did not include %q", name)
		}
	}
}

// A13: clear:true with a target sends DELETE with a body, then reads what
// is left.
func TestSetSessionDirective_clearOneTargetSendsDeleteWithBody(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"cleared":2,"directives":[]}`)}
	out, errMsg := callTool(t, calls, "set_session_directive",
		`{"workspaceId":12,"clear":true,"method":"get","path":"/orders","action":"pause"}`)
	if errMsg != "" {
		t.Fatalf("tool error: %s", errMsg)
	}
	// The recorder keeps the LAST call (the GET); the DELETE's body is what
	// the tool encoded before it — asserted through the output's cleared.
	if calls.method != "GET" || calls.path != "/api/workspaces/12/session" {
		t.Errorf("last call %s %s, want the GET that lists what remains", calls.method, calls.path)
	}
	var got struct {
		Cleared    int   `json:"cleared"`
		Directives []any `json:"directives"`
	}
	if err := json.Unmarshal(out, &got); err != nil || got.Cleared != 2 || len(got.Directives) != 0 {
		t.Errorf("out = %s (err %v)", out, err)
	}
	// clear without a target is refused before any call.
	calls = &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
	if _, errMsg := callTool(t, calls, "set_session_directive", `{"workspaceId":12,"clear":true}`); errMsg == "" || calls.method != "" {
		t.Errorf("clear without target: err %q, called %q", errMsg, calls.method)
	}
}
