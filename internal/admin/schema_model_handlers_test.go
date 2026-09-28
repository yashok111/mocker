package admin

import (
	"fmt"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestSchemaModelHTTPPreviewAndAtomicCommands(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	ctx := t.Context()
	src := loopbackTestSrc()
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		status, out, err := s.CallAsMCP(ctx, src, method, path, []byte(body))
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v", method, path, status, out, err)
		}
		return out
	}
	raw := call("POST", "/api/designs", `{"name":"Schemas"}`, 201)
	var detail apidesign.Detail
	if err := jsonx.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/designs/%d/schema-model", detail.Design.ID)
	call("POST", base+"/preview", `{"commands":[{"kind":"create_schema","schemaName":"User","schemaJSON":"{\"type\":\"object\",\"example\":9007199254740993}"}]}`, 200)
	var current apidesign.SchemaModelDetail
	if err := jsonx.Unmarshal(call("GET", base, "", 200), &current); err != nil {
		t.Fatal(err)
	}
	if current.Version != 1 || len(current.Model.Schemas) != 0 {
		t.Fatalf("preview persisted %+v", current)
	}
	call("POST", base+"/commands", `{"expectedVersion":1,"commands":[{"kind":"create_schema","schemaName":"User","schemaJSON":"{\"type\":\"object\"}"}]}`, 200)
	call("POST", base+"/commands", `{"expectedVersion":1,"commands":[{"kind":"delete_schema","schemaName":"User"}]}`, 409)
	call("POST", base+"/commands", `{"expectedVersion":2,"commands":[{"kind":"rename_schema","schemaName":"User","newName":"Person"},{"kind":"unknown","schemaName":"Person"}]}`, 400)
	call("POST", base+"/preview", `{"commands":[{"kind":"create_schema","schemaName":"MissingJSON"}]}`, 400)
	call("POST", base+"/commands", `{"expectedVersion":2}`, 400)
	if err := jsonx.Unmarshal(call("GET", base, "", 200), &current); err != nil {
		t.Fatal(err)
	}
	if current.Version != 2 || current.Model.Schemas[0].Name != "User" {
		t.Fatalf("batch partially applied %+v", current)
	}
}
