package mcp

import (
	"strings"
	"testing"
)

func TestBackendEventsSDKExactPins(t *testing.T) {
	calls := &recordingCaller{status: 200, body: []byte(`{"examinedEdgeCount":9007199254740993}`)}
	raw, msg := callTool(t, calls, "query_backend_events", `{"projectId":"`+backendTestID+`","revisionId":"`+backendTestID+`","view":"routes"}`)
	if msg != "" || calls.method != "POST" || calls.path != "/api/backend-projects/"+backendTestID+"/events/query" || !strings.Contains(string(calls.sent), `"revisionId":"`+backendTestID+`"`) || strings.Contains(string(calls.sent), "projectId") || !strings.Contains(string(raw), "9007199254740993") {
		t.Fatalf("pin/raw loss %s %s %s %s", msg, calls.path, calls.sent, raw)
	}
}

func TestBackendEventsSDKStrictViewSelectors(t *testing.T) {
	calls := &recordingCaller{status: 200, body: []byte(`{}`)}
	fixture := newToolFixture(calls)
	base := `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","view":`
	for _, view := range []string{"routes", "jobs", "service_calls"} {
		selector, wrong := "serviceId", "seedNodeId"
		if view == "routes" {
			selector, wrong = wrong, selector
		}
		valid := base + `"` + view + `"`
		*calls = recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := fixture.Call(t, "query_backend_events", valid+`,"`+selector+`":"`+backendTestID+`","limit":100}`)
		if msg != "" || calls.method != "POST" || !strings.Contains(string(calls.sent), `"`+selector+`":"`+backendTestID+`"`) {
			t.Fatalf("valid selector refused %s %s", msg, calls.sent)
		}
		for _, suffix := range []string{`,"` + wrong + `":"` + backendTestID + `"}`, `,"` + selector + `":null}`, `,"` + selector + `":""}`, `,"proposal":{}}`, `,"extra":true}`, `,"limit":null}`, `,"limit":0}`, `,"limit":101}`, `,"view":"routes"}`, `,"cursor":null}`} {
			*calls = recordingCaller{status: 200, body: []byte(`{}`)}
			_, msg := fixture.Call(t, "query_backend_events", valid+suffix)
			if msg == "" || calls.method != "" {
				t.Errorf("invalid events input reached route %s %s", suffix, msg)
			}
		}
	}
	for _, args := range []string{`{"projectId":"` + backendTestID + `","view":"routes"}`, base + `"other"}`} {
		*calls = recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := fixture.Call(t, "query_backend_events", args)
		if msg == "" || calls.method != "" {
			t.Error("invalid pin/view reached route", args, msg)
		}
	}
}

func TestBackendEventsSDKContextualLineagePin(t *testing.T) {
	const secondRoute = "00000000-0000-4000-8000-000000000002"
	for _, route := range []string{backendTestID, secondRoute} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		input := `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","direction":"forward","seed":{"kind":"event_field","nodeId":"` + backendTestID + `","endpointId":"` + backendTestID + `","routeId":"` + route + `"}}`
		_, msg := callTool(t, calls, "query_backend_lineage", input)
		if msg != "" || calls.method != "POST" || !strings.Contains(string(calls.sent), `"routeId":"`+route+`"`) || !strings.Contains(string(calls.sent), `"endpointId":"`+backendTestID+`"`) {
			t.Fatalf("context loss %s %s", msg, calls.sent)
		}
	}
}
