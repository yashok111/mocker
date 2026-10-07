// Package resourcemap projects OpenAPI operations into editable resource families.
package resourcemap

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"maps"
	"math"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/schemamodel"
)

const Extension = "x-mocker-resource-map"
const OperationKey = "x-mocker-canvas-operation-id"
const PathOperationKeys = "x-mocker-canvas-operation-ids"
const MaxCommands = 100
const MaxResources = 200
const MaxOperations = 1000
const MaxRelations = 500

type Resource struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Service       string   `json:"service"`
	Description   string   `json:"description"`
	OperationKeys []string `json:"operationKeys"`
	X             float64  `json:"x"`
	Y             float64  `json:"y"`
}
type ResourceView struct {
	Resource
	Inferred bool `json:"inferred"`
}
type Relation struct {
	ID             string `json:"id"`
	FromResourceID string `json:"fromResourceId"`
	ToResourceID   string `json:"toResourceId"`
	Label          string `json:"label"`
}
type Operation struct {
	Key     string   `json:"key"`
	Method  string   `json:"method"`
	Path    string   `json:"path"`
	Summary string   `json:"summary"`
	Schemas []string `json:"schemas"`
	// SourcePointer is present when the operation is authored in a referenced
	// Path Item. It is a decoded JSON Pointer, not a URI fragment.
	SourcePointer string `json:"sourcePointer,omitempty"`
}
type MapDiagnostic struct {
	Code         string `json:"code"`
	Severity     string `json:"severity"`
	Message      string `json:"message"`
	ResourceID   string `json:"resourceId,omitempty"`
	OperationKey string `json:"operationKey,omitempty"`
	RelationID   string `json:"relationId,omitempty"`
}
type Model struct {
	Resources   []ResourceView  `json:"resources"`
	Operations  []Operation     `json:"operations"`
	Relations   []Relation      `json:"relations"`
	Diagnostics []MapDiagnostic `json:"diagnostics"`
}
type Command struct {
	Kind         string    `json:"kind"`
	Resource     *Resource `json:"resource,omitempty"`
	ResourceID   string    `json:"resourceId,omitempty"`
	OperationKey string    `json:"operationKey,omitempty"`
	Relation     *Relation `json:"relation,omitempty"`
	RelationID   string    `json:"relationId,omitempty"`
	X            *float64  `json:"x,omitempty"`
	Y            *float64  `json:"y,omitempty"`
}

// MarshalJSON emits only the selected variant's fields. In particular, an
// empty resourceId is meaningful for assign_operation: it restores grouping.
func (c Command) MarshalJSON() ([]byte, error) {
	if err := validateCommand(c); err != nil {
		return nil, err
	}
	value := map[string]any{"kind": c.Kind}
	switch c.Kind {
	case "auto_layout":
	case "upsert_resource":
		value["resource"] = c.Resource
	case "remove_resource":
		value["resourceId"] = c.ResourceID
	case "assign_operation":
		value["operationKey"] = c.OperationKey
		value["resourceId"] = c.ResourceID
	case "upsert_relation":
		value["relation"] = c.Relation
	case "remove_relation":
		value["relationId"] = c.RelationID
	case "move_resource":
		value["resourceId"] = c.ResourceID
		value["x"] = c.X
		value["y"] = c.Y
	}
	return jsonx.Marshal(value)
}

type Error struct{ Pointer, Message string }

func (e *Error) Error() string    { return e.Pointer + ": " + e.Message }
func fail(p, m string) error      { return &Error{p, m} }
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func text(v any) string           { s, _ := v.(string); return s }
func escape(s string) string      { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func validID(s string) bool       { return len(s) > 0 && len(s) <= 200 && strings.TrimSpace(s) != "" }
func coordinate(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case jsonx.Number:
		var err error
		f, err = n.Float64()
		if err != nil {
			return 0, false
		}
	case float64:
		f = n
	case int:
		f = float64(n)
	default:
		return 0, false
	}
	return f, !math.IsNaN(f) && !math.IsInf(f, 0) && math.Abs(f) <= 100000
}

