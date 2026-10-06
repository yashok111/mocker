package mcp

import (
	"fmt"
	"strings"
	"testing"
)

func TestBackendMeasurementSDKParity(t *testing.T) {
	for _, kind := range []string{"scenario_measurement", "scenario_comparison"} {
		t.Run(kind, func(t *testing.T) {
			pin := fmt.Sprintf(`{"observationSetId":%q,"version":1,"contentHash":%q,"correlationVersion":1,"correlationHash":%q,"side":"before"}`, backendTestID, strings.Repeat("a", 64), strings.Repeat("b", 64))
			selection := `{"side":"before","executionIds":["run"],"rootSpanIds":["root"],"basis":{"kind":"spans"},"policy":"backend-scenario-measures-v1"}`
			after := ""
			if kind == "scenario_comparison" {
				after = `,"afterRevisionId":"` + backendTestID + `"`
				pin += "," + strings.ReplaceAll(pin, `"before"`, `"after"`)
				selection += "," + strings.ReplaceAll(selection, `"before"`, `"after"`)
			}
			input := fmt.Sprintf(`{"projectId":%q,"kind":%q,"beforeRevisionId":%q%s,"observationPins":[%s],"measurements":[%s],"limits":{},"idempotencyKey":"measure"}`, backendTestID, kind, backendTestID, after, pin, selection)
			c := &recordingCaller{status: 202, body: []byte(`{}`)}
			_, msg := callTool(t, c, "start_backend_analysis", input)
			if msg != "" || c.method != "POST" {
				t.Fatal(msg, c.method)
			}
			c = &recordingCaller{status: 202, body: []byte(`{}`)}
			_, msg = callTool(t, c, "start_backend_analysis", strings.TrimSuffix(input, "}")+`,"latest":true}`)
			if msg == "" || c.method != "" {
				t.Fatal("latest reached handler", msg)
			}
		})
	}
}
