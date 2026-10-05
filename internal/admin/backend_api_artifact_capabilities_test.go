package admin

import (
	"encoding/json/v2"
	"slices"
	"testing"

	"github.com/yashok111/mocker/internal/guide"
)

// A client must qualify the implemented API pin procedure through discovery,
// while current guide v7 retains the earlier schemas and pin contracts.
func TestBackendAPIArtifactCapabilityNegotiation(t *testing.T) {
	s := loopbackTestServer(t, nil)
	status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", "/api/backend-projects/capabilities", nil)
	if err != nil || status != 200 {
		t.Fatalf("capabilities: %d %s %v", status, raw, err)
	}
	var out struct {
		Features            []string         `json:"features"`
		ViewSchemaVersions  []string         `json:"viewSchemaVersions"`
		ModelSchemaVersions []string         `json:"modelSchemaVersions"`
		ProviderProfiles    []string         `json:"providerProfiles"`
		WorkflowVersions    []guide.Workflow `json:"workflowVersions"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(out.Features, "backend-api-artifact-pins") || !slices.Contains(out.ViewSchemaVersions, "api-artifact-pins-v1") {
		t.Fatalf("API pin procedure cannot qualify: features=%v contracts=%v", out.Features, out.ViewSchemaVersions)
	}
	if !slices.Equal(out.ModelSchemaVersions, []string{"1", "2", "3", "4", "5", "6"}) || !slices.Equal(out.ProviderProfiles, []string{"foundation-graph-v1", "relational-graph-v1", "runtime-flow-v1", "field-lineage-v1", "events-service-v1", "composed-source-v1"}) {
		t.Fatalf("API contract changed source/profile versions: %+v", out)
	}
	for _, want := range []struct {
		owner, version string
		schemas        []string
	}{
		{"mocker-backend-import", "8", []string{"1", "2", "3", "4", "5", "6"}},
		{"mocker-backend-database", "7", []string{"2", "3", "4", "5", "6"}},
		{"mocker-backend-inspect", "9", []string{"3", "4", "5", "6"}},
	} {
		i := slices.IndexFunc(out.WorkflowVersions, func(w guide.Workflow) bool { return w.WorkflowID == want.owner })
		if i < 0 || out.WorkflowVersions[i].WorkflowVersion != want.version {
			t.Fatalf("missing compatible %s v%s: %+v", want.owner, want.version, out.WorkflowVersions)
		}
		w := out.WorkflowVersions[i]
		if !slices.Equal(w.RequiredModelSchemaVersions, want.schemas) {
			t.Fatalf("%s v%s changed retained schema requirements: %v; want %v", w.WorkflowID, want.version, w.RequiredModelSchemaVersions, want.schemas)
		}
		for _, schema := range w.RequiredModelSchemaVersions {
			if !slices.Contains(out.ModelSchemaVersions, schema) {
				t.Fatalf("%s cannot qualify: missing schema %s", w.WorkflowID, schema)
			}
		}
		for _, feature := range w.RequiredCapabilities {
			if !slices.Contains(out.Features, feature) {
				t.Fatalf("%s cannot qualify: missing %s", w.WorkflowID, feature)
			}
		}
		for _, contract := range w.RequiredViewSchemaVersions {
			if !slices.Contains(out.ViewSchemaVersions, contract) {
				t.Fatalf("%s cannot qualify: missing %s", w.WorkflowID, contract)
			}
		}
	}
}
