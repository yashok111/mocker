package mcp

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestSchemaModelToolsUseSharedAdminCommands(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, args, command string }{
		{"create_api_schema", `"schemaName":"Order","schemaJSON":"{\"type\":\"object\",\"example\":9007199254740993}"`, `{"kind":"create_schema","schemaName":"Order","schemaJSON":"{\"type\":\"object\",\"example\":9007199254740993}"}`},
		{"replace_api_schema", `"schemaName":"Order","schemaJSON":"false"`, `{"kind":"replace_schema","schemaName":"Order","schemaJSON":"false"}`},
		{"rename_api_schema", `"schemaName":"User","newName":"Customer"`, `{"kind":"rename_schema","schemaName":"User","newName":"Customer"}`},
		{"delete_api_schema", `"schemaName":"User"`, `{"kind":"delete_schema","schemaName":"User"}`},
		{"upsert_api_schema_property", `"schemaName":"Order","propertyName":"id","schemaJSON":"true","required":false`, `{"kind":"upsert_property","schemaName":"Order","propertyName":"id","schemaJSON":"true","required":false}`},
		{"upsert_api_schema_property", `"schemaName":"Order","propertyName":"id","schemaJSON":"true"`, `{"kind":"upsert_property","schemaName":"Order","propertyName":"id","schemaJSON":"true"}`},
		{"rename_api_schema_property", `"schemaName":"Order","propertyName":"id","newName":"orderId"`, `{"kind":"rename_property","schemaName":"Order","newName":"orderId","propertyName":"id"}`},
		{"delete_api_schema_property", `"schemaName":"Order","propertyName":"id"`, `{"kind":"delete_property","schemaName":"Order","propertyName":"id"}`},
		{"set_api_schema_reference", `"schemaName":"Order","propertyName":"user","targetSchema":"User","array":false`, `{"kind":"set_reference","schemaName":"Order","propertyName":"user","targetSchema":"User","array":false}`},
		{"move_api_schema", `"schemaName":"Order","x":0,"y":-1.5`, `{"kind":"move_schema","schemaName":"Order","x":0,"y":-1.5}`},
		{"apply_schema_model_commands", `"commands":[{"kind":"create_schema","schemaName":"Order","schemaJSON":"{}"}]`, `{"kind":"create_schema","schemaName":"Order","schemaJSON":"{}"}`},
	} {
		t.Run(tt.name+tt.args, func(t *testing.T) {
			t.Parallel()
			calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"version":9007199254740993,"document":"unchanged"}`)}
			args := `{"designId":7,"expectedVersion":9007199254740993,` + tt.args + `}`
			raw, msg := callTool(t, calls, tt.name, args)
			if msg != "" || calls.method != "POST" || calls.path != "/api/designs/7/schema-model/commands" {
				t.Fatalf("%s %s: %s", calls.method, calls.path, msg)
			}
			if !strings.Contains(string(raw), `9007199254740993`) {
				t.Fatalf("response rounded integer: %s", raw)
			}
			var body struct {
				ExpectedVersion int64              `json:"expectedVersion"`
				Commands        []jsonx.RawMessage `json:"commands"`
			}
			if err := jsonx.Unmarshal(calls.sent, &body); err != nil || body.ExpectedVersion != 9007199254740993 || len(body.Commands) != 1 {
				t.Fatalf("bad version/commands: %s (%v)", calls.sent, err)
			}
			if strings.Contains(string(calls.sent), `"designId"`) || string(body.Commands[0]) != tt.command {
				t.Fatalf("sent %s; want command %s", calls.sent, tt.command)
			}
		})
	}
}

func TestSchemaModelReadAndPreviewRoutes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, args, method, path, body string }{
		{"get_schema_model", `{"designId":7}`, "GET", "/api/designs/7/schema-model", ""},
		{"preview_schema_model_changes", `{"designId":7}`, "POST", "/api/designs/7/schema-model/preview", `{}`},
		{"preview_schema_model_changes", `{"designId":7,"document":"{\"example\":9007199254740993}","commands":[{"kind":"move_schema","schemaName":"Order","x":0,"y":0}]}`, "POST", "/api/designs/7/schema-model/preview", `{"document":"{\"example\":9007199254740993}","commands":[{"kind":"move_schema","schemaName":"Order","x":0,"y":0}]}`},
	} {
		t.Run(tt.name+tt.args, func(t *testing.T) {
			calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"model":{"schemas":[]},"valid":true,"diagnostics":[]}`)}
			_, msg := callTool(t, calls, tt.name, tt.args)
			if msg != "" || calls.method != tt.method || calls.path != tt.path || string(calls.sent) != tt.body {
				t.Fatalf("%s %s %s: %s", calls.method, calls.path, calls.sent, msg)
			}
		})
	}
}

