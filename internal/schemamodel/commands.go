package schemamodel

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

// Apply clones once, so failed batches never expose partial changes to callers.
func Apply(root map[string]any, commands []Command) (map[string]any, error) {
	if len(commands) > MaxCommands {
		return nil, fail("/commands", "Допускается не более 100 команд")
	}
	if err := checkBounds(root); err != nil {
		return nil, err
	}
	if _, _, err := references(root); err != nil {
		return nil, err
	}
	raw, err := jsonx.Marshal(root)
	if err != nil {
		return nil, err
	}
	decoder := jsonx.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out map[string]any
	if err = decoder.Decode(&out); err != nil {
		return nil, err
	}
	for i, command := range commands {
		if err := apply(out, command); err != nil {
			return nil, fmt.Errorf("/commands/%d: %w", i, err)
		}
		if err := checkBounds(out); err != nil {
			return nil, err
		}
		if _, _, err := references(out); err != nil {
			return nil, err
		}
	}
	return out, nil
}
func commandSchema(c Command) (any, error) {
	if len(c.SchemaJSON) > MaxSchemaBytes {
		return nil, fail("/schemaJSON", "Схема превышает 64 KiB")
	}
	if !jsonx.Valid([]byte(c.SchemaJSON)) {
		return nil, fail("/schemaJSON", "Ожидается JSON-схема")
	}
	d := jsonx.NewDecoder(strings.NewReader(c.SchemaJSON))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, fail("/schemaJSON", err.Error())
	}
	if !schemaShape(value) {
		return nil, fail("/schemaJSON", "Схема должна быть объектом или boolean")
	}
	return value, nil
}
func nameValid(name string) bool { return strings.TrimSpace(name) != "" }
func apply(root map[string]any, c Command) error {
	if err := commandShape(c); err != nil {
		return err
	}
	if !nameValid(c.SchemaName) {
		return fail("/schemaName", "Укажите имя схемы")
	}
	ss := schemaMap(root)
	value, exists := ss[c.SchemaName]
	p := schemaPointer(c.SchemaName)
	if c.Kind == "create_schema" {
		return createSchema(root, c, ss, exists, p)
	}
	if !exists {
		return fail(p, "Схема не найдена")
	}
	switch c.Kind {
	case "replace_schema":
		v, err := commandSchema(c)
		if err != nil {
			return err
		}
		ss[c.SchemaName] = v
	case "rename_schema":
		return renameSchema(root, c, ss, value, p)
	case "delete_schema":
		if pointer := resourceWithin(value, p); pointer != "" {
			return fail(pointer, "Схема содержит собственный $id; удалите её через JSON после проверки потребителей URI ресурса")
		}
		if err := deletable(root, p); err != nil {
			return err
		}
		delete(ss, c.SchemaName)
		delete(object(object(root[Extension])["positions"]), c.SchemaName)
	case "move_schema":
		return moveSchema(root, c)
	case "upsert_property", "rename_property", "delete_property", "set_reference":
		return propertyCommand(root, c, value, p)
	default:
		return fail("/kind", "Неизвестная команда: "+c.Kind)
	}
	return nil
}
func propertyCommand(root map[string]any, c Command, value any, p string) error {
	m := object(value)
	if m == nil || m["$ref"] != nil || (m["type"] != nil && m["type"] != "object") {
		return fail(p, "Поля доступны только для объектной схемы без корневой ссылки; используйте JSON схемы")
	}
	if !nameValid(c.PropertyName) {
		return fail("/propertyName", "Укажите имя поля")
	}
	if (c.Kind == "rename_property" || c.Kind == "delete_property") && text(m["$id"]) != "" {
		return fail(p+"/$id", "Схема имеет собственный $id; измените поле через JSON с учётом ссылок ресурса")
	}
	props := object(m["properties"])
	v, exists := props[c.PropertyName]
	pointer := p + "/properties/" + escape(c.PropertyName)
	if c.Kind != "upsert_property" && !exists {
		return fail(pointer, "Поле не найдено")
	}
	switch c.Kind {
	case "upsert_property":
		v, err := commandSchema(c)
		if err != nil {
			return err
		}
		if props == nil {
			props = map[string]any{}
			m["properties"] = props
		}
		props[c.PropertyName] = v
		if c.Required != nil {
			setRequired(m, c.PropertyName, *c.Required)
		}
	case "rename_property":
		return renameProperty(root, c, m, props, v, p, pointer)
	case "delete_property":
		if pointer := resourceWithin(v, pointer); pointer != "" {
			return fail(pointer, "Поле содержит ресурс с собственным $id; удалите его через JSON после проверки потребителей")
		}
		if err := deletable(root, pointer); err != nil {
			return err
		}
		delete(props, c.PropertyName)
		setRequired(m, c.PropertyName, false)
	case "set_reference":
		return setReference(root, c, m, v, pointer)
	}
	return nil
}
func setRequired(m map[string]any, name string, want bool) {
	values, _ := m["required"].([]any)
	out := make([]any, 0, len(values)+1)
	found := false
	for _, v := range values {
		if v == name {
			found = true
			if !want {
				continue
			}
		}
		out = append(out, v)
	}
	if want && !found {
		out = append(out, name)
	}
	if len(out) == 0 {
		delete(m, "required")
	} else {
		m["required"] = out
	}
}
func rewrite(root map[string]any, old, next string) error {
	sites, _, err := references(root)
	if err != nil {
		return err
	}
	for _, site := range sites {
		p := localPointer(site.effectiveRef())
		if !under(p, old) {
			continue
		}
		target := next + strings.TrimPrefix(p, old)
		if site.bare {
			name, _ := schemaTarget(pointerRef(target))
			site.owner[site.key] = name
		} else {
			site.owner[site.key] = pointerRef(target)
		}
	}
	return nil
}
func deletable(root map[string]any, pointer string) error {
	sites, _, err := references(root)
	if err != nil {
		return err
	}
	for _, site := range sites {
		if strings.HasPrefix(site.effectiveRef(), "#") && localPointer(site.effectiveRef()) == "" && !under(site.pointer, pointer) {
			return fail(site.pointer, "Удаление при ссылке на якорь требует проверки в JSON: "+site.ref)
		}
		if under(localPointer(site.effectiveRef()), pointer) && !under(site.pointer, pointer) {
			return fail(site.pointer, "Удаление нарушит ссылку "+site.ref+"; сначала измените потребителя")
		}
	}
	return nil
}

