package api

import (
	"strings"
	"testing"
)

// TestBackendIdempotencyKeysMatchTheServer pins review 2026-10-06, F19: the
// diagram and diagram-view write contracts admitted any 1..128 characters
// while the server's validateKey refuses everything outside printable ASCII,
// and ReviewBackendFindingRequest admitted 256 characters while the server
// accepts at most 128 bytes. A key the contract calls valid was refused
// with a generic closed-schema 422.
func TestBackendIdempotencyKeysMatchTheServer(t *testing.T) {
	for _, name := range []string{"CreateBackendDiagramRequest", "SaveBackendDiagramRequest", "ForkBackendDiagramRequest", "CreateBackendDiagramViewRequest", "SaveBackendDiagramViewRequest"} {
		schema, err := BackendSchema(name)
		if err != nil {
			t.Fatal(err)
		}
		key, _ := schema["properties"].(map[string]any)["idempotencyKey"].(map[string]any)
		if key["pattern"] != "^[!-~]+$" {
			t.Errorf("%s idempotencyKey = %v, want the printable-ASCII pattern", name, key)
		}
	}
	validate := lineageSchemaValidator(t, "ReviewBackendFindingRequest")
	raw := `{"expectedVersion":1,"basisHash":"` + strings.Repeat("a", 64) + `","status":"accepted_risk","reason":"reviewed","idempotencyKey":"` + strings.Repeat("k", 129) + `"}`
	if validate(raw) == nil {
		t.Error("ReviewBackendFindingRequest admits a 129-character key the server refuses")
	}
}
