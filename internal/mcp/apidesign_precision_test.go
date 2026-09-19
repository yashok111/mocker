package mcp

import (
	"net/http"
	"strings"
	"testing"
)

func TestAPIDesignMCPDiffPreservesLargeNumbers(t *testing.T) {
	calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"changes":[{"pointer":"/example/id","kind":"changed","before":9007199254740993,"after":9007199254740994,"impact":"review","description":"value"}]}`)}
	raw, errMsg := callTool(t, calls, "get_api_design_diff", `{"designId":7}`)
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	if !strings.Contains(string(raw), `"before":9007199254740993`) {
		t.Fatalf("authored number changed on MCP boundary: %s", raw)
	}
}
