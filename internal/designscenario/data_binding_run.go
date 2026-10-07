package designscenario

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/jsonx"
)

func (e *runEngine) resolveBindingRequest(index int, message Message, config *StepExecution) (StepRequest, []BindingResult, error) {
	clean := withoutBindingTargets(config)
	request, err := resolveRunRequest(e.report.RevisionID, message.ID, &clean, e.report.Variables)
	if err != nil {
		return StepRequest{}, nil, err
	}
	if len(config.Bindings) == 0 {
		return request, nil, nil
	}
	targetSchema := e.bindingTargetSchema(message.ID)
	results := make([]BindingResult, 0, len(config.Bindings))
	body := bindingBody{}
	for _, binding := range config.Bindings {
		resolved, err := e.readBindingSource(index, binding)
		if err == nil {
			err = applyBinding(&request, &body, binding, resolved, targetSchema.targetType(binding.Target))
		}
		if err != nil {
			return StepRequest{}, nil, fmt.Errorf("binding %q: %s", binding.ID, err.Error())
		}
		results = append(results, resolved.result(binding))
	}
	if body.decoded {
		encoded, err := jsonx.Marshal(body.value)
		if err != nil {
			return StepRequest{}, nil, err
		}
		if len(encoded) > MaxExecutionBody {
			return StepRequest{}, nil, fmt.Errorf("resolved body exceeds 1 MiB")
		}
		request.Body = string(encoded)
	}
	return request, results, nil
}

// withoutBindingTargets drops the authored values a binding will overwrite,
// so a static placeholder never reaches the resolved request.
func withoutBindingTargets(config *StepExecution) StepExecution {
	clean := *config
	clean.PathParams, clean.Query, clean.Headers = maps.Clone(config.PathParams), maps.Clone(config.Query), maps.Clone(config.Headers)
	for _, b := range config.Bindings {
		switch b.Target.Kind {
		case "path":
			delete(clean.PathParams, b.Target.Name)
		case "query":
			delete(clean.Query, b.Target.Name)
		case "header":
			for key := range clean.Headers {
				if strings.EqualFold(key, b.Target.Name) {
					delete(clean.Headers, key)
				}
			}
		}
	}
	return clean
}

func (e *runEngine) bindingTargetSchema(messageID string) bindingSchema {
	if e.bindingSchemas == nil {
		e.bindingSchemas = bindingSchemas(e.report.Document)
	}
	for i, m := range e.report.Document.Messages {
		if m.ID == messageID {
			return e.bindingSchemas[i]
		}
	}
	return bindingSchema{}
}

// resolvedBinding is one binding's source value, read and transformed.
type resolvedBinding struct {
	source      *StepResult
	value       any
	encoded     []byte
	actual      string
	transformed *string
}

// readBindingSource returns the error text without the binding prefix; the
// caller adds it once for every failure of the binding.
func (e *runEngine) readBindingSource(index int, binding DataBinding) (resolvedBinding, error) {
	source := e.bindingSource(binding.SourceMessageID, e.report.Steps[index].Iterations)
	if source == nil {
		return resolvedBinding{}, errors.New("successful source occurrence is unavailable")
	}
	if !jsonx.Valid([]byte(source.Response.Body)) {
		return resolvedBinding{}, errors.New("source response is not valid JSON")
	}
	value, err := decodeJSONValue([]byte(source.Response.Body))
	if err != nil {
		return resolvedBinding{}, errors.New("source response is not valid JSON")
	}
	value, exists := runReadPointer(value, binding.SourcePointer)
	if !exists {
		return resolvedBinding{}, errors.New("source JSON Pointer is missing")
	}
	encoded, err := jsonx.Marshal(value)
	if err != nil {
		return resolvedBinding{}, err
	}
	out := resolvedBinding{source: source, value: value, encoded: encoded, actual: bindingValueType(value)}
	if len(binding.Transforms) > 0 {
		out.value, out.actual, err = applyBindingTransforms(value, binding.Transforms)
		if err != nil {
			return resolvedBinding{}, err
		}
		transformed, err := jsonx.Marshal(out.value)
		if err != nil {
			return resolvedBinding{}, errors.New("преобразованное значение нельзя кодировать как JSON")
		}
		out.transformed = new(string(transformed))
	}
	return out, nil
}

