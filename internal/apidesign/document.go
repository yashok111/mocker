package apidesign

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/resourcemap"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/schemamodel"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/statediagram"
	"github.com/yashok111/mocker/internal/yamlx"
)

const emptyDocument = `{"openapi":"3.1.0","info":{"title":"Новый API","version":"1.0.0"},"paths":{}}`

var methods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true, "trace": true}
var pathParameter = regexp.MustCompile(`\{([^{}]+)\}`)
var oasVersion = regexp.MustCompile(`^3\.(0|1)\.\d+$`)
var responseStatus = regexp.MustCompile(`^[1-5]([0-9]{2}|XX)$`)

type preparedDocument struct {
	document, hash string
	runtime        *specs.PreparedImport
}

// validateMockerExtensions admits the x-mocker-* authoring and execution
// copies, each mapped to the field error its editor expects, in a fixed order.
func validateMockerExtensions(ctx context.Context, root map[string]any) error {
	if _, err := statediagram.Decode(root); err != nil {
		return invalidField("/"+statediagram.Extension, err.Error())
	}
	if err := schemamodel.ValidateLayout(root); err != nil {
		return schemaModelError(err)
	}
	if err := resourcemap.ValidateStored(root); err != nil {
		return resourceMapError(err)
	}
	if _, err := responserules.Decode(root); err != nil {
		return responseRuleError(err)
	}
	if _, err := responserules.CompileExecution(ctx, root); err != nil {
		return responseRuleError(err)
	}
	if _, err := statediagram.CompileExecution(ctx, root); err != nil {
		if field, ok := errors.AsType[*statediagram.FieldError](err); ok {
			return invalidField(field.Pointer, field.Message)
		}
		return invalidField("/"+statediagram.ExecutionExtension, err.Error())
	}
	return nil
}

func (r *Repo) prepare(ctx context.Context, raw string) (*preparedDocument, error) {
	if int64(len(raw)) > r.cfg.MaxBody {
		return nil, specs.ErrTooLarge
	}
	root, err := decodeDocument(raw)
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			return nil, err
		}
		return nil, &InvalidError{Diagnostics: []Diagnostic{{Pointer: "", Message: err.Error(), Severity: "error"}}}
	}
	if err := validateMockerExtensions(ctx, root); err != nil {
		return nil, err
	}
	diagnostics := validateRoot(root)
	if len(diagnostics) > 0 {
		return nil, &InvalidError{Diagnostics: diagnostics}
	}
	operations, err := authoredOperations(root)
	if err != nil {
		return nil, err
	}
	if _, err := operationKeys(operations); err != nil {
		return nil, err
	}
	diagnostics, err = validateGrammar(root)
	if err != nil {
		return nil, err
	}
	if len(diagnostics) > 0 {
		return nil, &InvalidError{Diagnostics: diagnostics}
	}
	canonical, err := jsonx.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	runtime, err := r.specs.PrepareImportContext(ctx, specs.ImportInput{Document: canonical, Source: "upload"})
	if err != nil {
		if errors.Is(err, specs.ErrTooLarge) {
			return nil, err
		}
		return nil, invalidField("", err.Error())
	}
	for _, op := range runtime.Operations {
		if op.ParseError != nil {
			diagnostics = append(diagnostics, Diagnostic{Pointer: op.Pointer, Message: *op.ParseError, Severity: "error"})
		}
	}
	if len(diagnostics) > 0 {
		return nil, &InvalidError{Diagnostics: diagnostics}
	}
	hash := sha256.Sum256(canonical)
	return &preparedDocument{document: string(canonical), hash: hex.EncodeToString(hash[:]), runtime: runtime}, nil
}

func decodeDocument(raw string) (map[string]any, error) {
	data := []byte(raw)
	if !jsonx.Valid(data) {
		var err error
		data, err = yamlx.ToJSON(data)
		if err != nil {
			return nil, err
		}
	}
	if err := admitRawResponseRuleExtensions(data); err != nil {
		return nil, err
	}
	decoder := jsonx.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, fmt.Errorf("корень документа должен быть объектом")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("ожидается один документ")
	}
	return root, nil
}

// The generic document decoder collapses duplicate names. Admit raw rule
// extensions first so predicate/ref ambiguity cannot disappear before their
// strict codec runs. Other OpenAPI fields retain their established policy.
func admitRawResponseRuleExtensions(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil
	}
	decoder := jsonx.NewDecoder(bytes.NewReader(data))
	if _, err := decoder.Token(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		var raw jsonx.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		switch token {
		case responserules.Extension, responserules.ExecutionExtension:
			name := token.(string)
			if seen[name] {
				return invalidField("/"+name, "повторяющееся расширение")
			}
			seen[name] = true
			if name == responserules.Extension {
				_, err = responserules.Decode(map[string]any{responserules.Extension: raw})
			} else {
				_, err = responserules.DecodeExecution(map[string]any{responserules.ExecutionExtension: raw})
			}
		}
		if err != nil {
			return responseRuleError(err)
		}
	}
	return nil
}

