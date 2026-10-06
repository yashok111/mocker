package mcp

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func TestBackendObservationExactRoute(t *testing.T) {
	c := &recordingCaller{status: 200, body: []byte(`{}`)}
	_, msg := callTool(t, c, "get_backend_observation_records", `{"projectId":"`+backendTestID+`","observationSetId":"`+backendTestID+`","version":9007199254740993,"limit":10}`)
	if msg != "" || !strings.Contains(c.path, "/versions/9007199254740993/records") {
		t.Fatal(msg, c.path)
	}
}
func TestBackendObservationUnknownField(t *testing.T) {
	c := &recordingCaller{status: 200}
	_, msg := callTool(t, c, "get_backend_observation_version", `{"projectId":"`+backendTestID+`","observationSetId":"`+backendTestID+`","version":1,"latest":true}`)
	if msg == "" || c.method != "" {
		t.Fatal("unknown/latest admitted")
	}
}

func TestBackendObservationRoster(t *testing.T) {
	response := doMCP(t, newToolFixture(&recordingCaller{}).Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + testKey})
	var env struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Annotations struct {
					ReadOnlyHint   bool `json:"readOnlyHint"`
					IdempotentHint bool `json:"idempotentHint"`
				} `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{"import_backend_observations": false, "list_backend_observations": true, "get_backend_observation_version": true, "get_backend_observation_records": true, "adapt_backend_observations": true, "correlate_backend_observations": false, "get_backend_observation_correlation": true}
	for _, tool := range env.Result.Tools {
		if read, ok := expected[tool.Name]; ok {
			if tool.Annotations.ReadOnlyHint != read || !tool.Annotations.IdempotentHint {
				t.Fatal("incorrect hints", tool.Name)
			}
			delete(expected, tool.Name)
		}
	}
	if len(expected) != 0 || len(env.Result.Tools) != toolCount {
		t.Fatal("registration mismatch", len(env.Result.Tools), toolCount, expected)
	}
}
