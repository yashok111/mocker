package backendmodel

import (
	"errors"
	"strings"
	"testing"
)

// TestSource6MalformedReferenceMembersAnswer422 pins review 2026-10-06, F49:
// a caller-supplied source6 member of the wrong shape (a facet without the
// required evidenceKeys, mapping sources that are not an array, a reference
// array given as a string, a scalar reference) used to surface as the raw
// decoder error, which the admin plane answers as a logged 500
// backend_internal. Each must be a 422 FaultError naming the member's path.
func TestSource6MalformedReferenceMembersAnswer422(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, kind, attrs, path string }{
		{"facet without evidenceKeys", "constraint", `{"facets":{"sql/orm~1":{"sourceKind":"sql","dialect":"postgresql","analysisStatus":"complete","gaps":[],"constraintKind":"unique","columnRefs":[],"expression":{"status":"known","value":null},"nativeDefinition":"UNIQUE(id)","deferrable":{"status":"known","value":false},"initiallyDeferred":{"status":"known","value":false}}}}`, "evidenceKeys"},
		{"mapping sources not an array", "field_mapping", `{"analysisStatus":"complete","gaps":[],"sources":"x","destination":{"kind":"column","nodeRef":{"localKey":"node-a"},"facetKey":"sql"},"transform":{"kind":"constant","description":"1","redacted":false}}`, "/attributes/sources"},
		{"reference array as string", "flow", `{"analysisStatus":"complete","gaps":[],"entryStepRef":{"localKey":"node-a"},"exitStepRefs":"node-a","exitStatus":"complete"}`, "exitStep"},
		{"scalar reference", "flow", `{"analysisStatus":"complete","gaps":[],"entryStepRef":"node-a","exitStepRefs":[],"exitStatus":"complete"}`, "entryStep"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := ImportCommand{Op: "upsert_node", Node: &ImportNode{Kind: tc.kind, Name: "subject", Attributes: sourceContractAttrs(t, tc.attrs)}}
			_, _, err := normalizeSourcePayload(command, &ImportSession{SnapshotID: sourceContractSnapshot, BaseRevisionID: sourceContractBase}, func(SourceReferenceSite) (BaseAssertionRef, error) {
				return BaseAssertionRef{RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", RecordType: "node", ExternalKey: "node-a", ExpectedID: sourceContractNode, AssertionHash: strings.Repeat("a", 64)}, nil
			}, func(string) (string, error) { return sourceContractProof, nil })
			fault, ok := errors.AsType[*FaultError](err)
			if !ok || fault.Status != 422 {
				t.Fatalf("err = %T %v, want a 422 FaultError", err, err)
			}
			if path, _ := fault.Details["path"].(string); !strings.Contains(path, tc.path) {
				t.Errorf("details.path = %q, want it to name %q", path, tc.path)
			}
		})
	}
}
