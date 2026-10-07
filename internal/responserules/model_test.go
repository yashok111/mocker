package responserules

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

func baseRule() Rule {
	return Rule{ID: "r", Name: "Rule", Binding: &Binding{Method: "GET", Path: "/orders"}, Nodes: []Node{{ID: "s", Type: "start", Name: "Start"}, {ID: "f", Type: "fallback", Name: "Fallback"}}, Edges: []Edge{{ID: "e", From: "s", Port: "next", To: "f"}}}
}

func TestDecodeStrict(t *testing.T) {
	env, err := Decode(map[string]any{})
	if err != nil || env.FormatVersion != 1 || env.Rules == nil {
		t.Fatalf("absent: %+v %v", env, err)
	}
	good := `{"formatVersion":1,"rules":[{"id":"r","name":"","nodes":[{"id":"c","type":"condition","name":"","x":0,"y":0,"condition":{"in":"query","name":"","op":"equals","value":""}}],"edges":[]}]}`
	cases := []struct {
		name, raw string
		bad       bool
	}{
		{"incomplete", good, false}, {"version", strings.Replace(good, `"formatVersion":1`, `"formatVersion":2`, 1), true},
		{"null rules", `{"formatVersion":1,"rules":null}`, true}, {"missing rules", `{"formatVersion":1}`, true},
		{"extra", strings.Replace(good, `"formatVersion":1`, `"extra":true,"formatVersion":1`, 1), true},
		{"missing x", strings.Replace(good, `"x":0,`, "", 1), true}, {"null x", strings.Replace(good, `"x":0`, `"x":null`, 1), true},
		{"wrong payload", strings.Replace(good, `"type":"condition"`, `"type":"start"`, 1), true},
		{"exists value", strings.Replace(good, `"op":"equals"`, `"op":"exists"`, 1), true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var v any
			if err := json.Unmarshal([]byte(tt.raw), &v); err != nil {
				t.Fatal(err)
			}
			_, err := Decode(map[string]any{Extension: v})
			if (err != nil) != tt.bad {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestDedicatedDTOIsStrict(t *testing.T) {
	for _, raw := range []string{
		`{"id":"r","name":"R","nodes":[],"edges":[],"binding":null}`,
		`{"id":"r","name":"R","nodes":[{"id":"n","type":"delay","name":"D","x":0,"y":0}],"edges":[]}`,
		`{"id":"r","name":"R","nodes":[{"id":"n","type":"response","name":"R","x":0,"y":0,"response":{"mediaType":"application/json","headers":[]}}],"edges":[]}`,
	} {
		var r Rule
		if json.Unmarshal([]byte(raw), &r) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestStructureSafety(t *testing.T) {
	for _, name := range []string{"Set-Cookie", "Set-Cookie2", "content-length", "Bad Name"} {
		r := baseRule()
		r.Nodes[1] = Node{ID: "f", Type: "response", Name: "R", Response: &Response{Status: 200, MediaType: "application/json", Headers: []Field{{Name: name, Value: "x"}}}}
		if CheckStructure(r) == nil {
			t.Errorf("accepted %s", name)
		}
	}
	for _, media := range []string{"text/html", "application/json; broken", "image/svg+xml", "application/*+json"} {
		r := baseRule()
		r.Nodes[1] = Node{ID: "f", Type: "response", Name: "R", Response: &Response{Status: 200, MediaType: media, Headers: []Field{}}}
		if CheckStructure(r) == nil {
			t.Errorf("accepted %s", media)
		}
	}
}

func TestCompactLimitsAndBounds(t *testing.T) {
	r := baseRule()
	data, _ := json.Marshal(r)
	padded := append([]byte("{"+strings.Repeat(" ", MaxGraphBytes)), data[1:]...)
	var decoded Rule
	if err := json.Unmarshal(padded, &decoded); err != nil {
		t.Fatalf("compact-sized rule rejected: %v", err)
	}
	for _, edit := range []func(*Rule){
		func(r *Rule) { r.ID = strings.Repeat("x", 81) }, func(r *Rule) { r.Name = strings.Repeat("я", 101) },
		func(r *Rule) { r.Nodes[0].X = 100001 }, func(r *Rule) { r.Nodes[0].Y = math.Inf(1) },
		func(r *Rule) { r.Nodes = append(r.Nodes, r.Nodes[0]) }, func(r *Rule) { r.Edges = append(r.Edges, r.Edges[0]) },
		func(r *Rule) { r.Binding.Path = strings.Repeat("x", 2049) },
	} {
		r := baseRule()
		edit(&r)
		if CheckStructure(r) == nil {
			t.Fatal("accepted invalid bounds")
		}
	}
	env := Envelope{FormatVersion: 1, Rules: []Rule{}}
	for i := 0; i < 21; i++ {
		r := baseRule()
		r.ID = fmt.Sprint("r", i)
		env.Rules = append(env.Rules, r)
	}
	raw, _ := json.Marshal(env)
	if json.Unmarshal(raw, &Envelope{}) == nil {
		t.Fatal("accepted 21 rules")
	}
	commands := make([]Command, 201)
	if _, err := ApplyCommands(t.Context(), baseRule(), commands); err == nil {
		t.Fatal("accepted 201 commands")
	}
}

func TestStructuralCollectionLimits(t *testing.T) {
	r := baseRule()
	r.Nodes = []Node{}
	for i := 0; i < 101; i++ {
		r.Nodes = append(r.Nodes, Node{ID: fmt.Sprint("n", i), Type: "fallback", Name: "N"})
	}
	if CheckStructure(r) == nil {
		t.Fatal("101 nodes accepted")
	}
	r = baseRule()
	r.Edges = []Edge{}
	for i := 0; i < 201; i++ {
		r.Edges = append(r.Edges, Edge{ID: fmt.Sprint("e", i), From: "s", Port: "next", To: "f"})
	}
	if CheckStructure(r) == nil {
		t.Fatal("201 edges accepted")
	}
	r = baseRule()
	r.Nodes[1] = Node{ID: "f", Type: "response", Name: "R", Response: &Response{Status: 200, MediaType: "application/problem+json; charset=utf-8", Headers: []Field{{Name: "X-Test", Value: "one"}, {Name: "x-test", Value: "two"}}}}
	if CheckStructure(r) == nil {
		t.Fatal("duplicate headers accepted")
	}
	r.Nodes[1].Response.Headers = []Field{{Name: "X-Test", Value: "bad\r\nvalue"}}
	if CheckStructure(r) == nil {
		t.Fatal("CRLF accepted")
	}
	r.Nodes[1].Response.Headers = []Field{}
	if err := CheckStructure(r); err != nil {
		t.Fatal(err)
	}
}

func TestResponseMediaTypeRejectsRawControls(t *testing.T) {
	cases := []struct{ name, media string }{
		{"CR", "application/json\r"},
		{"LF", "application/json\n"},
		{"CRLF", "application/json\r\n"},
		{"quoted NUL", "application/json; x=\"a\x00b\""},
		{"quoted DEL", "application/json; x=\"a\x7fb\""},
		{"HTAB", "application/json\t"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := baseRule()
			r.Nodes[1] = Node{ID: "f", Type: "response", Name: "Response", Response: &Response{Status: 200, MediaType: tt.media, Headers: []Field{}}}
			if err := CheckStructure(r); err == nil {
				t.Fatal("structure accepted unsafe media type")
			}
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var decoded Rule
			if err := json.Unmarshal(raw, &decoded); err == nil {
				t.Fatal("dedicated DTO accepted unsafe media type")
			}
			_, err = Decode(map[string]any{Extension: Envelope{FormatVersion: 1, Rules: []Rule{r}}})
			if err == nil {
				t.Fatal("document accepted unsafe media type")
			}
			var field *FieldError
			if !errors.As(err, &field) || field.Pointer != "/"+Extension+"/rules/0/nodes/1/response/mediaType" {
				t.Fatalf("wrong field pointer: %v", err)
			}
		})
	}
	r := baseRule()
	r.Nodes[1] = Node{ID: "f", Type: "response", Name: "Response", Response: &Response{Status: 200, MediaType: `application/problem+json; charset=utf-8; profile="a b"`, Headers: []Field{}}}
	if err := CheckStructure(r); err != nil {
		t.Fatalf("valid parameters rejected: %v", err)
	}
}
