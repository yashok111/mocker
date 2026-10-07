package schemamodel

import (
	"context"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

const maxDepth = 128
const maxNodes = 100000

type referenceSite struct {
	pointer, ref, schema, property string
	resource                       string
	owner                          map[string]any
	key                            string
	bare                           bool
}
type operation struct {
	OperationUsage
	common []string
}
type traversal struct {
	ctx         context.Context
	schemaEntry func(string, any)

	sites    []referenceSite
	nodes    int
	resource string
}

func localPointer(ref string) string {
	if ref == "#" {
		return "/"
	}
	if !strings.HasPrefix(ref, "#/") {
		return ""
	}
	p, e := url.PathUnescape(ref[1:])
	if e != nil {
		return ""
	}
	return p
}
func schemaTarget(ref string) (string, string) {
	p := localPointer(ref)
	suffix, ok := strings.CutPrefix(p, "/components/schemas/")
	if !ok {
		return "", p
	}
	token, _, _ := strings.Cut(suffix, "/")
	return unescape(token), p
}
func unescape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
}
func references(root map[string]any) ([]referenceSite, []operation, error) {
	w := traversal{}
	if err := w.walk(root, "", "root", "", "", 0); err != nil {
		return nil, nil, err
	}
	slices.SortFunc(w.sites, func(a, b referenceSite) int { return strings.Compare(a.pointer, b.pointer) })
	ops, err := collectOperations(root)
	return w.sites, ops, err
}
func (w *traversal) add(m map[string]any, key, p, schema, property string, bare bool) error {
	v, ok := m[key]
	if !ok {
		return nil
	}
	ref, ok := v.(string)
	if !ok {
		return fail(p, "Ссылка должна быть строкой")
	}
	w.sites = append(w.sites, referenceSite{pointer: p, ref: ref, schema: schema, property: property, owner: m, key: key, bare: bare, resource: w.resource})
	return nil
}

// Only vocabulary-defined children are structural. Unknown keywords and extension
// values remain opaque even when their data happens to contain a "$ref" key.
type childRule struct {
	kind string
	mode string
}

var childRules = map[string]map[string]childRule{
	"root":       {"components": {"components", ""}, "paths": {"path", "map"}, "webhooks": {"path", "map"}},
	"components": {"schemas": {"schema", "map"}, "responses": {"response", "map"}, "parameters": {"parameter", "map"}, "headers": {"parameter", "map"}, "requestBodies": {"request", "map"}, "callbacks": {"callback", "map"}, "pathItems": {"path", "map"}, "examples": {"example", "map"}, "links": {"link", "map"}, "securitySchemes": {"security", "map"}},
	"path":       {"parameters": {"parameter", "array"}},
	"operation":  {"parameters": {"parameter", "array"}, "requestBody": {"request", ""}, "responses": {"response", "map"}, "callbacks": {"callback", "map"}},
	"response":   {"content": {"media", "map"}, "headers": {"parameter", "map"}, "links": {"link", "map"}},
	"request":    {"content": {"media", "map"}},
	"parameter":  {"schema": {"schema", ""}, "content": {"media", "map"}, "examples": {"example", "map"}},
	"media":      {"schema": {"schema", ""}, "examples": {"example", "map"}, "encoding": {"encoding", "map"}},
	"encoding":   {"headers": {"parameter", "map"}},
	"schema": {
		"properties": {"schema", "map"}, "patternProperties": {"schema", "map"}, "$defs": {"schema", "map"}, "definitions": {"schema", "map"}, "dependentSchemas": {"schema", "map"}, "dependencies": {"schema", "map"},
		"items": {"schema", "items"}, "additionalItems": {"schema", ""}, "additionalProperties": {"schema", ""}, "unevaluatedProperties": {"schema", ""}, "unevaluatedItems": {"schema", ""}, "contains": {"schema", ""}, "propertyNames": {"schema", ""}, "not": {"schema", ""}, "if": {"schema", ""}, "then": {"schema", ""}, "else": {"schema", ""}, "contentSchema": {"schema", ""},
		"allOf": {"schema", "array"}, "anyOf": {"schema", "array"}, "oneOf": {"schema", "array"}, "prefixItems": {"schema", "array"}, "discriminator": {"discriminator", ""},
	},
}

func (w *traversal) walk(v any, p, kind, schema, property string, depth int) error {
	if w.ctx != nil {
		if err := w.ctx.Err(); err != nil {
			return err
		}
	}
	w.nodes++
	if depth > maxDepth || w.nodes > maxNodes {
		return fail(p, "Превышен предел обхода документа (128 уровней, 100000 узлов)")
	}
	if kind == "schema" && w.schemaEntry != nil {
		w.schemaEntry(p, v)
	}
	m := object(v)
	if m == nil {
		return nil
	}
	if kind == "schema" {
		if id, ok := m["$id"].(string); ok {
			previous := w.resource
			w.resource = resolveURI(previous, id)
			defer func() { w.resource = previous }()
		}
	}

	if err := w.ownReferences(m, p, kind, schema, property); err != nil {
		return err
	}
	if kind == "discriminator" {
		return w.discriminator(m, p, schema, property)
	}
	for _, key := range slices.Sorted(maps.Keys(m)) {
		rule := effectiveChildRule(kind, key)
		if rule.kind == "" {
			continue
		}
		if err := w.children(m[key], p, key, kind, schema, property, rule, depth); err != nil {
			return err
		}
	}
	return nil
}

