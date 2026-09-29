package schemamodel

import (
	"maps"
	"slices"
	"strings"
)

// PathItemNode is an authored Path Item in nearest-to-farthest reference order.
// Value aliases the input document; traversal never changes it.
type PathItemNode struct {
	Pointer string
	Value   map[string]any
}

// PathItemDiagnostic describes a bounded local-reference traversal problem.
type PathItemDiagnostic struct {
	Code, Pointer, Message string
}

// PathItemOperation is the nearest authored method for one concrete path.
// Value may be invalid; callers performing validation must check its type.
type PathItemOperation struct {
	Method, Pointer string
	Value           any
}

// PathItems follows local JSON Pointer references and retains valid siblings
// even when a later reference is broken. It never fetches external documents.
func PathItems(root map[string]any, value any, pointer string) ([]PathItemNode, []PathItemDiagnostic) {
	nodes := []PathItemNode{}
	diagnostics := []PathItemDiagnostic{}
	seen := map[string]bool{}
	methods := map[string]string{}
	referenceSite := pointer
	add := func(code, at, message string) {
		diagnostics = append(diagnostics, PathItemDiagnostic{Code: code, Pointer: at, Message: message})
	}
	for {
		if seen[pointer] {
			add("path_item_ref_cycle", referenceSite, "Циклическая ссылка Path Item: "+pointer)
			break
		}
		if len(nodes) >= maxDepth {
			add("path_item_ref_depth", referenceSite, "Слишком длинная цепочка ссылок Path Item (не более 128)")
			break
		}
		seen[pointer] = true
		item := object(value)
		if item == nil {
			add("path_item_ref_invalid", referenceSite, "Ожидается объект Path Item: "+pointer)
			break
		}
		// A local pointer may resolve to any JSON object. Only accept the Path
		// Item vocabulary, wherever the author stored it; empty items are valid.
		if len(nodes) > 0 {
			for _, field := range slices.Sorted(maps.Keys(item)) {
				if !pathItemField(field) {
					add("path_item_ref_invalid", referenceSite, "Цель ссылки не является Path Item: недопустимое поле "+field)
					return nodes, diagnostics
				}
			}
		}
		nodes = append(nodes, PathItemNode{Pointer: pointer, Value: item})
		for _, method := range slices.Sorted(maps.Keys(item)) {
			if !isMethod(method) {
				continue
			}
			if source, exists := methods[method]; exists {
				add("path_item_method_conflict", pointer+"/"+method, "Метод "+strings.ToUpper(method)+" переопределён в "+source)
			} else {
				methods[method] = pointer + "/" + method
			}
		}
		raw, exists := item["$ref"]
		if !exists {
			break
		}
		referenceSite = pointer + "/$ref"
		ref, ok := raw.(string)
		if !ok {
			add("path_item_ref_invalid", referenceSite, "Ссылка Path Item должна быть строкой")
			break
		}
		if ref == "#" {
			pointer, value = "", root
			continue
		}
		pointer = localPointer(ref)
		if pointer == "" {
			add("path_item_ref_unsupported", referenceSite, "Поддерживаются только локальные JSON Pointer ссылки Path Item: "+ref)
			break
		}
		value, exists = resolveFound(root, pointer)
		if !exists {
			add("path_item_ref_missing", referenceSite, "Ссылка Path Item не найдена: "+ref)
			break
		}
	}
	return nodes, diagnostics
}

func pathItemField(field string) bool {
	if isMethod(field) || strings.HasPrefix(field, "x-") {
		return true
	}
	switch field {
	case "$ref", "summary", "description", "servers", "parameters":
		return true
	}
	return false
}

// PathItemOperations applies the established nearest-sibling precedence.
func PathItemOperations(nodes []PathItemNode) []PathItemOperation {
	methods := map[string]PathItemOperation{}
	for _, node := range nodes {
		for method, value := range node.Value {
			if _, exists := methods[method]; isMethod(method) && !exists {
				methods[method] = PathItemOperation{Method: method, Pointer: node.Pointer + "/" + method, Value: value}
			}
		}
	}
	operations := make([]PathItemOperation, 0, len(methods))
	for _, method := range slices.Sorted(maps.Keys(methods)) {
		operations = append(operations, methods[method])
	}
	return operations
}