type diagnosticSink func(string, string)

func validateRoot(root map[string]any) []Diagnostic {
	out := []Diagnostic{}
	add := func(pointer, message string) {
		out = append(out, Diagnostic{Pointer: pointer, Message: message, Severity: "error"})
	}
	version, _ := root["openapi"].(string)
	if !oasVersion.MatchString(version) {
		add("/openapi", "Поддерживается OpenAPI 3.0.x или 3.1.x")
	}
	info, ok := root["info"].(map[string]any)
	if !ok {
		add("/info", "Обязателен объект info")
	} else {
		for _, key := range []string{"title", "version"} {
			value, ok := info[key].(string)
			if !ok || strings.TrimSpace(value) == "" {
				add("/info/"+key, "Обязательна непустая строка")
			}
		}
	}
	paths, ok := root["paths"].(map[string]any)
	if !ok {
		add("/paths", "Обязателен объект paths")
	}
	walkRefs(root, add)
	validateSchemaLocations(root, "", add)
	ids := map[string]string{}
	shapes := map[string]string{}
	for path, value := range paths {
		if strings.HasPrefix(path, "x-") {
			continue
		}
		pointer := "/paths/" + escape(path)
		if !strings.HasPrefix(path, "/") {
			add(pointer, "Путь должен начинаться с /")
		}
		shape := pathParameter.ReplaceAllString(path, "{}")
		if previous, exists := shapes[shape]; exists {
			add(pointer, "Путь повторяет шаблон "+previous)
		}
		shapes[shape] = path
		item, ok := value.(map[string]any)
		if !ok {
			add(pointer, "Ожидается объект пути")
			continue
		}
		validatePathItem(root, path, item, ids, add)
	}
	return out
}

func validatePathItem(root map[string]any, path string, item map[string]any, ids map[string]string, add diagnosticSink) {
	nodes, diagnostics := schemamodel.PathItems(root, item, "/paths/"+escape(path))
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != "path_item_method_conflict" {
			add(diagnostic.Pointer, diagnostic.Message)
		}
	}
	for _, operation := range schemamodel.PathItemOperations(nodes) {
		p := "/paths/" + escape(path) + "/" + operation.Method
		op, ok := operation.Value.(map[string]any)
		if !ok {
			add(p, "Ожидается объект операции")
			continue
		}
		if id, exists := op["operationId"]; exists {
			name, ok := id.(string)
			if !ok || name == "" {
				add(p+"/operationId", "Ожидается непустая строка")
			} else if previous, exists := ids[name]; exists {
				add(p+"/operationId", "operationId уже используется: "+previous)
			} else {
				ids[name] = p
			}
		}
		validateResponses(root, op, p, add)
		parameters := make([]map[string]any, 0, len(nodes)+1)
		for i := len(nodes) - 1; i >= 0; i-- {
			parameters = append(parameters, nodes[i].Value)
		}
		parameters = append(parameters, op)
		validateParameters(root, path, parameters, p, add)
		if body, exists := op["requestBody"]; exists {
			obj, ok := body.(map[string]any)
			if !ok {
				add(p+"/requestBody", "Ожидается объект requestBody")
			} else {
				obj = resolveObject(root, obj)
				content, ok := obj["content"].(map[string]any)
				if !ok || len(content) == 0 {
					add(p+"/requestBody/content", "Обязателен непустой объект content")
				}
			}
		}
	}
}

func validateResponses(root, op map[string]any, p string, add diagnosticSink) {
	responses, ok := op["responses"].(map[string]any)
	if !ok || len(responses) == 0 {
		add(p+"/responses", "Обязателен непустой объект responses")
		return
	}
	count := 0
	for status, response := range responses {
		if strings.HasPrefix(status, "x-") {
			continue
		}
		count++
		at := p + "/responses/" + escape(status)
		if status != "default" && !responseStatus.MatchString(status) {
			add(at, "Некорректный код ответа")
		}
		obj, ok := response.(map[string]any)
		if !ok {
			add(at, "Ожидается объект ответа")
			continue
		}
		obj = resolveObject(root, obj)
		if _, ok := obj["description"].(string); !ok {
			add(at+"/description", "Обязательна строка description")
		}
		if content, exists := obj["content"]; exists {
			if _, ok := content.(map[string]any); !ok {
				add(at+"/content", "Ожидается объект content")
			}
		}
	}
	if count == 0 {
		add(p+"/responses", "Обязателен хотя бы один HTTP-ответ")
	}
}

