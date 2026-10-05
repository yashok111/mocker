package admin

import (
	"encoding/json/v2"
	"slices"
	"testing"

	"github.com/yashok111/mocker/internal/guide"
)

func TestBackendEditorCapabilityNegotiation(t *testing.T) {
	s := loopbackTestServer(t, nil)
	status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", "/api/backend-projects/capabilities", nil)
	if err != nil || status != 200 {
		t.Fatalf("capabilities: %d %s %v", status, raw, err)
	}
	var out struct {
		ModelSchemaVersions []string         `json:"modelSchemaVersions"`
		Features            []string         `json:"features"`
		ViewSchemaVersions  []string         `json:"viewSchemaVersions"`
		WorkflowVersions    []guide.Workflow `json:"workflowVersions"`
		Limits              map[string]int64 `json:"limits"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(out.Features, "backend-editor-projections") || !slices.Contains(out.ViewSchemaVersions, "backend-editor-artifacts-v1") {
		t.Fatalf("editor contract unavailable: %+v", out)
	}
	for _, field := range []string{"maxEditorArtifactContextBytes", "maxEditorEventConstructionBytes", "maxEventMapBytes"} {
		if out.Limits[field] != 4<<20 {
			t.Fatalf("independent limit %s = %d; want 4 MiB", field, out.Limits[field])
		}
	}
	owner, _ := guide.WorkflowForTopic("backend-editor-projections")
	i := slices.IndexFunc(out.WorkflowVersions, func(w guide.Workflow) bool { return w.WorkflowID == owner.WorkflowID })
	if i < 0 || out.WorkflowVersions[i].WorkflowVersion != "8" || out.WorkflowVersions[i].GuideSetID != guide.CurrentGuideSetID() {
		t.Fatalf("editor owner unavailable: %+v", out.WorkflowVersions)
	}
	w := out.WorkflowVersions[i]
	if !slices.Equal(w.RequiredModelSchemaVersions, []string{"3", "4", "5", "6"}) {
		t.Fatalf("editor guide v6 changed retained source requirements: %v", w.RequiredModelSchemaVersions)
	}
	for _, schema := range w.RequiredModelSchemaVersions {
		if !slices.Contains(out.ModelSchemaVersions, schema) {
			t.Fatalf("editor guide cannot qualify: missing schema %s", schema)
		}
	}
}