// UnmarshalJSON refuses fields outside the selected command variant, including
// fields inside replacement objects. HTTP and MCP share this decoder.
func (c *Command) UnmarshalJSON(data []byte) error {
	var raw map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &raw); err != nil {
		return err
	}
	var kind string
	if err := jsonx.Unmarshal(raw["kind"], &kind); err != nil {
		return err
	}
	allowed, err := commandFields(kind)
	if err != nil {
		return err
	}
	if err := checkExactFields("", raw, allowed, "Поле не относится к команде"); err != nil {
		return err
	}
	if kind == "upsert_resource" || kind == "upsert_relation" {
		field := map[bool]string{true: "resource", false: "relation"}[kind == "upsert_resource"]
		if err := checkReplacementObject(field, raw[field]); err != nil {
			return err
		}
	}
	type plain Command
	var decoded plain
	if err := jsonx.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = Command(decoded)
	return validateCommand(*c)
}

// commandFields is the exact top-level field set of each command variant.
func commandFields(kind string) (map[string]bool, error) {
	allowed := map[string]bool{"kind": true}
	switch kind {
	case "auto_layout":
	case "upsert_resource":
		allowed["resource"] = true
	case "remove_resource":
		allowed["resourceId"] = true
	case "assign_operation":
		allowed["operationKey"] = true
		allowed["resourceId"] = true
	case "upsert_relation":
		allowed["relation"] = true
	case "remove_relation":
		allowed["relationId"] = true
	case "move_resource":
		allowed["resourceId"] = true
		allowed["x"] = true
		allowed["y"] = true
	default:
		return nil, fail("/kind", "Неизвестная команда")
	}
	return allowed, nil
}

// checkExactFields requires exactly the allowed keys, none of them null, in
// that order of checks: unknown, then missing, then null.
func checkExactFields(prefix string, raw map[string]jsonx.RawMessage, allowed map[string]bool, unknownMessage string) error {
	for key := range raw {
		if !allowed[key] {
			return fail(prefix+"/"+escape(key), unknownMessage)
		}
	}
	for key := range allowed {
		if _, ok := raw[key]; !ok {
			return fail(prefix+"/"+escape(key), "Обязательное поле отсутствует")
		}
	}
	for key, value := range raw {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fail(prefix+"/"+escape(key), "Поле не может быть null")
		}
	}
	return nil
}

