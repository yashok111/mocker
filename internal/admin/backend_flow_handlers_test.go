package admin

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendFlowRoutePinnedScopeAndStrictInput(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Flow", IdempotencyKey: "flow"})
	if err != nil {
		t.Fatal(err)
	}
	route := "/api/backend-projects/" + p.ID + "/flow/query"
	for _, tc := range []struct {
		body   string
		status int
		code   string
	}{
		{`{"revisionId":"` + p.CurrentRevisionID + `","view":"entrypoints"}`, 422, "backend_unsupported_scope"},
		{`{"revisionId":"` + p.CurrentRevisionID + `","view":"entrypoints","flowId":""}`, 400, "backend_invalid"},
		{`{"revisionId":"` + p.CurrentRevisionID + `","view":"accesses","entrypointId":"` + p.ID + `","dataNodeId":"` + p.ID + `"}`, 400, "backend_invalid"},
		{`{"revisionId":"` + p.CurrentRevisionID + `","view":"entrypoints","limit":0}`, 400, "backend_invalid"},
		{`{"revisionId":"` + p.CurrentRevisionID + `","view":"entrypoints","limit":null}`, 400, "backend_invalid"},
		{`{"revisionId":"` + p.CurrentRevisionID + `","view":"entrypoints","view":"steps"}`, 400, "backend_invalid"},
		{`{"proposal":{"proposalId":"` + p.ID + `","proposalRevisionId":"` + p.ID + `"},"view":"entrypoints"}`, 400, "backend_invalid"},
	} {
		status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", route, []byte(tc.body))
		if err != nil || status != tc.status || !strings.Contains(string(raw), tc.code) {
			t.Errorf("%s: %d %s %v", tc.body, status, raw, err)
		}
	}
}

func TestBackendFlowNeverCreatesCheckpoint(t *testing.T) {
	for _, route := range (&Server{}).routes() {
		if route.pattern == "POST /api/backend-projects/{id}/flow/query" {
			if route.checkpoint != cpNeverTouchesLayer {
				t.Fatalf("flow read checkpoint policy: %v", route.checkpoint)
			}
			return
		}
	}
	t.Fatal("flow route missing")
}
