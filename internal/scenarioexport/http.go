package scenarioexport

import (
	"bytes"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
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
	MessageID                string
	BindingTypes             map[string]string
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

// httpPlanner carries the state one HTTP export accumulates across steps:
// variable availability, which variables come from responses, and the base
// URL variable allocated per (target participant, contract, server).
type httpPlanner struct {
	s            *Service
	rev          designscenario.Revision
	format       Format
	out          httpExport
	ds           []Diagnostic
	bindingTypes map[string]map[string]string
	available    map[string]bool
	reserved     map[string]bool
	dynamic      map[string]bool
	bases        map[string]string
}

func (p *httpPlanner) add(code, severity, message string) {
	p.ds = append(p.ds, Diagnostic{Code: code, Severity: severity, Message: message})
}

func (s *Service) prepareHTTP(rev designscenario.Revision, format Format) (httpExport, []Diagnostic, error) {
	p := newHTTPPlanner(s, rev, format)
	for i, m := range rev.Document.Messages {
		if err := s.CheckResponse(p.ds); err != nil {
			return p.out, nil, err
		}
		first := len(p.ds)
		p.planMessage(m)
		for j := first; j < len(p.ds); j++ {
			p.ds[j].Target = &Target{Kind: "message", ID: m.ID}
			p.ds[j].Pointer = fmt.Sprintf("/messages/%d", i)
		}
	}
	if len(p.out.Requests) == 0 {
		p.add("http_requests_empty", "error", "В сценарии нет включённых HTTP-запросов с операцией API")
	}
	if err := s.CheckResponse(p.ds); err != nil {
		return p.out, nil, err
	}
	return p.out, p.ds, nil
}

func newHTTPPlanner(s *Service, rev designscenario.Revision, format Format) *httpPlanner {
	p := &httpPlanner{
		s: s, rev: rev, format: format, ds: []Diagnostic{},
		out:     httpExport{Requests: []httpRequest{}, Bases: []httpBase{}, Variables: designscenario.ExecutionValues{}},
		dynamic: map[string]bool{}, bases: map[string]string{},
	}
	if rev.Document.Execution != nil {
		maps.Copy(p.out.Variables, rev.Document.Execution.Variables)
	}
	if len(rev.Document.Fragments) > 0 {
		p.add("execution_fragments_unsupported", "error", "Экспорт исполнения alt/opt/loop не поддерживается")
	}
	if format == Postman {
		var bindingDiagnostics []Diagnostic
		p.bindingTypes, bindingDiagnostics = postmanBindingPreflight(rev.Document)
		p.ds = append(p.ds, bindingDiagnostics...)
	}
	p.available = map[string]bool{}
	for name := range p.out.Variables {
		p.available[name] = true
	}
	// Base URL variables must not collide with a declared or extracted name.
	p.reserved = map[string]bool{}
	for name := range p.available {
		p.reserved[name] = true
	}
	for _, message := range rev.Document.Messages {
		if message.Execution != nil {
			for _, extraction := range message.Execution.Extract {
				p.reserved[extraction.Name] = true
			}
		}
	}
	return p
}

// planMessage decides whether a step becomes a request, and diagnoses why not.
func (p *httpPlanner) planMessage(m designscenario.Message) {
	if p.format == CURL && m.Kind == "request" && m.Execution != nil && m.Execution.Enabled && len(m.Execution.Bindings) > 0 {
		p.add("data_bindings_unsupported", "error",
			"cURL не поддерживает передачу данных между шагами; используйте Postman или выполните сценарий в Mocker")
	}
	switch {
	case m.Execution != nil && !m.Execution.Enabled:
		p.add("disabled_messages_omitted", "info", "Выключенный шаг пропущен")
	case m.Kind == "event":
		p.add("event_execution_unsupported", "info", "Kafka event пропущен: Postman и cURL экспортируют только HTTP-запросы")
	case m.Kind != "request" || m.Operation == nil:
		p.add("descriptive_messages_omitted", "info", "Описательное сообщение пропущено")
		if m.Kind == "request" && m.Execution != nil {
			p.add("binding_missing", "error", "Для включённого HTTP-шагa укажите операцию API")
		}
	default:
		p.planOperationStep(m)
	}
}

func (p *httpPlanner) planOperationStep(m designscenario.Message) {
	index := slices.IndexFunc(p.rev.Document.Contracts, func(c designscenario.Contract) bool { return c.ID == m.Operation.ContractID })
	if index < 0 {
		p.add("binding_missing", "error", "Контракт сообщения отсутствует")
		return
	}
	contract := p.rev.Document.Contracts[index]
	op, ok := findSavedOperation(contract.Document, m.Operation.OperationKey)
	if !ok {
		p.add("binding_missing", "error", "Операция сообщения отсутствует или неоднозначна")
		return
	}
	if hasPendingForms(p.rev.FormDrafts, contract.ID) {
		p.add("api_forms_pending", "error", "Завершите редактирование API")
	}
	p.planRequest(m, contract, op)
}

func (p *httpPlanner) planRequest(m designscenario.Message, contract designscenario.Contract, op savedOperation) {
	config := designscenario.StepExecution{Enabled: true, PathParams: designscenario.ExecutionValues{}, Query: designscenario.ExecutionValues{}, Headers: designscenario.ExecutionValues{}, Assertions: []designscenario.ExecutionAssertion{}, Extract: []designscenario.ExecutionExtraction{}}
	if m.Execution != nil {
		config = *m.Execution
	}
	if p.format == Postman && len(config.Bindings) > 0 {
		config = omitBoundHTTPInputs(config, p.add)
	}
	resolved := resolveHTTPExecution(config, p.out.Variables, p.available, p.dynamic, p.format, p.s.maxBytes, p.add)
	validateHTTPInputs(op, p.validationView(config, resolved), p.add)
	baseName := p.baseName(m, contract, savedBaseURL(op))
	if p.format == CURL {
		if len(config.Extract) > 0 {
			p.add("response_extraction_omitted", "warning", "cURL не извлекает переменные из ответов")
		}
		if len(config.Assertions) > 0 {
			p.add("json_assertions_omitted", "warning", "cURL не проверяет JSON-ответы")
		}
	}
	p.out.Requests = append(p.out.Requests, httpRequest{Name: m.Label, Method: op.Method, Path: op.Path, Base: baseName, MessageID: m.ID, BindingTypes: p.bindingTypes[m.ID], Execution: resolved})
	for _, e := range config.Extract {
		p.available[e.Name] = true
		p.dynamic[e.Name] = true
	}
}

// validationView is what the operation's inputs are checked against: Postman
// keeps response-derived templates in the body, so only static variables are
// substituted, and bound inputs count as present.
func (p *httpPlanner) validationView(config, resolved designscenario.StepExecution) designscenario.StepExecution {
	validation := resolved
	if p.format == Postman {
		validation.Body = httpTemplate.ReplaceAllStringFunc(resolved.Body, func(token string) string {
			name := token[2 : len(token)-2]
			if p.dynamic[name] {
				return token
			}
			return p.out.Variables[name]
		})
		if len(config.Bindings) > 0 {
			validation = bindingHTTPValidation(validation)
		}
	}
	return validation
}

// baseName reuses or allocates the base URL variable for this target.
func (p *httpPlanner) baseName(m designscenario.Message, contract designscenario.Contract, server string) string {
	baseKey := m.ToID + "\x00" + contract.ID + "\x00" + server
	if baseName, exists := p.bases[baseKey]; exists {
		return baseName
	}
	baseName := fmt.Sprintf("MOCKER_BASE_URL_%d", len(p.out.Bases)+1)
	for p.reserved[baseName] {
		baseName += "_"
	}
	p.reserved[baseName] = true
	p.bases[baseKey] = baseName
	p.out.Bases = append(p.out.Bases, httpBase{Name: baseName, URL: server})
	if server == "" {
		p.add("base_url_missing", "warning", "Заполните переменную "+baseName+" перед запуском: в сохранённом API нет абсолютного HTTP(S) server URL")
	}
	return baseName
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

// httpTemplateResolver substitutes {{name}} templates for one step: cURL gets
// the concrete value, Postman keeps the template for its own variables.
type httpTemplateResolver struct {
	variables          designscenario.ExecutionValues
	available, dynamic map[string]bool
	format             Format
	limit              int64
	add                func(string, string, string)
}

func (r httpTemplateResolver) resolve(source string) string {
	var out strings.Builder
	remaining := source
	for remaining != "" {
		loc := httpTemplate.FindStringSubmatchIndex(remaining)
		if loc == nil {
			if int64(out.Len()+len(remaining)) > r.limit {
				r.add("input_too_large", "error", "Значение после подстановки превышает лимит экспорта")
				return ""
			}
			out.WriteString(remaining)
			break
		}
		name := remaining[loc[2]:loc[3]]
		r.checkVariable(name)
		value := remaining[loc[0]:loc[1]]
		if r.format == CURL {
			value = r.variables[name]
		}
		if int64(out.Len()+loc[0]+len(value)) > r.limit {
			r.add("input_too_large", "error", "Значение после подстановки превышает лимит экспорта")
			return ""
		}
		out.WriteString(remaining[:loc[0]])
		out.WriteString(value)
		remaining = remaining[loc[1]:]
	}
	if strings.ContainsRune(out.String(), 0) {
		r.add("input_invalid", "error", "NUL в HTTP-данных не поддерживается")
	}
	return out.String()
}

func (r httpTemplateResolver) checkVariable(name string) {
	if !r.available[name] {
		r.add("variable_missing", "error", "Укажите значение переменной "+name)
	}
	if r.format == CURL && r.dynamic[name] {
		r.add("dynamic_variable_unsupported", "error", "Переменная "+name+" зависит от ответа; используйте Postman")
	}
	if strings.ContainsRune(r.variables[name], 0) {
		r.add("input_invalid", "error", "NUL в значении переменной не поддерживается")
	}
	if r.format == Postman && strings.Contains(r.variables[name], "{{") {
		r.add("template_value_unsupported", "error", "Postman не поддерживает вложенные шаблоны в значениях переменных")
	}
	if r.format == Postman && !httpVariableName.MatchString(name) {
		r.add("variable_invalid", "error", "Недопустимое имя переменной "+name)
	}
}

func resolveHTTPExecution(config designscenario.StepExecution, variables designscenario.ExecutionValues, available, dynamic map[string]bool, format Format, limit int64, add func(string, string, string)) designscenario.StepExecution {
	r := httpTemplateResolver{variables: variables, available: available, dynamic: dynamic, format: format, limit: limit, add: add}
	for _, pair := range []struct {
		source designscenario.ExecutionValues
		target *designscenario.ExecutionValues
	}{{config.PathParams, &config.PathParams}, {config.Query, &config.Query}, {config.Headers, &config.Headers}} {
		*pair.target = designscenario.ExecutionValues{}
		for _, key := range slices.Sorted(maps.Keys(pair.source)) {
			if strings.ContainsRune(key, 0) {
				add("input_invalid", "error", "NUL в имени параметра не поддерживается")
			}
			(*pair.target)[key] = r.resolve(pair.source[key])
		}
	}
	config.Body = r.resolve(config.Body)
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
	parameters := operationParameters(op, add)
	for _, key := range slices.Sorted(maps.Keys(parameters)) {
		checkParameterInput(parameters[key], config, add)
	}
	checkRequestBodyInput(op, config, add)
	security, exists := op.Operation["security"]
	if !exists {
		security = op.Root["security"]
	}
	if requirements, ok := security.([]any); ok && len(requirements) > 0 {
		add("auth_manual", "warning", "Авторизация OpenAPI автоматически не создаётся; задайте необходимые заголовки и параметры шага")
	}
}

// operationParameters merges path-item and operation parameters keyed by
// location and name, the operation's entry overriding the path item's.
func operationParameters(op savedOperation, add func(string, string, string)) map[string]map[string]any {
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
	return parameters
}

func checkParameterInput(p map[string]any, config designscenario.StepExecution, add func(string, string, string)) {
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

func checkRequestBodyInput(op savedOperation, config designscenario.StepExecution, add func(string, string, string)) {
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

func concretePath(path string, values designscenario.ExecutionValues) string {
	return httpPathParameter.ReplaceAllStringFunc(path, func(token string) string { return url.PathEscape(values[token[1:len(token)-1]]) })
}
