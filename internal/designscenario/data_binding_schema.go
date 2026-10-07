package designscenario

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// fieldAt distinguishes an undeclared field from a schema that cannot be
// classified. Only a closed object can prove that a property is undeclared.
func (s bindingSchema) fieldAt(ctx context.Context, value any, pointer string) (string, string) {
	if !validExecutionPointer(pointer) {
		return "unknown", ""
	}
	tokens := []string{}
	if pointer != "" {
		tokens = strings.Split(pointer[1:], "/")
	}
	if len(tokens) > maxDataFlowDepth {
		return "unknown", ""
	}
	for _, token := range tokens {
		if ctx.Err() != nil {
			return "unknown", ""
		}
		node, ok := s.resolve(value)
		if !ok {
			return "unknown", ""
		}
		if impactUnsupportedFieldKeywords(node) {
			return "unknown", ""
		}
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch schemaType(node) {
		case "object":
			properties := object(node["properties"])
			if next, found := properties[token]; found {
				value = next
				continue
			}
			if additional, closed := node["additionalProperties"].(bool); closed && !additional {
				return "absent", ""
			}
			return "unknown", ""
		case "array":
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || strconv.Itoa(index) != token {
				return "unknown", ""
			}
			value = node["items"]
		default:
			return "unknown", ""
		}
	}
	node, ok := s.resolve(value)
	if !ok || impactUnsupportedFieldKeywords(node) || schemaType(node) == "unknown" {
		return "unknown", ""
	}
	return "present", schemaType(node)
}

func impactUnsupportedFieldKeywords(node map[string]any) bool {
	for _, key := range []string{"patternProperties", "unevaluatedProperties", "if", "then", "else", "dependentSchemas"} {
		if _, exists := node[key]; exists {
			return true
		}
	}
	return false
}

func (s bindingSchema) responseField(ctx context.Context, pointer string) (string, string) {
	if !s.available || s.uncertain || len(s.responses) == 0 {
		return "unknown", ""
	}
	var presence, typ string
	for _, response := range s.responses {
		p, t := s.fieldAt(ctx, response, pointer)
		if presence != "" && (presence != p || typ != t) {
			return "unknown", ""
		}
		presence, typ = p, t
	}
	return presence, typ
}

func (s bindingSchema) targetField(ctx context.Context, target DataBindingTarget) (string, string) {
	if !s.available || s.uncertain {
		return "unknown", ""
	}
	if target.Kind == "body" {
		if s.body == nil {
			return "absent", ""
		}
		return s.fieldAt(ctx, s.body, target.Pointer)
	}
	name := target.Name
	if target.Kind == "header" {
		name = strings.ToLower(name)
	}
	value, exists := s.parameters[target.Kind+":"+name]
	if !exists {
		return "absent", ""
	}
	return s.fieldAt(ctx, value, "")
}

const maxDataFlowFields = 2000
const maxDataFlowDepth = 20

type bindingSchema struct {
	available         bool
	root              map[string]any
	responses         []any
	body              any
	parameters        map[string]any
	parameterRequired map[string]bool
	bodyRequired      bool
	uncertain         bool
}

func object(value any) map[string]any { v, _ := value.(map[string]any); return v }
func stringValue(value any) string    { v, _ := value.(string); return v }
func schemaType(value any) string {
	s := object(value)
	if s == nil {
		return "unknown"
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf", "not"} {
		if _, ok := s[key]; ok {
			return "unknown"
		}
	}
	if nullable, _ := s["nullable"].(bool); nullable {
		return "unknown"
	}
	switch t := stringValue(s["type"]); t {
	case "string", "number", "integer", "boolean", "null", "object", "array":
		return t
	}
	if _, ok := s["properties"]; ok {
		return "object"
	}
	return "unknown"
}
func resolveBindingSchema(root map[string]any, value any, depth int, seen map[string]bool) (map[string]any, bool) {
	if depth > maxDataFlowDepth {
		return nil, false
	}
	s := object(value)
	if s == nil {
		return nil, false
	}
	ref, has := s["$ref"]
	if !has {
		return s, true
	}
	name := stringValue(ref)
	if !strings.HasPrefix(name, "#/") || seen[name] || len(s) != 1 {
		return nil, false
	}
	seen[name] = true
	defer delete(seen, name)
	next, ok := runReadPointer(root, name[1:])
	if !ok {
		return nil, false
	}
	return resolveBindingSchema(root, next, depth+1, seen)
}
func (s bindingSchema) resolve(value any) (map[string]any, bool) {
	return resolveBindingSchema(s.root, value, 0, map[string]bool{})
}
func (s bindingSchema) typeAt(value any, pointer string) string {
	if !validExecutionPointer(pointer) {
		return "unknown"
	}
	tokens := []string{}
	if pointer != "" {
		tokens = strings.Split(pointer[1:], "/")
	}
	if len(tokens) > maxDataFlowDepth {
		return "unknown"
	}
	for _, token := range tokens {
		node, ok := s.resolve(value)
		if !ok {
			return "unknown"
		}
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch schemaType(node) {
		case "object":
			value = object(node["properties"])[token]
		case "array":
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || strconv.Itoa(index) != token {
				return "unknown"
			}
			value = node["items"]
		default:
			return "unknown"
		}
	}
	node, _ := s.resolve(value)
	return schemaType(node)
}
func (s bindingSchema) responseType(pointer string) string {
	result := ""
	for _, schema := range s.responses {
		t := s.typeAt(schema, pointer)
		if result != "" && result != t {
			return "unknown"
		}
		result = t
	}
	if result == "" {
		return "unknown"
	}
	return result
}
func (s bindingSchema) targetType(target DataBindingTarget) string {
	if target.Kind == "body" {
		return s.typeAt(s.body, target.Pointer)
	}
	name := target.Name
	if target.Kind == "header" {
		name = strings.ToLower(name)
	}
	return s.typeAt(s.parameters[target.Kind+":"+name], "")
}