// checkReplacementObject applies the same exact-field rule inside the
// resource or relation object an upsert carries.
func checkReplacementObject(field string, value jsonx.RawMessage) error {
	var nested map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(value, &nested); err != nil || nested == nil {
		return fail("/"+field, "Ожидается объект")
	}
	keys := map[string]bool{}
	if field == "resource" {
		for _, k := range []string{"id", "name", "service", "description", "operationKeys", "x", "y"} {
			keys[k] = true
		}
	} else {
		for _, k := range []string{"id", "fromResourceId", "toResourceId", "label"} {
			keys[k] = true
		}
	}
	return checkExactFields("/"+field, nested, keys, "Неизвестное поле")
}
func validateCommand(c Command) error {
	switch c.Kind {
	case "auto_layout":
		return nil
	case "upsert_resource":
		if c.Resource == nil {
			return fail("/resource", "Ожидается объект")
		}
		return validateResource(*c.Resource, "/resource")
	case "remove_resource", "move_resource":
		if !validID(c.ResourceID) {
			return fail("/resourceId", "Некорректный ID ресурса")
		}
	case "assign_operation":
		if !validID(c.OperationKey) {
			return fail("/operationKey", "Некорректный ключ операции")
		}
		if c.ResourceID != "" && !validID(c.ResourceID) {
			return fail("/resourceId", "Некорректный ID ресурса")
		}
	case "upsert_relation":
		if c.Relation == nil {
			return fail("/relation", "Ожидается объект")
		}
		return validateRelation(*c.Relation, "/relation")
	case "remove_relation":
		if !validID(c.RelationID) {
			return fail("/relationId", "Некорректный ID связи")
		}
	default:
		return fail("/kind", "Неизвестная команда")
	}
	if c.Kind == "move_resource" && (c.X == nil || c.Y == nil || !validFloat(*c.X) || !validFloat(*c.Y)) {
		return fail("/x", "Координаты должны быть конечными числами от -100000 до 100000")
	}
	return nil
}
func validFloat(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) && math.Abs(f) <= 100000 }
func validateResource(r Resource, p string) error {
	if !validID(r.ID) {
		return fail(p+"/id", "Некорректный ID ресурса")
	}
	if strings.TrimSpace(r.Name) == "" || utf8.RuneCountInString(r.Name) > 200 {
		return fail(p+"/name", "Название должно содержать от 1 до 200 символов")
	}
	if utf8.RuneCountInString(r.Service) > 200 || utf8.RuneCountInString(r.Description) > 2000 {
		return fail(p, "Слишком длинный текст ресурса")
	}
	if r.OperationKeys == nil || len(r.OperationKeys) > MaxOperations {
		return fail(p+"/operationKeys", "Ожидается массив не более 1000 ключей")
	}
	seen := map[string]bool{}
	for i, key := range r.OperationKeys {
		if !validID(key) || seen[key] {
			return fail(fmt.Sprintf("%s/operationKeys/%d", p, i), "Некорректный или повторяющийся ключ операции")
		}
		seen[key] = true
	}
	if !validFloat(r.X) || !validFloat(r.Y) {
		return fail(p, "Координаты должны быть конечными числами от -100000 до 100000")
	}
	return nil
}
func validateRelation(r Relation, p string) error {
	if !validID(r.ID) || !validID(r.FromResourceID) || !validID(r.ToResourceID) {
		return fail(p, "Некорректный ID связи или ресурса")
	}
	if r.FromResourceID == r.ToResourceID {
		return fail(p, "Связь ресурса с самим собой запрещена")
	}
	if utf8.RuneCountInString(r.Label) > 200 {
		return fail(p+"/label", "Подпись длиннее 200 символов")
	}
	return nil
}

type stored struct {
	FormatVersion int        `json:"formatVersion"`
	Resources     []Resource `json:"resources"`
	Relations     []Relation `json:"relations"`
}

func readStored(root map[string]any) (stored, error) {
	s := stored{FormatVersion: 1, Resources: []Resource{}, Relations: []Relation{}}
	v, exists := root[Extension]
	if !exists {
		return s, nil
	}
	p := "/" + Extension
	m := object(v)
	if m == nil {
		return s, fail(p, "Ожидается объект")
	}
	f, ok := coordinate(m["formatVersion"])
	if !ok || f != 1 {
		return s, fail(p+"/formatVersion", "Поддерживается formatVersion: 1")
	}
	rawResources, ok := m["resources"].([]any)
	if !ok {
		return s, fail(p+"/resources", "Ожидается массив")
	}
	rawRelations, ok := m["relations"].([]any)
	if !ok {
		return s, fail(p+"/relations", "Ожидается массив")
	}
	if len(rawResources) > MaxResources || len(rawRelations) > MaxRelations {
		return s, fail(p, "Превышен предел ресурсов или связей")
	}
	resourceIDs := map[string]bool{}
	assignments := map[string]bool{}
	for i, v := range rawResources {
		at := fmt.Sprintf("%s/resources/%d", p, i)
		r, err := readStoredResource(v, at)
		if err != nil {
			return s, err
		}
		if resourceIDs[r.ID] {
			return s, fail(at+"/id", "ID ресурса повторяется")
		}
		resourceIDs[r.ID] = true
		for _, key := range r.OperationKeys {
			if assignments[key] {
				return s, fail(at+"/operationKeys", "Операция назначена нескольким ресурсам")
			}
			assignments[key] = true
		}
		s.Resources = append(s.Resources, r)
	}
	relationIDs := map[string]bool{}
	pairs := map[string]bool{}
	for i, v := range rawRelations {
		at := fmt.Sprintf("%s/relations/%d", p, i)
		r, err := readStoredRelation(v, at)
		if err != nil {
			return s, err
		}
		pair := r.FromResourceID + "\x00" + r.ToResourceID
		if relationIDs[r.ID] || pairs[pair] {
			return s, fail(at, "ID или направленная пара связи повторяется")
		}
		relationIDs[r.ID] = true
		pairs[pair] = true
		s.Relations = append(s.Relations, r)
	}
	return s, nil
}

