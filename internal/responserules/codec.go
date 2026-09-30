package responserules

import (
	"bytes"
	"fmt"
	"math"
	"mime"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
)

// FieldError identifies a rejected field using a JSON pointer.
type FieldError struct {
	Pointer string
	Message string
}

func (e *FieldError) Error() string         { return e.Pointer + ": " + e.Message }
func invalid(pointer, message string) error { return &FieldError{Pointer: pointer, Message: message} }
func at(prefix string, err error) error {
	if e, ok := err.(*FieldError); ok {
		return &FieldError{Pointer: prefix + e.Pointer, Message: e.Message}
	}
	return invalid(prefix, err.Error())
}

type wireField struct {
	name     string
	target   any
	required bool
}

func required(name string, target any) wireField { return wireField{name, target, true} }
func optional(name string, target any) wireField { return wireField{name, target, false} }

// decodeObject enforces presence separately from Go zero values, including nulls.
// Read object names before folding to a map so duplicates cannot silently
// replace exact request text, a named example or a condition payload.
func decodeObject(data []byte, fields ...wireField) error {
	raw, err := decodeObjectFields(data)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(fields))
	for _, f := range fields {
		known[f.name] = true
	}
	names := make([]string, 0, len(raw))
	for k := range raw {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, name := range names {
		if !known[name] {
			return invalid("/"+name, "неизвестное поле")
		}
	}
	for _, f := range fields {
		value, ok := raw[f.name]
		if !ok {
			if f.required {
				return invalid("/"+f.name, "обязательное поле")
			}
			continue
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid("/"+f.name, "null не допускается")
		}
		if err := jsonx.Unmarshal(value, f.target); err != nil {
			return at("/"+f.name, err)
		}
	}
	return nil
}

func decodeObjectFields(data []byte) (map[string]jsonx.RawMessage, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, invalid("", "ожидается объект")
	}
	if !jsonx.Valid(data) {
		return nil, invalid("", "недопустимый JSON")
	}
	decoder := jsonx.NewDecoder(bytes.NewReader(data))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	fields := map[string]jsonx.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, invalid("", "ожидается имя поля")
		}
		if _, duplicate := fields[name]; duplicate {
			return nil, invalid("/"+name, "повторяющееся поле")
		}
		var raw jsonx.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		fields[name] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return fields, nil
}

type wireArray[T any] []T

func (a *wireArray[T]) UnmarshalJSON(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '[' {
		return invalid("", "ожидается массив")
	}
	var raw []jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &raw); err != nil {
		return err
	}
	result := make([]T, len(raw))
	for i, v := range raw {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return invalid(fmt.Sprintf("/%d", i), "null не допускается")
		}
		if err := jsonx.Unmarshal(v, &result[i]); err != nil {
			return at(fmt.Sprintf("/%d", i), err)
		}
	}
	*a = result
	return nil
}
func (e *Envelope) UnmarshalJSON(data []byte) error {
	var err error
	data, err = compactBound(data, MaxExtensionBytes)
	if err != nil {
		return err
	}
	var v Envelope
	var rules wireArray[Rule]
	if err := decodeObject(data, required("formatVersion", &v.FormatVersion), required("rules", &rules)); err != nil {
		return err
	}
	v.Rules = rules
	if v.FormatVersion != 1 {
		return invalid("/formatVersion", "неподдерживаемая версия")
	}
	if len(v.Rules) > MaxRules {
		return invalid("/rules", "слишком много правил")
	}
	ids := map[string]bool{}
	for i, r := range v.Rules {
		if ids[r.ID] {
			return invalid(fmt.Sprintf("/rules/%d/id", i), "повторяющийся ID")
		}
		ids[r.ID] = true
	}
	*e = v
	return nil
}
func (r *Rule) UnmarshalJSON(data []byte) error {
	var err error
	data, err = compactBound(data, MaxGraphBytes)
	if err != nil {
		return err
	}
	var v Rule
	var nodes wireArray[Node]
	var edges wireArray[Edge]
	var examples wireArray[Example]
	err = decodeObject(data, required("id", &v.ID), required("name", &v.Name), optional("binding", &v.Binding), required("nodes", &nodes), required("edges", &edges), optional("examples", &examples))
	if err != nil {
		return err
	}
	v.Nodes = nodes
	v.Edges = edges
	v.Examples = examples
	if err := CheckStructure(v); err != nil {
		return err
	}
	*r = v
	return nil
}
func (b *Binding) UnmarshalJSON(data []byte) error {
	var v Binding
	if err := decodeObject(data, required("method", &v.Method), required("path", &v.Path)); err != nil {
		return err
	}
	*b = v
	return nil
}
func (f *Field) UnmarshalJSON(data []byte) error {
	var v Field
	if err := decodeObject(data, required("name", &v.Name), required("value", &v.Value)); err != nil {
		return err
	}
	*f = v
	return nil
}
func (p *Position) UnmarshalJSON(data []byte) error {
	var v Position
	if err := decodeObject(data, required("nodeId", &v.NodeID), required("x", &v.X), required("y", &v.Y)); err != nil {
		return err
	}
	*p = v
	return nil
}
func (e *Edge) UnmarshalJSON(data []byte) error {
	var v Edge
	if err := decodeObject(data, required("id", &v.ID), required("from", &v.From), required("port", &v.Port), required("to", &v.To)); err != nil {
		return err
	}
	*e = v
	return nil
}
func (r *Response) UnmarshalJSON(data []byte) error {
	var v Response
	var headers wireArray[Field]
	if err := decodeObject(data, required("status", &v.Status), required("mediaType", &v.MediaType), required("headers", &headers), optional("bodyJSON", &v.BodyJSON), optional("bodyFrom", &v.BodyFrom)); err != nil {
		return err
	}
	v.Headers = headers
	*r = v
	return nil
}

