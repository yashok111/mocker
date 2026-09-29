package scenarioexport

import (
	"bytes"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

var httpTemplate = regexp.MustCompile(`\{\{([^{}]+)\}\}`)
var httpPathParameter = regexp.MustCompile(`\{([^{}]+)\}`)
var httpHeaderName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
var httpVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,99}$`)

type httpBase struct{ Name, URL string }
type httpRequest struct {
	Name, Method, Path, Base string
	Execution                designscenario.StepExecution
}
type httpExport struct {
	Requests  []httpRequest
	Bases     []httpBase
	Variables designscenario.ExecutionValues
}
type savedOperation struct {
	Method, Path          string
	Root, Item, Operation map[string]any
}

func (s *Service) prepareHTTP(rev designscenario.Revision, format Format) (httpExport, []Diagnostic, error) {
	out := httpExport{Requests: []httpRequest{}, Bases: []httpBase{}, Variables: designscenario.ExecutionValues{}}
	if rev.Document.Execution != nil {
		maps.Copy(out.Variables, rev.Document.Execution.Variables)
	}
	ds := []Diagnostic{}
	add := func(code, severity, message string) {
		ds = append(ds, Diagnostic{Code: code, Severity: severity, Message: message})
	}
	if len(rev.Document.Fragments) > 0 {
		add("execution_fragments_unsupported", "error", "Экспорт исполнения alt/opt/loop не поддерживается")
	}
	available := map[string]bool{}
	for name := range out.Variables {
		available[name] = true
	}
	reserved := map[string]bool{}
	for name := range available {
		reserved[name] = true
	}
	for _, message := range rev.Document.Messages {
		if message.Execution != nil {
			for _, extraction := range message.Execution.Extract {
				reserved[extraction.Name] = true
			}
		}
	}
	dynamic := map[string]bool{}
	bases := map[string]string{}
	for i, m := range rev.Document.Messages {
		if err := s.CheckResponse(ds); err != nil {
			return out, nil, err
		}
		first := len(ds)
		stepAdd := func(code, severity, message string) { add(code, severity, message) }
		if m.Execution != nil && len(m.Execution.Bindings) > 0 {
			stepAdd("data_bindings_unsupported", "error",
				"Экспорт передачи данных между шагами пока не поддерживается; выполните сценарий в Mocker")
		}
		if m.Execution != nil && !m.Execution.Enabled {
			stepAdd("disabled_messages_omitted", "info", "Выключенный шаг пропущен")
		} else if m.Kind == "event" {
			stepAdd("event_execution_unsupported", "info", "Kafka event пропущен: Postman и cURL экспортируют только HTTP-запросы")
		} else if m.Kind != "request" || m.Operation == nil {
			stepAdd("descriptive_messages_omitted", "info", "Описательное сообщение пропущено")
			if m.Kind == "request" && m.Execution != nil {
				stepAdd("binding_missing", "error", "Для включённого HTTP-шагa укажите операцию API")
			}
		} else {
			index := slices.IndexFunc(rev.Document.Contracts, func(c designscenario.Contract) bool { return c.ID == m.Operation.ContractID })
			if index < 0 {
				stepAdd("binding_missing", "error", "Контракт сообщения отсутствует")
			} else {
				contract := rev.Document.Contracts[index]
				op, ok := findSavedOperation(contract.Document, m.Operation.OperationKey)
				if !ok {
					stepAdd("binding_missing", "error", "Операция сообщения отсутствует или неоднозначна")
				} else {
					if hasPendingForms(rev.FormDrafts, contract.ID) {
						stepAdd("api_forms_pending", "error", "Завершите редактирование API")
					}
					config := designscenario.StepExecution{Enabled: true, PathParams: designscenario.ExecutionValues{}, Query: designscenario.ExecutionValues{}, Headers: designscenario.ExecutionValues{}, Assertions: []designscenario.ExecutionAssertion{}, Extract: []designscenario.ExecutionExtraction{}}
					if m.Execution != nil {
						config = *m.Execution
					}
					resolved := resolveHTTPExecution(config, out.Variables, available, dynamic, format, s.maxBytes, stepAdd)
					validation := resolved
					if format == Postman {
						validation.Body = httpTemplate.ReplaceAllStringFunc(resolved.Body, func(token string) string {
							name := token[2 : len(token)-2]
							if dynamic[name] {
								return token
							}
							return out.Variables[name]
						})
					}
					validateHTTPInputs(op, validation, stepAdd)
					server := savedBaseURL(op)
					baseKey := m.ToID + "\x00" + contract.ID + "\x00" + server
					baseName, exists := bases[baseKey]
					if !exists {
						baseName = fmt.Sprintf("MOCKER_BASE_URL_%d", len(out.Bases)+1)
						for reserved[baseName] {
							baseName += "_"
						}
						reserved[baseName] = true
						bases[baseKey] = baseName
						out.Bases = append(out.Bases, httpBase{Name: baseName, URL: server})
						if server == "" {
							stepAdd("base_url_missing", "warning", "Заполните переменную "+baseName+" перед запуском: в сохранённом API нет абсолютного HTTP(S) server URL")
						}
					}
					if format == CURL {
						if len(config.Extract) > 0 {
							stepAdd("response_extraction_omitted", "warning", "cURL не извлекает переменные из ответов")
						}
						if len(config.Assertions) > 0 {
							stepAdd("json_assertions_omitted", "warning", "cURL не проверяет JSON-ответы")
						}
					} else {
						for _, a := range config.Assertions {
							if !safeAssertion(a.Equals) {
								stepAdd("assertion_number_unsupported", "error", "Postman поддерживает в проверках только целые JSON-числа от -9007199254740991 до 9007199254740991, кроме -0")
							}
						}
						if len(config.Assertions)+len(config.Extract) > 0 {
							stepAdd("postman_numeric_limits", "warning", "Postman остановит JSON-проверки и извлечения, если ответ содержит дробные числа, небезопасные целые числа или -0")
						}
					}
					out.Requests = append(out.Requests, httpRequest{Name: m.Label, Method: op.Method, Path: op.Path, Base: baseName, Execution: resolved})
					for _, e := range config.Extract {
						available[e.Name] = true
						dynamic[e.Name] = true
					}
				}
			}
		}
		for j := first; j < len(ds); j++ {
			ds[j].Target = &Target{Kind: "message", ID: m.ID}
			ds[j].Pointer = fmt.Sprintf("/messages/%d", i)
		}
	}
	if len(out.Requests) == 0 {
		add("http_requests_empty", "error", "В сценарии нет включённых HTTP-запросов с операцией API")
	}
	if err := s.CheckResponse(ds); err != nil {
		return out, nil, err
	}
	return out, ds, nil
}

func findSavedOperation(raw []byte, key string) (savedOperation, bool) {
	decoder := jsonx.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	root := map[string]any{}
	if decoder.Decode(&root) != nil {
		return savedOperation{}, false
	}
	var found savedOperation
	count := 0
	for _, operation := range designscenario.ContractOperations(root) {
		if operation.Key == key && key != "" {
			count++
			found = savedOperation{Method: strings.ToUpper(operation.Method), Path: operation.Path, Root: root, Item: operation.Item, Operation: operation.Operation}
		}
	}
	return found, count == 1
}

func resolveHTTPExecution(config designscenario.StepExecution, variables designscenario.ExecutionValues, available, dynamic map[string]bool, format Format, limit int64, add func(string, string, string)) designscenario.StepExecution {
	resolve := func(source string) string {
		var out strings.Builder
		remaining := source
		for remaining != "" {
			loc := httpTemplate.FindStringSubmatchIndex(remaining)
			if loc == nil {
				if int64(out.Len()+len(remaining)) > limit {
					add("input_too_large", "error", "Значение после подстановки превышает лимит экспорта")
					return ""
				}
				out.WriteString(remaining)
				break
			}
			name := remaining[loc[2]:loc[3]]
			if !available[name] {
				add("variable_missing", "error", "Укажите значение переменной "+name)
			}
			if format == CURL && dynamic[name] {
				add("dynamic_variable_unsupported", "error", "Переменная "+name+" зависит от ответа; используйте Postman")
			}
			value := remaining[loc[0]:loc[1]]
			if format == CURL {
				value = variables[name]
			}
			if strings.ContainsRune(variables[name], 0) {
				add("input_invalid", "error", "NUL в значении переменной не поддерживается")
			}
			if format == Postman && strings.Contains(variables[name], "{{") {
				add("template_value_unsupported", "error", "Postman не поддерживает вложенные шаблоны в значениях переменных")
			}
			if format == Postman && !httpVariableName.MatchString(name) {
				add("variable_invalid", "error", "Недопустимое имя переменной "+name)
			}
			if int64(out.Len()+loc[0]+len(value)) > limit {
				add("input_too_large", "error", "Значение после подстановки превышает лимит экспорта")
				return ""
			}
			out.WriteString(remaining[:loc[0]])
			out.WriteString(value)
			remaining = remaining[loc[1]:]
		}
		if strings.ContainsRune(out.String(), 0) {
			add("input_invalid", "error", "NUL в HTTP-данных не поддерживается")
		}
		return out.String()
	}
	for _, pair := range []struct {
		source designscenario.ExecutionValues
		target *designscenario.ExecutionValues
	}{{config.PathParams, &config.PathParams}, {config.Query, &config.Query}, {config.Headers, &config.Headers}} {
		*pair.target = designscenario.ExecutionValues{}
		for _, key := range slices.Sorted(maps.Keys(pair.source)) {
			if strings.ContainsRune(key, 0) {
				add("input_invalid", "error", "NUL в имени параметра не поддерживается")
			}
			(*pair.target)[key] = resolve(pair.source[key])
		}
	}
	config.Body = resolve(config.Body)
	for key, value := range config.Headers {
		concrete := value
		if format == Postman {
			concrete = httpTemplate.ReplaceAllStringFunc(value, func(token string) string { return variables[token[2:len(token)-2]] })
		}
		if !httpHeaderName.MatchString(key) || strings.ContainsFunc(concrete, func(r rune) bool { return (r < 32 && r != '\t') || r == 127 }) {
			add("header_invalid", "error", "HTTP-заголовок содержит недопустимое имя или управляющий символ")
		}
	}
	return config
}

func validateHTTPInputs(op savedOperation, config designscenario.StepExecution, add func(string, string, string)) {
	if strings.ContainsAny(op.Path, "\r\n\x00?#") {
		add("path_invalid", "error", "Недопустимый путь HTTP-операции")
	}
	for _, match := range httpPathParameter.FindAllStringSubmatch(op.Path, -1) {
		if _, ok := config.PathParams[match[1]]; !ok {
			add("path_parameter_missing", "error", "Укажите path-параметр "+match[1])
		}
	}
	parameters := map[string]map[string]any{}
	for _, container := range []map[string]any{op.Item, op.Operation} {
		if container["$ref"] != nil {
			add("http_reference_unsupported", "error", "Ссылки $ref в HTTP-операции не поддерживаются экспортом")
		}
		items, _ := container["parameters"].([]any)
		for _, item := range items {
			parameter, _ := item.(map[string]any)
			if parameter["$ref"] != nil {
				add("http_reference_unsupported", "error", "Ссылки $ref в параметрах не поддерживаются экспортом")
				continue
			}
			name, _ := parameter["name"].(string)
			in, _ := parameter["in"].(string)
			parameters[in+"\x00"+name] = parameter
		}
	}
	for _, key := range slices.Sorted(maps.Keys(parameters)) {
		p := parameters[key]
		name, _ := p["name"].(string)
		in, _ := p["in"].(string)
		var values designscenario.ExecutionValues
		switch in {
		case "path":
			values = config.PathParams
		case "query":
			values = config.Query
		case "header":
			values = config.Headers
		}
		_, exists := values[name]
		if in == "header" {
			for k := range values {
				exists = exists || strings.EqualFold(k, name)
			}
		}
		required, _ := p["required"].(bool)
		if required && !exists {
			add("required_input_missing", "error", "Укажите обязательный параметр "+in+": "+name)
		}
		if exists {
			schema, _ := p["schema"].(map[string]any)
			style, _ := p["style"].(string)
			unsupportedStyle := style != "" && ((in == "query" && style != "form") || (in != "query" && style != "simple"))
			if unsupportedParameterSchema(schema) || p["content"] != nil || unsupportedStyle || p["allowReserved"] == true {
				add("parameter_serialization_unsupported", "error", "Сложная сериализация параметра "+name+" не поддерживается")
			}
		}
	}
	hasContentType := false
	for key := range config.Headers {
		hasContentType = hasContentType || strings.EqualFold(key, "Content-Type")
	}
	if config.Body != "" && !hasContentType {
		add("content_type_missing", "warning", "Укажите Content-Type в заголовках шага: HTTP-клиент может выбрать свой тип тела")
	}
	body, _ := op.Operation["requestBody"].(map[string]any)
	if body["$ref"] != nil {
		add("http_reference_unsupported", "error", "Ссылка $ref для тела запроса не поддерживается")
	}
	if body["required"] == true && config.Body == "" {
		add("required_body_missing", "error", "Укажите обязательное тело запроса")
	}
	security, exists := op.Operation["security"]
	if !exists {
		security = op.Root["security"]
	}
	if requirements, ok := security.([]any); ok && len(requirements) > 0 {
		add("auth_manual", "warning", "Авторизация OpenAPI автоматически не создаётся; задайте необходимые заголовки и параметры шага")
	}
}

// The execution form stores scalar strings. References and composed schemas
// can resolve to arrays or objects whose OpenAPI wire serialization differs;
// without resolving them, treating their value as a scalar would change it.
func unsupportedParameterSchema(schema map[string]any) bool {
	if kind, ok := schema["type"].(string); ok && (kind == "array" || kind == "object") {
		return true
	}
	if _, union := schema["type"].([]any); union {
		return true
	}
	for _, keyword := range []string{
		"$ref", "$dynamicRef", "allOf", "anyOf", "oneOf", "if", "then", "else", "not",
		"items", "prefixItems", "properties", "additionalProperties", "unevaluatedProperties",
	} {
		if _, exists := schema[keyword]; exists {
			return true
		}
	}
	return false
}

func savedBaseURL(op savedOperation) string {
	servers, exists := op.Operation["servers"]
	if !exists {
		servers, exists = op.Item["servers"]
	}
	if !exists {
		servers = op.Root["servers"]
	}
	list, _ := servers.([]any)
	for _, value := range list {
		server, _ := value.(map[string]any)
		raw, _ := server["url"].(string)
		variables, _ := server["variables"].(map[string]any)
		raw = httpPathParameter.ReplaceAllStringFunc(raw, func(token string) string {
			v, _ := variables[token[1:len(token)-1]].(map[string]any)
			text, ok := v["default"].(string)
			if !ok {
				return token
			}
			return text
		})
		parsed, err := url.Parse(raw)
		if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && !strings.ContainsAny(raw, "{}\r\n\x00") {
			return strings.TrimRight(raw, "/")
		}
	}
	return ""
}

func safeAssertion(raw []byte) bool {
	decoder := jsonx.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var v any
	if decoder.Decode(&v) != nil {
		return false
	}
	var safe func(any) bool
	safe = func(v any) bool {
		switch v := v.(type) {
		case jsonx.Number:
			if string(v) == "-0" {
				return false
			}
			n, e := strconv.ParseInt(string(v), 10, 64)
			return e == nil && n >= -9007199254740991 && n <= 9007199254740991
		case []any:
			for _, x := range v {
				if !safe(x) {
					return false
				}
			}
		case map[string]any:
			for _, x := range v {
				if !safe(x) {
					return false
				}
			}
		}
		return true
	}
	return safe(v)
}

func concretePath(path string, values designscenario.ExecutionValues) string {
	return httpPathParameter.ReplaceAllStringFunc(path, func(token string) string { return url.PathEscape(values[token[1:len(token)-1]]) })
}
