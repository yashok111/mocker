package schemamodel

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// DependencyReference describes an authored structural reference and its scope.
// Unsupported references never create an edge to a coincidentally local pointer.
type DependencyReference struct {
	Pointer, TargetPointer, RawRef, EffectiveRef, Kind string
	ObjectKind                                         string
	Supported                                          bool
}
type DependencyDiagnostic struct{ Code, Pointer, Message string }
type DependencyIndex struct {
	References      []DependencyReference
	PathItems       []string
	Diagnostics     []DependencyDiagnostic
	Truncated       bool
	TruncatedReason string
	Visits          int
}

type dependencyWalker struct {
	ctx       context.Context
	root      map[string]any
	maxSites  int
	maxVisits int
	index     DependencyIndex
}

// DependencyReferences visits only OpenAPI and JSON Schema vocabulary. It keeps
// useful local edges when other references are invalid and never fetches a URI.
func DependencyReferences(ctx context.Context, root map[string]any, maxSites int) (DependencyIndex, error) {
	return DependencyReferencesWithinBudget(ctx, root, maxSites, maxNodes)
}

// DependencyReferencesWithinBudget shares a caller's traversal budget across
// multiple documents. The returned Visits counts the work actually performed.
func DependencyReferencesWithinBudget(ctx context.Context, root map[string]any, maxSites, maxVisits int) (DependencyIndex, error) {
	w := dependencyWalker{ctx: ctx, root: root, maxSites: maxSites, maxVisits: maxVisits, index: DependencyIndex{
		References: []DependencyReference{}, PathItems: []string{}, Diagnostics: []DependencyDiagnostic{},
	}}
	err := w.walk(root, "", "root", "", 0)
	return w.index, err
}

func (w *dependencyWalker) diagnostic(code, pointer, message string) {
	if len(w.index.Diagnostics) < max(1, w.maxSites) {
		w.index.Diagnostics = append(w.index.Diagnostics, DependencyDiagnostic{Code: code, Pointer: pointer, Message: message})
	}
}

func (w *dependencyWalker) reference(m map[string]any, key, pointer, kind, resource string) {
	raw, exists := m[key]
	if !exists {
		return
	}
	if len(w.index.References) >= w.maxSites {
		w.index.Truncated = true
		w.index.TruncatedReason = "references"
		return
	}
	ref, valid := raw.(string)
	site := referenceSite{ref: ref, resource: resource, bare: kind == "discriminator" && !strings.ContainsAny(ref, "/#:")}
	result := DependencyReference{Pointer: pointer, RawRef: ref, EffectiveRef: site.effectiveRef(), Kind: "ref", ObjectKind: kind}
	if key == "$dynamicRef" {
		result.Kind = "dynamicRef"
	}
	if kind == "discriminator" {
		result.Kind = "discriminator"
	}
	code, message := "", ""
	switch {
	case !valid:
		code, message = "invalid_reference", "Ссылка должна быть строкой"
	case key == "$dynamicRef":
		code, message = "unsupported_dynamic_reference", "Динамическая ссылка требует отдельного разрешения"
	case resource != "":
		code, message = "unsupported_reference_scope", "Ссылка находится в области $id; локальная зависимость не доказана"
	default:
		target := localPointer(result.EffectiveRef)
		if result.EffectiveRef == "#" {
			target = ""
		}
		if target == "" && result.EffectiveRef != "#" {
			code, message = "unsupported_reference", "Внешняя ссылка или anchor не разрешаются этим анализом"
		} else {
			value, found := resolveFound(w.root, target)
			if target == "" {
				found = true
				value = w.root
			}
			if !dependencyValidPointer(target) {
				code, message = "invalid_reference_pointer", "Некорректный JSON Pointer ссылки"
			} else if !found {
				code, message = "missing_reference", "Цель локальной ссылки не найдена"
			} else if !dependencyValidTarget(value, kind) {
				code, message = "invalid_reference_target", "Цель ссылки имеет неподдерживаемый тип"
			} else {
				result.TargetPointer, result.Supported = target, true
			}
		}
	}
	w.index.References = append(w.index.References, result)
	if code != "" {
		w.diagnostic(code, pointer, message)
	}
}

