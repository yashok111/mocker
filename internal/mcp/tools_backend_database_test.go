package mcp

import (
	"encoding/json/v2"
	"maps"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendDatabaseRealSDKReachesPinnedDomain(t *testing.T) {
	server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	raw, message := callTool(t, server, "create_backend_project", `{"name":"Database","idempotencyKey":"database"}`)
	if message != "" {
		t.Fatal(message)
	}
	var project backendmodel.Project
	if err := json.Unmarshal(raw, &project); err != nil {
		t.Fatal(err)
	}
	_, message = callTool(t, server, "query_backend_database", `{"projectId":"`+project.ID+`","revisionId":"`+project.CurrentRevisionID+`","datastoreId":"`+project.ID+`","facetKey":"sql","recordType":"tables"}`)
	if !strings.Contains(message, "backend_not_found") || !strings.Contains(message, "HTTP 404") {
		t.Fatalf("missing datastore must reach real domain404: %s", message)
	}
}

func TestBackendDatabaseSDKStrictSelectorsAndRawResponse(t *testing.T) {
	calls := &recordingCaller{status: 200, body: []byte(`{}`)}
	fixture := newToolFixture(calls)
	prefix := `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","datastoreId":"` + backendTestID + `","facetKey":"sql","recordType":`
	for _, body := range []string{
		prefix + `"relationships","search":""}`, prefix + `"tables","tableId":""}`,
		prefix + `"tables","limit":0}`, prefix + `"tables","limit":501}`, prefix + `"tables","limit":null}`,
		prefix + `"tables","facetKey":"sql"}`, prefix + `"tables","unknown":true}`, prefix + `null}`,
	} {
		*calls = recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := fixture.Call(t, "query_backend_database", body)
		if msg == "" || calls.method != "" {
			t.Fatalf("invalid selector reached loopback: %s %s", body, msg)
		}
	}
	for _, suffix := range []string{`"tables"}`, `"tables","search":""}`, `"relationships","tableId":"` + backendTestID + `"}`} {
		*calls = recordingCaller{status: 200, body: []byte(`{"tableItems":[{"columnCount":9223372036854775807}],"coverage":{"inventory":[{"knownCount":9007199254740993}]}}`)}
		raw, msg := fixture.Call(t, "query_backend_database", prefix+suffix)
		if msg != "" || !strings.Contains(string(raw), "9223372036854775807") || !strings.Contains(string(calls.sent), `"revisionId":"`+backendTestID+`"`) || strings.Contains(string(calls.sent), "projectId") {
			t.Fatalf("raw pinned SDK transport: %s %s %s", raw, calls.sent, msg)
		}
	}
	*calls = recordingCaller{status: 422, body: []byte(`{"error":{"code":"backend_relational_unavailable","details":{"value":9223372036854775807}}}`)}
	_, msg := fixture.Call(t, "query_backend_database", prefix+`"tables"}`)
	if !strings.Contains(msg, "HTTP 422: "+string(calls.body)) {
		t.Fatalf("domain error bytes changed: %s", msg)
	}
}

func TestBackendImportSDKSelectedProfileUnions(t *testing.T) {
	base := map[string]any{
		"projectId": backendTestID, "expectedVersion": int64(1), "baseRevisionId": backendTestID, "idempotencyKey": "profile",
		"manifest": map[string]any{"repositoryName": "orders", "provider": map[string]any{"name": "collector", "version": "1", "namespace": "orders", "method": "agent", "profiles": []string{backendmodel.GraphProfile, backendmodel.RelationalProfile}, "limitations": []string{}}, "snapshot": map[string]any{"dirty": false, "consistency": "verified", "capturedAt": "2026-09-30T10:00:00Z", "files": []any{}}},
	}
	categories := strings.Fields("files endpoints datastores migrations producers consumers jobs contracts tests")
	inventory := make([]any, 0, len(categories))
	for _, category := range categories {
		inventory = append(inventory, map[string]any{"category": category, "status": "complete", "knownCount": 0, "denominator": nil, "discoverySource": "collector", "gaps": []string{}, "reason": ""})
	}
	base["inventory"] = inventory
	for _, tc := range []struct {
		name    string
		changes map[string]any
		valid   bool
	}{
		{"legacy-omission", nil, true},
		{"relational-initial", map[string]any{"profile": backendmodel.RelationalProfile}, true},
		{"explicit-null", map[string]any{"profile": nil}, false},
		{"explicit-empty", map[string]any{"profile": ""}, false},
		{"wrong-type", map[string]any{"profile": 2}, false},
		{"wrong-enum", map[string]any{"profile": "other"}, false},
		{"null-extension", map[string]any{"profile": backendmodel.RelationalProfile, "profileExtension": nil}, false},
		{"initial-extension", map[string]any{"profile": backendmodel.RelationalProfile, "profileExtension": backendmodel.ImportProfileExtension{FromProfile: backendmodel.GraphProfile, ToProfile: backendmodel.RelationalProfile}}, false},
		{"reconcile-extension", map[string]any{"profile": backendmodel.RelationalProfile, "mode": "reconcile", "repositoryId": backendTestID, "graphScope": backendmodel.GraphScope{Profile: backendmodel.RelationalProfile, Status: "complete", Gaps: []string{}}, "profileExtension": backendmodel.ImportProfileExtension{FromProfile: backendmodel.GraphProfile, ToProfile: backendmodel.RelationalProfile}}, true},
		{"reversed-extension", map[string]any{"profile": backendmodel.RelationalProfile, "mode": "reconcile", "repositoryId": backendTestID, "graphScope": backendmodel.GraphScope{Profile: backendmodel.RelationalProfile, Status: "complete", Gaps: []string{}}, "profileExtension": backendmodel.ImportProfileExtension{FromProfile: backendmodel.RelationalProfile, ToProfile: backendmodel.GraphProfile}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := maps.Clone(base)
			for name, value := range tc.changes {
				fields[name] = value
			}
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			calls := &recordingCaller{status: 200, body: []byte(`{}`)}
			_, msg := callTool(t, calls, "begin_backend_import", string(raw))
			if tc.valid && (msg != "" || calls.method != "POST") || !tc.valid && (msg == "" || calls.method != "") {
				t.Fatalf("profile union valid=%v method=%s error=%s", tc.valid, calls.method, msg)
			}
		})
	}
}
