package apidesign

import (
	"context"
	"database/sql"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

type Change struct {
	Pointer     string `json:"pointer"`
	Kind        string `json:"kind"`
	Before      any    `json:"before,omitempty"`
	After       any    `json:"after,omitempty"`
	Impact      string `json:"impact"`
	Description string `json:"description"`
}
type Diff struct {
	From    Revision `json:"from"`
	To      Revision `json:"to"`
	Changes []Change `json:"changes"`
}

func (r *Repo) Diff(ctx context.Context, id, fromID, toID int64) (*Diff, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	d, err := getDesign(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if fromID == 0 {
		fromID, err = baseline(ctx, tx, d)
		if err != nil {
			return nil, err
		}
	}
	if toID == 0 {
		toID = d.DraftRevisionID
	}
	from, err := getRevision(ctx, tx, id, fromID)
	if err != nil {
		return nil, err
	}
	to, err := getRevision(ctx, tx, id, toID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	a, err := decodeDocument(from.Document)
	if err != nil {
		return nil, err
	}
	b, err := decodeDocument(to.Document)
	if err != nil {
		return nil, err
	}
	changes := []Change{}
	diffValue("", a, b, &changes)
	return &Diff{From: from, To: to, Changes: changes}, nil
}
func diffValue(pointer string, a, b any, out *[]Change) {
	if reflect.DeepEqual(a, b) {
		return
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := maps.Clone(am)
		maps.Copy(keys, bm)
		for _, key := range slices.Sorted(maps.Keys(keys)) {
			av, ae := am[key]
			bv, be := bm[key]
			p := pointer + "/" + escape(key)
			switch {
			case !ae:
				appendChange(p, "added", nil, bv, out)
			case !be:
				appendChange(p, "removed", av, nil, out)
			default:
				diffValue(p, av, bv, out)
			}
		}
		return
	}
	aa, aok := a.([]any)
	ba, bok := b.([]any)
	if aok && bok {
		for i := 0; i < max(len(aa), len(ba)); i++ {
			p := pointer + "/" + strconv.Itoa(i)
			switch {
			case i >= len(aa):
				appendChange(p, "added", nil, ba[i], out)
			case i >= len(ba):
				appendChange(p, "removed", aa[i], nil, out)
			default:
				diffValue(p, aa[i], ba[i], out)
			}
		}
		return
	}
	appendChange(pointer, "changed", a, b, out)
}
func appendChange(pointer, kind string, a, b any, out *[]Change) {
	impact := changeImpact(pointer, kind, a, b)
	if a == nil && kind != "added" {
		a = jsonx.RawMessage("null")
	}
	if b == nil && kind != "removed" {
		b = jsonx.RawMessage("null")
	}
	description := map[string]string{"added": "Добавлено", "removed": "Удалено", "changed": "Изменено"}[kind] + ": " + pointer
	*out = append(*out, Change{Pointer: pointer, Kind: kind, Before: a, After: b, Impact: impact, Description: description})
}

func changeImpact(pointer, kind string, a, b any) string {
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for i, token := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		if strings.HasPrefix(parts[i], "x-") {
			return "review"
		}
	}
	if len(parts) >= 2 && parts[0] == "paths" && strings.HasPrefix(parts[1], "/") &&
		(len(parts) == 2 || (len(parts) == 3 && methods[parts[2]])) {
		switch kind {
		case "removed":
			return "breaking"
		case "added":
			return "compatible"
		}
	}
	if location, tail := inputLocation(parts); location != "" {
		if impact := inputImpact(location, tail, kind, a, b); impact != "" {
			return impact
		}
	}
	if descriptiveChange(parts, a, b) {
		return "compatible"
	}
	return "review"
}

func descriptiveChange(parts []string, a, b any) bool {
	textOrAbsent := func(value any) bool {
		_, text := value.(string)
		return text || value == nil
	}
	leaf := parts[len(parts)-1]
	if (leaf == "description" || leaf == "summary") && textOrAbsent(a) && textOrAbsent(b) {
		return true
	}
	if len(parts) >= 2 && parts[0] == "info" && textOrAbsent(a) && textOrAbsent(b) {
		return true
	}
	// Named maps contain arbitrary schema/property/header names. A schema named
	// "example" is contract structure, not the example keyword of its parent.
	return leaf == "example" && len(parts) > 1 && !namedObjectMap(parts[len(parts)-2])
}

// Return only direct input locations. Shared schemas and response schemas may
// have opposite compatibility directions and deliberately remain "review".
func inputLocation(parts []string) (string, []string) {
	if len(parts) >= 3 && parts[0] == "components" {
		switch parts[1] {
		case "parameters":
			return "parameter", parts[3:]
		case "requestBodies":
			return "body", parts[3:]
		}
	}
	if len(parts) < 3 || parts[0] != "paths" {
		return "", nil
	}
	tail := parts[2:]
	if methods[tail[0]] {
		tail = tail[1:]
	}
	if len(tail) == 0 {
		return "", nil
	}
	switch tail[0] {
	case "parameters":
		if len(tail) == 1 {
			return "parameters", nil
		}
		if _, err := strconv.Atoi(tail[1]); err == nil {
			return "parameter", tail[2:]
		}
	case "requestBody":
		return "body", tail[1:]
	}
	return "", nil
}

func inputImpact(location string, tail []string, kind string, a, b any) string {
	if location == "parameters" && kind == "added" {
		return addedParameterListImpact(b)
	}
	if len(tail) == 0 && location != "parameters" {
		return inputObjectImpact(location, kind, a, b)
	}
	if len(tail) == 1 && tail[0] == "required" {
		if b == true {
			return "breaking"
		}
		if a == true && (b == false || b == nil) {
			return "compatible"
		}
	}
	if len(tail) > 0 && tail[0] == "schema" {
		return inputSchemaRequiredImpact(tail[1:], kind, a, b)
	}
	if len(tail) > 2 && tail[0] == "content" && tail[2] == "schema" {
		return inputSchemaRequiredImpact(tail[3:], kind, a, b)
	}
	return ""
}

func addedParameterListImpact(value any) string {
	if list, ok := value.([]any); ok {
		for _, parameter := range list {
			if object, ok := parameter.(map[string]any); ok && object["required"] == true {
				return "breaking"
			}
		}
	}
	return ""
}

func inputObjectImpact(location, kind string, a, b any) string {
	before, _ := a.(map[string]any)
	after, ok := b.(map[string]any)
	if ok && after["required"] == true && before["required"] != true {
		return "breaking"
	}
	if ok && location == "parameter" && kind == "added" && after["$ref"] == nil {
		return "compatible"
	}
	return ""
}

func inputSchemaRequiredImpact(parts []string, kind string, a, b any) string {
	if len(parts) == 0 {
		return ""
	}
	if parts[len(parts)-1] == "required" {
		if !directInputSchema(parts[:len(parts)-1]) {
			return ""
		}
		if required, ok := b.([]any); ok && len(required) > 0 && kind == "added" {
			return "breaking"
		}
		if _, ok := a.([]any); ok && kind == "removed" {
			return "compatible"
		}
	}
	if len(parts) >= 2 && parts[len(parts)-2] == "required" {
		if !directInputSchema(parts[:len(parts)-2]) {
			return ""
		}
		if _, err := strconv.Atoi(parts[len(parts)-1]); err != nil {
			return ""
		}
		if _, ok := b.(string); ok && (kind == "added" || kind == "changed") {
			return "breaking"
		}
		if _, ok := a.(string); ok && kind == "removed" {
			return "compatible"
		}
	}
	return ""
}

func directInputSchema(parts []string) bool {
	for len(parts) > 0 {
		switch parts[0] {
		case "properties", "patternProperties":
			if len(parts) < 2 {
				return false
			}
			parts = parts[2:]
		case "items", "additionalProperties":
			parts = parts[1:]
		default:
			// Negation, composition, and conditional schemas need human analysis;
			// removing required under `not`, for example, can reject more inputs.
			return false
		}
	}
	return true
}
