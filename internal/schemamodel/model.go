// Package schemamodel projects and edits authored OpenAPI component schemas.
package schemamodel

import (
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

const Extension = "x-mocker-schema-layout"
const MaxCommands = 100
const MaxSchemas = 200
const MaxProperties = 200
const MaxSchemaBytes = 64 << 10

type Command struct {
	Kind         string   `json:"kind"`
	SchemaName   string   `json:"schemaName,omitempty"`
	NewName      string   `json:"newName,omitempty"`
	PropertyName string   `json:"propertyName,omitempty"`
	SchemaJSON   string   `json:"schemaJSON,omitempty"`
	Required     *bool    `json:"required,omitempty"`
	TargetSchema string   `json:"targetSchema,omitempty"`
	Array        *bool    `json:"array,omitempty"`
	X            *float64 `json:"x,omitempty"`
	Y            *float64 `json:"y,omitempty"`
}
type Model struct {
	Schemas    []SchemaView     `json:"schemas"`
	References []Reference      `json:"references"`
	Operations []OperationUsage `json:"operations"`
}
type SchemaView struct {
	Name        string         `json:"name"`
	Pointer     string         `json:"pointer"`
	SchemaJSON  string         `json:"schemaJSON"`
	Type        string         `json:"type"`
	Description string         `json:"description"`
	Properties  []PropertyView `json:"properties"`
	X           float64        `json:"x"`
	Y           float64        `json:"y"`
}
type PropertyView struct {
	Name       string `json:"name"`
	Pointer    string `json:"pointer"`
	SchemaJSON string `json:"schemaJSON"`
	Type       string `json:"type"`
	Required   bool   `json:"required"`
}
type Reference struct {
	SourceSchema   string `json:"sourceSchema"`
	SourceProperty string `json:"sourceProperty,omitempty"`
	TargetSchema   string `json:"targetSchema"`
	Pointer        string `json:"pointer"`
	Ref            string `json:"ref"`
}
type OperationUsage struct {
	Method  string   `json:"method"`
	Path    string   `json:"path"`
	Pointer string   `json:"pointer"`
	Schemas []string `json:"schemas"`
}
type Error struct {
	Pointer string
	Message string
}

func (e *Error) Error() string         { return e.Pointer + ": " + e.Message }
func fail(p, m string) error           { return &Error{Pointer: p, Message: m} }
func escape(s string) string           { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func schemaPointer(name string) string { return "/components/schemas/" + escape(name) }
func object(v any) map[string]any      { m, _ := v.(map[string]any); return m }
func text(v any) string                { s, _ := v.(string); return s }
func schemaMap(root map[string]any) map[string]any {
	return object(object(root["components"])["schemas"])
}
func schemaType(v any) string {
	if b, ok := v.(bool); ok {
		if b {
			return "any"
		}
		return "never"
	}
	m := object(v)
	if s := text(m["type"]); s != "" {
		return s
	}
	if s := text(m["$ref"]); s != "" {
		return s
	}
	if _, ok := m["properties"]; ok {
		return "object"
	}
	return "any"
}
func schemaShape(v any) bool {
	switch v.(type) {
	case map[string]any, bool:
		return true
	}
	return false
}
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case jsonx.Number:
		f, e := n.Float64()
		return f, e == nil
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}
func coordinate(v any) (float64, bool) {
	n, ok := number(v)
	return n, ok && !math.IsNaN(n) && !math.IsInf(n, 0) && math.Abs(n) <= 100000
}

// ValidateLayout intentionally applies no editor size limits to ordinary saves.
func ValidateLayout(root map[string]any) error {
	value, exists := root[Extension]
	if !exists {
		return nil
	}
	p := "/" + Extension
	m := object(value)
	if m == nil {
		return fail(p, "Ожидается объект раскладки")
	}
	v, ok := number(m["formatVersion"])
	if !ok || v != 1 {
		return fail(p+"/formatVersion", "Поддерживается formatVersion: 1")
	}
	positions := object(m["positions"])
	if positions == nil {
		return fail(p+"/positions", "Ожидается объект позиций")
	}
	for _, name := range slices.Sorted(maps.Keys(positions)) {
		pos := object(positions[name])
		for _, key := range []string{"x", "y"} {
			if _, ok := coordinate(pos[key]); !ok {
				return fail(p+"/positions/"+escape(name)+"/"+key, "Координата должна быть конечным числом от -100000 до 100000")
			}
		}
	}
	return nil
}
func checkBounds(root map[string]any) error {
	if c, ok := root["components"]; ok && object(c) == nil {
		return fail("/components", "Ожидается объект")
	}
	components := object(root["components"])
	if s, ok := components["schemas"]; ok && object(s) == nil {
		return fail("/components/schemas", "Ожидается объект")
	}
	ss := schemaMap(root)
	if len(ss) > MaxSchemas {
		return fail("/components/schemas", "Редактор поддерживает не более 200 схем")
	}
	for _, name := range slices.Sorted(maps.Keys(ss)) {
		v := ss[name]
		p := schemaPointer(name)
		if !schemaShape(v) {
			return fail(p, "Схема должна быть объектом или boolean")
		}
		if props, ok := object(v)["properties"]; ok {
			if object(props) == nil {
				return fail(p+"/properties", "Ожидается объект")
			}
			if len(object(props)) > MaxProperties {
				return fail(p+"/properties", "Редактор поддерживает не более 200 полей")
			}
		}
	}
	return ValidateLayout(root)
}
func Project(root map[string]any) (Model, error) {
	m := Model{Schemas: []SchemaView{}, References: []Reference{}, Operations: []OperationUsage{}}
	if err := checkBounds(root); err != nil {
		return m, err
	}
	sites, ops, err := references(root)
	if err != nil {
		return m, err
	}
	m.Schemas, err = schemaViews(root)
	if err != nil {
		return m, err
	}
	for _, site := range sites {
		target, _ := schemaTarget(site.effectiveRef())
		if target == "" {
			if !strings.HasPrefix(site.effectiveRef(), "#") {
				target = site.effectiveRef()
			} else {
				continue
			}
		}
		m.References = append(m.References, Reference{SourceSchema: site.schema, SourceProperty: site.property, TargetSchema: target, Pointer: site.pointer, Ref: site.ref})
	}
	m.Operations, err = operationUsages(sites, ops)
	if err != nil {
		return m, err
	}
	return m, nil
}
func required(obj map[string]any, name string) bool {
	values, _ := obj["required"].([]any)
	return slices.Contains(values, any(name))
}
func under(pointer, prefix string) bool {
	return pointer == prefix || strings.HasPrefix(pointer, prefix+"/")
}

func schemaViews(root map[string]any) ([]SchemaView, error) {
	views := []SchemaView{}
	positions := object(object(root[Extension])["positions"])
	rowY, rowHeight := 0.0, 0.0
	for i, name := range slices.Sorted(maps.Keys(schemaMap(root))) {
		if i > 0 && i%3 == 0 {
			rowY += rowHeight + 100
			rowHeight = 0
		}
		value := schemaMap(root)[name]
		obj := object(value)
		raw, err := jsonx.Marshal(value)
		if err != nil {
			return nil, err
		}
		s := SchemaView{Name: name, Pointer: schemaPointer(name), SchemaJSON: string(raw), Type: schemaType(value), Description: text(obj["description"]), Properties: []PropertyView{}, X: float64(i%3) * 360, Y: rowY}
		if pos := object(positions[name]); pos != nil {
			s.X, _ = number(pos["x"])
			s.Y, _ = number(pos["y"])
		}
		for _, key := range slices.Sorted(maps.Keys(object(obj["properties"]))) {
			v := object(obj["properties"])[key]
			raw, err := jsonx.Marshal(v)
			if err != nil {
				return nil, err
			}
			s.Properties = append(s.Properties, PropertyView{Name: key, Pointer: s.Pointer + "/properties/" + escape(key), SchemaJSON: string(raw), Type: schemaType(v), Required: required(obj, key)})
		}
		rowHeight = max(rowHeight, 54+30*float64(max(1, len(s.Properties))))
		views = append(views, s)
	}
	return views, nil
}
func operationUsages(sites []referenceSite, ops []operation) ([]OperationUsage, error) {
	visits := 0
	usages := make([]OperationUsage, 0, len(ops))
	for _, op := range ops {
		names := map[string]bool{}
		visited := map[string]bool{}
		queue := []string{op.Pointer}
		queue = append(queue, op.common...)
		for len(queue) > 0 {
			p := queue[0]
			queue = queue[1:]
			if visited[p] {
				continue
			}
			visited[p] = true
			start, _ := slices.BinarySearchFunc(sites, p+"/", func(site referenceSite, p string) int { return strings.Compare(site.pointer, p) })
			for _, site := range sites[start:] {
				if !under(site.pointer, p) {
					break
				}
				visits++
				if visits > 1000000 {
					return nil, fail(op.Pointer, "Слишком много транзитивных зависимостей операций (предел 1000000)")
				}
				target, pointer := schemaTarget(site.effectiveRef())
				if target != "" {
					names[target] = true
				}
				if pointer == "" {
					pointer = localPointer(site.effectiveRef())
				}
				if pointer != "" && !visited[pointer] {
					queue = append(queue, pointer)
				}
			}
		}
		op.Schemas = slices.Sorted(maps.Keys(names))
		usages = append(usages, op.OperationUsage)
	}
	return usages, nil
}