// readStoredResource decodes and validates one stored resource on its own;
// uniqueness across resources stays with the caller.
func readStoredResource(v any, at string) (Resource, error) {
	obj := object(v)
	if obj == nil {
		return Resource{}, fail(at, "Ожидается объект")
	}
	r := Resource{}
	for _, field := range []string{"id", "name", "service", "description"} {
		value, ok := obj[field].(string)
		if !ok {
			return Resource{}, fail(at+"/"+field, "Ожидается строка")
		}
		switch field {
		case "id":
			r.ID = value
		case "name":
			r.Name = value
		case "service":
			r.Service = value
		case "description":
			r.Description = value
		}
	}
	keys, ok := obj["operationKeys"].([]any)
	if !ok {
		return Resource{}, fail(at+"/operationKeys", "Ожидается массив")
	}
	r.OperationKeys = []string{}
	for j, v := range keys {
		key, ok := v.(string)
		if !ok {
			return Resource{}, fail(fmt.Sprintf("%s/operationKeys/%d", at, j), "Ожидается строка")
		}
		r.OperationKeys = append(r.OperationKeys, key)
	}
	for _, field := range []string{"x", "y"} {
		f, ok := coordinate(obj[field])
		if !ok {
			return Resource{}, fail(at+"/"+field, "Некорректная координата")
		}
		if field == "x" {
			r.X = f
		} else {
			r.Y = f
		}
	}
	if err := validateResource(r, at); err != nil {
		return Resource{}, err
	}
	return r, nil
}

// readStoredRelation decodes and validates one stored relation on its own;
// uniqueness of IDs and directed pairs stays with the caller.
func readStoredRelation(v any, at string) (Relation, error) {
	obj := object(v)
	if obj == nil {
		return Relation{}, fail(at, "Ожидается объект")
	}
	r := Relation{}
	for _, field := range []string{"id", "fromResourceId", "toResourceId", "label"} {
		value, ok := obj[field].(string)
		if !ok {
			return Relation{}, fail(at+"/"+field, "Ожидается строка")
		}
		switch field {
		case "id":
			r.ID = value
		case "fromResourceId":
			r.FromResourceID = value
		case "toResourceId":
			r.ToResourceID = value
		case "label":
			r.Label = value
		}
	}
	if err := validateRelation(r, at); err != nil {
		return Relation{}, err
	}
	return r, nil
}

// ValidateStored checks a present extension without constraining ordinary documents.
func ValidateStored(root map[string]any) error { _, err := readStored(root); return err }

func family(path string) string {
	if path == "/" {
		return path
	}
	parts := strings.Split(path, "/")
	for len(parts) > 1 && strings.HasPrefix(parts[len(parts)-1], "{") && strings.HasSuffix(parts[len(parts)-1], "}") {
		parts = parts[:len(parts)-1]
	}
	for i, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			parts[i] = "{}"
		}
	}
	result := strings.Join(parts, "/")
	if result == "" {
		return "/"
	}
	return result
}
func familyID(name string) string {
	hash := sha256.Sum256([]byte(name))
	return fmt.Sprintf("auto-%x", hash[:12])
}
func Project(root map[string]any) (Model, error) {
	m := Model{Resources: []ResourceView{}, Operations: []Operation{}, Relations: []Relation{}, Diagnostics: []MapDiagnostic{}}
	s, err := readStored(root)
	if err != nil {
		return m, err
	}
	families, err := projectOperations(root, &m)
	if err != nil {
		return m, err
	}
	projectSchemaDependencies(root, &m)
	resourceIDs, familyNames, err := projectResources(s, families, &m)
	if err != nil {
		return m, err
	}
	for _, r := range m.Resources {
		if len(r.OperationKeys) == 0 {
			m.Diagnostics = append(m.Diagnostics, MapDiagnostic{Code: "empty_resource", Severity: "info", Message: "Ресурс пока не содержит операций", ResourceID: r.ID})
		}
	}
	for _, rel := range s.Relations {
		m.Relations = append(m.Relations, rel)
		if !resourceIDs[rel.FromResourceID] && !hasResource(m.Resources, rel.FromResourceID) || !resourceIDs[rel.ToResourceID] && !hasResource(m.Resources, rel.ToResourceID) {
			m.Diagnostics = append(m.Diagnostics, MapDiagnostic{Code: "missing_relation_endpoint", Severity: "warning", Message: "Один из ресурсов связи не найден", RelationID: rel.ID})
		}
	}
	for _, name := range familyNames {
		if !hasCollectionGet(m.Operations, name) && hasCollectionPost(m.Operations, name) {
			m.Diagnostics = append(m.Diagnostics, MapDiagnostic{Code: "consider_collection_get", Severity: "info", Message: "Для " + name + " есть POST без GET коллекции; рассмотрите чтение коллекции", ResourceID: familyID(name)})
		}
	}
	return m, nil
}

