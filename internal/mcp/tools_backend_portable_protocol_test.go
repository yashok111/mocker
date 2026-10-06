package mcp

import (
	"fmt"
	"strings"
	"testing"
)

func TestPortableMCPRoutesAndClosedWire(t *testing.T) {
	id := backendTestID
	hash := strings.Repeat("a", 64)
	for _, c := range []struct{ name, args, path string }{
		{"resolve_backend_portable_selection", fmt.Sprintf(`{"projectId":%q,"target":{"revisionId":%q},"diagramViews":[]}`, id, id), "/api/backend-projects/" + id + "/portable/selection"},
		{"export_backend_project", fmt.Sprintf(`{"projectId":%q,"selection":{"projectId":%q,"target":{"revisionId":%q},"targetHash":%q,"diagramViews":[]},"idempotencyKey":"export"}`, id, id, id, hash), "/api/backend-projects/" + id + "/portable/export"},
		{"put_backend_portable_import_chunk", fmt.Sprintf(`{"importId":%q,"expectedVersion":1,"index":0,"body":"[]","idempotencyKey":"put"}`, id), "/api/backend-projects/portable/imports/" + id + "/chunks"},
		{"preview_backend_portable_import", fmt.Sprintf(`{"importId":%q,"expectedVersion":2,"name":"Copy","artifactMappings":[],"idempotencyKey":"preview"}`, id), "/api/backend-projects/portable/imports/" + id + "/preview"},
		{"commit_backend_portable_import", fmt.Sprintf(`{"importId":%q,"expectedVersion":3,"candidateHash":%q,"idempotencyKey":"commit"}`, id, hash), "/api/backend-projects/portable/imports/" + id + "/commit"},
		{"abort_backend_portable_import", fmt.Sprintf(`{"importId":%q,"expectedVersion":3,"idempotencyKey":"abort"}`, id), "/api/backend-projects/portable/imports/" + id + "/abort"},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := &recordingCaller{status: 200, body: []byte(`{}`)}
			_, message := callTool(t, calls, c.name, c.args)
			if message != "" || calls.method != "POST" || calls.path != c.path {
				t.Fatal(message, calls.method, calls.path)
			}
		})
	}
	calls := &recordingCaller{status: 200, body: []byte(`{"index":0,"manifestHash":"` + hash + `","body":"[]"}`)}
	_, message := callTool(t, calls, "get_backend_export_chunk", fmt.Sprintf(`{"exportId":%q,"index":0,"manifestHash":%q}`, id, hash))
	if message != "" || calls.method != "GET" || calls.path != "/api/backend-projects/portable/exports/"+id+"/chunks/0?manifestHash="+hash {
		t.Fatal(message, calls.path)
	}
	_, message = callTool(t, calls, "get_backend_export_chunk", fmt.Sprintf(`{"exportId":%q,"manifestHash":%q}`, id, hash))
	if message == "" {
		t.Fatal("missing zero-valued index accepted")
	}
}
