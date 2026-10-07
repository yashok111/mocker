package statediagram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/schemamodel"
)

const ExecutionExtension = "x-mocker-state-diagrams-execution"

type FieldError struct{ Pointer, Message string }

func (e *FieldError) Error() string { return e.Pointer + ": " + e.Message }
func executionInvalid(pointer, message string) error {
	return &FieldError{Pointer: pointer, Message: message}
}

type Admission struct {
	MediaType string
	NoBody    bool
}
type Selection struct {
	TransitionID, FromStateID, ToStateID string
	ResponseStatus                       int
	Patch                                map[string]any
}
type TransitionError struct{ Code, Message, StateID string }

func (e *TransitionError) Error() string { return e.Message }

// Program owns its validated copy. Selection returns independent patches, so
// concurrent requests cannot change another request or the compiled diagram.
type Program struct {
	id, initial string
	entity      EntityBinding
	admission   Admission
	states      map[string]State
	values      map[string]string
	from        map[string]Transition
}

func (p *Program) ID() string            { return p.id }
func (p *Program) Entity() EntityBinding { return p.entity }
func (p *Program) Admission() Admission  { return p.admission }

func DecodeExecution(root map[string]any) (Envelope, error) {
	authoring := map[string]any{}
	if value, ok := root[ExecutionExtension]; ok {
		authoring[Extension] = value
	}
	env, err := Decode(authoring)
	if err != nil {
		return env, executionInvalid("/"+ExecutionExtension, err.Error())
	}
	return env, nil
}

