package mcp

import (
	"encoding/json/v2"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/api"
)

// maxToolsListBytes bounds the whole tools/list answer. Review 2026-10-06,
// F23: api.BackendSchema inlines every $ref, and the backend tools published
// those expanded schemas, so one tools/list measured 4,723,098 bytes (the
// start_backend_analysis input schema alone 906,620). Sharing repeated
// subtrees through $defs brought it to 1,551,340; the owner then split the
// surface (tool_catalog.go): tools/list is a one-sentence summary with a
// one-level argument schema per tool, 111,246 bytes for 263 tools on the
// day, and describe_tool returns one tool's full form. Validation still
// compiles the expanded registered schema.
const maxToolsListBytes = 256 << 10

func TestToolsListStaysBounded(t *testing.T) {
	response := doMCP(t, newTestEndpoint(t).Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + testKey})
	size := response.Body.Len()
	var env struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema any    `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	largest, largestName := 0, ""
	for _, tool := range env.Result.Tools {
		raw, _ := json.Marshal(tool.InputSchema)
		if len(raw) > largest {
			largest, largestName = len(raw), tool.Name
		}
	}
	t.Logf("tools/list: %d bytes, %d tools, largest input schema %s at %d bytes", size, len(env.Result.Tools), largestName, largest)
	if size > maxToolsListBytes {
		t.Errorf("tools/list is %d bytes, cap %d", size, maxToolsListBytes)
	}
}

// inlineLocalDefs undoes compactSchema: every bare "#/$defs/<id>" reference
// is replaced by its definition and $defs is dropped, giving back the
// expanded schema a test compares against the contract.
func inlineLocalDefs(schema map[string]any) map[string]any {
	defs, _ := schema["$defs"].(map[string]any)
	var inline func(any) any
	inline = func(value any) any {
		switch v := value.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok && len(v) == 1 && strings.HasPrefix(ref, "#/$defs/") {
				return inline(defs[strings.TrimPrefix(ref, "#/$defs/")])
			}
			out := map[string]any{}
			for key, child := range v {
				if key != "$defs" {
					out[key] = inline(child)
				}
			}
			return out
		case []any:
			out := make([]any, len(v))
			for i, child := range v {
				out[i] = inline(child)
			}
			return out
		}
		return value
	}
	return inline(schema).(map[string]any)
}

// TestCompactSchemaIsLossless proves the published form is the same
// constraint: inlining its $refs gives back the expanded schema exactly, and
// the validator compiles it.
func TestCompactSchemaIsLossless(t *testing.T) {
	for _, name := range []string{"StartBackendAnalysisRequest", "ApplyBackendChangeProposalCommandsRequest", "PutBackendImportBatchRequest"} {
		t.Run(name, func(t *testing.T) {
			expanded, err := api.BackendSchema(name)
			if err != nil {
				t.Fatal(err)
			}
			compact, ok := compactSchema(expanded).(map[string]any)
			if !ok || compact["$defs"] == nil {
				t.Fatal("nothing was shared")
			}
			if !reflect.DeepEqual(inlineLocalDefs(compact), expanded) {
				t.Fatal("inlining the compact schema does not give the expanded one back")
			}
			if _, err := compileBackendImportToolSchema(&sdk.Tool{Name: "compact", InputSchema: compact}); err != nil {
				t.Fatalf("compact schema does not compile: %v", err)
			}
		})
	}
}
