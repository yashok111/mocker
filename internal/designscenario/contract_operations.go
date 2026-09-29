package designscenario

import (
	"maps"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/schemamodel"
)

// ContractOperation separates a concrete path's identity from its shared source.
// Item is a read-only effective Path Item; Operation still aliases the source.
type ContractOperation struct {
	Key, Method, Path, Pointer string
	Item, Operation            map[string]any
	Uncertain                  bool
	keyPointer                 string
	keyPresent, invalidKey     bool
}

// ContractOperations projects local Path Item references without changing the
// pinned snapshot. Parameter entries are ordered from farthest to nearest so
// existing consumers can apply their normal (in, name) override behavior.
func ContractOperations(root map[string]any) []ContractOperation {
	operations, _ := contractOperations(root)
	return operations
}

func contractOperations(root map[string]any) ([]ContractOperation, []Diagnostic) {
	paths := object(root["paths"])
	var result []ContractOperation
	var warnings []Diagnostic
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		if !strings.HasPrefix(path, "/") {
			continue
		}
		pointer := "/paths/" + escapePointer(path)
		nodes, diagnostics := schemamodel.PathItems(root, paths[path], pointer)
		uncertain := false
		for _, diagnostic := range diagnostics {
			// Nearest-method precedence resolves this conflict completely;
			// it does not make the chosen operation's schema unknown.
			if diagnostic.Code == "path_item_method_conflict" {
				continue
			}
			uncertain = true
			warnings = append(warnings, Diagnostic{Pointer: diagnostic.Pointer, Message: diagnostic.Message, Severity: "warning"})
		}
		item := map[string]any{}
		var parameters []any
		for i := len(nodes) - 1; i >= 0; i-- {
			maps.Copy(item, nodes[i].Value)
			entries, _ := nodes[i].Value["parameters"].([]any)
			parameters = append(parameters, entries...)
		}
		delete(item, "$ref")
		if len(parameters) > 0 {
			item["parameters"] = parameters
		}
		methods := map[string]schemamodel.PathItemOperation{}
		for _, operation := range schemamodel.PathItemOperations(nodes) {
			methods[operation.Method] = operation
		}
		for _, method := range operationMethods {
			source, exists := methods[method]
			op := object(source.Value)
			if !exists || op == nil {
				continue
			}
			keyValue, present := op[apidesign.OperationKey]
			keyPointer := source.Pointer + "/" + apidesign.OperationKey
			invalidAliases := false
			if source.Pointer != pointer+"/"+method {
				if aliasesValue, hasAliases := object(paths[path])[apidesign.PathOperationKeys]; hasAliases {
					aliases, valid := aliasesValue.(map[string]any)
					if !valid {
						keyValue, present, invalidAliases = nil, true, true
						keyPointer = pointer + "/" + apidesign.PathOperationKeys
					} else if alias, hasAlias := aliases[method]; hasAlias {
						keyValue, present = alias, true
						keyPointer = pointer + "/" + apidesign.PathOperationKeys + "/" + method
					}
				}
			}
			key, validKey := keyValue.(string)
			invalidKey := present && (!validKey || strings.TrimSpace(key) == "" || invalidAliases)
			if invalidKey {
				key = ""
			}
			result = append(result, ContractOperation{
				Key: key, Method: method, Path: path, Pointer: source.Pointer,
				Item: item, Operation: op, Uncertain: uncertain,
				keyPointer: keyPointer, keyPresent: present, invalidKey: invalidKey,
			})
		}
	}
	return result, warnings
}