type wireCondition overrides.Condition

func (c *wireCondition) UnmarshalJSON(data []byte) error {
	var v wireCondition
	if err := decodeObject(data, required("in", &v.In), required("name", &v.Name), required("op", &v.Op), optional("value", &v.Value)); err != nil {
		return err
	}
	if v.Op == "exists" {
		var raw map[string]jsonx.RawMessage
		if err := jsonx.Unmarshal(data, &raw); err != nil {
			return err
		}
		if _, ok := raw["value"]; ok {
			return invalid("/value", "exists не принимает value")
		}
	}
	*c = v
	return nil
}
func (n *Node) UnmarshalJSON(data []byte) error {
	var tag struct {
		Type string `json:"type"`
	}
	if err := jsonx.Unmarshal(data, &tag); err != nil {
		return err
	}
	var v Node
	var condition *wireCondition
	fields := []wireField{required("id", &v.ID), required("type", &v.Type), required("name", &v.Name), required("x", &v.X), required("y", &v.Y)}
	switch tag.Type {
	case "start", "fallback":
	case "condition":
		fields = append(fields, optional("condition", &condition), optional("resultCondition", &v.ResultCondition))
	case "delay":
		fields = append(fields, required("delayMs", &v.DelayMs))
	case "response":
		fields = append(fields, required("response", &v.Response))
	case "entity_read", "entity_create", "entity_update":
		fields = append(fields, required("entity", &v.Entity))
	default:
		return invalid("/type", "неизвестный тип узла")
	}
	if err := decodeObject(data, fields...); err != nil {
		return err
	}
	if tag.Type == "condition" {
		if (condition != nil) == (v.ResultCondition != nil) {
			return invalid("/condition", "требуется ровно одно из condition и resultCondition")
		}
		if condition != nil {
			c := overrides.Condition(*condition)
			v.Condition = &c
		}
	}
	*n = v
	return nil
}
func (c *Command) UnmarshalJSON(data []byte) error {
	var tag struct {
		Type string `json:"type"`
	}
	if err := jsonx.Unmarshal(data, &tag); err != nil {
		return err
	}
	var v Command
	var positions wireArray[Position]
	fields := []wireField{required("type", &v.Type)}
	switch tag.Type {
	case "set_rule":
		fields = append(fields, required("name", &v.Name), optional("binding", &v.Binding))
	case "add_node", "update_node":
		fields = append(fields, required("node", &v.Node))
	case "remove_node":
		fields = append(fields, required("nodeId", &v.NodeID))
	case "add_edge", "update_edge":
		fields = append(fields, required("edge", &v.Edge))
	case "remove_edge":
		fields = append(fields, required("edgeId", &v.EdgeID))
	case "move_nodes":
		fields = append(fields, required("positions", &positions))
	case "add_example", "update_example":
		fields = append(fields, required("example", &v.Example))
	case "remove_example":
		fields = append(fields, required("exampleId", &v.ExampleID))
	default:
		return invalid("/type", "неизвестная команда")
	}
	if err := decodeObject(data, fields...); err != nil {
		return err
	}
	if tag.Type == "move_nodes" {
		v.Positions = positions
	}
	if tag.Type == "remove_example" && !ValidID(v.ExampleID) {
		return invalid("/exampleId", "недопустимый ID примера")
	}
	*c = v
	return nil
}
func (r *Request) UnmarshalJSON(data []byte) error {
	var err error
	data, err = compactBound(data, MaxFixtureBytes)
	if err != nil {
		return err
	}
	var v Request
	var query, headers, path wireArray[Field]
	var entities wireArray[EntityFixture]
	if err := decodeObject(data, required("query", &query), required("headers", &headers), optional("bodyJSON", &v.BodyJSON), optional("path", &path), optional("entities", &entities)); err != nil {
		return err
	}
	v.Query = query
	v.Headers = headers
	v.Path = path
	v.Entities = entities
	*r = v
	return nil
}

