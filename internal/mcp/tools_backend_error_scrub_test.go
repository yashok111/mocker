package mcp

import (
	"strings"
	"testing"
)

// TestBackendToolFamiliesScrub5xxBodies pins review 2026-10-06, F133: the
// import, artifact and API-artifact tool families turned every non-2xx into
// "HTTP %d: <raw body>", 5xx included, while toolErr's §I rule (followed by
// addBackendTool) is that a 5xx body is never echoed. A 4xx keeps the exact
// envelope, which carries currentVersion and retry metadata.
func TestBackendToolFamiliesScrub5xxBodies(t *testing.T) {
	secret := `{"error":{"code":"backend_internal","message":"Unable to complete backend project operation","retryable":false},"leak":"internal-detail"}`
	for name, args := range map[string]string{
		"list_backend_imports":                  `{"projectId":"` + backendTestID + `"}`,
		"get_design_scenario_artifact_snapshot": `{"scenarioId":"1","revisionId":"1"}`,
		"get_api_artifact_snapshot":             `{"artifactId":"1","revisionId":"1"}`,
		"get_backend_project":                   `{"projectId":"` + backendTestID + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, msg := callTool(t, &fakeCaller{status: 500, body: []byte(secret)}, name, args)
			if msg == "" || strings.Contains(msg, "internal-detail") || strings.Contains(msg, "Unable to complete") {
				t.Errorf("5xx surfaced as %q", msg)
			}
			conflict := `{"error":{"code":"backend_version_conflict","message":"Project changed","retryable":false,"currentVersion":6}}`
			_, msg = callTool(t, &fakeCaller{status: 409, body: []byte(conflict)}, name, args)
			if !strings.Contains(msg, `"currentVersion":6`) {
				t.Errorf("4xx lost its envelope: %q", msg)
			}
		})
	}
}
