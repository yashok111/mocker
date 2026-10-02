package schemamodel

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ResolveAuthoredPointer resolves a strict, bounded RFC6901 pointer. URI
// fragments and noncanonical array indices are intentionally unsupported.
func ResolveAuthoredPointer(root map[string]any, pointer string) (any, bool, error) {
	if !utf8.ValidString(pointer) || len(pointer) > 2048 || (pointer != "" && !strings.HasPrefix(pointer, "/")) {
		return nil, false, fail(pointer, "Недопустимый JSON Pointer")
	}
	if pointer == "" {
		return root, true, nil
	}
	tokens := strings.Split(pointer[1:], "/")
	if len(tokens) > 64 {
		return nil, false, fail(pointer, "Слишком много сегментов JSON Pointer")
	}
	for _, token := range tokens {
		for i := 0; i < len(token); i++ {
			if token[i] == '~' {
				if i+1 >= len(token) || (token[i+1] != '0' && token[i+1] != '1') {
					return nil, false, fail(pointer, "Недопустимая escape-последовательность JSON Pointer")
				}
				i++
			}
		}
	}
	var value any = root
	for _, token := range tokens {
		key := unescape(token)
		switch node := value.(type) {
		case map[string]any:
			var exists bool
			value, exists = node[key]
			if !exists {
				return nil, false, nil
			}
		case []any:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || strconv.Itoa(index) != key {
				return nil, false, fail(pointer, "Неканонический индекс массива")
			}
			if index >= len(node) {
				return nil, false, nil
			}
			value = node[index]
		default:
			return nil, false, nil
		}
	}
	return value, true, nil
}

// IsSchemaPosition admits authored objects and boolean schemas using the same
// vocabulary traversal as VisitReferences; it never follows schema references.
func IsSchemaPosition(ctx context.Context, root map[string]any, pointer string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	value, exists, err := ResolveAuthoredPointer(root, pointer)
	if err != nil || !exists {
		return false, err
	}
	switch value.(type) {
	case map[string]any, bool:
	default:
		return false, nil
	}
	found := false
	w := traversal{ctx: ctx, schemaEntry: func(at string, _ any) {
		if at == pointer {
			found = true
		}
	}}
	if err := w.walk(root, "", "root", "", "", 0); err != nil {
		return false, err
	}
	return found, nil
}