func commandShape(c Command) error {
	allowed := map[string]string{
		"create_schema": "schemaJSON", "replace_schema": "schemaJSON", "rename_schema": "newName", "delete_schema": "",
		"upsert_property": "propertyName schemaJSON required", "rename_property": "propertyName newName", "delete_property": "propertyName", "set_reference": "propertyName targetSchema array", "move_schema": "x y",
	}
	fields, ok := allowed[c.Kind]
	if !ok {
		return fail("/kind", "Неизвестная команда: "+c.Kind)
	}
	present := map[string]bool{"newName": c.NewName != "", "propertyName": c.PropertyName != "", "schemaJSON": c.SchemaJSON != "", "required": c.Required != nil, "targetSchema": c.TargetSchema != "", "array": c.Array != nil, "x": c.X != nil, "y": c.Y != nil}
	for _, key := range []string{"newName", "propertyName", "schemaJSON", "required", "targetSchema", "array", "x", "y"} {
		if present[key] && !strings.Contains(" "+fields+" ", " "+key+" ") {
			return fail("/"+key, "Поле неприменимо к команде "+c.Kind)
		}
	}
	return nil
}

// Structural traversal already applies depth limits before this helper is used.
func resourceWithin(value any, p string) string {
	m := object(value)
	if m == nil {
		return ""
	}
	if text(m["$id"]) != "" {
		return p + "/$id"
	}
	for _, key := range slices.Sorted(maps.Keys(childRules["schema"])) {
		rule := childRules["schema"][key]
		if rule.kind != "schema" {
			continue
		}
		child, ok := m[key]
		if !ok {
			continue
		}
		at := p + "/" + escape(key)
		if rule.mode == "map" {
			for _, name := range slices.Sorted(maps.Keys(object(child))) {
				if found := resourceWithin(object(child)[name], at+"/"+escape(name)); found != "" {
					return found
				}
			}
			continue
		}
		if values, ok := child.([]any); ok {
			for i, v := range values {
				if found := resourceWithin(v, at+"/"+strconv.Itoa(i)); found != "" {
					return found
				}
			}
			continue
		}
		if found := resourceWithin(child, at); found != "" {
			return found
		}
	}
	return ""
}

