package designscenario

import (
	"maps"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestBindingTargetTypesResolvePinnedAliasesReferencesAndBodyPointers(t *testing.T) {
	rev := runRevision()
	rev.Document.Contracts[0].Document = jsonx.RawMessage(`{"components":{"schemas":{"ID":{"type":"integer"},"Body":{"type":"object","properties":{"a/b":{"type":"array","items":{"$ref":"#/components/schemas/ID"}}}}},"parameters":{"Token":{"in":"header","name":"X-Token","schema":{"type":"string"}}},"pathItems":{"Profile":{"parameters":[{"in":"query","name":"q","schema":{"type":"boolean"}},{"$ref":"#/components/parameters/Token"}],"get":{"x-mocker-canvas-operation-id":"profile","parameters":[{"in":"query","name":"q","schema":{"type":"number"}}],"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Body"}}}}}}}},"paths":{"/profile":{"$ref":"#/components/pathItems/Profile"}}}`)
	rev.Document.Messages[1].Execution.Bindings = []DataBinding{
		{ID: "query", Target: DataBindingTarget{Kind: "query", Name: "q"}},
		{ID: "header", Target: DataBindingTarget{Kind: "header", Name: "x-TOKEN"}},
		{ID: "body", Target: DataBindingTarget{Kind: "body"}},
		{ID: "nested", Target: DataBindingTarget{Kind: "body", Pointer: "/a~1b/0"}},
		{ID: "noncanonical", Target: DataBindingTarget{Kind: "body", Pointer: "/a~1b/01"}},
		{ID: "missing", Target: DataBindingTarget{Kind: "path", Name: "id"}},
	}
	before, _ := jsonx.Marshal(rev.Document)
	types := BindingTargetTypes(rev.Document)
	want := map[string]string{"query": "number", "header": "string", "body": "object", "nested": "integer", "noncanonical": "unknown", "missing": "unknown"}
	if len(types) != 1 || !maps.Equal(types["profile"], want) {
		t.Fatalf("target types: %+v", types)
	}
	after, _ := jsonx.Marshal(rev.Document)
	if string(before) != string(after) {
		t.Fatal("type resolution changed pinned document")
	}
}

func TestBindingTargetTypesDoNotShareBindingsBetweenMessagesOfSameOperation(t *testing.T) {
	rev := runRevision()
	rev.Document.Contracts[0].Document = jsonx.RawMessage(`{"paths":{"/profile":{"get":{"x-mocker-canvas-operation-id":"profile","parameters":[{"in":"query","name":"flag","schema":{"type":"boolean"}},{"in":"query","name":"name","schema":{"type":"string"}}]}}}}`)
	rev.Document.Messages[0].Operation = rev.Document.Messages[1].Operation
	rev.Document.Messages[0].Execution.Bindings = []DataBinding{{ID: "value", Target: DataBindingTarget{Kind: "query", Name: "flag"}}}
	rev.Document.Messages[1].Execution.Bindings = []DataBinding{{ID: "value", Target: DataBindingTarget{Kind: "query", Name: "name"}}}
	types := BindingTargetTypes(rev.Document)
	if types["login"]["value"] != "boolean" || types["profile"]["value"] != "string" {
		t.Fatalf("binding types mixed between repeated operation: %+v", types)
	}
}
