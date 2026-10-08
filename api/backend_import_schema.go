package api

import "fmt"

// BackendImportRecordSchema selects a wire record arm before expanding refs;
// clients do not need every command/node/profile union just to inspect one kind.
// Session-dependent semantic requirements remain the import validator's job.
func BackendImportRecordSchema(composed bool, recordType, kind string) (map[string]any, error) {
	var name string
	switch recordType {
	case "node":
		name = "BackendNodeInput"
	case "edge":
		name = "BackendEdgeInput"
	case "evidence":
		if kind != "" {
			return nil, fmt.Errorf("evidence has no kind selector")
		}
		return BackendSchema("BackendEvidenceInput")
	default:
		return nil, fmt.Errorf("select node, edge or evidence")
	}
	if composed {
		if recordType == "node" {
			name = "BackendComposedNodeInput"
		} else {
			name = "BackendComposedEdgeInput"
		}
	}
	definitions, err := backendDefinitions()
	if err != nil {
		return nil, err
	}
	root, ok := definitions[name].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing record schema")
	}
	for _, raw := range root["oneOf"].([]any) {
		branch := raw.(map[string]any)
		properties := branch["properties"].(map[string]any)
		discriminator := properties["kind"].(map[string]any)
		matches := discriminator["const"] == kind
		if values, ok := discriminator["enum"].([]any); ok {
			for _, value := range values {
				matches = matches || value == kind
			}
		}
		if !matches {
			continue
		}
		expanded, err := expandBackendSchema(branch, definitions)
		if err != nil {
			return nil, err
		}
		result := expanded.(map[string]any)
		result["properties"].(map[string]any)["kind"] = map[string]any{"type": "string", "const": kind}
		return result, nil
	}
	return nil, fmt.Errorf("unknown %s kind", recordType)
}
