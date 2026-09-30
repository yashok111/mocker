package responserules

import (
	"context"
	"fmt"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

func (r *ValueRef) UnmarshalJSON(data []byte) error {
	var tag struct {
		Source string `json:"source"`
	}
	// Decode the tag first, then admit only fields belonging to its source.
	if err := jsonx.Unmarshal(data, &tag); err != nil {
		return err
	}
	var v ValueRef
	fields := []wireField{required("source", &v.Source)}
	switch tag.Source {
	case "literal":
		fields = append(fields, required("valueJSON", &v.ValueJSON))
	case "path", "query", "header":
		fields = append(fields, required("name", &v.Name))
	case "body":
		fields = append(fields, optional("pointer", &v.Pointer))
	case "result":
		fields = append(fields, required("nodeId", &v.NodeID), optional("pointer", &v.Pointer))
	default:
		return invalid("/source", "неизвестный источник значения")
	}
	if err := decodeObject(data, fields...); err != nil {
		return err
	}
	if err := checkValueRef(v); err != nil {
		return err
	}
	*r = v
	return nil
}
func (e *EntityOperation) UnmarshalJSON(data []byte) error {
	var v EntityOperation
	var scope wireArray[ValueRef]
	if err := decodeObject(data, required("family", &v.Family), optional("operation", &v.Operation), optional("scope", &scope), optional("key", &v.Key), optional("data", &v.Data)); err != nil {
		return err
	}
	if scope != nil {
		refs := []ValueRef(scope)
		v.Scope = &refs
	}
	*e = v
	return nil
}
func (f *EntityFixture) UnmarshalJSON(data []byte) error {
	var v EntityFixture
	var rows wireArray[EntityFixtureRow]
	if err := decodeObject(data, required("family", &v.Family), required("idField", &v.IDField), required("idType", &v.IDType), required("rows", &rows)); err != nil {
		return err
	}
	v.Rows = rows
	*f = v
	return nil
}
func (r *EntityFixtureRow) UnmarshalJSON(data []byte) error {
	var v EntityFixtureRow
	var scope wireArray[string]
	if err := decodeObject(data, required("key", &v.Key), required("scope", &scope), required("dataJSON", &v.DataJSON)); err != nil {
		return err
	}
	v.Scope = scope
	*r = v
	return nil
}
func checkValueRef(r ValueRef) error {
	remaining := r
	remaining.Source = ""
	switch r.Source {
	case "literal":
		if r.ValueJSON == nil {
			return invalid("/valueJSON", "требуется valueJSON")
		}
		if _, err := decodeBody(context.Background(), *r.ValueJSON); err != nil {
			return at("/valueJSON", err)
		}
		remaining.ValueJSON = nil
	case "path", "query", "header":
		if r.Name == "" || !textBound(r.Name, 256) {
			return invalid("/name", "требуется имя источника до 256 байт")
		}
		remaining.Name = ""
	case "body", "result":
		if r.Source == "result" {
			if !ValidID(r.NodeID) {
				return invalid("/nodeId", "недопустимый ID результата")
			}
			remaining.NodeID = ""
		}
		if !textBound(r.Pointer, 2048) || !validValuePointer(r.Pointer) {
			return invalid("/pointer", "недопустимый JSON Pointer")
		}
		remaining.Pointer = ""
	default:
		return invalid("/source", "неизвестный источник значения")
	}
	if remaining != (ValueRef{}) {
		return invalid("", "лишние поля ссылки")
	}
	return nil
}
func validValuePointer(pointer string) bool {
	if pointer == "" {
		return true
	}
	if !strings.HasPrefix(pointer, "/") {
		return false
	}
	for i := 0; i < len(pointer); i++ {
		if pointer[i] == '~' {
			i++
			if i >= len(pointer) || pointer[i] != '0' && pointer[i] != '1' {
				return false
			}
		}
	}
	return true
}
func checkEntityFamily(family string) bool {
	if !strings.HasPrefix(family, "/") || !textBound(family, 2048) || strings.ContainsAny(family, "?#\\") || strings.HasSuffix(family, "/") && family != "/" {
		return false
	}
	for seg := range strings.SplitSeq(strings.TrimPrefix(family, "/"), "/") {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, "{}") && seg != "{}" {
			return false
		}
	}
	return strings.Count(family, "{}") <= 3
}
func checkEntityOperation(kind string, e EntityOperation) error {
	if !checkEntityFamily(e.Family) {
		return invalid("/entity/family", "укажите каноническое семейство ресурса")
	}
	if e.Scope != nil {
		if len(*e.Scope) > 3 {
			return invalid("/entity/scope", "допустимо до трёх значений scope")
		}
		for i, r := range *e.Scope {
			if err := checkValueRef(r); err != nil {
				return at(fmt.Sprintf("/entity/scope/%d", i), err)
			}
		}
	}
	switch kind {
	case "entity_read":
		if e.Operation != "get" && e.Operation != "list" || e.Data != nil || e.Operation == "get" && e.Key == nil || e.Operation == "list" && e.Key != nil {
			return invalid("/entity", "get требует key; list не принимает key или data")
		}
	case "entity_create":
		if e.Operation != "" || e.Key != nil || e.Data == nil {
			return invalid("/entity", "create требует только data")
		}
	case "entity_update":
		if e.Operation != "" || e.Key == nil || e.Data == nil {
			return invalid("/entity", "update требует key и data")
		}
	}
	if e.Key != nil {
		if err := checkValueRef(*e.Key); err != nil {
			return at("/entity/key", err)
		}
	}
	if e.Data != nil {
		if err := checkValueRef(*e.Data); err != nil {
			return at("/entity/data", err)
		}
	}
	return nil
}