func (w *dependencyWalker) walk(value any, pointer, kind, resource string, depth int) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if w.index.Truncated {
		return nil
	}
	w.index.Visits++
	if depth > maxDepth || w.index.Visits > w.maxVisits {
		w.index.Truncated = true
		w.index.TruncatedReason = "traversal"
		w.diagnostic("dependency_traversal_limit", pointer, "Достигнут предел обхода зависимостей")
		return nil
	}
	if kind == "path" {
		w.index.PathItems = append(w.index.PathItems, pointer)
	}
	m := object(value)
	if m == nil {
		if _, boolean := value.(bool); boolean && kind == "schema" {
			return nil
		}
		w.diagnostic("invalid_contract_node", pointer, "Структурный узел контракта имеет неподдерживаемый тип")
		return nil
	}
	if kind == "schema" {
		if id, ok := m["$id"].(string); ok {
			resource = resolveURI(resource, id)
		}
	}
	if kind != "root" && kind != "components" && kind != "discriminator" {
		w.reference(m, "$ref", pointer+"/$ref", kind, resource)
	}
	if kind == "schema" {
		w.reference(m, "$dynamicRef", pointer+"/$dynamicRef", kind, resource)
	}
	if kind == "discriminator" {
		mapping := object(m["mapping"])
		for _, key := range slices.Sorted(maps.Keys(mapping)) {
			w.reference(mapping, key, pointer+"/mapping/"+escape(key), kind, resource)
		}
		return nil
	}
	for _, key := range slices.Sorted(maps.Keys(m)) {
		if w.index.Truncated {
			break
		}
		rule := childRules[kind][key]
		if kind == "path" && isMethod(key) {
			rule = childRule{kind: "operation"}
		}
		if kind == "callback" && key != "$ref" && !strings.HasPrefix(key, "x-") {
			rule = childRule{kind: "path"}
		}
		if rule.kind == "" {
			continue
		}
		at := pointer + "/" + escape(key)
		switch rule.mode {
		case "map":
			child := object(m[key])
			if child == nil {
				w.diagnostic("invalid_contract_node", at, "Ожидается объект именованных элементов")
				continue
			}
			for _, name := range slices.Sorted(maps.Keys(child)) {
				if strings.HasPrefix(name, "x-") && ((kind == "root" && key == "paths") || (kind == "operation" && key == "responses")) {
					continue
				}
				if kind == "schema" && key == "dependencies" {
					if _, propertyDependency := child[name].([]any); propertyDependency {
						continue
					}
				}
				if err := w.walk(child[name], at+"/"+escape(name), rule.kind, resource, depth+1); err != nil {
					return err
				}
				if w.index.Truncated {
					break
				}
			}
		case "array", "items":
			values, ok := m[key].([]any)
			if ok {
				for i, v := range values {
					if err := w.walk(v, at+"/"+strconv.Itoa(i), rule.kind, resource, depth+1); err != nil {
						return err
					}
					if w.index.Truncated {
						break
					}
				}
			} else if rule.mode == "array" {
				w.diagnostic("invalid_contract_node", at, "Ожидается массив структурных элементов")
			} else if rule.mode == "items" {
				if err := w.walk(m[key], at, rule.kind, resource, depth+1); err != nil {
					return err
				}
			}
		default:
			if err := w.walk(m[key], at, rule.kind, resource, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func dependencyValidPointer(pointer string) bool {
	for i := 0; i < len(pointer); i++ {
		if pointer[i] == '~' {
			i++
			if i >= len(pointer) || (pointer[i] != '0' && pointer[i] != '1') {
				return false
			}
		}
	}
	return true
}
func dependencyValidTarget(value any, kind string) bool {
	if _, ok := value.(map[string]any); ok {
		return true
	}
	_, boolean := value.(bool)
	return boolean && (kind == "schema" || kind == "discriminator")
}