func (r resolvedBinding) result(binding DataBinding) BindingResult {
	occurrence := r.source.Occurrence
	if occurrence == 0 {
		occurrence = 1
	}
	return BindingResult{BindingID: binding.ID, SourceMessageID: binding.SourceMessageID, SourcePointer: binding.SourcePointer, SourceOccurrence: occurrence, SourceIterations: slices.Clone(r.source.Iterations), Target: binding.Target, ValueJSON: string(r.encoded), TransformedValueJSON: r.transformed}
}

// bindingBody is the request body, decoded at most once and only when a body
// binding first needs it.
type bindingBody struct {
	value   any
	decoded bool
}

func applyBinding(request *StepRequest, body *bindingBody, binding DataBinding, resolved resolvedBinding, want string) error {
	if binding.Target.Kind != "body" {
		text, err := bindingText(binding, resolved, want)
		if err != nil {
			return err
		}
		switch binding.Target.Kind {
		case "path":
			request.PathParams[binding.Target.Name] = text
		case "query":
			request.Query[binding.Target.Name] = text
		case "header":
			request.Headers[binding.Target.Name] = text
		default:
			return errors.New("invalid target kind")
		}
		return nil
	}
	if !compatibleBindingTypes(resolved.actual, want) {
		return errors.New(bindingTypeError(resolved.actual, want))
	}
	if !body.decoded {
		// Whole-body replacement also accepts an absent request body.
		if binding.Target.Pointer != "" || strings.TrimSpace(request.Body) != "" {
			if !jsonx.Valid([]byte(request.Body)) {
				return errors.New("request body is not valid JSON")
			}
			decoded, err := decodeJSONValue([]byte(request.Body))
			if err != nil {
				return errors.New("request body is not valid JSON")
			}
			body.value = decoded
		}
		body.decoded = true
	}
	written, err := writeBindingPointer(body.value, binding.Target.Pointer, resolved.value)
	body.value = written
	return err
}

// bindingText renders a scalar for a path, query or header target.
func bindingText(binding DataBinding, resolved resolvedBinding, want string) (string, error) {
	actual := resolved.actual
	if actual == "null" || actual == "object" || actual == "array" {
		return "", errors.New("text targets require a non-null scalar")
	}
	if binding.Prefix != "" && want != "unknown" && want != "string" {
		return "", errors.New("prefix requires a string target")
	}
	if want != "string" && !compatibleBindingTypes(actual, want) {
		return "", errors.New(bindingTypeError(actual, want))
	}
	text, ok := resolved.value.(string)
	if !ok {
		if resolved.transformed != nil {
			text = *resolved.transformed
		} else {
			text = string(resolved.encoded)
		}
	}
	text = binding.Prefix + text
	if utf8.RuneCountInString(text) > maxText {
		return "", errors.New("resolved parameter is too large")
	}
	return text, nil
}
func (e *runEngine) bindingSource(messageID string, path []LoopIteration) *StepResult {
	var best *StepResult
	for i := range e.report.Steps {
		step := &e.report.Steps[i]
		if step.MessageID != messageID || step.Status != "passed" || step.Response == nil {
			continue
		}
		matches := true
		for _, iteration := range step.Iterations {
			if !slices.Contains(path, iteration) {
				matches = false
				break
			}
		}
		if matches && (best == nil || step.Occurrence > best.Occurrence) {
			best = step
		}
	}
	return best
}
func bindingValueType(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case jsonx.Number:
		_, exponent := normalizeRunNumber(string(v))
		if exponent.Sign() >= 0 {
			return "integer"
		}
		return "number"
	default:
		return "unknown"
	}
}
func writeBindingPointer(root any, pointer string, value any) (any, error) {
	if !validExecutionPointer(pointer) {
		return nil, fmt.Errorf("invalid target JSON Pointer")
	}
	if pointer == "" {
		return value, nil
	}
	slash := strings.LastIndexByte(pointer, '/')
	parentPointer, token := pointer[:slash], pointer[slash+1:]
	parent, ok := runReadPointer(root, parentPointer)
	if !ok {
		return nil, fmt.Errorf("target intermediate container is missing")
	}
	token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
	switch node := parent.(type) {
	case map[string]any:
		node[token] = value
	case []any:
		// Reuse pointer reading's canonical-index checks before indexing.
		_, ok := runReadPointer(node, "/"+escapePointer(token))
		if !ok {
			return nil, fmt.Errorf("target array index is missing")
		}
		index := 0
		for _, digit := range token {
			index = index*10 + int(digit-'0')
		}
		node[index] = value
	default:
		return nil, fmt.Errorf("target parent must be an object or array")
	}
	return root, nil
}