func resolveObject(root map[string]any, obj map[string]any) map[string]any {
	seen := map[string]bool{}
	for {
		ref, ok := obj["$ref"].(string)
		if !ok || seen[ref] {
			return obj
		}
		seen[ref] = true
		v, ok := resolvePointer(root, ref)
		if !ok {
			return obj
		}
		next, ok := v.(map[string]any)
		if !ok {
			return obj
		}
		obj = next
	}
}

func validateParameters(root map[string]any, path string, objects []map[string]any, p string, add diagnosticSink) {
	params := map[string]map[string]any{}
	for _, object := range objects {
		raw, exists := object["parameters"]
		if !exists {
			continue
		}
		list, ok := raw.([]any)
		if !ok {
			add(p+"/parameters", "Ожидается массив параметров")
			continue
		}
		seen := map[string]bool{}
		for i, value := range list {
			at := p + "/parameters/" + strconv.Itoa(i)
			param, ok := value.(map[string]any)
			if !ok {
				add(at, "Ожидается объект параметра")
				continue
			}
			param = resolveObject(root, param)
			key := validateParameter(path, param, at, add)
			if seen[key] {
				add(at, "Повторяющийся параметр")
			}
			seen[key] = true
			params[key] = param
		}
	}
	for _, match := range pathParameter.FindAllStringSubmatch(path, -1) {
		if _, ok := params["path:"+match[1]]; !ok {
			add(p+"/parameters", "Не объявлен path-параметр "+match[1])
		}
	}
}

func validateParameter(path string, param map[string]any, at string, add diagnosticSink) string {
	name, _ := param["name"].(string)
	location, _ := param["in"].(string)
	if name == "" || (location != "path" && location != "query" && location != "header" && location != "cookie") {
		add(at, "Параметру нужны name и in")
	}
	_, schema := param["schema"]
	_, content := param["content"]
	if schema == content {
		add(at, "Укажите ровно одно из schema или content")
	}
	if location == "path" {
		if param["required"] != true {
			add(at+"/required", "Path-параметр должен быть обязательным")
		}
		if !strings.Contains(path, "{"+name+"}") {
			add(at, "Path-параметр отсутствует в пути")
		}
	}
	return location + ":" + name
}

func walkRefs(root map[string]any, add diagnosticSink) {
	err := schemamodel.VisitReferences(root, func(pointer, ref string) { validateReference(root, ref, pointer, add) })
	if problem, ok := errors.AsType[*schemamodel.Error](err); ok {
		add(problem.Pointer, problem.Message)
	} else if err != nil {
		add("", err.Error())
	}
}

// In these maps keys are authored names, never OpenAPI keywords. A property
// literally called "example" or "x-field" still has a schema to validate.
func namedObjectMap(key string) bool {
	switch key {
	case "properties", "patternProperties", "$defs", "dependentSchemas", "schemas", "paths", "responses", "headers", "parameters", "requestBodies", "securitySchemes", "links", "callbacks", "pathItems", "content", "examples":
		return true
	}
	return false
}

func validateReference(root map[string]any, value any, pointer string, add diagnosticSink) {
	ref, ok := value.(string)
	if !ok {
		add(pointer, "$ref должен быть строкой")
		return
	}
	if _, ok := resolvePointer(root, ref); !ok {
		add(pointer, "Ссылка не найдена или внешняя ссылка не поддерживается: "+ref)
	}
}

func resolvePointer(root map[string]any, ref string) (any, bool) {
	if ref == "#" {
		return root, true
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	fragment, err := url.PathUnescape(strings.TrimPrefix(ref, "#"))
	if err != nil {
		return nil, false
	}
	var current any = root
	for token := range strings.SplitSeq(strings.TrimPrefix(fragment, "/"), "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch obj := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = obj[token]
			if !ok {
				return nil, false
			}
		case []any:
			i, err := strconv.Atoi(token)
			if err != nil || i < 0 || i >= len(obj) {
				return nil, false
			}
			current = obj[i]
		default:
			return nil, false
		}
	}
	return current, true
}
func escape(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

// Validate reports a document's diagnostics without saving it. ctx bounds the
// execution-graph compilation inside, so a cancelled request stops it.
func (r *Repo) Validate(ctx context.Context, document string) ([]Diagnostic, error) {
	_, err := r.prepare(ctx, document)
	if invalid, ok := errors.AsType[*InvalidError](err); ok {
		return invalid.Diagnostics, nil
	}
	if err != nil {
		return nil, err
	}
	return []Diagnostic{}, nil
}