func ValidID(id string) bool {
	if len(id) < 1 || len(id) > 80 {
		return false
	}
	for _, c := range []byte(id) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func Decode(root map[string]any) (Envelope, error) {
	raw, ok := root[Extension]
	if !ok {
		return Envelope{FormatVersion: 1, Rules: []Rule{}}, nil
	}
	data, err := jsonx.Marshal(raw)
	if err != nil {
		return Envelope{}, at("/"+Extension, err)
	}
	var env Envelope
	if err := jsonx.Unmarshal(data, &env); err != nil {
		return Envelope{}, at("/"+Extension, err)
	}
	return env, nil
}
func textBound(value string, max int) bool { return len(value) <= max && utf8.ValidString(value) }
func coordinate(v float64) bool            { return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) <= 100000 }
func CheckStructure(r Rule) error {
	if !ValidID(r.ID) {
		return invalid("/id", "недопустимый ID")
	}
	if !textBound(r.Name, 200) {
		return invalid("/name", "имя превышает 200 байт UTF-8")
	}
	if r.Binding != nil && (!textBound(r.Binding.Path, 2048) || !textBound(r.Binding.Method, 256)) {
		return invalid("/binding", "слишком длинная привязка")
	}
	if err := checkExamples(r.Examples); err != nil {
		return at("/examples", err)
	}
	if r.Nodes == nil || len(r.Nodes) > MaxNodes {
		return invalid("/nodes", "ожидается массив до 100 узлов")
	}
	if r.Edges == nil || len(r.Edges) > MaxEdges {
		return invalid("/edges", "ожидается массив до 200 рёбер")
	}
	ids := map[string]bool{}
	for i, n := range r.Nodes {
		prefix := fmt.Sprintf("/nodes/%d", i)
		if !ValidID(n.ID) || ids[n.ID] {
			return invalid(prefix+"/id", "недопустимый или повторяющийся ID")
		}
		ids[n.ID] = true
		if !textBound(n.Name, 200) {
			return invalid(prefix+"/name", "имя превышает 200 байт UTF-8")
		}
		if !coordinate(n.X) || !coordinate(n.Y) {
			return invalid(prefix, "координаты должны быть конечными в пределах ±100000")
		}
		if err := checkNode(n); err != nil {
			return at(prefix, err)
		}
	}
	ids = map[string]bool{}
	for i, e := range r.Edges {
		prefix := fmt.Sprintf("/edges/%d", i)
		if !ValidID(e.ID) || ids[e.ID] {
			return invalid(prefix+"/id", "недопустимый или повторяющийся ID")
		}
		ids[e.ID] = true
		if !ValidID(e.From) || !ValidID(e.To) {
			return invalid(prefix, "недопустимый ID узла")
		}
		if !textBound(e.Port, 256) {
			return invalid(prefix+"/port", "слишком длинный порт")
		}
	}
	data, err := jsonx.Marshal(r)
	if err != nil {
		return err
	}
	if len(data) > MaxGraphBytes {
		return invalid("", "правило превышает 512 КиБ")
	}
	return nil
}
func checkNode(n Node) error {
	condition, delay, response := n.Condition != nil, n.DelayMs != nil, n.Response != nil
	resultCondition := n.ResultCondition != nil
	entity := n.Entity != nil
	switch n.Type {
	case "start", "fallback":
		if condition || resultCondition || delay || response || entity {
			return invalid("", "лишние поля узла")
		}
	case "condition":
		if condition == resultCondition || delay || response || entity {
			return invalid("/condition", "требуется ровно одно из condition и resultCondition")
		}
		if resultCondition {
			if err := checkResultCondition(*n.ResultCondition); err != nil {
				return at("/resultCondition", err)
			}
			return nil
		}
		c := n.Condition
		if !textBound(c.In, 256) || !textBound(c.Op, 256) || !textBound(c.Name, 256) || !textBound(c.Value, 4096) {
			return invalid("/condition", "превышена длина условия")
		}
		if c.Op == "exists" && c.Value != "" {
			return invalid("/condition/value", "exists не принимает value")
		}
	case "delay":
		if !delay || condition || resultCondition || response || entity {
			return invalid("/delayMs", "требуется только delayMs")
		}
	case "response":
		if !response || condition || resultCondition || delay || entity {
			return invalid("/response", "требуется только response")
		}
		if err := checkResponse(*n.Response); err != nil {
			return at("/response", err)
		}
	case "entity_read", "entity_create", "entity_update":
		if !entity || condition || resultCondition || delay || response {
			return invalid("/entity", "требуется только entity")
		}
		return checkEntityOperation(n.Type, *n.Entity)
	default:
		return invalid("/type", "неизвестный тип узла")
	}
	return nil
}
func checkResponse(r Response) error {
	if r.BodyFrom != nil {
		if r.BodyJSON != nil {
			return invalid("/bodyFrom", "источники тела взаимоисключающие")
		}
		if err := checkValueRef(*r.BodyFrom); err != nil {
			return at("/bodyFrom", err)
		}
	}
	if err := checkFieldValue(r.MediaType); err != nil {
		return at("/mediaType", err)
	}
	media, _, err := mime.ParseMediaType(r.MediaType)
	if err != nil || httpx.BrowserExecutableMediaType(r.MediaType) || !(media == "application/json" || strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json") && !strings.Contains(media, "*")) {
		return invalid("/mediaType", "требуется корректный JSON media type")
	}
	if r.BodyJSON != nil && !textBound(*r.BodyJSON, MaxBodyBytes) {
		return invalid("/bodyJSON", "тело превышает 64 КиБ UTF-8")
	}
	if r.Headers == nil || len(r.Headers) > 100 {
		return invalid("/headers", "ожидается массив до 100 заголовков")
	}
	seen := map[string]bool{}
	for i, f := range r.Headers {
		prefix := fmt.Sprintf("/headers/%d", i)
		key := strings.ToLower(f.Name)
		if err := checkHeader(f); err != nil {
			return at(prefix, err)
		}
		if seen[key] {
			return invalid(prefix+"/name", "повторяющийся заголовок")
		}
		seen[key] = true
		switch key {
		case "content-type", "content-length", "transfer-encoding", "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "upgrade", "set-cookie", "set-cookie2":
			return invalid(prefix+"/name", "управляемый заголовок запрещён")
		}
	}
	return nil
}
func checkHeader(f Field) error {
	if len(f.Name) == 0 || !textBound(f.Name, 256) {
		return invalid("/name", "недопустимое имя заголовка")
	}
	for _, c := range []byte(f.Name) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return invalid("/name", "недопустимое имя заголовка")
		}
	}
	if err := checkFieldValue(f.Value); err != nil {
		return at("/value", err)
	}
	return nil
}