// operationProjection carries the cross-path state of one Project call's
// operation walk: families for inference, first sightings for diagnostics.
type operationProjection struct {
	m                   *Model
	families            map[string][]string
	operationIDs        map[string]string
	shapes              map[string]string
	pathItemDiagnostics int
}

// projectOperations walks every non-extension path in sorted order and
// returns the operation keys grouped by resource family.
func projectOperations(root map[string]any, m *Model) (map[string][]string, error) {
	p := operationProjection{m: m, families: map[string][]string{}, operationIDs: map[string]string{}, shapes: map[string]string{}}
	paths := object(root["paths"])
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		if strings.HasPrefix(path, "x-") {
			continue
		}
		item := object(paths[path])
		pointer := "/paths/" + escape(path)
		nodes, diagnostics := schemamodel.PathItems(root, item, pointer)
		p.pathDiagnostics(path, diagnostics)
		for _, occurrence := range schemamodel.PathItemOperations(nodes) {
			if err := p.operation(item, pointer, path, occurrence); err != nil {
				return nil, err
			}
		}
	}
	return p.families, nil
}

// pathDiagnostics keeps the first 100 Path Item diagnostics and one marker.
func (p *operationProjection) pathDiagnostics(path string, diagnostics []schemamodel.PathItemDiagnostic) {
	for _, diagnostic := range diagnostics {
		p.pathItemDiagnostics++
		if p.pathItemDiagnostics <= 100 {
			p.m.Diagnostics = append(p.m.Diagnostics, MapDiagnostic{Code: diagnostic.Code, Severity: "warning", Message: path + ": " + diagnostic.Message})
		} else if p.pathItemDiagnostics == 101 {
			p.m.Diagnostics = append(p.m.Diagnostics, MapDiagnostic{Code: "path_item_diagnostics_truncated", Severity: "warning", Message: "Есть дополнительные проблемы ссылок Path Item"})
		}
	}
}

func (p *operationProjection) operation(item map[string]any, pointer, path string, occurrence schemamodel.PathItemOperation) error {
	verb := occurrence.Method
	op := object(occurrence.Value)
	if op == nil {
		return nil
	}
	if len(p.m.Operations) >= MaxOperations {
		return fail("/paths", "Редактор поддерживает не более 1000 операций")
	}
	key := text(op[OperationKey])
	keyPointer := pointer + "/" + verb + "/" + OperationKey
	source := ""
	// An operation reached through a $ref'd Path Item keeps its key on the
	// referring path, so aliases of one shared item stay distinct.
	if occurrence.Pointer != pointer+"/"+verb {
		key = text(object(item[PathOperationKeys])[verb])
		keyPointer = pointer + "/" + PathOperationKeys + "/" + verb
		source = occurrence.Pointer
	}
	if !validID(key) {
		return fail(keyPointer, "Нужен стабильный ключ операции")
	}
	for _, old := range p.m.Operations {
		if old.Key == key {
			return fail(keyPointer, "Ключ операции повторяется")
		}
	}
	p.m.Operations = append(p.m.Operations, Operation{Key: key, Method: strings.ToUpper(verb), Path: path, Summary: text(op["summary"]), Schemas: []string{}, SourcePointer: source})
	p.families[family(path)] = append(p.families[family(path)], key)
	p.overlaps(op, verb, path, key)
	return nil
}

