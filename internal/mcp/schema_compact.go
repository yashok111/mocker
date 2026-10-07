package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

// compactMinBytes keeps small subtrees inline: a $ref to a one-line schema
// would cost the reader more than it saves.
const compactMinBytes = 64

// schemaMapKeywords hold a map of name -> schema; schemaKeywords hold one
// schema; schemaListKeywords hold a list of schemas. Only these positions are
// rewritten: enum, const, default, examples and required are data, and a
// subtree there is never a schema.
var (
	schemaMapKeywords  = []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"}
	schemaKeywords     = []string{"items", "additionalProperties", "unevaluatedProperties", "unevaluatedItems", "contains", "not", "if", "then", "else", "propertyNames", "contentSchema"}
	schemaListKeywords = []string{"allOf", "anyOf", "oneOf", "prefixItems"}
)

// compactSchema shares repeated schema subtrees through $defs in the copy
// describe_tool returns (tools_describe.go); tools/list itself publishes
// only a one-level summary since the catalog split (tool_catalog.go).
//
// Review 2026-10-06, F23: api.BackendSchema inlines every $ref of the
// OpenAPI contract, and the backend tools published those expanded schemas
// (start_backend_analysis alone about 905 KB), so one tools/list was about
// 4.7 MB — more than a client that puts tool schemas in the model context
// can hold. The expanded schema stays what each handler COMPILES and
// validates against (one validation path, unchanged); only the published copy
// is rewritten, once per registered tool, and the rewrite is lossless: a
// subtree in a schema position that occurs twice or more is moved to
// $defs/<digest> and replaced by a bare $ref, which JSON Schema 2020-12 (the
// MCP default dialect) reads as the same constraint.
func compactSchema(schema any) any {
	root, ok := schema.(map[string]any)
	if !ok || root["$defs"] != nil {
		return schema
	}
	c := &schemaCompactor{count: map[string]int{}, size: map[string]int{}, ids: map[uintptr]string{}, defs: map[string]any{}}
	c.measure(root)
	out := c.rebuild(root, true)
	if len(c.defs) == 0 {
		return schema
	}
	out["$defs"] = c.defs
	return out
}

type schemaCompactor struct {
	count map[string]int
	size  map[string]int
	// ids holds each schema node's digest by map identity, so rebuild finds
	// it without encoding the subtree again.
	ids  map[uintptr]string
	defs map[string]any
}

// measure digests a schema node Merkle-style: its own members in canonical
// form with every child schema replaced by the child's digest, so the whole
// pass is linear in the schema's size. It returns the digest and the node's
// approximate canonical size, which the threshold reads.
func (c *schemaCompactor) measure(node map[string]any) (string, int) {
	var b strings.Builder
	size := 2
	b.WriteByte('{')
	for _, key := range sortedKeys(node) {
		encoded, n := c.encode(key, node[key])
		b.WriteString(strconv.Quote(key) + ":" + encoded + ",")
		size += len(key) + n + 4
	}
	b.WriteByte('}')
	sum := sha256.Sum256([]byte(b.String()))
	id := hex.EncodeToString(sum[:8])
	c.count[id]++
	c.size[id] = size
	c.ids[reflect.ValueOf(node).Pointer()] = id
	return id, size
}

func (c *schemaCompactor) child(value any) (string, int) {
	if m, ok := value.(map[string]any); ok {
		id, size := c.measure(m)
		return "#" + id, size
	}
	encoded := canonicalData(value)
	return encoded, len(encoded)
}

// encode returns a member's canonical form and its approximate size.
func (c *schemaCompactor) encode(key string, value any) (string, int) {
	switch {
	case slices.Contains(schemaMapKeywords, key):
		if m, ok := value.(map[string]any); ok {
			var b strings.Builder
			size := 2
			b.WriteByte('{')
			for _, name := range sortedKeys(m) {
				encoded, n := c.child(m[name])
				b.WriteString(strconv.Quote(name) + ":" + encoded + ",")
				size += len(name) + n + 4
			}
			b.WriteByte('}')
			return b.String(), size
		}
	case slices.Contains(schemaKeywords, key):
		if _, ok := value.(map[string]any); ok {
			return c.child(value)
		}
	case slices.Contains(schemaListKeywords, key):
		if list, ok := value.([]any); ok {
			var b strings.Builder
			size := 2
			b.WriteByte('[')
			for _, item := range list {
				encoded, n := c.child(item)
				b.WriteString(encoded + ",")
				size += n + 1
			}
			b.WriteByte(']')
			return b.String(), size
		}
	}
	encoded := canonicalData(value)
	return encoded, len(encoded)
}

// canonicalData is a type-tagged canonical form of a data value: a number
// token and the string of the same digits must never collide, or two
// different schemas would share one $defs entry.
func canonicalData(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return "s" + strconv.Quote(v)
	case jsonx.Number:
		return "n" + string(jsonx.CanonicalNumber(v))
	case bool:
		return strconv.FormatBool(v)
	case map[string]any:
		var b strings.Builder
		b.WriteByte('{')
		for _, key := range sortedKeys(v) {
			b.WriteString(strconv.Quote(key) + ":" + canonicalData(v[key]) + ",")
		}
		b.WriteByte('}')
		return b.String()
	case []any:
		var b strings.Builder
		b.WriteByte('[')
		for _, item := range v {
			b.WriteString(canonicalData(item) + ",")
		}
		b.WriteByte(']')
		return b.String()
	default:
		// Hand-built schemas carry Go ints (minLength: 1) and []string
		// (required); the type tag keeps them apart from look-alike strings.
		return fmt.Sprintf("%T:%#v", v, v)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// rebuild copies node top-down; a repeated, large enough schema node below
// the root becomes a $ref, its copy (rebuilt the same way) a $defs entry.
func (c *schemaCompactor) rebuild(node map[string]any, root bool) map[string]any {
	if !root {
		if id, ok := c.ids[reflect.ValueOf(node).Pointer()]; ok && c.count[id] > 1 && c.size[id] >= compactMinBytes {
			if _, done := c.defs[id]; !done {
				c.defs[id] = nil // reserved before the copy: a subtree never contains itself
				c.defs[id] = c.rebuild(node, true)
			}
			return map[string]any{"$ref": "#/$defs/" + id}
		}
	}
	out := make(map[string]any, len(node))
	for key, value := range node {
		out[key] = c.rebuildMember(key, value)
	}
	return out
}

func (c *schemaCompactor) rebuildMember(key string, value any) any {
	switch {
	case slices.Contains(schemaMapKeywords, key):
		if m, ok := value.(map[string]any); ok {
			copied := make(map[string]any, len(m))
			for name, child := range m {
				copied[name] = c.rebuildChild(child)
			}
			return copied
		}
	case slices.Contains(schemaKeywords, key):
		return c.rebuildChild(value)
	case slices.Contains(schemaListKeywords, key):
		if list, ok := value.([]any); ok {
			copied := make([]any, len(list))
			for i, item := range list {
				copied[i] = c.rebuildChild(item)
			}
			return copied
		}
	}
	return value
}

func (c *schemaCompactor) rebuildChild(value any) any {
	if schema, ok := value.(map[string]any); ok {
		return c.rebuild(schema, false)
	}
	return value
}
