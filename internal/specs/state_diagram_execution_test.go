package specs_test

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/specs"
)

func TestStateDiagramExecutionImportAdmission(t *testing.T) {
	t.Parallel()
	r, _ := newRepo(t)
	base := `{"openapi":"3.1.0","info":{"title":"Execution","version":"1"},"paths":{}`
	incomplete := `{"formatVersion":1,"diagrams":[{"id":"draft","name":"Unfinished","initialStateId":"","states":[],"transitions":[]}]}`
	for _, extension := range []string{`null`, `{"formatVersion":9,"diagrams":[]}`, incomplete} {
		if _, err := r.PrepareImport(specs.ImportInput{Document: []byte(base + `,"x-mocker-state-diagrams-execution":` + extension + `}`)}); err == nil {
			t.Fatalf("invalid applied diagram admitted: %s", extension)
		}
	}
	if _, err := r.PrepareImport(specs.ImportInput{Document: []byte(base + `,"x-mocker-state-diagrams":` + incomplete + `}`)}); err != nil {
		t.Fatalf("incomplete authoring refused: %v", err)
	}
	diagram := `{"formatVersion":1,"diagrams":[{"id":"lifecycle","name":"Lifecycle","initialStateId":"created","entity":{"family":"/orders","keyParam":"id","stateField":"status"},"states":[{"id":"created","name":"Created","x":0,"y":0,"terminal":false},{"id":"paid","name":"Paid","x":1,"y":1,"terminal":true}],"transitions":[{"id":"pay","name":"Pay","from":"created","to":"paid","binding":{"method":"post","path":"/orders/{id}/pay"},"patchJSON":"{\"amount\":9007199254740993}","responseStatus":200}]}]}`
	document := `{"openapi":"3.1.0","info":{"title":"Execution","version":"1"},"paths":{"/orders/{id}/pay":{"post":{"responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"type":"object"}}}}}}}},"x-mocker-state-diagrams-execution":` + diagram + `}`
	imported, err := r.Import(t.Context(), specs.ImportInput{Document: []byte(document), Source: "upload"})
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := r.Normalized(t.Context(), imported.Spec.ID)
	if err != nil || !strings.Contains(string(normalized), `9007199254740993`) {
		t.Fatalf("normalized %s %v", normalized, err)
	}
}