// overlaps reports a repeated operationId and a path template that shadows
// another path with the same verb; the first sighting of each is remembered.
func (p *operationProjection) overlaps(op map[string]any, verb, path, key string) {
	if id := text(op["operationId"]); id != "" {
		if first := p.operationIDs[id]; first != "" {
			p.m.Diagnostics = append(p.m.Diagnostics, MapDiagnostic{Code: "duplicate_operation_id", Severity: "warning", Message: "operationId " + id + " повторяется у " + first, OperationKey: key})
		} else {
			p.operationIDs[id] = strings.ToUpper(verb) + " " + path
		}
	}
	shape := verb + " " + normalizePath(path)
	if first := p.shapes[shape]; first != "" && first != path {
		p.m.Diagnostics = append(p.m.Diagnostics, MapDiagnostic{Code: "overlapping_path", Severity: "warning", Message: "Шаблон пути пересекается с " + first, OperationKey: key})
	} else {
		p.shapes[shape] = path
	}
}

// projectSchemaDependencies attaches schema names to operations; analysis
// failures degrade to warnings because the map stays usable without them.
func projectSchemaDependencies(root map[string]any, m *Model) {
	if dependencies, err := schemamodel.Project(root); err == nil {
		byAddress := map[string][]string{}
		for _, usage := range dependencies.Operations {
			byAddress[usage.Method+" "+usage.Path] = usage.Schemas
		}
		for i := range m.Operations {
			if names, ok := byAddress[m.Operations[i].Method+" "+m.Operations[i].Path]; ok {
				m.Operations[i].Schemas = names
			}
		}
	} else {
		m.Diagnostics = append(m.Diagnostics, MapDiagnostic{Code: "schema_analysis_unavailable", Severity: "warning", Message: "Зависимости схем не определены: " + err.Error()})
	}
	unsupportedRef := ""
	if err := schemamodel.VisitReferences(root, func(_, ref string) {
		if unsupportedRef == "" && ref != "#" && !strings.HasPrefix(ref, "#/") {
			unsupportedRef = ref
		}
	}); err != nil {
		m.Diagnostics = append(m.Diagnostics, MapDiagnostic{Code: "schema_analysis_unavailable", Severity: "warning", Message: "Зависимости схем проверены не полностью: " + err.Error()})
	} else if unsupportedRef != "" {
		m.Diagnostics = append(m.Diagnostics, MapDiagnostic{Code: "schema_analysis_unavailable", Severity: "warning", Message: "Внешняя или якорная ссылка не анализируется без сети: " + unsupportedRef})
	}
}

