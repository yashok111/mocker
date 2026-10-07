package responserules

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

type fixtureEntities struct {
	families []EntityFixture
	outer    []string
	seq      map[string]int64
}

func fixturePath(request Request) (map[string]string, error) {
	if len(request.Path) > 100 {
		return nil, invalid("/path", "допустимо до 100 параметров пути")
	}
	values := map[string]string{}
	for i, f := range request.Path {
		if f.Name == "" || !textBound(f.Name, 256) || !textBound(f.Value, 4096) {
			return nil, invalid(fmt.Sprintf("/path/%d", i), "недопустимый параметр пути")
		}
		if _, ok := values[f.Name]; ok {
			return nil, invalid(fmt.Sprintf("/path/%d/name", i), "повторяющийся параметр пути")
		}
		values[f.Name] = f.Value
	}
	return values, nil
}
func newFixtureEntities(ctx context.Context, request Request, binding *Binding) (*fixtureEntities, error) {
	path, err := fixturePath(request)
	if err != nil {
		return nil, err
	}
	host := &fixtureEntities{seq: map[string]int64{}, families: make([]EntityFixture, 0, len(request.Entities))}
	if len(request.Entities) > 100 {
		return nil, invalid("/entities", "допустимо до 100 семейств")
	}
	host.outer = fixtureOuter(binding, path)
	seenFamilies := map[string]bool{}
	totalRows := 0
	for i, f := range request.Entities {
		prefix := fmt.Sprintf("/entities/%d", i)
		if !checkEntityFamily(f.Family) || seenFamilies[f.Family] {
			return nil, invalid(prefix+"/family", "недопустимое или повторяющееся семейство")
		}
		seenFamilies[f.Family] = true
		if f.IDField == "" || !textBound(f.IDField, 256) || f.IDType != "integer" && f.IDType != "string" {
			return nil, invalid(prefix, "требуются idField и idType integer/string")
		}
		if f.Rows == nil || len(f.Rows) > 100 {
			return nil, invalid(prefix+"/rows", "ожидается массив до 100 записей")
		}
		totalRows += len(f.Rows)
		if totalRows > 100 {
			return nil, invalid("/entities", "допустимо до 100 записей всего")
		}
		fixtureCopy, seq, err := normalizeFixtureRows(ctx, f, prefix)
		if err != nil {
			return nil, err
		}
		host.seq[f.Family] = seq
		host.families = append(host.families, fixtureCopy)
	}
	return host, nil
}
func validateFixtureKey(key, idType string) error {
	if len(key) == 0 || len(key) > 128 || key == "." || key == ".." {
		return invalid("", "недопустимый ключ сущности")
	}
	for _, c := range key {
		if !asciiAlnum(c) && !strings.ContainsRune("._~-", c) {
			return invalid("", "недопустимый ключ сущности")
		}
	}
	if idType == "integer" {
		n, err := strconv.ParseInt(key, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != key {
			return invalid("", "ключ не соответствует integer")
		}
	}
	return nil
}
func fixtureID(key, idType string) any {
	if idType == "integer" {
		return jsonx.Number(key)
	}
	return key
}
func (h *fixtureEntities) target(target EntityTarget) (*EntityFixture, []string, error) {
	for i := range h.families {
		f := &h.families[i]
		if f.Family != target.Family {
			continue
		}
		depth := strings.Count(f.Family, "{}")
		scope := target.Scope
		if scope == nil {
			if len(h.outer) < depth {
				return nil, nil, invalid("", "bad_scope")
			}
			scope = h.outer[:depth]
		}
		if len(scope) != depth {
			return nil, nil, invalid("", "bad_scope")
		}
		return f, scope, nil
	}
	return nil, nil, invalid("", "unknown_family")
}
func (h *fixtureEntities) Read(ctx context.Context, target EntityTarget, key *string) (EntityResult, error) {
	f, scope, err := h.target(target)
	if err != nil {
		return EntityResult{}, err
	}
	if key != nil {
		if err := validateFixtureKey(*key, f.IDType); err != nil {
			return EntityResult{}, err
		}
	}
	values := []any{}
	for _, row := range f.Rows {
		if !slices.Equal(row.Scope, scope) || key != nil && row.Key != *key {
			continue
		}
		value, err := decodeBody(ctx, row.DataJSON)
		if err != nil {
			return EntityResult{}, err
		}
		if key != nil {
			return EntityResult{Found: true, Value: value}, nil
		}
		values = append(values, value)
	}
	if key == nil {
		return EntityResult{Found: true, Value: values}, nil
	}
	return EntityResult{Found: false, Value: nil}, nil
}
func (h *fixtureEntities) Create(ctx context.Context, target EntityTarget, data map[string]any) (EntityResult, error) {
	f, scope, err := h.target(target)
	if err != nil {
		return EntityResult{}, err
	}
	total := 0
	for _, family := range h.families {
		total += len(family.Rows)
	}
	if total >= 100 {
		return EntityResult{}, invalid("", "entity_limit")
	}
	seq := h.seq[f.Family]
	if seq == math.MaxInt64 {
		return EntityResult{}, invalid("", "entity_limit")
	}
	seq++
	key := strconv.FormatInt(seq, 10)
	data[f.IDField] = fixtureID(key, f.IDType)
	raw, err := jsonx.Marshal(data)
	if err != nil {
		return EntityResult{}, err
	}
	value, err := decodeBody(ctx, string(raw))
	if err != nil {
		return EntityResult{}, err
	}
	h.seq[f.Family] = seq
	f.Rows = append(f.Rows, EntityFixtureRow{Key: key, Scope: append([]string{}, scope...), DataJSON: string(raw)})
	return EntityResult{Found: true, Value: value}, nil
}
func (h *fixtureEntities) Update(ctx context.Context, target EntityTarget, key string, patch map[string]any) (EntityResult, error) {
	f, scope, err := h.target(target)
	if err != nil {
		return EntityResult{}, err
	}
	if err := validateFixtureKey(key, f.IDType); err != nil {
		return EntityResult{}, err
	}
	for i, row := range f.Rows {
		if row.Key != key || !slices.Equal(row.Scope, scope) {
			continue
		}
		value, err := decodeBody(ctx, row.DataJSON)
		if err != nil {
			return EntityResult{}, err
		}
		data := value.(map[string]any)
		for name, value := range patch {
			data[name] = value
		}
		data[f.IDField] = fixtureID(key, f.IDType)
		raw, err := jsonx.Marshal(data)
		if err != nil {
			return EntityResult{}, err
		}
		if _, err := decodeBody(ctx, string(raw)); err != nil {
			return EntityResult{}, err
		}
		f.Rows[i].DataJSON = string(raw)
		return EntityResult{Found: true, Value: data}, nil
	}
	return EntityResult{Found: false, Value: nil}, nil
}

// Keep all declared path parameters, including a parent detail identity.
// target selects the prefix required by the requested family's scope depth.
func fixtureOuter(binding *Binding, path map[string]string) []string {
	var outer []string
	if binding != nil {
		for segment := range strings.SplitSeq(binding.Path, "/") {
			if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
				value, ok := path[strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")]
				if !ok {
					break
				}
				outer = append(outer, value)
			}
		}
	}

	return outer
}
func normalizeFixtureRows(ctx context.Context, f EntityFixture, prefix string) (EntityFixture, int64, error) {
	var seq int64
	fixtureCopy := EntityFixture{Family: f.Family, IDField: f.IDField, IDType: f.IDType, Rows: make([]EntityFixtureRow, 0, len(f.Rows))}
	for j, row := range f.Rows {
		pointer := fmt.Sprintf("%s/rows/%d", prefix, j)
		if err := validateFixtureKey(row.Key, f.IDType); err != nil {
			return EntityFixture{}, 0, at(pointer+"/key", err)
		}
		if row.Scope == nil || len(row.Scope) != strings.Count(f.Family, "{}") {
			return EntityFixture{}, 0, invalid(pointer+"/scope", "scope должен соответствовать глубине семейства")
		}
		for _, value := range row.Scope {
			if !textBound(value, 4096) {
				return EntityFixture{}, 0, invalid(pointer+"/scope", "слишком длинное значение scope")
			}
		}
		for _, old := range fixtureCopy.Rows {
			if old.Key == row.Key && slices.Equal(old.Scope, row.Scope) {
				return EntityFixture{}, 0, invalid(pointer, "повторяющаяся запись")
			}
		}
		value, err := decodeBody(ctx, row.DataJSON)
		if err != nil {
			return EntityFixture{}, 0, at(pointer+"/dataJSON", err)
		}
		data, ok := value.(map[string]any)
		if !ok {
			return EntityFixture{}, 0, invalid(pointer+"/dataJSON", "данные записи должны быть объектом")
		}
		data[f.IDField] = fixtureID(row.Key, f.IDType)
		raw, err := jsonx.Marshal(data)
		if err != nil {
			return EntityFixture{}, 0, err
		}
		fixtureCopy.Rows = append(fixtureCopy.Rows, EntityFixtureRow{Key: row.Key, Scope: slices.Clone(row.Scope), DataJSON: string(raw)})
		if n, err := strconv.ParseInt(row.Key, 10, 64); err == nil && strconv.FormatInt(n, 10) == row.Key && n > seq {
			seq = n
		}
	}

	return fixtureCopy, seq, nil
}
