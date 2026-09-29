package admin

import (
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
)

func TestSavedStepOperationResolvesConcreteAlias(t *testing.T) {
	t.Parallel()
	raw := `{"paths":{"/one":{"$ref":"#/components/pathItems/Common","x-mocker-canvas-operation-ids":{"get":"first"}},"/two":{"$ref":"#/components/pathItems/Common","x-mocker-canvas-operation-ids":{"get":"second"}}},"components":{"pathItems":{"Common":{"get":{"x-mocker-canvas-operation-id":"shared"}}}}}`
	document := designscenario.Document{Messages: []designscenario.Message{{ID: "call", Operation: &designscenario.OperationBinding{OperationKey: "second"}}}}
	method, path, err := savedStepOperation(document, "call", raw)
	if err != nil || method != "GET" || path != "/two" {
		t.Fatalf("got %s %s, %v", method, path, err)
	}
}
