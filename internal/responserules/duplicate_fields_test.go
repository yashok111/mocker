package responserules

import (
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestResponseRuleClosedDTOsRejectDuplicateFields(t *testing.T) {
	t.Parallel()
	const request = `{"query":[],"headers":[]}`
	const example = `{"id":"case","name":"Case","request":` + request + `}`
	const predicate = `{"source":{"source":"result","nodeId":"read","pointer":"/a"},"op":"exists"}`
	for _, tc := range []struct {
		name, raw, pointer string
		target             any
	}{
		{"example id", `{"id":"first","id":"second","name":"Case","request":` + request + `}`, "/id", &Example{}},
		{"escaped example id", `{"id":"first","i\u0064":"second","name":"Case","request":` + request + `}`, "/id", &Example{}},
		{"example name", `{"id":"case","name":"First","name":"Second","request":` + request + `}`, "/name", &Example{}},
		{"example request", `{"id":"case","name":"Case","request":{"query":[],"headers":[],"bodyJSON":"9007199254740993"},"request":{"query":[],"headers":[],"bodyJSON":"0"}}`, "/request", &Example{}},
		{"request exact body", `{"query":[],"headers":[],"bodyJSON":"9007199254740993","bodyJSON":"0"}`, "/bodyJSON", &Request{}},
		{"node predicate", `{"id":"c","type":"condition","name":"Check","x":0,"y":0,"resultCondition":` + predicate + `,"resultCondition":` + strings.Replace(predicate, "/a", "/b", 1) + `}`, "/resultCondition", &Node{}},
		{"command example", `{"type":"add_example","example":` + example + `,"example":` + strings.Replace(example, "case", "other", 1) + `}`, "/example", &Command{}},
		{"command exact body", `{"type":"add_example","example":{"id":"case","name":"Case","request":{"query":[],"headers":[],"bodyJSON":"9007199254740993","bodyJSON":"0"}}}`, "/example/request/bodyJSON", &Command{}},
		{"envelope rules", `{"formatVersion":1,"rules":[],"rules":[]}`, "/rules", &Envelope{}},
		{"rule examples", `{"id":"r","name":"Rule","nodes":[],"edges":[],"examples":[` + example + `],"examples":[]}`, "/examples", &Rule{}},
		{"response body", `{"status":200,"mediaType":"application/json","headers":[],"bodyJSON":"9007199254740993","bodyJSON":"0"}`, "/bodyJSON", &Response{}},
		{"fixture exact row", `{"key":"1","scope":[],"dataJSON":"{\"n\":9007199254740993}","dataJSON":"{\"n\":0}"}`, "/dataJSON", &EntityFixtureRow{}},
		{"legacy condition", `{"in":"query","name":"first","name":"second","op":"exists"}`, "/name", &wireCondition{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := jsonx.Unmarshal([]byte(tc.raw), tc.target)
			field, ok := errors.AsType[*FieldError](err)
			if !ok || field.Pointer != tc.pointer || !strings.Contains(field.Message, "повторяющееся") {
				t.Fatalf("duplicate field was folded: err=%v, target=%+v", err, tc.target)
			}
		})
	}
}
