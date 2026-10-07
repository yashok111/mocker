package scenarioexport

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

var draft07Keywords = map[string]bool{
	"$schema": true, "$id": true, "$ref": true, "$comment": true, "definitions": true,
	"title": true, "description": true, "default": true, "examples": true, "readOnly": true, "writeOnly": true,
	"type": true, "enum": true, "const": true, "multipleOf": true, "maximum": true, "exclusiveMaximum": true,
	"minimum": true, "exclusiveMinimum": true, "maxLength": true, "minLength": true, "pattern": true,
	"additionalItems": true, "items": true, "maxItems": true, "minItems": true, "uniqueItems": true, "contains": true,
	"maxProperties": true, "minProperties": true, "required": true, "properties": true, "patternProperties": true,
	"additionalProperties": true, "dependencies": true, "propertyNames": true,
	"if": true, "then": true, "else": true, "allOf": true, "anyOf": true, "oneOf": true, "not": true,
	"format": true, "contentMediaType": true, "contentEncoding": true,
}

func (c *eventValidationCache) parseSchema(id string) eventParsedSchema {
	if parsed, ok := c.parsed[id]; ok {
		return parsed
	}
	parsed := c.parseSchemaUncached(id)
	c.parsed[id] = parsed
	return parsed
}

func (c *eventValidationCache) parseSchemaUncached(id string) eventParsedSchema {
	item, ok := c.schemas[id]
	parsed := eventParsedSchema{}
	sourcePointer := c.schemaPointer(id)
	invalid := func(message string) eventParsedSchema {
		parsed.issues = append(parsed.issues, diagnostic("event_schema_invalid", "error", message, "event-schema", id, sourcePointer))
		return parsed
	}
	if !ok {
		return invalid("Схема отсутствует")
	}
	if len(item.SchemaJSON) > 256<<10 {
		return invalid("Схема превышает 256 KiB")
	}
	v, err := decodeJSON([]byte(item.SchemaJSON))
	if err != nil {
		return invalid("Неверный JSON схемы: " + err.Error())
	}
	if _, ok := v.(map[string]any); !ok {
		if _, yes := v.(bool); !yes {
			invalid("Корень схемы должен быть объектом или boolean")
		}
	}
	parsed.value = v
	if !validJSONShape(v, 0, new(int)) {
		return invalid("Превышен лимит глубины или узлов схемы")
	}
	walker := schemaWalker{ctx: c.ctx, parsed: &parsed, id: id, sourcePointer: sourcePointer}
	walker.walk(v, 0, "")
	if v == true || isEmptyObject(v) {
		parsed.issues = append(parsed.issues, diagnostic("event_schema_unconstrained", "warning", "Схема не ограничивает содержимое события", "event-schema", id, sourcePointer))
	}
	if v == false {
		invalid("Схема false не допускает сообщений")
	}
	return parsed
}

// schemaWalker checks one authored schema against the Draft 07 subset the
// export supports and records its components.schemas references.
type schemaWalker struct {
	ctx               context.Context
	parsed            *eventParsedSchema
	id, sourcePointer string
	nodes             int
}

func (w *schemaWalker) issue(code, message string) {
	w.parsed.issues = append(w.parsed.issues, diagnostic(code, "error", message, "event-schema", w.id, w.sourcePointer))
}

func (w *schemaWalker) walk(value any, depth int, pointer string) {
	if w.ctx.Err() != nil {
		return
	}
	w.nodes++
	if w.nodes > 10000 || depth > 64 {
		if w.nodes == 10001 || depth == 65 {
			w.issue("event_schema_invalid", "Превышен лимит глубины или узлов схемы")
		}
		return
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return
	}
	w.checkKeywords(obj, pointer)
	w.walkChildren(obj, depth, pointer)
}

