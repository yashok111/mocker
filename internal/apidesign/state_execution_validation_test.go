package apidesign

import (
	"errors"
	"strings"
	"testing"
)

func TestStateExecutionPrepareRejectsAppliedMetadataBeforeWriting(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	for _, extension := range []string{`null`, `{"formatVersion":8,"diagrams":[]}`, `{"formatVersion":1,"diagrams":[{"id":"draft","name":"Draft","initialStateId":"","states":[],"transitions":[]}]}`} {
		document := strings.TrimSuffix(testDocument, "}") + `,"x-mocker-state-diagrams-execution":` + extension + `}`
		_, err := r.prepare(document)
		invalid, ok := errors.AsType[*InvalidError](err)
		if !ok || len(invalid.Diagnostics) != 1 || !strings.HasPrefix(invalid.Diagnostics[0].Pointer, "/x-mocker-state-diagrams-execution") {
			t.Fatalf("invalid execution: %+v %v", invalid, err)
		}
	}
}