// ownReferences records the node's own $ref and, for a schema, $dynamicRef;
// root, components and discriminator objects carry no $ref of their own.
func (w *traversal) ownReferences(m map[string]any, p, kind, schema, property string) error {
	if kind != "root" && kind != "components" && kind != "discriminator" {
		if err := w.add(m, "$ref", p+"/$ref", schema, property, false); err != nil {
			return err
		}
	}
	if kind == "schema" {
		if err := w.add(m, "$dynamicRef", p+"/$dynamicRef", schema, property, false); err != nil {
			return err
		}
	}
	return nil
}

func (w *traversal) children(child any, p, key, kind, schema, property string, rule childRule, depth int) error {
	at := p + "/" + escape(key)
	if rule.mode == "map" {
		for _, name := range slices.Sorted(maps.Keys(object(child))) {
			if strings.HasPrefix(name, "x-") && ((kind == "root" && key == "paths") || (kind == "operation" && key == "responses")) {
				continue
			}
			s, prop := schema, property
			if kind == "components" && key == "schemas" {
				s = name
			}
			if kind == "schema" && key == "properties" && p == schemaPointer(schema) {
				prop = name
			}
			if err := w.walk(object(child)[name], at+"/"+escape(name), rule.kind, s, prop, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	values, isArray := child.([]any)
	if rule.mode == "array" || (rule.mode == "items" && isArray) {
		for i, value := range values {
			if err := w.walk(value, at+"/"+strconv.Itoa(i), rule.kind, schema, property, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return w.walk(child, at, rule.kind, schema, property, depth+1)
}
func (w *traversal) discriminator(m map[string]any, p, schema, property string) error {
	mapping := object(m["mapping"])
	for _, name := range slices.Sorted(maps.Keys(mapping)) {
		ref := text(mapping[name])
		bare := !strings.ContainsAny(ref, "/#:")
		if err := w.add(mapping, name, p+"/mapping/"+escape(name), schema, property, bare); err != nil {
			return err
		}
	}
	return nil
}
func isMethod(s string) bool {
	switch s {
	case "get", "post", "put", "patch", "delete", "head", "options", "trace":
		return true
	}
	return false
}
func pointerRef(pointer string) string {
	u := url.URL{Fragment: pointer}
	return "#" + u.EscapedFragment()
}

// Resolve Path Item references when enumerating operations. Per-path visited sets
// allow recursive component references without looping or inventing operations.
func collectOperations(root map[string]any) ([]operation, error) {
	ops := []operation{}
	for _, section := range []string{"paths", "webhooks"} {
		for _, path := range slices.Sorted(maps.Keys(object(root[section]))) {
			if section == "paths" && strings.HasPrefix(path, "x-") {
				continue
			}
			p := "/" + section + "/" + escape(path)
			nodes, diagnostics := PathItems(root, object(root[section])[path], p)
			for _, diagnostic := range diagnostics {
				if diagnostic.Code == "path_item_ref_depth" {
					return nil, fail(diagnostic.Pointer, diagnostic.Message)
				}
			}
			common := []string{}
			for _, node := range nodes {
				common = append(common, node.Pointer+"/parameters")
			}
			for _, method := range PathItemOperations(nodes) {
				sources := append(slices.Clone(common), method.Pointer)
				ops = append(ops, operation{OperationUsage: OperationUsage{Method: strings.ToUpper(method.Method), Path: path, Pointer: p + "/" + method.Method}, common: sources})
			}
		}
	}
	return ops, nil
}

func resolveFound(root map[string]any, pointer string) (any, bool) {
	var value any = root
	for token := range strings.SplitSeq(strings.TrimPrefix(pointer, "/"), "/") {
		key := unescape(token)
		switch node := value.(type) {
		case map[string]any:
			var exists bool
			value, exists = node[key]
			if !exists {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(node) {
				return nil, false
			}
			value = node[index]
		default:
			return nil, false
		}
	}
	return value, true
}

func resolveURI(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return base
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}
func (site referenceSite) effectiveRef() string {
	if site.bare {
		return pointerRef(schemaPointer(site.ref))
	}
	if site.resource == "" {
		return site.ref
	}
	return resolveURI(site.resource, site.ref)
}

// VisitReferences visits real OpenAPI and JSON Schema reference sites, excluding
// example data, extensions and unknown schema keywords. It does not impose model
// editing limits, so ordinary draft validation supports larger component maps.
// Bare discriminator mappings are resolved to their component-schema pointer.
func VisitReferences(root map[string]any, visit func(pointer, ref string)) error {
	w := traversal{}
	if err := w.walk(root, "", "root", "", "", 0); err != nil {
		return err
	}
	slices.SortFunc(w.sites, func(a, b referenceSite) int { return strings.Compare(a.pointer, b.pointer) })
	for _, site := range w.sites {
		// Anchor and URI dynamic references were preserved by ordinary saves
		// before the model editor; the document validator only resolves pointers.
		if site.key == "$dynamicRef" && !strings.HasPrefix(site.ref, "#/") {
			continue
		}
		ref := site.ref
		if site.bare {
			ref = pointerRef(schemaPointer(ref))
		}
		visit(site.pointer, ref)
	}
	return nil
}