func (w *schemaWalker) checkKeywords(obj map[string]any, pointer string) {
	for _, key := range slices.Sorted(maps.Keys(obj)) {
		if !draft07Keywords[key] {
			w.issue("event_schema_invalid", "Неподдерживаемое ключевое слово схемы: "+pointer+"/"+escapePointer(key))
		}
	}
	if dialect, ok := obj["$schema"]; ok && dialect != "http://json-schema.org/draft-07/schema#" && dialect != "https://json-schema.org/draft-07/schema#" && dialect != "http://json-schema.org/draft-07/schema" && dialect != "https://json-schema.org/draft-07/schema" {
		w.issue("event_schema_invalid", "Допустим только JSON Schema Draft 07")
	}
	if _, ok := obj["$id"]; ok {
		w.issue("event_ref_unsupported", "$id с изменением базы ссылок не поддерживается")
	}
	if raw, ok := obj["$ref"]; ok {
		ref, yes := raw.(string)
		parts := strings.Split(strings.TrimPrefix(ref, "#/components/schemas/"), "/")
		if !yes || !strings.HasPrefix(ref, "#/components/schemas/") || parts[0] == "" || strings.Contains(parts[0], "~") {
			w.issue("event_ref_unsupported", "Разрешены только ссылки на components.schemas")
		} else {
			w.parsed.refs = append(w.parsed.refs, ref)
		}
	}
}

