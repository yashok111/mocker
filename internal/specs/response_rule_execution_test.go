package specs_test

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/specs"
)

func TestResponseRuleExecutionImportAdmission(t *testing.T) {
	t.Parallel()
	r, _ := newRepo(t)
	base := `{"openapi":"3.1.0","info":{"title":"Execution","version":"1"},"paths":{"/orders":{"get":{"responses":{"200":{"description":"OK"}}}}}`
	incomplete := `{"formatVersion":1,"rules":[{"id":"r","name":"Draft","nodes":[],"edges":[]}]}`
	for _, extension := range []string{`null`, `{"formatVersion":9,"rules":[]}`, incomplete} {
		if _, err := r.PrepareImport(specs.ImportInput{Document: []byte(base + `,"x-mocker-response-rules-execution":` + extension + `}`)}); err == nil {
			t.Fatalf("invalid executable extension admitted: %s", extension)
		}
	}
	if _, err := r.PrepareImport(specs.ImportInput{Document: []byte(base + `,"x-mocker-response-rules":` + incomplete + `}`)}); err != nil {
		t.Fatalf("passive incomplete authoring rejected: %v", err)
	}
	response := `{"formatVersion":1,"rules":[{"id":"r","name":"Rule","binding":{"method":"GET","path":"/orders"},"nodes":[{"id":"s","type":"start","name":"Start","x":0,"y":0},{"id":"r","type":"response","name":"Response","x":200,"y":0,"response":{"status":200,"mediaType":"application/json","headers":[],"bodyJSON":"{\"example\":1.0,\"nullable\":true,\"type\":\"string\",\"large\":9007199254740993,\"exp\":1e0}"}}],"edges":[{"id":"e","from":"s","port":"next","to":"r"}]}]}`
	imported, err := r.Import(t.Context(), specs.ImportInput{Document: []byte(base + `,"x-mocker-response-rules-execution":` + response + `}`), Source: "upload"})
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := r.Normalized(t.Context(), imported.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{`\"example\":1.0`, `\"nullable\":true`, `9007199254740993`, `\"exp\":1e0`} {
		if !strings.Contains(string(normalized), literal) {
			t.Fatalf("normalization changed response %s: %s", literal, normalized)
		}
	}
}
