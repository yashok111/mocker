package apidesign

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"slices"
	"strings"
	"uuid"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/schemamodel"
)

// OperationKey identifies an authored operation across method/path changes.
const OperationKey = "x-mocker-canvas-operation-id"

// PathOperationKeys stores identities of inherited methods on their consumer.
const PathOperationKeys = "x-mocker-canvas-operation-ids"

type authoredOperation struct {
	pointer string
	value   map[string]any
	method  string
}

func (o authoredOperation) key() (any, bool) {
	if o.method == "" {
		value, exists := o.value[OperationKey]
		return value, exists
	}
	keys, _ := o.value[PathOperationKeys].(map[string]any)
	value, exists := keys[o.method]
	return value, exists
}

func (o authoredOperation) keyPointer() string {
	if o.method == "" {
		return o.pointer + "/" + OperationKey
	}
	path, _, _ := strings.CutLast(o.pointer, "/")
	return path + "/" + PathOperationKeys + "/" + o.method
}

func (o authoredOperation) setKey(key string) {
	if o.method == "" {
		o.value[OperationKey] = key
		return
	}
	keys, ok := o.value[PathOperationKeys].(map[string]any)
	if !ok {
		keys = map[string]any{}
		o.value[PathOperationKeys] = keys
	}
	keys[o.method] = key
}

func authoredOperations(root map[string]any) ([]authoredOperation, error) {
	paths, _ := root["paths"].(map[string]any)
	out := []authoredOperation{}
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		if strings.HasPrefix(path, "x-") {
			continue
		}
		item, _ := paths[path].(map[string]any)
		pointer := "/paths/" + escape(path)
		if raw, exists := item[PathOperationKeys]; exists {
			keys, ok := raw.(map[string]any)
			if !ok {
				return nil, invalidField(pointer+"/"+PathOperationKeys, "Ожидается объект ключей операций пути")
			}
			for method, value := range keys {
				key, ok := value.(string)
				if !methods[method] || !ok || strings.TrimSpace(key) == "" || len(key) > 200 {
					return nil, invalidField(pointer+"/"+PathOperationKeys+"/"+escape(method), "Ожидается HTTP-метод и непустой ключ до 200 байт")
				}
			}
		}
		nodes, _ := schemamodel.PathItems(root, item, pointer)
		for _, method := range schemamodel.PathItemOperations(nodes) {
			operation, ok := method.Value.(map[string]any)
			if !ok {
				continue
			}
			at := pointer + "/" + method.Method
			if method.Pointer == at {
				out = append(out, authoredOperation{pointer: at, value: operation})
			} else {
				out = append(out, authoredOperation{pointer: at, value: item, method: method.Method})
			}
		}
	}
	return out, nil
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
		operations, err := authoredOperations(old)
		if err != nil {
			return "", err
		}
		for _, operation := range operations {
			value, _ := operation.key()
			oldKeys[operation.pointer], _ = value.(string)
		}
	}
	operations, err := authoredOperations(root)
	if err != nil {
		return "", err
	}
	used, err := operationKeys(operations)
	if err != nil {
		return "", err
	}
	for _, operation := range operations {
		if _, exists := operation.key(); exists {
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
			return "", invalidField(operation.keyPointer(), "Ключ операции уже используется")
		}
		operation.setKey(key)
		used[key] = true
	}
	canonical, err := jsonx.MarshalIndent(root, "", "  ")
	return string(canonical), err
}

func operationKeys(operations []authoredOperation) (map[string]bool, error) {
	used := map[string]bool{}
	for _, operation := range operations {
		if value, exists := operation.key(); exists {
			key, ok := value.(string)
			if !ok || strings.TrimSpace(key) == "" || len(key) > 200 {
				return nil, invalidField(operation.keyPointer(), "Ожидается непустой ключ операции длиной до 200 байт")
			}
			if used[key] {
				return nil, invalidField(operation.keyPointer(), "Ключ операции уже используется")
			}
			used[key] = true
		}
	}
	return used, nil
}