// walkChildren visits subschemas in a fixed keyword order so diagnostics and
// references come out in the same order on every run.
func (w *schemaWalker) walkChildren(obj map[string]any, depth int, pointer string) {
	for _, key := range []string{"properties", "patternProperties", "definitions"} {
		if children, ok := obj[key].(map[string]any); ok {
			for _, name := range slices.Sorted(maps.Keys(children)) {
				w.walk(children[name], depth+1, pointer+"/"+key+"/"+escapePointer(name))
			}
		}
	}
	if children, ok := obj["dependencies"].(map[string]any); ok {
		for _, name := range slices.Sorted(maps.Keys(children)) {
			child := children[name]
			if _, isArray := child.([]any); !isArray {
				w.walk(child, depth+1, pointer+"/dependencies/"+escapePointer(name))
			}
		}
	}
	for _, key := range []string{"additionalItems", "items", "contains", "additionalProperties", "propertyNames", "if", "then", "else", "not"} {
		if child, ok := obj[key]; ok {
			if arr, yes := child.([]any); yes {
				for i, v := range arr {
					w.walk(v, depth+1, fmt.Sprintf("%s/%s/%d", pointer, key, i))
				}
			} else {
				w.walk(child, depth+1, pointer+"/"+key)
			}
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		if arr, ok := obj[key].([]any); ok {
			for i, child := range arr {
				w.walk(child, depth+1, fmt.Sprintf("%s/%s/%d", pointer, key, i))
			}
		}
	}
}

func isEmptyObject(v any) bool { obj, ok := v.(map[string]any); return ok && len(obj) == 0 }
func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func (c *eventValidationCache) collectSchema(id string, used map[string]any, ds *eventDiagnostics) {
	if _, exists := used[id]; exists {
		return
	}
	if len(used) > 1000 {
		ds.add(diagnostic("event_schema_invalid", "error", "Слишком много зависимостей схем", "event-schema", id, "/eventModel/schemas"))
		return
	}
	parsed := c.parseSchema(id)
	for _, issue := range parsed.issues {
		ds.add(issue)
	}
	used[id] = parsed.value
	for _, reference := range parsed.refs {
		parts := strings.Split(strings.TrimPrefix(reference, "#/components/schemas/"), "/")
		dependency := parts[0]
		c.collectSchema(dependency, used, ds)
		value := c.parseSchema(dependency).value
		for _, token := range parts[1:] {
			if strings.Contains(token, "~") && strings.Contains(strings.ReplaceAll(strings.ReplaceAll(token, "~0", ""), "~1", ""), "~") {
				value = nil
				break
			}
			token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
			switch current := value.(type) {
			case map[string]any:
				var ok bool
				value, ok = current[token]
				if !ok {
					value = nil
				}
			case []any:
				index, err := strconv.Atoi(token)
				if err != nil || index < 0 || index >= len(current) {
					value = nil
				} else {
					value = current[index]
				}
			default:
				value = nil
			}
			if value == nil {
				break
			}
		}
		if value == nil {
			ds.add(diagnostic("event_ref_unsupported", "error", "Ссылка на недоступный JSON Pointer: "+reference, "event-schema", id, "/eventModel/schemas"))
		}
	}
}

func (c *eventValidationCache) resolvedRoot(id string, visited map[string]bool) any {
	return c.resolveRootRef("#/components/schemas/"+id, visited)
}

func (c *eventValidationCache) resolveRootRef(reference string, visited map[string]bool) any {
	if !strings.HasPrefix(reference, "#/components/schemas/") || visited[reference] {
		return nil
	}
	visited[reference] = true
	parts := strings.Split(strings.TrimPrefix(reference, "#/components/schemas/"), "/")
	value := c.parseSchema(parts[0]).value
	for _, token := range parts[1:] {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch current := value.(type) {
		case map[string]any:
			value = current[token]
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(current) {
				return nil
			}
			value = current[index]
		default:
			return nil
		}
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return value
	}
	if next, ok := obj["$ref"].(string); ok {
		return c.resolveRootRef(next, visited)
	}
	return value
}

func (c *eventValidationCache) checkDiscriminator(channel designscenario.EventChannel, ds *eventDiagnostics) {
	seen := map[string]bool{}
	property := channel.DiscriminatorProperty
	if property == "" {
		ds.add(diagnostic("event_channel_ambiguous", "error", "Выберите discriminatorProperty для нескольких типов сообщений", "event-channel", channel.ID, "/eventModel/channels"))
		return
	}
	for _, id := range channel.MessageIDs {
		message, ok := c.messages[id]
		if !ok {
			continue
		}
		root, ok := c.resolvedRoot(message.PayloadSchemaID, map[string]bool{}).(map[string]any)
		if !ok || root["type"] != "object" {
			ds.add(diagnostic("event_channel_ambiguous", "error", "Payload должен быть схемой объекта с discriminator", "event-message", id, "/eventModel/messages"))
			continue
		}
		required, _ := root["required"].([]any)
		if !slices.ContainsFunc(required, func(v any) bool { return v == property }) {
			ds.add(diagnostic("event_channel_ambiguous", "error", "Discriminator должен быть обязательным полем", "event-message", id, "/eventModel/messages"))
		}
		properties, _ := root["properties"].(map[string]any)
		field, _ := properties[property].(map[string]any)
		constant, _ := field["const"].(string)
		if constant == "" || field["type"] != "string" || seen[constant] {
			ds.add(diagnostic("event_channel_ambiguous", "error", "Discriminator требует уникальный непустой строковый const", "event-message", id, "/eventModel/messages"))
		}
		seen[constant] = true
	}
}

func eventFormDiagnostics(envelope map[string]string, contractID string, channels map[string]designscenario.EventChannel, messages map[string]designscenario.EventMessage, schemas map[string]any, servers map[string]designscenario.EventServer) []Diagnostic {
	forms, corrupt := parseFormDrafts(envelope)
	if corrupt {
		return []Diagnostic{{Code: "event_forms_pending", Severity: "error", Message: "Восстановите незавершённые формы"}}
	}
	for _, pointer := range slices.Sorted(maps.Keys(forms)) {
		parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
		if len(parts) < 2 {
			return []Diagnostic{{Code: "event_forms_pending", Severity: "error", Message: "Неизвестная область формы"}}
		}
		var used bool
		switch parts[0] {
		case "event-contract":
			used = parts[1] == contractID
		case "event-channel":
			_, used = channels[parts[1]]
		case "event-message":
			_, used = messages[parts[1]]
		case "event-schema":
			_, used = schemas[parts[1]]
		case "event-server":
			_, used = servers[parts[1]]
		case "canvas-contract":
			continue
		default:
			return []Diagnostic{{Code: "event_forms_pending", Severity: "error", Message: "Неизвестная область формы"}}
		}
		if used {
			return []Diagnostic{diagnostic("event_forms_pending", "error", "Завершите редактирование событийного контракта", parts[0], parts[1], pointer)}
		}
	}
	return nil
}

func parseFormDrafts(envelope map[string]string) (map[string]jsonx.RawMessage, bool) {
	forms := map[string]jsonx.RawMessage{}
	for key, raw := range envelope {
		if key != "all" {
			return nil, true
		}
		if err := jsonx.Unmarshal([]byte(raw), &forms); err != nil || forms == nil {
			return nil, true
		}
		for pointer, value := range forms {
			var field map[string]jsonx.RawMessage
			if err := jsonx.Unmarshal(value, &field); err != nil || field == nil || field["source"] == nil || field["propertySource"] == nil {
				return nil, true
			}
			var source, property string
			if jsonx.Unmarshal(field["source"], &source) != nil || jsonx.Unmarshal(field["propertySource"], &property) != nil || !strings.HasPrefix(pointer, "/") {
				return nil, true
			}
		}
	}
	return forms, false
}
