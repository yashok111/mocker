package mcp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestResourceMapToolsDispatchThroughAdmin(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, args, method, path, body string }{
		{"auto_layout_api_resources", `{"designId":7,"expectedVersion":2}`, "POST", "/api/designs/7/resource-map/commands", `{"expectedVersion":2,"commands":[{"kind":"auto_layout"}]}`},
		{"get_api_resource_map", `{"designId":7}`, "GET", "/api/designs/7/resource-map", ""},
		{"preview_api_resource_map", `{"designId":7}`, "POST", "/api/designs/7/resource-map/preview", `{}`},
		{"apply_api_resource_map_commands", `{"designId":7,"expectedVersion":2,"commands":[{"kind":"remove_resource","resourceId":"r"}]}`, "POST", "/api/designs/7/resource-map/commands", `{"expectedVersion":2,"commands":[{"kind":"remove_resource","resourceId":"r"}]}`},
		{"apply_api_resource_map_commands", `{"designId":7,"expectedVersion":2,"commands":[{"kind":"assign_operation","operationKey":"order-get","resourceId":""}]}`, "POST", "/api/designs/7/resource-map/commands", `{"expectedVersion":2,"commands":[{"kind":"assign_operation","operationKey":"order-get","resourceId":""}]}`},
		{"move_api_resource", `{"designId":7,"expectedVersion":2,"resourceId":"r","x":0,"y":1}`, "POST", "/api/designs/7/resource-map/commands", `{"expectedVersion":2,"commands":[{"kind":"move_resource","resourceId":"r","x":0,"y":1}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
			_, msg := callTool(t, calls, tt.name, tt.args)
			if msg != "" || calls.method != tt.method || calls.path != tt.path || string(calls.sent) != tt.body {
				t.Fatalf("%s %s %s: %s", calls.method, calls.path, calls.sent, msg)
			}
		})
	}
}

func TestResourceMapNamedActionsCarryPreciseCommands(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, fields, command string }{
		{"upsert_api_resource", `"resource":{"id":"r","name":"Orders","service":"billing","description":"","operationKeys":[],"x":0,"y":0}`, `"kind":"upsert_resource"`},
		{"remove_api_resource", `"resourceId":"r"`, `"kind":"remove_resource"`},
		{"assign_api_resource_operation", `"operationKey":"order-get","resourceId":""`, `"kind":"assign_operation"`},
		{"upsert_api_resource_relation", `"relation":{"id":"edge","fromResourceId":"r","toResourceId":"s","label":"owns"}`, `"kind":"upsert_relation"`},
		{"remove_api_resource_relation", `"relationId":"edge"`, `"kind":"remove_relation"`},
		{"move_api_resource", `"resourceId":"r","x":0,"y":-1.5`, `"kind":"move_resource"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
			_, msg := callTool(t, calls, tt.name, `{"designId":7,"expectedVersion":9007199254740993,`+tt.fields+`}`)
			if msg != "" || calls.method != "POST" || calls.path != "/api/designs/7/resource-map/commands" {
				t.Fatalf("dispatch: %s %s %s", calls.method, calls.path, msg)
			}
			var body struct {
				ExpectedVersion int64              `json:"expectedVersion"`
				Commands        []jsonx.RawMessage `json:"commands"`
			}
			if err := jsonx.Unmarshal(calls.sent, &body); err != nil || body.ExpectedVersion != 9007199254740993 || len(body.Commands) != 1 {
				t.Fatalf("bad body: %s %v", calls.sent, err)
			}
			if !strings.Contains(string(body.Commands[0]), tt.command) || (tt.name == "assign_api_resource_operation" && !strings.Contains(string(body.Commands[0]), `"resourceId":""`)) {
				t.Fatalf("wrong command: %s", body.Commands[0])
			}
		})
	}
}

func TestResourceMapToolsAdvertiseReadOnlyAndStrictVariants(t *testing.T) {
	t.Parallel()
	rec := describedToolsList(t)
	var env struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Required   []string                    `json:"required"`
					Properties map[string]jsonx.RawMessage `json:"properties"`
				} `json:"inputSchema"`
				Annotations struct {
					ReadOnlyHint bool `json:"readOnlyHint"`
				} `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := jsonx.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, tool := range env.Result.Tools {
		if !strings.Contains(strings.Join(toolRoutes[tool.Name], " "), "/resource-map") {
			continue
		}
		seen++
		readOnly := tool.Name == "get_api_resource_map" || tool.Name == "preview_api_resource_map"
		if tool.Annotations.ReadOnlyHint != readOnly {
			t.Errorf("%s incorrect readOnly annotation", tool.Name)
		}
		if p := tool.InputSchema.Properties["commands"]; p != nil && (!strings.Contains(string(p), `"oneOf"`) || !strings.Contains(string(p), `"maxItems":100`)) {
			t.Errorf("%s incomplete command schema: %s", tool.Name, p)
		}
	}
	if seen != 10 {
		t.Fatalf("saw %d resource map tools, want 10", seen)
	}
}

func TestResourceMapToolSchemasRejectMalformedBeforeDispatch(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, args string }{
		{"auto_layout_api_resources", `{"designId":7}`},
		{"auto_layout_api_resources", `{"designId":7,"expectedVersion":1,"x":1}`},
		{"preview_api_resource_map", `{"designId":7,"commands":[{"kind":"auto_layout","resourceId":"r"}]}`},
		{"get_api_resource_map", `{"designId":0}`},
		{"apply_api_resource_map_commands", `{"designId":7,"expectedVersion":1,"commands":[]}`},
		{"apply_api_resource_map_commands", `{"designId":7,"expectedVersion":1,"commands":[{"kind":"move_resource","resourceId":"r","x":0}]}`},
		{"move_api_resource", `{"designId":7,"expectedVersion":1,"resourceId":"r","x":100001,"y":0}`},
		{"move_api_resource", `{"designId":7,"expectedVersion":1,"resourceId":"r","x":0,"y":0,"extra":1}`},
		{"upsert_api_resource", `{"designId":7,"expectedVersion":1,"resource":{"id":"r","name":"R","service":"","description":"","operationKeys":[],"x":0}}`},
		{"assign_api_resource_operation", `{"designId":7,"expectedVersion":1,"resourceId":"r"}`},
	} {
		t.Run(tt.name+tt.args, func(t *testing.T) {
			calls := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
			_, msg := callTool(t, calls, tt.name, tt.args)
			if msg == "" || calls.method != "" {
				t.Fatalf("invalid input reached admin: %s; error %s", calls.sent, msg)
			}
		})
	}
}

func TestResourceMapToolsPreserveVersionConflict(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusConflict, body: []byte(`{"error":{"code":"design_conflict","message":"draft changed","details":{"version":9,"draftRevisionId":23}}}`)}
	_, msg := callTool(t, calls, "remove_api_resource", `{"designId":7,"expectedVersion":3,"resourceId":"r"}`)
	if !strings.Contains(msg, "409") || !strings.Contains(msg, `"version":9`) {
		t.Fatalf("missing reconciliation details: %s", msg)
	}
}