type bindingOperation struct {
	pathItem  map[string]any
	operation map[string]any
	uncertain bool
}

type bindingSchemaKey struct {
	contractID        string
	operationKey      string
	expectedStatus    int
	hasExpectedStatus bool
}

// Index each referenced contract once. Sorted paths and operationMethods retain
// the existing deterministic first match when a proposed document has duplicate
// operation keys; persisted documents separately reject duplicate keys.
func indexBindingOperations(root map[string]any) map[string]bindingOperation {
	out := map[string]bindingOperation{}
	for _, operation := range ContractOperations(root) {
		if operation.Key == "" {
			continue
		}
		if previous, exists := out[operation.Key]; exists {
			previous.uncertain = true
			out[operation.Key] = previous
		} else {
			out[operation.Key] = bindingOperation{pathItem: operation.Item, operation: operation.Operation, uncertain: operation.Uncertain}
		}
	}
	return out
}

// bindingSchemas serves the synchronous validation and run paths, which carry
// no context: the build is bounded by the document's size caps, so it runs
// to completion rather than borrowing a context.Background it cannot cancel.
func bindingSchemas(document Document) []bindingSchema {
	out, _ := collectBindingSchemas(document, func() error { return nil })
	return out
}

func bindingSchemasContext(ctx context.Context, document Document) ([]bindingSchema, error) {
	return collectBindingSchemas(document, ctx.Err)
}

// collectBindingSchemas takes the cancellation probe as a function so the
// context-free and context-bound callers share one walk.
func collectBindingSchemas(document Document, cancelled func() error) ([]bindingSchema, error) {
	roots := map[string]map[string]any{}
	for _, contract := range document.Contracts {
		if err := cancelled(); err != nil {
			return nil, err
		}
		value, _ := decodeJSONValue(contract.Document)
		roots[contract.ID] = object(value)
	}
	indexes := map[string]map[string]bindingOperation{}
	cache := map[bindingSchemaKey]bindingSchema{}
	out := make([]bindingSchema, len(document.Messages))
	for i, message := range document.Messages {
		if err := cancelled(); err != nil {
			return nil, err
		}
		if message.Kind != "request" || message.Operation == nil {
			continue
		}
		key := bindingSchemaKey{contractID: message.Operation.ContractID, operationKey: message.Operation.OperationKey}
		if message.Execution != nil && message.Execution.ExpectedStatus != nil {
			key.hasExpectedStatus = true
			key.expectedStatus = *message.Execution.ExpectedStatus
		}
		if schema, exists := cache[key]; exists {
			out[i] = schema
			continue
		}
		index, exists := indexes[key.contractID]
		if !exists {
			index = indexBindingOperations(roots[key.contractID])
			indexes[key.contractID] = index
		}
		schema := resolveOperationBindingSchema(roots[key.contractID], index[key.operationKey], key)
		// Schemas and their referenced maps/slices are immutable after construction.
		cache[key] = schema
		out[i] = schema
	}
	return out, nil
}

