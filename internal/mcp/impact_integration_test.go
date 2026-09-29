package mcp

import (
	"strings"
	"testing"
)

func TestImpactMCPUsesPersistentSnapshotsWithoutSaving(t *testing.T) {
	t.Parallel()
	srv, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	created := designToolObject(t, srv, "create_api_design", map[string]any{"name": "Impact", "document": apiDesignFixture})
	design := created["design"].(map[string]any)
	draft := created["draft"].(map[string]any)
	candidate := strings.Replace(draft["document"].(string), `"type": "string"`, `"type": "integer"`, 1)
	if candidate == draft["document"] {
		t.Fatal("fixture did not change")
	}
	local := designToolObject(t, srv, "analyze_api_design_impact", map[string]any{"designId": design["id"], "fromRevisionId": draft["id"], "document": candidate})
	if len(local["changes"].([]any)) != 1 || len(local["evidence"].([]any)) == 0 || local["toRevisionId"] != nil || local["complete"] != true {
		t.Fatalf("local report: %v", local)
	}
	historical := designToolObject(t, srv, "analyze_api_design_impact", map[string]any{"designId": design["id"], "fromRevisionId": draft["id"], "toRevisionId": draft["id"]})
	if len(historical["changes"].([]any)) != 0 || historical["fromHash"] != historical["proposedHash"] {
		t.Fatalf("same revision: %v", historical)
	}
	after := designToolObject(t, srv, "get_api_design", map[string]any{"designId": design["id"]})
	if after["design"].(map[string]any)["version"] != float64(1) || after["draft"].(map[string]any)["id"] != draft["id"] || len(after["revisions"].([]any)) != 1 {
		t.Fatalf("analysis saved state: %v", after)
	}
}
