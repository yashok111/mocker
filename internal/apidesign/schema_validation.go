package apidesign

import (
	"strconv"
	"strings"
)

// Validate schema objects in their OpenAPI locations, leaving example values and
// vendor extensions untouched. This catches malformed authored shapes before the
// runtime's intentionally tolerant importer can degrade them silently.
func validateSchemaLocations(value any, pointer string, add diagnosticSink) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			p := pointer + "/" + escape(key)
			switch {
			case key == "example" || key == "examples" || strings.HasPrefix(key, "x-"):
				continue
			case key == "schema":
				validateSchema(child, p, add)
			case pointer == "/components" && key == "schemas":
				schemas, ok := child.(map[string]any)
				if !ok {
					add(p, "Ожидается объект schemas")
					continue
				}
				for name, schema := range schemas {
					validateSchema(schema, p+"/"+escape(name), add)
				}
			default:
				validateSchemaLocations(child, p, add)
			}
		}
	case []any:
		for i, child := range value {
			validateSchemaLocations(child, pointer+"/"+strconv.Itoa(i), add)
		}
	}
}

func validateSchema(value any, pointer string, add diagnosticSink) {
	if _, ok := value.(bool); ok {
		return
	}
	schema, ok := value.(map[string]any)
	if !ok {
		add(pointer, "Схема должна быть объектом или boolean")
		return
	}
	if typ, exists := schema["type"]; exists {
		validateSchemaType(typ, pointer+"/type", add)
	}
	if required, exists := schema["required"]; exists {
		names, ok := required.([]any)
		if !ok {
			add(pointer+"/required", "Ожидается массив строк")
		} else {
			seen := map[string]bool{}
			for _, name := range names {
				text, ok := name.(string)
				if !ok || seen[text] {
					add(pointer+"/required", "Имена обязательных свойств должны быть уникальными строками")
				}
				seen[text] = true
			}
		}
	}
	for _, key := range []string{"properties", "patternProperties", "$defs", "dependentSchemas"} {
		if raw, exists := schema[key]; exists {
			children, ok := raw.(map[string]any)
			if !ok {
				add(pointer+"/"+key, "Ожидается объект схем")
				continue
			}
			for name, child := range children {
				validateSchema(child, pointer+"/"+key+"/"+escape(name), add)
			}
		}
	}
	validateSchemaChildren(schema, pointer, add)
}
func validateSchemaChildren(schema map[string]any, pointer string, add diagnosticSink) {
	for _, key := range []string{"items", "additionalProperties", "unevaluatedProperties", "unevaluatedItems", "contains", "not", "if", "then", "else", "propertyNames"} {
		if child, exists := schema[key]; exists {
			validateSchema(child, pointer+"/"+key, add)
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if raw, exists := schema[key]; exists {
			children, ok := raw.([]any)
			if !ok || len(children) == 0 {
				add(pointer+"/"+key, "Ожидается непустой массив схем")
				continue
			}
			for i, child := range children {
				validateSchema(child, pointer+"/"+key+"/"+strconv.Itoa(i), add)
			}
		}
	}
}
func validateSchemaType(value any, pointer string, add diagnosticSink) {
	valid := func(v any) bool {
		switch v {
		case "object", "array", "string", "number", "integer", "boolean", "null":
			return true
		}
		return false
	}
	if types, ok := value.([]any); ok {
		if len(types) == 0 {
			add(pointer, "Массив типов не может быть пустым")
		}
		seen := map[string]bool{}
		for _, typ := range types {
			name, ok := typ.(string)
			if !ok || !valid(name) || seen[name] {
				add(pointer, "Неизвестный или повторяющийся тип")
			}
			seen[name] = true
		}
		return
	}
	name, ok := value.(string)
	if !ok || !valid(name) {
		add(pointer, "Неизвестный тип схемы")
	}
}