func TestSchemaModelToolSchemasRejectMalformedInputsBeforeCall(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, args string }{
		{"get_schema_model", `{"designId":0}`},
		{"preview_schema_model_changes", `{"designId":7,"commands":[{"kind":"unknown"}]}`},
		{"apply_schema_model_commands", `{"designId":7,"expectedVersion":3,"commands":[]}`},
		{"apply_schema_model_commands", `{"designId":7,"expectedVersion":3,"commands":[{"kind":"move_schema","schemaName":"Order","x":0}]}`},
		{"create_api_schema", `{"designId":7,"schemaName":"Order","schemaJSON":"{}"}`},
		{"create_api_schema", `{"designId":7,"expectedVersion":0,"schemaName":"Order","schemaJSON":"{}"}`},
		{"create_api_schema", `{"designId":7,"expectedVersion":3,"schemaName":"Order","schemaJSON":{}}`},
		{"delete_api_schema", `{"designId":7,"expectedVersion":3,"schemaName":"Order","newName":"Ignored"}`},
		{"upsert_api_schema_property", `{"designId":7,"expectedVersion":3,"schemaName":"Order","propertyName":"id","schemaJSON":"{}","required":null}`},
		{"move_api_schema", `{"designId":7,"expectedVersion":3,"schemaName":"Order","x":100001,"y":0}`},
		{"move_api_schema", `{"designId":7,"expectedVersion":3,"schemaName":"Order","x":1e999,"y":0}`},
		{"move_api_schema", `{"designId":7,"expectedVersion":3,"schemaName":"Order","x":null,"y":0}`},
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

func TestSchemaModelToolsAdvertiseAuthoritativeSchemas(t *testing.T) {
	t.Parallel()
	ep := newTestEndpoint(t)
	rec := doMCP(t, ep.Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + testKey})
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
		if !strings.Contains(strings.Join(toolRoutes[tool.Name], " "), "/schema-model") {
			continue
		}
		seen++
		readOnly := tool.Name == "get_schema_model" || tool.Name == "preview_schema_model_changes"
		if tool.Annotations.ReadOnlyHint != readOnly || slices.Contains(tool.InputSchema.Required, "expectedVersion") == readOnly {
			t.Errorf("%s: incorrect version/readOnly schema", tool.Name)
		}
		if p := tool.InputSchema.Properties["commands"]; p != nil && (!strings.Contains(string(p), `"oneOf"`) || !strings.Contains(string(p), `"maxItems":100`)) {
			t.Errorf("%s: commands lack complete discriminated schema: %s", tool.Name, p)
		}
		if tool.Name == "upsert_api_schema_property" && (slices.Contains(tool.InputSchema.Required, "required") || strings.Contains(string(tool.InputSchema.Properties["required"]), "null")) {
			t.Errorf("optional required must be a non-null boolean")
		}
	}
	if seen != 12 {
		t.Fatalf("saw %d schema model tools, want 12; %s", seen, rec.Body.String())
	}
}

func TestSchemaModelToolsPreserveConflictDetails(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusConflict, body: []byte(`{"error":{"code":"design_conflict","message":"draft changed","details":{"version":9,"draftRevisionId":23}}}`)}
	_, msg := callTool(t, calls, "delete_api_schema", `{"designId":7,"expectedVersion":3,"schemaName":"Order"}`)
	if !strings.Contains(msg, "409") || !strings.Contains(msg, `"version":9`) {
		t.Fatalf("missing reconciliation details: %s", msg)
	}
}

