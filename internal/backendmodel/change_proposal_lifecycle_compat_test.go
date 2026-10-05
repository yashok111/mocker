package backendmodel

import (
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"
)

func TestB43UnknownLifecyclePreservesLegacyStatusAfterClosedDecode(t *testing.T) {
	const body = `{"expectedVersion":1,"proposalRevisionId":"0197aaf9-5555-7000-8000-000000000001","action":"merge","idempotencyKey":"old-client","report":{"jobId":"0197aaf9-5555-7000-8000-000000000001","resultVersion":1,"inputHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","resultHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"acknowledgedGapIds":[]}`
	for _, tt := range []struct {
		name, body string
		status     int
	}{
		{"old closed unsupported", body, 422},
		{"null action", strings.Replace(body, `"merge"`, `null`, 1), 400},
		{"missing array", strings.Replace(body, `,"acknowledgedGapIds":[]`, "", 1), 400},
		{"null array", strings.Replace(body, `"acknowledgedGapIds":[]`, `"acknowledgedGapIds":null`, 1), 400},
		{"unknown field", strings.Replace(body, `"expectedVersion":1`, `"extra":true,"expectedVersion":1`, 1), 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var input ApplyChangeProposalLifecycleInput
			err := json.Unmarshal([]byte(tt.body), &input)
			fault, ok := errors.AsType[*FaultError](err)
			if !ok || fault.Status != tt.status {
				t.Fatalf("status want %d: %v", tt.status, err)
			}
			if tt.status == 422 && fault.Code != "backend_unsupported_scope" {
				t.Fatal(fault)
			}
		})
	}
}
