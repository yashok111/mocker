package apidesign

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"slices"
	"strings"
	"uuid"

	"github.com/yashok111/mocker/internal/jsonx"
)

// OperationKey identifies an authored operation across method/path changes.
const OperationKey = "x-mocker-canvas-operation-id"

type authoredOperation struct {
	pointer string
	value   map[string]any
}

func authoredOperations(root map[string]any) []authoredOperation {
	paths, _ := root["paths"].(map[string]any)
	out := []authoredOperation{}
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		if strings.HasPrefix(path, "x-") {
			continue
		}
		item, _ := paths[path].(map[string]any)
		for _, method := range slices.Sorted(maps.Keys(item)) {
			operation, ok := item[method].(map[string]any)
			if methods[method] && ok {
				out = append(out, authoredOperation{"/paths/" + escape(path) + "/" + method, operation})
			}
		}
	}
	return out
}

// withOperationKeys preserves explicit IDs and matches unannotated operations only
// at the same address. A rename without an ID is deliberately a new operation.
// legacyID supplies deterministic read-time keys for pre-identity revisions.
func withOperationKeys(raw, previous string, legacyID int64) (string, error) {
	root, err := decodeDocument(raw)
	if err != nil {
		return "", invalidField("", err.Error())
	}
	oldKeys := map[string]string{}
	if previous != "" {
		old, err := decodeDocument(previous)
		if err != nil {
			return "", err
		}
		for _, operation := range authoredOperations(old) {
			oldKeys[operation.pointer], _ = operation.value[OperationKey].(string)
		}
	}
	operations := authoredOperations(root)
	used, err := operationKeys(operations)
	if err != nil {
		return "", err
	}
	for _, operation := range operations {
		if _, exists := operation.value[OperationKey]; exists {
			continue
		}
		key := oldKeys[operation.pointer]
		if key == "" || used[key] {
			if legacyID > 0 {
				hash := sha256.Sum256(fmt.Appendf(nil, "%d:%s", legacyID, operation.pointer))
				key = fmt.Sprintf("legacy-%x", hash[:16])
			} else {
				key = uuid.New().String()
			}
		}
		if used[key] {
			return "", invalidField(operation.pointer+"/"+OperationKey, "Ключ операции уже используется")
		}
		operation.value[OperationKey] = key
		used[key] = true
	}
	canonical, err := jsonx.MarshalIndent(root, "", "  ")
	return string(canonical), err
}

func operationKeys(operations []authoredOperation) (map[string]bool, error) {
	used := map[string]bool{}
	for _, operation := range operations {
		if value, exists := operation.value[OperationKey]; exists {
			key, ok := value.(string)
			if !ok || strings.TrimSpace(key) == "" || len(key) > 200 {
				return nil, invalidField(operation.pointer+"/"+OperationKey, "Ожидается непустой ключ операции длиной до 200 байт")
			}
			if used[key] {
				return nil, invalidField(operation.pointer+"/"+OperationKey, "Ключ операции уже используется")
			}
			used[key] = true
		}
	}
	return used, nil
}