func TestSchemaModelToolsLifecycleThroughRealAdmin(t *testing.T) {
	t.Parallel()
	cfg := resourcesTestConfig(t)
	server, _ := newResourcesTestServer(t, cfg)
	fixture := newToolFixture(server)
	var detail struct {
		Design struct{ ID, Version int64 } `json:"design"`
		Draft  struct {
			ID       int64
			Document string
		} `json:"draft"`
		Revisions []jsonx.RawMessage `json:"revisions"`
	}
	invoke := func(name string, args any) jsonx.RawMessage {
		t.Helper()
		body, err := jsonx.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		raw, msg := fixture.Call(t, name, string(body))
		if msg != "" {
			t.Fatalf("%s: %s", name, msg)
		}
		return raw
	}
	readDetail := func(raw []byte) {
		t.Helper()
		if err := jsonx.Unmarshal(raw, &detail); err != nil {
			t.Fatal(err)
		}
	}
	readDetail(invoke("create_api_design", map[string]any{"name": "Schema model lifecycle", "document": `{"openapi":"3.1.0","info":{"title":"Orders","version":"1"},"paths":{"/orders":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}}}}}},"components":{"schemas":{"Order":{"type":"object"}}},"x-kept":{"example":9007199254740993}}`}))
	id := detail.Design.ID
	write := func(name string, fields map[string]any) {
		t.Helper()
		fields["designId"], fields["expectedVersion"] = id, detail.Design.Version
		readDetail(invoke(name, fields))
	}
	write("create_api_schema", map[string]any{"schemaName": "User", "schemaJSON": `{"type":"object","x-unknown":{"example":9007199254740993}}`})
	write("upsert_api_schema_property", map[string]any{"schemaName": "User", "propertyName": "id", "schemaJSON": `{"type":"integer","minimum":1,"example":9007199254740993}`, "required": true})
	write("upsert_api_schema_property", map[string]any{"schemaName": "Order", "propertyName": "user", "schemaJSON": `{"description":"The customer"}`, "required": true})
	write("set_api_schema_reference", map[string]any{"schemaName": "Order", "propertyName": "user", "targetSchema": "User"})
	write("move_api_schema", map[string]any{"schemaName": "User", "x": 0, "y": 240})
	write("rename_api_schema_property", map[string]any{"schemaName": "User", "propertyName": "id", "newName": "userId"})
	write("rename_api_schema", map[string]any{"schemaName": "User", "newName": "Customer"})
	var model struct {
		Version int64 `json:"version"`
		Model   struct {
			Schemas []struct {
				Name, SchemaJSON string
				X, Y             float64
			} `json:"schemas"`
			Operations []struct {
				Method, Path string
				Schemas      []string
			} `json:"operations"`
		} `json:"model"`
	}
	raw := invoke("get_schema_model", map[string]any{"designId": id})
	if err := jsonx.Unmarshal(raw, &model); err != nil {
		t.Fatal(err)
	}
	if model.Version != detail.Design.Version || len(model.Model.Operations) != 1 || !slices.Contains(model.Model.Operations[0].Schemas, "Customer") {
		t.Fatalf("missing transitive usage/version: %s", raw)
	}
	idx := slices.IndexFunc(model.Model.Schemas, func(s struct {
		Name, SchemaJSON string
		X, Y             float64
	}) bool {
		return s.Name == "Customer"
	})
	if idx < 0 || model.Model.Schemas[idx].Y != 240 || !strings.Contains(model.Model.Schemas[idx].SchemaJSON, "9007199254740993") {
		t.Fatalf("rename lost layout/exact data: %s", raw)
	}
	version, revision, count, document := detail.Design.Version, detail.Draft.ID, len(detail.Revisions), detail.Draft.Document
	preview := invoke("preview_schema_model_changes", map[string]any{"designId": id, "document": document, "commands": []any{map[string]any{"kind": "move_schema", "schemaName": "Customer", "x": 99, "y": 99}}})
	if !strings.Contains(string(preview), `"valid":true`) {
		t.Fatalf("preview invalid: %s", preview)
	}
	readDetail(invoke("get_api_design", map[string]any{"designId": id}))
	if detail.Design.Version != version || detail.Draft.ID != revision || len(detail.Revisions) != count || detail.Draft.Document != document {
		t.Fatal("preview persisted a document or history change")
	}
	for _, fields := range []map[string]any{
		{"designId": id, "expectedVersion": version - 1, "schemaName": "Customer"},
		{"designId": id, "expectedVersion": version, "schemaName": "Customer"},
	} {
		args, err := jsonx.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		_, msg := fixture.Call(t, "delete_api_schema", string(args))
		if msg == "" {
			t.Fatal("stale/referenced deletion succeeded")
		}
		if fields["expectedVersion"] == version-1 && !strings.Contains(msg, "409") {
			t.Fatalf("expected stale conflict: %s", msg)
		}
	}
	write("delete_api_schema_property", map[string]any{"schemaName": "Order", "propertyName": "user"})
	write("replace_api_schema", map[string]any{"schemaName": "Customer", "schemaJSON": `false`})
	write("delete_api_schema", map[string]any{"schemaName": "Customer"})
	write("apply_schema_model_commands", map[string]any{"commands": []any{map[string]any{"kind": "create_schema", "schemaName": "OrderItem", "schemaJSON": `{"type":"object"}`}}})
	if !strings.Contains(detail.Draft.Document, "9007199254740993") {
		t.Fatal("unrelated extension number lost")
	}
}
