package design_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/design"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/router"
)

// baseDoc is the fixture every test composes over: two operations, one of
// them at a parameterised path spelled `{id}`, and a component schema a
// custom endpoint can `$ref` into.
const baseDoc = `{
  "openapi": "3.0.3",
  "info": {"title": "platform", "version": "1.2.0"},
  "components": {"schemas": {
    "User": {"type": "object", "required": ["id"], "properties": {"id": {"type": "integer"}, "name": {"type": "string"}}}
  }},
  "paths": {
    "/users": {"get": {"operationId": "listUsers", "responses": {"200": {"description": "ok",
      "content": {"application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/User"}}}}}}}},
    "/users/{id}": {"get": {"operationId": "getUser", "responses": {"200": {"description": "ok",
      "content": {"application/json": {"schema": {"$ref": "#/components/schemas/User"}}}}}}}
  }
}`

func normalizedBase(t *testing.T) []byte {
	t.Helper()
	doc, _, err := openapi.Load([]byte(baseDoc))
	if err != nil {
		t.Fatalf("load the base fixture: %v", err)
	}
	return doc.Normalized()
}

func composeDoc(t *testing.T, in design.Input) map[string]any {
	t.Helper()
	out, err := design.Compose(in)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("decode the composed document: %v", err)
	}
	return doc
}

func paths(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	p, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatalf("paths is %T, want an object", doc["paths"])
	}
	return p
}

func operation(t *testing.T, doc map[string]any, path, method string) map[string]any {
	t.Helper()
	item, ok := paths(t, doc)[path].(map[string]any)
	if !ok {
		t.Fatalf("no path item at %s; paths = %v", path, keysOf(paths(t, doc)))
	}
	op, ok := item[method].(map[string]any)
	if !ok {
		t.Fatalf("no %s at %s", method, path)
	}
	return op
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func customRow(id int64, method, path string) *customep.Row {
	return &customep.Row{
		ID: id, Method: method, Path: path, CanonicalPath: router.CanonicalPath(path),
		OverrideOn: true, ActiveStatus: 200, Kind: customep.KindHTTP,
		Responses: map[string]overrides.Variant{},
	}
}

// TestCompose_baseAloneIsTheBasePlusADraftVersion: with no delta at all,
// the export is the base document with `-draft.<revision>` on its version
// and nothing else moved. Fails an implementation that rewrites,
// reorders or drops any part of the base.
func TestCompose_baseAloneIsTheBasePlusADraftVersion(t *testing.T) {
	base := normalizedBase(t)
	doc := composeDoc(t, design.Input{Base: base, Revision: 7})

	info := doc["info"].(map[string]any)
	if info["version"] != "1.2.0-draft.7" {
		t.Errorf("info.version = %v, want 1.2.0-draft.7", info["version"])
	}

	var want map[string]any
	if err := json.Unmarshal(base, &want); err != nil {
		t.Fatalf("decode the base: %v", err)
	}
	wantComponents, _ := json.Marshal(want["components"])
	gotComponents, _ := json.Marshal(doc["components"])
	if string(gotComponents) != string(wantComponents) {
		t.Errorf("components changed:\n got %s\nwant %s", gotComponents, wantComponents)
	}
	wantPaths, _ := json.Marshal(want["paths"])
	gotPaths, _ := json.Marshal(doc["paths"])
	if string(gotPaths) != string(wantPaths) {
		t.Errorf("paths changed with an empty delta:\n got %s\nwant %s", gotPaths, wantPaths)
	}
}

// TestCompose_draftSuffixIsStrippedNotStacked: exporting a document that
// was itself an accepted draft gives `1.2.0-draft.9`, never
// `1.2.0-draft.7-draft.9`.
func TestCompose_draftSuffixIsStrippedNotStacked(t *testing.T) {
	first, err := design.Compose(design.Input{Base: normalizedBase(t), Revision: 7})
	if err != nil {
		t.Fatalf("first Compose: %v", err)
	}
	doc := composeDoc(t, design.Input{Base: first, Revision: 9})
	if v := doc["info"].(map[string]any)["version"]; v != "1.2.0-draft.9" {
		t.Errorf("info.version = %v, want 1.2.0-draft.9", v)
	}
}

// TestCompose_customEndpointBecomesAnOperation is rule 2 with the
// interview's additions: the operation fields, the derived path
// parameter, the requestBody from reqSchema, the response schema.
func TestCompose_customEndpointBecomesAnOperation(t *testing.T) {
	row := customRow(1, "POST", "/widgets/{widgetId}")
	row.ActiveStatus = 201
	row.ReqSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)
	row.Operation = &customep.Operation{
		Summary: "Create a widget", Tags: []string{"widgets"}, OperationID: "createWidget",
	}
	row.Responses["201"] = overrides.Variant{Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}}}`)}

	doc := composeDoc(t, design.Input{Base: normalizedBase(t), Revision: 1, Endpoints: []*customep.Row{row}})
	op := operation(t, doc, "/widgets/{widgetId}", "post")

	if op["summary"] != "Create a widget" || op["operationId"] != "createWidget" {
		t.Errorf("summary/operationId = %v/%v, want the row's own", op["summary"], op["operationId"])
	}
	params, ok := op["parameters"].([]any)
	if !ok || len(params) != 1 {
		t.Fatalf("parameters = %v, want one derived path parameter", op["parameters"])
	}
	prm := params[0].(map[string]any)
	if prm["name"] != "widgetId" || prm["in"] != "path" || prm["required"] != true {
		t.Errorf("derived parameter = %v, want {name: widgetId, in: path, required: true}", prm)
	}
	body, ok := op["requestBody"].(map[string]any)
	if !ok {
		t.Fatalf("requestBody = %v, want the reqSchema", op["requestBody"])
	}
	if _, ok := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"]; !ok {
		t.Errorf("requestBody carries no schema: %v", body)
	}
	resp := op["responses"].(map[string]any)["201"].(map[string]any)
	if _, ok := resp["content"].(map[string]any)["application/json"].(map[string]any)["schema"]; !ok {
		t.Errorf("201 response carries no schema: %v", resp)
	}
}

// TestCompose_customEndpointReplacesTheBaseOperationAtEqualCanonicalShape
// is rule 3 read as intent: ONE entry, under the custom row's own
// spelling, and the base's differently-spelled key is gone. Fails an
// implementation that leaves both (a reader would see two operations for
// one route, only one of which the mock serves).
func TestCompose_customEndpointReplacesTheBaseOperationAtEqualCanonicalShape(t *testing.T) {
	row := customRow(1, "GET", "/users/{userId}")
	row.Responses["200"] = overrides.Variant{Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"nickname":{"type":"string"}}}`)}

	doc := composeDoc(t, design.Input{Base: normalizedBase(t), Revision: 3, Endpoints: []*customep.Row{row}})
	p := paths(t, doc)
	if _, present := p["/users/{id}"]; present {
		t.Errorf("the base's /users/{id} survived beside the custom row's /users/{userId}: %v", keysOf(p))
	}
	op := operation(t, doc, "/users/{userId}", "get")
	schema := op["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	if _, ok := props["nickname"]; !ok {
		t.Errorf("the replaced operation does not carry the custom row's schema: %v", schema)
	}
	// The base's untouched sibling is still there.
	operation(t, doc, "/users", "get")
}

