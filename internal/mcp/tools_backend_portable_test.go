package mcp

import "testing"

func TestBackendPortableSVGExactToolPath(t *testing.T) {
	const id = "10000000-0000-4000-8000-000000000001"
	path, err := backendSVGPath([]byte(`{"projectId":"` + id + `","viewId":"` + id + `","viewVersion":9007199254740993}`))
	if err != nil || path != "/api/backend-projects/"+id+"/diagram-views/"+id+"/versions/9007199254740993/svg" {
		t.Fatal(path, err)
	}
	for _, raw := range []string{`{"projectId":"` + id + `","viewId":"../x","viewVersion":1}`, `{"projectId":"` + id + `","viewId":"` + id + `","viewVersion":1,"head":true}`, `{"projectId":"` + id + `","viewId":"` + id + `","viewVersion":1,"viewVersion":2}`} {
		if _, err = backendSVGPath([]byte(raw)); err == nil {
			t.Fatal("invalid tool args accepted", raw)
		}
	}
}