func resolveOperationBindingSchema(root map[string]any, operation bindingOperation, key bindingSchemaKey) bindingSchema {
	s := bindingSchema{root: root, parameters: map[string]any{}, parameterRequired: map[string]bool{}, uncertain: operation.uncertain}
	op := operation.operation
	if op == nil {
		s.uncertain = true
		return s
	}
	s.available = true
	for _, owner := range []map[string]any{operation.pathItem, op} {
		parameters, _ := owner["parameters"].([]any)
		for _, raw := range parameters {
			p, ok := s.resolve(raw)
			if !ok {
				s.uncertain = true
				continue
			}
			kind, name := stringValue(p["in"]), stringValue(p["name"])
			if kind != "path" && kind != "query" && kind != "header" {
				continue
			}
			if kind == "header" {
				name = strings.ToLower(name)
			}
			parameterKey := kind + ":" + name
			s.parameters[parameterKey] = p["schema"]
			s.parameterRequired[parameterKey], _ = p["required"].(bool)
		}
	}
	if raw, exists := op["requestBody"]; exists {
		body, ok := s.resolve(raw)
		if !ok {
			s.uncertain = true
		}
		s.bodyRequired, _ = body["required"].(bool)
		s.body = jsonContentSchema(body)
	}
	responses := object(op["responses"])
	for _, status := range slices.Sorted(maps.Keys(responses)) {
		selected := strings.HasPrefix(status, "2")
		if key.hasExpectedStatus {
			selected = status == strconv.Itoa(key.expectedStatus)
		}
		if !selected {
			continue
		}
		response, ok := s.resolve(responses[status])
		if !ok {
			s.uncertain = true
		}
		s.responses = append(s.responses, jsonContentSchema(response))
	}
	return s
}
func jsonContentSchema(owner map[string]any) any {
	content := object(owner["content"])
	var schema any
	count := 0
	for _, name := range slices.Sorted(maps.Keys(content)) {
		if name == "application/json" || strings.HasSuffix(name, "+json") {
			schema = object(content[name])["schema"]
			count++
		}
	}
	if count > 1 {
		return map[string]any{}
	}
	return schema
}

func schemaCatalog(s bindingSchema, messageID string, remaining int) (DataFlowMessage, bool, bool) {
	out := DataFlowMessage{MessageID: messageID, ResponseFields: []DataFlowField{}, RequestFields: []DataFlowField{}}
	limit := min(maxDataFlowFields, remaining)
	count := 0
	truncated, unknown := false, s.uncertain
	var walk func(any, string, string, bool, int, *[]DataFlowField)
	walk = func(raw any, kind, pointer string, required bool, depth int, fields *[]DataFlowField) {
		if count >= limit || depth > maxDataFlowDepth || len(pointer) > 2000 {
			truncated = true
			return
		}
		node, ok := s.resolve(raw)
		t := schemaType(node)
		if !ok || t == "unknown" {
			unknown = true
		}
		*fields = append(*fields, DataFlowField{Kind: kind, Pointer: pointer, Type: t, Required: required})
		count++
		if t != "object" {
			return
		}
		requiredNames := map[string]bool{}
		names, _ := node["required"].([]any)
		for _, name := range names {
			requiredNames[stringValue(name)] = true
		}
		properties := object(node["properties"])
		for _, name := range slices.Sorted(maps.Keys(properties)) {
			if count >= limit {
				truncated = true
				break
			}
			walk(properties[name], kind, pointer+"/"+escapePointer(name), required && requiredNames[name], depth+1, fields)
		}
	}
	// Use one catalog for all successful responses; disagreements remain unknown.
	seen := map[string]int{}
	present := map[string]int{}
	for _, response := range s.responses {
		fields := []DataFlowField{}
		walk(response, "response", "", true, 0, &fields)
		for _, field := range fields {
			present[field.Pointer]++
			if index, ok := seen[field.Pointer]; ok {
				out.ResponseFields[index].Required = out.ResponseFields[index].Required && field.Required
				continue
			}
			field.Type = s.responseType(field.Pointer)
			if field.Type == "unknown" {
				unknown = true
			}
			seen[field.Pointer] = len(out.ResponseFields)
			out.ResponseFields = append(out.ResponseFields, field)
		}
	}
	for i := range out.ResponseFields {
		if present[out.ResponseFields[i].Pointer] != len(s.responses) {
			out.ResponseFields[i].Required = false
		}
	}
	for _, key := range slices.Sorted(maps.Keys(s.parameters)) {
		if count >= limit {
			truncated = true
			break
		}
		kind, name, _ := strings.Cut(key, ":")
		t := s.typeAt(s.parameters[key], "")
		if t == "unknown" {
			unknown = true
		}
		out.RequestFields = append(out.RequestFields, DataFlowField{Kind: kind, Name: name, Type: t, Required: s.parameterRequired[key]})
		count++
	}
	if s.body != nil {
		walk(s.body, "body", "", s.bodyRequired, 0, &out.RequestFields)
	}
	slices.SortFunc(out.ResponseFields, func(a, b DataFlowField) int { return strings.Compare(a.Pointer, b.Pointer) })
	return out, truncated, unknown
}
func compatibleBindingTypes(source, target string) bool {
	return source == "unknown" || target == "unknown" || source == target || (source == "integer" && target == "number")
}
func bindingTypeError(source, target string) string {
	return fmt.Sprintf("incompatible binding types: %s → %s", source, target)
}