// TestCompose_respelledTwinRemovesTheBaseKeyEvenUnderAnOverride mirrors
// the smoke's D8 fixture: an override keyed on the base's GET /users/{id}
// AND a custom row at /users/{userId}. The override is applied first (it
// patches the base operation in place); the twin must still remove the
// base's key afterwards — the override's effect is on an operation the
// document no longer carries, which is exactly why the drift report calls
// that override orphaned after the design is accepted.
func TestCompose_respelledTwinRemovesTheBaseKeyEvenUnderAnOverride(t *testing.T) {
	ov := &overrides.Row{
		Method: "GET", Path: "/users/{id}", OverrideOn: true,
		Responses: map[string]overrides.Variant{"200": {Mode: "generated",
			Recipes: map[string]recipes.Recipe{"name": {Kind: "const", Data: json.RawMessage(`"X"`)}}}},
	}
	twin := customRow(7, "GET", "/users/{userId}")
	twin.Responses["200"] = overrides.Variant{Schema: json.RawMessage(`{"$ref":"#/components/schemas/User"}`)}
	twin.Operation = &customep.Operation{Summary: "A user, respelled", OperationID: "getUserRespelled"}

	doc := composeDoc(t, design.Input{Base: normalizedBase(t), Revision: 1,
		Overrides: map[string]*overrides.Row{overrides.OpKey("GET", "/users/{id}"): ov},
		Endpoints: []*customep.Row{twin}})
	p := paths(t, doc)
	if _, present := p["/users/{id}"]; present {
		t.Errorf("the base's /users/{id} survived beside /users/{userId} under an override: %v", keysOf(p))
	}
	if op := operation(t, doc, "/users/{userId}", "get"); op["operationId"] != "getUserRespelled" {
		t.Errorf("the twin's operation = %v", op)
	}
}

// TestCompose_overrideOffIsOmitted: a row switched off serves nothing and
// is no contract either.
func TestCompose_overrideOffIsOmitted(t *testing.T) {
	row := customRow(1, "GET", "/widgets")
	row.OverrideOn = false
	doc := composeDoc(t, design.Input{Base: normalizedBase(t), Revision: 1, Endpoints: []*customep.Row{row}})
	if _, present := paths(t, doc)["/widgets"]; present {
		t.Errorf("an overrideOn:false row was exported: %v", keysOf(paths(t, doc)))
	}
}