// CompileExecution validates only applied copies. Incomplete descriptive
// authoring diagrams cannot prevent an unrelated applied lifecycle from running.
func CompileExecution(ctx context.Context, root map[string]any) (map[string]*Program, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	env, err := DecodeExecution(root)
	if err != nil {
		return nil, err
	}
	programs := map[string]*Program{}
	if len(env.Diagrams) == 0 {
		return programs, nil
	}
	encoded, err := jsonx.Marshal(root)
	if err != nil {
		return nil, err
	}
	doc, _, err := openapi.Load(encoded)
	if err != nil {
		return nil, executionInvalid("/"+ExecutionExtension, err.Error())
	}
	resolver := openapi.NewResolver(doc, openapi.DefaultRefBudget)
	rules, err := responserules.DecodeExecution(root)
	if err != nil {
		return nil, err
	}
	ruleClaims := map[string]bool{}
	for _, rule := range rules.Rules {
		if rule.Binding != nil {
			ruleClaims[overrides.OpKey(rule.Binding.Method, rule.Binding.Path)] = true
		}
	}

	compiler := executionCompiler{root: root, resolver: resolver, programs: programs, ruleClaims: ruleClaims, fields: map[string]string{}}
	for i, d := range env.Diagrams {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := compiler.addDiagram(d, fmt.Sprintf("/%s/diagrams/%d", ExecutionExtension, i)); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return programs, nil
}

type executionCompiler struct {
	root       map[string]any
	resolver   *openapi.Resolver
	programs   map[string]*Program
	ruleClaims map[string]bool
	fields     map[string]string
}

func (c *executionCompiler) addDiagram(d Diagram, prefix string) error {
	if d.Entity == nil {
		return executionInvalid(prefix+"/entity", "Для применения нужна привязка сущности диаграммы "+d.ID)
	}
	for _, diagnostic := range Validate(d, c.root) {
		if diagnostic.Severity == "error" {
			return executionInvalid(prefix, diagnostic.ElementID+": "+diagnostic.Message)
		}
	}
	fieldKey := d.Entity.Family + "\x00" + d.Entity.StateField
	if previous, ok := c.fields[fieldKey]; ok {
		return executionInvalid(prefix+"/entity", "Поле состояния семьи уже используется диаграммой "+previous)
	}
	c.fields[fieldKey] = d.ID
	states, values := stateMaps(d)
	installed := 0
	for j, tr := range d.Transitions {
		if tr.Binding == nil {
			continue
		}
		at := fmt.Sprintf("%s/transitions/%d", prefix, j)
		if err := c.addTransition(d, tr, at, states, values); err != nil {
			return err
		}
		installed++
	}
	if installed == 0 {
		return executionInvalid(prefix+"/transitions", "Нужен хотя бы один переход с HTTP-привязкой")
	}
	return nil
}
func (c *executionCompiler) addTransition(d Diagram, tr Transition, at string, states map[string]State, values map[string]string) error {
	admission, err := c.transitionAdmission(*d.Entity, tr, at)
	if err != nil {
		return err
	}
	key := overrides.OpKey(strings.ToUpper(tr.Binding.Method), tr.Binding.Path)
	if c.ruleClaims[key] {
		return executionInvalid(at+"/binding", "Операция уже занята применённым правилом ответа")
	}
	p := c.programs[key]
	if p != nil && p.id != d.ID {
		return executionInvalid(at+"/binding", "Операция уже занята диаграммой "+p.id)
	}
	if p == nil {
		p = &Program{id: d.ID, initial: d.InitialStateID, entity: *d.Entity, admission: admission, states: states, values: values, from: map[string]Transition{}}
		c.programs[key] = p
	}
	if p.admission != admission {
		return executionInvalid(at+"/responseStatus", "Переходы одной операции должны иметь одинаковое правило тела ответа")
	}
	if _, ok := p.from[tr.From]; ok {
		return executionInvalid(at+"/binding", "Повтор операции из одного исходного состояния")
	}
	p.from[tr.From] = tr
	return nil
}
func (c *executionCompiler) transitionAdmission(entity EntityBinding, tr Transition, at string) (Admission, error) {
	switch tr.Binding.Method {
	case "post", "put", "patch", "delete": //nolint:usestdlibvars // lowercase OpenAPI path-item keys; http.MethodPost is "POST"
	default:
		return Admission{}, executionInvalid(at+"/binding", "Для исполнения доступны POST, PUT, PATCH и DELETE")
	}
	if tr.ResponseStatus < 200 || tr.ResponseStatus > 599 {
		return Admission{}, executionInvalid(at+"/responseStatus", "Для исполнения нужен статус 200–599")
	}
	if !entityPath(entity, tr.Binding.Path) {
		return Admission{}, executionInvalid(at+"/binding", "Переход должен обращаться к ключу сущности под выбранной семьёй")
	}
	operation, err := boundOperation(c.root, *tr.Binding)
	if err != nil {
		return Admission{}, executionInvalid(at+"/binding", err.Error())
	}
	admission := Admission{MediaType: "application/json", NoBody: noBody(tr.ResponseStatus)}
	if !admission.NoBody {
		if err := admitResponse(c.resolver, operation, tr.ResponseStatus); err != nil {
			return Admission{}, executionInvalid(at+"/responseStatus", err.Error())
		}
	}
	return admission, nil
}

func noBody(status int) bool { return status == 204 || status == 205 || status == 304 }
func stateMaps(d Diagram) (map[string]State, map[string]string) {
	states := make(map[string]State, len(d.States))
	values := make(map[string]string, len(d.States))
	for _, s := range d.States {
		states[s.ID] = s
		values[effectiveValue(s)] = s.ID
	}
	return states, values
}
func entityPath(entity EntityBinding, path string) bool {
	canonical := router.CanonicalPath(path)
	prefix := entity.Family + "/{}"
	if canonical != prefix && !strings.HasPrefix(canonical, prefix+"/") {
		return false
	}
	segments := strings.Split(path, "/")
	index := len(strings.Split(entity.Family, "/"))
	return index < len(segments) && segments[index] == "{"+entity.KeyParam+"}"
}
func boundOperation(root map[string]any, b Binding) (map[string]any, error) {
	paths, _ := root["paths"].(map[string]any)
	item, ok := paths[b.Path]
	if !ok {
		return nil, fmt.Errorf("Связанная операция API не найдена: %s %s", b.Method, b.Path) //nolint:staticcheck // ST1005: operator-facing Russian sentence, shown verbatim
	}
	pointer := "/paths/" + strings.ReplaceAll(strings.ReplaceAll(b.Path, "~", "~0"), "/", "~1")
	nodes, diagnostics := schemamodel.PathItems(root, item, pointer)
	if len(diagnostics) > 0 {
		return nil, fmt.Errorf("%s", diagnostics[0].Message)
	}
	for _, occurrence := range schemamodel.PathItemOperations(nodes) {
		if occurrence.Method == b.Method {
			if operation, ok := occurrence.Value.(map[string]any); ok && operation != nil {
				return operation, nil
			}
		}
	}
	return nil, fmt.Errorf("Связанная операция API не найдена: %s %s", b.Method, b.Path) //nolint:staticcheck // ST1005: operator-facing Russian sentence, shown verbatim
}
func admitResponse(resolver *openapi.Resolver, operation map[string]any, status int) error {
	responses, _ := operation["responses"].(map[string]any)
	var response any
	found := false
	for _, selector := range []string{strconv.Itoa(status), fmt.Sprintf("%dXX", status/100), "default"} {
		if response, found = responses[selector]; found {
			break
		}
	}
	if !found {
		return fmt.Errorf("Статус ответа %d не описан в операции API", status) //nolint:staticcheck // ST1005: operator-facing Russian sentence, shown verbatim
	}
	resolved, err := resolver.ResolveNode(response)
	if err != nil {
		return fmt.Errorf("Ответ %d: %w", status, err) //nolint:staticcheck // ST1005: operator-facing Russian sentence, shown verbatim
	}
	object, _ := resolved.(map[string]any)
	content, _ := object["content"].(map[string]any)
	if media, ok := content["application/json"].(map[string]any); !ok || media == nil {
		return fmt.Errorf("Ответ %d должен предлагать application/json", status) //nolint:staticcheck // ST1005: operator-facing Russian sentence, shown verbatim
	}
	return nil
}
func currentState(initial string, entity *EntityBinding, values map[string]string, data map[string]any) (string, error) {
	if entity == nil {
		return initial, nil
	}
	value, present := data[entity.StateField]
	if !present {
		return initial, nil
	}
	stored, ok := value.(string)
	if ok {
		if state, found := values[stored]; found {
			return state, nil
		}
	}
	return "", &TransitionError{Code: "invalid_state", Message: "Сохранённое поле состояния содержит неизвестное или нестроковое значение"}
}
func (p *Program) Select(ctx context.Context, data map[string]any) (Selection, error) {
	if err := ctx.Err(); err != nil {
		return Selection{}, err
	}
	state, err := currentState(p.initial, &p.entity, p.values, data)
	if err != nil {
		return Selection{}, err
	}
	tr, found := p.from[state]
	return advance(ctx, state, tr, found, p.states, data, &p.entity)
}

// advance is shared by real entity selection and isolated simulation. Guards
// inspect the original object; the returned fixed patch is newly decoded.
func advance(ctx context.Context, state string, tr Transition, found bool, states map[string]State, data map[string]any, entity *EntityBinding) (Selection, error) {
	if err := ctx.Err(); err != nil {
		return Selection{}, err
	}
	conflict := func(code, message string) (Selection, error) {
		return Selection{}, &TransitionError{Code: code, Message: message, StateID: state}
	}
	if states[state].Terminal {
		return conflict("terminal_state", "Достигнуто конечное состояние")
	}
	if !found || tr.From != state {
		return conflict("transition_unavailable", "Переход недоступен из текущего состояния")
	}
	if !guardPasses(tr.Guard, data) {
		return conflict("guard_failed", "Условие перехода не выполнено")
	}
	patch, err := Object(tr.PatchJSON)
	if err != nil {
		return Selection{}, err
	}
	if entity != nil {
		patch[entity.StateField] = effectiveValue(states[tr.To])
	}
	if err := ctx.Err(); err != nil {
		return Selection{}, err
	}
	return Selection{TransitionID: tr.ID, FromStateID: state, ToStateID: tr.To, ResponseStatus: tr.ResponseStatus, Patch: patch}, nil
}