func createSchema(root map[string]any, c Command, ss map[string]any, exists bool, p string) error {
	if exists {
		return fail(p, "Схема уже существует")
	}
	v, err := commandSchema(c)
	if err != nil {
		return err
	}
	if ss == nil {
		components := object(root["components"])
		if components == nil {
			components = map[string]any{}
			root["components"] = components
		}
		ss = map[string]any{}
		components["schemas"] = ss
	}
	ss[c.SchemaName] = v
	return nil
}

func renameSchema(root map[string]any, c Command, ss map[string]any, value any, p string) error {
	if !nameValid(c.NewName) {
		return fail("/newName", "Укажите новое имя")
	}
	if _, ok := ss[c.NewName]; ok {
		return fail(schemaPointer(c.NewName), "Схема уже существует")
	}
	if err := rewrite(root, p, schemaPointer(c.NewName)); err != nil {
		return err
	}
	ss[c.NewName] = value
	delete(ss, c.SchemaName)
	positions := object(object(root[Extension])["positions"])
	if pos, ok := positions[c.SchemaName]; ok {
		positions[c.NewName] = pos
		delete(positions, c.SchemaName)
	}
	return nil
}

func moveSchema(root map[string]any, c Command) error {
	if c.X == nil || c.Y == nil {
		return fail("/commands", "Укажите x и y")
	}
	if _, ok := coordinate(*c.X); !ok {
		return fail("/x", "Координата должна быть в пределах +/-100000")
	}
	if _, ok := coordinate(*c.Y); !ok {
		return fail("/y", "Координата должна быть в пределах +/-100000")
	}
	layout := object(root[Extension])
	if layout == nil {
		layout = map[string]any{"formatVersion": 1, "positions": map[string]any{}}
		root[Extension] = layout
	}
	object(layout["positions"])[c.SchemaName] = map[string]any{"x": *c.X, "y": *c.Y}
	return nil
}

func renameProperty(root map[string]any, c Command, m, props map[string]any, v any, p, pointer string) error {
	if !nameValid(c.NewName) {
		return fail("/newName", "Укажите новое имя")
	}
	if _, ok := props[c.NewName]; ok {
		return fail(p+"/properties/"+escape(c.NewName), "Поле уже существует")
	}
	if err := rewrite(root, pointer, p+"/properties/"+escape(c.NewName)); err != nil {
		return err
	}
	props[c.NewName] = v
	delete(props, c.PropertyName)
	req, _ := m["required"].([]any)
	for i, name := range req {
		if name == c.PropertyName {
			req[i] = c.NewName
		}
	}
	return nil
}

func setReference(root map[string]any, c Command, m map[string]any, v any, pointer string) error {
	if _, ok := schemaMap(root)[c.TargetSchema]; !ok {
		return fail(schemaPointer(c.TargetSchema), "Целевая схема не найдена")
	}
	field := object(v)
	if field == nil {
		return fail(pointer, "Для ссылки нужен объект JSON-схемы; замените boolean явно")
	}

	scopes := []map[string]any{m, field}
	if c.Array != nil && *c.Array {
		scopes = append(scopes, object(field["items"]))
	}
	for _, scope := range scopes {
		if text(scope["$id"]) != "" {
			return fail(pointer, "Схема содержит собственный $id: задайте ссылку явно в JSON с учётом URI ресурса")
		}
	}
	if c.Array == nil || !*c.Array {
		if at := resourceWithin(field["items"], pointer+"/items"); at != "" {
			return fail(at, "items содержит ресурс с собственным $id; замените форму через JSON после проверки потребителей")
		}
	}
	ref := pointerRef(schemaPointer(c.TargetSchema))
	if c.Array != nil && *c.Array {
		field["type"] = "array"
		delete(field, "$ref")
		items := object(field["items"])
		if field["items"] != nil && items == nil {
			return fail(pointer+"/items", "Замените items явно: ожидается объект схемы")
		}
		if items == nil {
			items = map[string]any{}
			field["items"] = items
		}
		if at := resourceWithin(items["items"], pointer+"/items/items"); at != "" {
			return fail(at, "items содержит вложенный ресурс с $id; измените форму явно через JSON")
		}
		delete(items, "type")
		delete(items, "items")
		items["$ref"] = ref
	} else {
		delete(field, "type")
		delete(field, "items")
		field["$ref"] = ref
	}
	return nil
}