// TestCompose_routeOffIsDeprecatedNotDeleted is rule 5 on both sides: an
// override's routeOff on a base operation, and a custom row's own.
func TestCompose_routeOffIsDeprecatedNotDeleted(t *testing.T) {
	ov := &overrides.Row{Method: "GET", Path: "/users", OverrideOn: true, RouteOff: true}
	row := customRow(1, "GET", "/widgets")
	row.RouteOff = true

	doc := composeDoc(t, design.Input{
		Base: normalizedBase(t), Revision: 1,
		Overrides: map[string]*overrides.Row{overrides.OpKey("GET", "/users"): ov},
		Endpoints: []*customep.Row{row},
	})
	if dep := operation(t, doc, "/users", "get")["deprecated"]; dep != true {
		t.Errorf("the routeOff base operation is not deprecated: %v", dep)
	}
	if dep := operation(t, doc, "/widgets", "get")["deprecated"]; dep != true {
		t.Errorf("the routeOff custom row is not deprecated: %v", dep)
	}
}

// TestCompose_schemaPatchIsWrittenInline is rule 4: the patched schema at
// the response, applied by the SAME primitive the runtime applies, and
// the base's own component untouched (a patch on one operation must not
// edit the shared schema every other operation references).
func TestCompose_schemaPatchIsWrittenInline(t *testing.T) {
	ov := &overrides.Row{
		Method: "GET", Path: "/users/{id}", OverrideOn: true,
		Responses: map[string]overrides.Variant{
			"200": {SchemaPatch: json.RawMessage(`[{"op":"add","path":"/properties/nickname","value":{"type":"string"}}]`)},
		},
	}
	doc := composeDoc(t, design.Input{
		Base: normalizedBase(t), Revision: 1,
		Overrides: map[string]*overrides.Row{overrides.OpKey("GET", "/users/{id}"): ov},
	})
	schema := operation(t, doc, "/users/{id}", "get")["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if _, isRef := schema["$ref"]; isRef {
		t.Fatalf("the patched response still carries a $ref: %v", schema)
	}
	if _, ok := schema["properties"].(map[string]any)["nickname"]; !ok {
		t.Errorf("the patch was not applied inline: %v", schema)
	}
	component := doc["components"].(map[string]any)["schemas"].(map[string]any)["User"].(map[string]any)
	if _, leaked := component["properties"].(map[string]any)["nickname"]; leaked {
		t.Errorf("the patch leaked into the shared component: %v", component)
	}
}

