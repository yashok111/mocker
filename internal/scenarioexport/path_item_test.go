package scenarioexport

import "testing"

func TestSavedOperationAndDiagnosticsResolveAliases(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"paths":{"/one":{"$ref":"#/components/pathItems/Common","x-mocker-canvas-operation-ids":{"get":"first"}},"/two":{"$ref":"#/components/pathItems/Common","x-mocker-canvas-operation-ids":{"get":"second"}}},"components":{"pathItems":{"Common":{"parameters":[{"in":"query","name":"limit","schema":{"type":"integer"}}],"get":{"x-mocker-canvas-operation-id":"shared"}}}}}`)
	op, ok := findSavedOperation(raw, "second")
	if !ok || op.Method != "GET" || op.Path != "/two" {
		t.Fatalf("operation %+v, found %v", op, ok)
	}
	parameters, _ := op.Item["parameters"].([]any)
	if len(parameters) != 1 {
		t.Fatalf("inherited parameters %#v", parameters)
	}
	keys := operationKeys(raw)
	if len(keys) != 2 || !keys["first"] || !keys["second"] {
		t.Fatalf("keys %#v", keys)
	}
}
