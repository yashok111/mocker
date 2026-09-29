package designscenario

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/jsonx"
)

func (e *runEngine) resolveBindingRequest(index int, message Message, config *StepExecution) (StepRequest, []BindingResult, error) {
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
	request, err := resolveRunRequest(e.report.RevisionID, message.ID, &clean, e.report.Variables)
	if err != nil {
		return StepRequest{}, nil, err
	}
	if len(config.Bindings) == 0 {
		return request, nil, nil
	}
	if e.bindingSchemas == nil {
		e.bindingSchemas = bindingSchemas(e.report.Document)
	}
	schemas := e.bindingSchemas
	targetSchema := bindingSchema{}
	for i, m := range e.report.Document.Messages {
		if m.ID == message.ID {
			targetSchema = schemas[i]
			break
		}
	}
	results := make([]BindingResult, 0, len(config.Bindings))
	var body any
	bodyDecoded := false
	for _, binding := range config.Bindings {
		fail := func(reason string) (StepRequest, []BindingResult, error) {
			return StepRequest{}, nil, fmt.Errorf("binding %q: %s", binding.ID, reason)
		}
		source := e.bindingSource(binding.SourceMessageID, e.report.Steps[index].Iterations)
		if source == nil {
			return fail("successful source occurrence is unavailable")
		}
		if !jsonx.Valid([]byte(source.Response.Body)) {
			return fail("source response is not valid JSON")
		}
		value, err := decodeJSONValue([]byte(source.Response.Body))
		if err != nil {
			return fail("source response is not valid JSON")
		}
		value, exists := runReadPointer(value, binding.SourcePointer)
		if !exists {
			return fail("source JSON Pointer is missing")
		}
		encoded, err := jsonx.Marshal(value)
		if err != nil {
			return fail(err.Error())
		}
		actual, want := bindingValueType(value), targetSchema.targetType(binding.Target)
		if binding.Target.Kind != "body" {
			if actual == "null" || actual == "object" || actual == "array" {
				return fail("text targets require a non-null scalar")
			}
			if binding.Prefix != "" && want != "unknown" && want != "string" {
				return fail("prefix requires a string target")
			}
			if want != "string" && !compatibleBindingTypes(actual, want) {
				return fail(bindingTypeError(actual, want))
			}
			text, ok := value.(string)
			if !ok {
				text = string(encoded)
			}
			text = binding.Prefix + text
			if utf8.RuneCountInString(text) > maxText {
				return fail("resolved parameter is too large")
			}
			switch binding.Target.Kind {
			case "path":
				request.PathParams[binding.Target.Name] = text
			case "query":
				request.Query[binding.Target.Name] = text
			case "header":
				request.Headers[binding.Target.Name] = text
			default:
				return fail("invalid target kind")
			}
		} else {
			if !compatibleBindingTypes(actual, want) {
				return fail(bindingTypeError(actual, want))
			}
			if !bodyDecoded {
				// Whole-body replacement also accepts an absent request body.
				if binding.Target.Pointer != "" || strings.TrimSpace(request.Body) != "" {
					if !jsonx.Valid([]byte(request.Body)) {
						return fail("request body is not valid JSON")
					}
					body, err = decodeJSONValue([]byte(request.Body))
					if err != nil {
						return fail("request body is not valid JSON")
					}
				}
				bodyDecoded = true
			}
			body, err = writeBindingPointer(body, binding.Target.Pointer, value)
			if err != nil {
				return fail(err.Error())
			}
		}
		occurrence := source.Occurrence
		if occurrence == 0 {
			occurrence = 1
		}
		results = append(results, BindingResult{BindingID: binding.ID, SourceMessageID: binding.SourceMessageID, SourcePointer: binding.SourcePointer, SourceOccurrence: occurrence, SourceIterations: slices.Clone(source.Iterations), Target: binding.Target, ValueJSON: string(encoded)})
	}
	if bodyDecoded {
		encoded, err := jsonx.Marshal(body)
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