// TestCompose_pinnedBodyBecomesAnExample: on a base operation and on a
// custom row alike, and as the `examples` ARRAY the normalizer produces —
// so a re-import does not rewrite it (the idempotence A12 rests on).
func TestCompose_pinnedBodyBecomesAnExample(t *testing.T) {
	ov := &overrides.Row{
		Method: "GET", Path: "/users/{id}", OverrideOn: true,
		Responses: map[string]overrides.Variant{
			"200": {Mode: "pinned", Body: json.RawMessage(`{"id":7,"name":"Ada"}`)},
		},
	}
	doc := composeDoc(t, design.Input{
		Base: normalizedBase(t), Revision: 1,
		Overrides: map[string]*overrides.Row{overrides.OpKey("GET", "/users/{id}"): ov},
	})
	mto := operation(t, doc, "/users/{id}", "get")["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)
	examples, ok := mto["examples"].([]any)
	if !ok || len(examples) != 1 {
		t.Fatalf("examples = %v, want a one-element array", mto["examples"])
	}
	if got := examples[0].(map[string]any)["name"]; got != "Ada" {
		t.Errorf("example = %v, want the pinned body", examples[0])
	}
}

// TestCompose_streamRowsBecomeOperations is the owner's "both as
// operations": sse as a text/event-stream response, ws with a 101 and the
// extension.
func TestCompose_streamRowsBecomeOperations(t *testing.T) {
	sse := customRow(1, "GET", "/events")
	sse.Kind = customep.KindSSE
	ws := customRow(2, "GET", "/socket")
	ws.Kind = customep.KindWS

	doc := composeDoc(t, design.Input{Base: normalizedBase(t), Revision: 1, Endpoints: []*customep.Row{sse, ws}})
	sseOp := operation(t, doc, "/events", "get")
	if _, ok := sseOp["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["text/event-stream"]; !ok {
		t.Errorf("the sse operation does not declare text/event-stream: %v", sseOp)
	}
	wsOp := operation(t, doc, "/socket", "get")
	if wsOp["x-websocket"] != true {
		t.Errorf("the ws operation carries no x-websocket: %v", wsOp)
	}
	if _, ok := wsOp["responses"].(map[string]any)["101"]; !ok {
		t.Errorf("the ws operation declares no 101: %v", wsOp)
	}
}

// TestCompose_withNoSpecIsTheSkeleton is "a design from nothing": the
// export of a workspace bound to no spec is the skeleton plus whatever the
// delta declares, and it loads as an OpenAPI document.
func TestCompose_withNoSpecIsTheSkeleton(t *testing.T) {
	row := customRow(1, "GET", "/things")
	row.Responses["200"] = overrides.Variant{Schema: json.RawMessage(`{"type":"object"}`)}

	out, err := design.Compose(design.Input{WorkspaceName: "fresh", Revision: 2, Endpoints: []*customep.Row{row}})
	if err != nil {
		t.Fatalf("Compose without a base: %v", err)
	}
	doc, _, err := openapi.Load(out)
	if err != nil {
		t.Fatalf("the skeleton-based export does not load: %v", err)
	}
	if doc.Title() != "fresh" || doc.Version() != "0.0.0-draft.2" {
		t.Errorf("info = %q/%q, want fresh/0.0.0-draft.2", doc.Title(), doc.Version())
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	operation(t, decoded, "/things", "get")
}

// TestCompose_isAFixedPointOfLoad is the property the round trip rests on
// (D8/A12): re-importing an export stores exactly the bytes it was given,
// so a second export of the accepted design differs only in the version.
// Fails an implementation that writes a raw document the normalizer then
// rewrites — a singular `example`, a boolean `exclusiveMinimum`, a
// `nullable: true` inside a pinned body or an inline schema.
func TestCompose_isAFixedPointOfLoad(t *testing.T) {
	row := customRow(1, "POST", "/widgets")
	row.Responses["200"] = overrides.Variant{
		Mode: "pinned",
		Body: json.RawMessage(`{"nullable":true,"example":"trap","exclusiveMinimum":true,"nested":{"nullable":false}}`),
	}
	row.Responses["201"] = overrides.Variant{
		Schema: json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer","minimum":1,"exclusiveMinimum":true},"s":{"type":"string","nullable":true,"example":"x"}}}`),
	}
	out, err := design.Compose(design.Input{Base: normalizedBase(t), Revision: 4, Endpoints: []*customep.Row{row}})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	reloaded, _, err := openapi.Load(out)
	if err != nil {
		t.Fatalf("the export does not load: %v", err)
	}
	if string(reloaded.Normalized()) != string(out) {
		t.Errorf("Load rewrote the export — it is not a fixed point:\nexport:   %s\nreloaded: %s", out, reloaded.Normalized())
	}
}

// TestCompose_twoExportsOfTheSameStateAreEqual: composition is
// deterministic over map-ordered input (Go randomises map iteration), so
// a diff of two exports of an unchanged workspace is empty.
func TestCompose_twoExportsOfTheSameStateAreEqual(t *testing.T) {
	rows := []*customep.Row{customRow(1, "GET", "/a"), customRow(2, "GET", "/b"), customRow(3, "GET", "/c")}
	for _, row := range rows {
		row.Responses["200"] = overrides.Variant{Schema: json.RawMessage(`{"type":"object"}`)}
	}
	ovs := map[string]*overrides.Row{}
	for _, path := range []string{"/users", "/users/{id}"} {
		ovs[overrides.OpKey("GET", path)] = &overrides.Row{
			Method: "GET", Path: path, OverrideOn: true,
			Responses: map[string]overrides.Variant{"200": {Mode: "pinned", Body: json.RawMessage(`{"x":1}`)}},
		}
	}
	in := design.Input{Base: normalizedBase(t), Revision: 5, Overrides: ovs, Endpoints: rows}
	first, err := design.Compose(in)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	for i := range 5 {
		again, err := design.Compose(in)
		if err != nil {
			t.Fatalf("Compose %d: %v", i, err)
		}
		if string(again) != string(first) {
			t.Fatalf("export %d differs from the first:\n%s\n%s", i, first, again)
		}
	}
}

// TestSkeleton_loadsAndCarriesTheName pins what the runtime and the export
// share: the skeleton is a document openapi.Load accepts, with the
// workspace's own name on it.
func TestSkeleton_loadsAndCarriesTheName(t *testing.T) {
	doc, _, err := openapi.Load(design.Skeleton("my workspace"))
	if err != nil {
		t.Fatalf("Load the skeleton: %v", err)
	}
	if doc.Title() != "my workspace" {
		t.Errorf("title = %q, want the workspace name", doc.Title())
	}
	if !strings.Contains(string(design.Skeleton("")), `"openapi":"3.1.0"`) {
		t.Errorf("the skeleton is not an OpenAPI 3.1 document: %s", design.Skeleton(""))
	}
}