// Wire limits concern compact encoding; whitespace is bounded by the transport.
func compactBound(data []byte, maximum int) ([]byte, error) {
	if !utf8.Valid(data) {
		return nil, invalid("", "ожидается UTF-8")
	}
	if len(data) <= maximum {
		return data, nil
	}
	var compact bytes.Buffer
	if err := jsonx.Compact(&compact, data); err != nil {
		return nil, err
	}
	if compact.Len() > maximum {
		return nil, invalid("", fmt.Sprintf("размер превышает %d байт", maximum))
	}
	return compact.Bytes(), nil
}

// MarshalJSON retains an explicitly empty move list, despite the union's omitempty.
func (c Command) MarshalJSON() ([]byte, error) {
	type plain Command
	if c.Type == "move_nodes" {
		return jsonx.Marshal(struct {
			plain
			Positions []Position `json:"positions"`
		}{plain(c), c.Positions})
	}
	return jsonx.Marshal(plain(c))
}

// checkFieldValue also guards raw media types; MIME parsing permits controls in
// otherwise valid parameters and trims trailing whitespace from the essence.
func checkFieldValue(value string) error {
	if !textBound(value, 4096) {
		return invalid("", "значение превышает 4096 байт UTF-8")
	}
	for _, c := range []byte(value) {
		if c < 32 || c == 127 {
			return invalid("", "управляющие символы запрещены")
		}
	}
	return nil
}