// projectResources lists the stored resources, then infers one resource per
// family for the operations no stored resource claims, and sorts the result.
func projectResources(s stored, families map[string][]string, m *Model) (map[string]bool, []string, error) {
	knownOps := map[string]Operation{}
	for _, op := range m.Operations {
		knownOps[op.Key] = op
	}
	assigned := map[string]bool{}
	resourceIDs := map[string]bool{}
	for _, r := range s.Resources {
		resourceIDs[r.ID] = true
		m.Resources = append(m.Resources, ResourceView{Resource: r, Inferred: false})
		for _, key := range r.OperationKeys {
			assigned[key] = true
			if _, ok := knownOps[key]; !ok {
				m.Diagnostics = append(m.Diagnostics, MapDiagnostic{Code: "stale_operation_assignment", Severity: "warning", Message: "Назначенная операция больше не найдена", ResourceID: r.ID, OperationKey: key})
			}
		}
	}
	familyNames := slices.Sorted(maps.Keys(families))
	for i, name := range familyNames {
		if err := inferFamilyResource(m, i, name, families[name], assigned, resourceIDs); err != nil {
			return nil, nil, err
		}
	}
	if len(m.Resources) > MaxResources {
		return nil, nil, fail("/"+Extension, "Редактор поддерживает не более 200 ресурсов")
	}
	slices.SortFunc(m.Resources, func(a, b ResourceView) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return resourceIDs, familyNames, nil
}

// inferFamilyResource gives a family's unassigned operations to its stored
// resource when one was saved under the family ID, else to a new inferred one
// laid out on the default three-column grid.
func inferFamilyResource(m *Model, i int, name string, keys []string, assigned, resourceIDs map[string]bool) error {
	id := familyID(name)
	remaining := []string{}
	for _, key := range keys {
		if !assigned[key] {
			remaining = append(remaining, key)
		}
	}
	if resourceIDs[id] {
		for j := range m.Resources {
			if m.Resources[j].ID == id {
				m.Resources[j].OperationKeys = append(m.Resources[j].OperationKeys, remaining...)
				break
			}
		}
		return nil
	}
	if len(remaining) == 0 {
		return nil
	}
	if utf8.RuneCountInString(name) > 200 {
		return fail("/paths", "Автоматическое название ресурса длиннее 200 символов")
	}
	m.Resources = append(m.Resources, ResourceView{Resource: Resource{ID: id, Name: name, Service: "", Description: "", OperationKeys: remaining, X: float64(i%3) * 340, Y: float64(i/3) * 220}, Inferred: true})
	return nil
}
func normalizePath(path string) string {
	parts := strings.Split(path, "/")
	for i, s := range parts {
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			parts[i] = "{}"
		}
	}
	return strings.Join(parts, "/")
}
func hasResource(rs []ResourceView, id string) bool {
	for _, r := range rs {
		if r.ID == id {
			return true
		}
	}
	return false
}
func hasCollectionGet(ops []Operation, name string) bool {
	for _, op := range ops {
		if op.Method == http.MethodGet && normalizePath(op.Path) == name {
			return true
		}
	}
	return false
}
func hasCollectionPost(ops []Operation, name string) bool {
	for _, op := range ops {
		if op.Method == http.MethodPost && normalizePath(op.Path) == name {
			return true
		}
	}
	return false
}

// Apply transforms a cloned root; on any error neither the input nor storage changes.
// Empty batches are useful for preview normalization; writes enforce nonempty batches.
func Apply(root map[string]any, commands []Command) (map[string]any, error) {
	if len(commands) > MaxCommands {
		return nil, fail("/commands", "Не более 100 команд")
	}
	s, err := readStored(root)
	if err != nil {
		return nil, err
	}
	copyRoot := maps.Clone(root)
	for i, c := range commands {
		if err := validateCommand(c); err != nil {
			return nil, fail(fmt.Sprintf("/commands/%d", i), err.Error())
		}
		model, err := Project(copyRoot)
		if err != nil {
			return nil, err
		}
		if err := applyCommand(&s, model, c); err != nil {
			return nil, err
		}
		copyRoot[Extension] = storedObjectWithMetadata(s, object(copyRoot[Extension]))
		if err := ValidateStored(copyRoot); err != nil {
			return nil, err
		}
	}
	if len(commands) > 0 {
		if _, err := Project(copyRoot); err != nil {
			return nil, err
		}
	}
	return copyRoot, nil
}

// applyCommand applies one validated command to the stored copy; model is
// the projection of the document as it stood before this command.
func applyCommand(s *stored, model Model, c Command) error {
	switch c.Kind {
	case "auto_layout":
		if err := autoLayout(s, model); err != nil {
			return err
		}
	case "upsert_resource":
		j := slices.IndexFunc(s.Resources, func(r Resource) bool { return r.ID == c.Resource.ID })
		if j < 0 {
			s.Resources = append(s.Resources, *c.Resource)
		} else {
			s.Resources[j] = *c.Resource
		}
	case "remove_resource":
		j := slices.IndexFunc(s.Resources, func(r Resource) bool { return r.ID == c.ResourceID })
		if j < 0 {
			return fail("/resourceId", "Нельзя удалить не сохранённый ресурс")
		}
		s.Resources = slices.Delete(s.Resources, j, j+1)
		s.Relations = slices.DeleteFunc(s.Relations, func(r Relation) bool { return r.FromResourceID == c.ResourceID || r.ToResourceID == c.ResourceID })
	case "assign_operation":
		return assignOperation(s, model, c)
	case "upsert_relation":
		j := slices.IndexFunc(s.Relations, func(r Relation) bool { return r.ID == c.Relation.ID })
		if j < 0 {
			s.Relations = append(s.Relations, *c.Relation)
		} else {
			s.Relations[j] = *c.Relation
		}
	case "remove_relation":
		j := slices.IndexFunc(s.Relations, func(r Relation) bool { return r.ID == c.RelationID })
		if j < 0 {
			return fail("/relationId", "Связь не найдена")
		}
		s.Relations = slices.Delete(s.Relations, j, j+1)
	case "move_resource":
		if err := materialize(s, model, c.ResourceID); err != nil {
			return err
		}
		j := slices.IndexFunc(s.Resources, func(r Resource) bool { return r.ID == c.ResourceID })
		s.Resources[j].X = *c.X
		s.Resources[j].Y = *c.Y
	}
	return nil
}

