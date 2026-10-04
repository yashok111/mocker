package api

import (
	"bytes"
	_ "embed"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/yashok111/mocker/internal/jsonx"
)

//go:embed openapi.json
var backendContract []byte

var backendDefinitions = sync.OnceValues(func() (map[string]any, error) {
	var document struct {
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	decoder := jsonx.NewDecoder(bytes.NewReader(backendContract))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode embedded backend contract: %w", err)
	}
	return document.Components.Schemas, nil
})

// BackendSchema returns a private, reference-free copy of a local OpenAPI schema.
// Exact JSON number tokens are retained for SDK validation of int64 bounds.
func BackendSchema(name string) (map[string]any, error) {
	definitions, err := backendDefinitions()
	if err != nil {
		return nil, err
	}
	root, ok := definitions[name]
	if !ok {
		return nil, fmt.Errorf("missing backend schema %q", name)
	}
	expanded, err := expandBackendSchema(root, definitions)
	if err != nil {
		return nil, err
	}
	object, ok := expanded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("backend schema %q must be an object", name)
	}
	return object, nil
}

func expandBackendSchema(root any, definitions map[string]any) (any, error) {
	// Effective responses repeat bounded provenance for each typed record arm.
	// Keep a finite bound while allowing the complete public graph vocabulary.
	remaining := 1000000
	active := map[string]bool{}
	var expand func(any, int) (any, error)
	expand = func(value any, depth int) (any, error) {
		remaining--
		if depth > 64 || remaining < 0 {
			return nil, fmt.Errorf("backend schema expansion exceeds local bound")
		}
		switch value := value.(type) {
		case map[string]any:
			result := map[string]any{}
			if raw, ok := value["$ref"]; ok {
				reference, ok := raw.(string)
				const prefix = "#/components/schemas/"
				if !ok || !strings.HasPrefix(reference, prefix) {
					return nil, fmt.Errorf("unsupported backend schema reference %v", raw)
				}
				name := strings.TrimPrefix(reference, prefix)
				if active[name] {
					return nil, fmt.Errorf("cyclic backend schema reference %q", name)
				}
				definition, ok := definitions[name]
				if !ok {
					return nil, fmt.Errorf("missing backend schema reference %q", name)
				}
				active[name] = true
				resolved, err := expand(definition, depth+1)
				delete(active, name)
				if err != nil {
					return nil, err
				}
				var object bool
				result, object = resolved.(map[string]any)
				if !object {
					return nil, fmt.Errorf("backend schema reference %q must resolve to an object", name)
				}
			}
			keys := make([]string, 0, len(value))
			for key := range value {
				if key != "$ref" {
					keys = append(keys, key)
				}
			}
			slices.Sort(keys)
			for _, key := range keys {
				child, err := expand(value[key], depth+1)
				if err != nil {
					return nil, err
				}
				result[key] = child
			}
			return result, nil
		case []any:
			result := make([]any, len(value))
			for index, item := range value {
				child, err := expand(item, depth+1)
				if err != nil {
					return nil, err
				}
				result[index] = child
			}
			return result, nil
		default:
			return value, nil
		}
	}
	return expand(root, 0)
}