// assignOperation moves an operation to one resource, or unassigns it when
// ResourceID is empty; an inferred target resource is saved first.
func assignOperation(s *stored, model Model, c Command) error {
	if !slices.ContainsFunc(model.Operations, func(op Operation) bool { return op.Key == c.OperationKey }) {
		return fail("/operationKey", "Операция не найдена")
	}
	if c.ResourceID != "" {
		if err := materialize(s, model, c.ResourceID); err != nil {
			return err
		}
	}
	for j := range s.Resources {
		s.Resources[j].OperationKeys = slices.DeleteFunc(s.Resources[j].OperationKeys, func(key string) bool { return key == c.OperationKey })
	}
	if c.ResourceID != "" {
		j := slices.IndexFunc(s.Resources, func(r Resource) bool { return r.ID == c.ResourceID })
		s.Resources[j].OperationKeys = append(s.Resources[j].OperationKeys, c.OperationKey)
	}
	return nil
}

func materialize(s *stored, m Model, id string) error {
	if slices.ContainsFunc(s.Resources, func(r Resource) bool { return r.ID == id }) {
		return nil
	}
	for _, v := range m.Resources {
		if v.ID == id {
			r := v.Resource
			r.OperationKeys = []string{}
			s.Resources = append(s.Resources, r)
			return nil
		}
	}
	return fail("/resourceId", "Ресурс не найден")
}
func storedObject(s stored) map[string]any {
	resources := make([]any, 0, len(s.Resources))
	for _, r := range s.Resources {
		keys := make([]any, 0, len(r.OperationKeys))
		for _, k := range r.OperationKeys {
			keys = append(keys, k)
		}
		resources = append(resources, map[string]any{"id": r.ID, "name": r.Name, "service": r.Service, "description": r.Description, "operationKeys": keys, "x": r.X, "y": r.Y})
	}
	relations := make([]any, 0, len(s.Relations))
	for _, r := range s.Relations {
		relations = append(relations, map[string]any{"id": r.ID, "fromResourceId": r.FromResourceID, "toResourceId": r.ToResourceID, "label": r.Label})
	}
	return map[string]any{"formatVersion": jsonx.Number("1"), "resources": resources, "relations": relations}
}

// Commands replace known fields while retaining opaque metadata on surviving
// elements. Clone every changed map so failed batches never mutate their input.
func storedObjectWithMetadata(s stored, previous map[string]any) map[string]any {
	next := storedObject(s)
	for _, collection := range []string{"resources", "relations"} {
		oldByID := map[string]map[string]any{}
		items, _ := previous[collection].([]any)
		for _, item := range items {
			value := object(item)
			oldByID[text(value["id"])] = value
		}
		for _, item := range next[collection].([]any) {
			value := object(item)
			for key, opaque := range oldByID[text(value["id"])] {
				if _, known := value[key]; !known {
					value[key] = opaque
				}
			}
		}
	}
	for key, opaque := range previous {
		if _, known := next[key]; !known {
			next[key] = opaque
		}
	}
	return next
}

// Compile-time assurance that Error remains a normal error value.
var _ error = (*Error)(nil)
