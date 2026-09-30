package responserules

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

func (p *Program) HasEntities() bool {
	if p == nil {
		return false
	}
	for _, n := range p.nodes {
		if n.Entity != nil {
			return true
		}
	}
	return false
}

// EntityFamilies returns the applied graph's distinct resource targets.
// Callers own the sorted slice; it exposes no mutable program data.
func (p *Program) EntityFamilies() []string {
	var families []string
	if p != nil {
		for _, n := range p.nodes {
			if n.Entity != nil {
				families = append(families, n.Entity.Family)
			}
		}
	}
	slices.Sort(families)
	return slices.Compact(families)
}

func (p *Program) EntityAdmission() *EntityAdmission {
	if p == nil || p.admission == nil {
		return nil
	}
	return new(*p.admission)
}
func valuePointer(value any, pointer string) (any, bool) {
	if pointer == "" {
		return value, true
	}
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch current := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = current[part]
			if !ok {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(current) || strconv.Itoa(index) != part {
				return nil, false
			}
			value = current[index]
		default:
			return nil, false
		}
	}
	return value, true
}
func resolveValue(ctx context.Context, ref ValueRef, input EvaluationInput, results map[string]any) (any, error) {
	switch ref.Source {
	case "literal":
		return decodeBody(ctx, *ref.ValueJSON)
	case "path":
		if value, ok := input.Path[ref.Name]; ok {
			return value, nil
		}
	case "query":
		if values, ok := input.Request.Query[ref.Name]; ok && len(values) > 0 {
			return values[0], nil
		}
	case "header":
		if values := input.Request.Header.Values(ref.Name); len(values) > 0 {
			return values[0], nil
		}
	case "body":
		if input.Request.BodyOK {
			if value, ok := valuePointer(input.Request.Body, ref.Pointer); ok {
				return value, nil
			}
		}
	case "result":
		if result, ok := results[ref.NodeID]; ok {
			if value, exists := valuePointer(result, ref.Pointer); exists {
				return value, nil
			}
		}
	}
	return nil, invalid("", "значение источника или JSON Pointer отсутствует")
}
func scalarText(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case jsonx.Number:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	}
	return "", invalid("", "ключ и scope должны быть строкой, числом или boolean")
}

// JSON cloning both breaks aliases and preserves number tokens. Hosts are allowed
// to change input maps (Create writes the allocated id into its data argument).
func cloneValue(ctx context.Context, value any) (any, error) {
	data, err := jsonx.Marshal(value)
	if err != nil {
		return nil, err
	}
	return decodeJSONValue(ctx, string(data), MaxResultBytes)
}
func evaluateEntity(ctx context.Context, n Node, input EvaluationInput, options EvaluationOptions, results map[string]any) (EntityResult, error) {
	if options.Entities == nil {
		return EntityResult{}, invalid("", "нет исполнителя сущностей")
	}
	op := n.Entity
	target := EntityTarget{Family: op.Family}
	if op.Scope != nil {
		target.Scope = make([]string, len(*op.Scope))
		for i, ref := range *op.Scope {
			value, err := resolveValue(ctx, ref, input, results)
			if err != nil {
				return EntityResult{}, at(fmt.Sprintf("/scope/%d", i), err)
			}
			target.Scope[i], err = scalarText(value)
			if err != nil {
				return EntityResult{}, at(fmt.Sprintf("/scope/%d", i), err)
			}
		}
	}
	var key *string
	if op.Key != nil {
		value, err := resolveValue(ctx, *op.Key, input, results)
		if err != nil {
			return EntityResult{}, at("/key", err)
		}
		text, err := scalarText(value)
		if err != nil {
			return EntityResult{}, at("/key", err)
		}
		key = &text
	}
	var data map[string]any
	if op.Data != nil {
		value, err := resolveValue(ctx, *op.Data, input, results)
		if err != nil {
			return EntityResult{}, at("/data", err)
		}
		valueCopy, err := cloneValue(ctx, value)
		if err != nil {
			return EntityResult{}, at("/data", err)
		}
		var ok bool
		data, ok = valueCopy.(map[string]any)
		if !ok {
			return EntityResult{}, invalid("/data", "данные сущности должны быть JSON-объектом")
		}
	}
	if err := ctx.Err(); err != nil {
		return EntityResult{}, err
	}
	switch n.Type {
	case "entity_read":
		return options.Entities.Read(ctx, target, key)
	case "entity_create":
		return options.Entities.Create(ctx, target, data)
	default:
		return options.Entities.Update(ctx, target, *key, data)
	}
}

func recordEntityStep(ctx context.Context, n Node, nodePointer string, input EvaluationInput, options EvaluationOptions, results map[string]any, result *Simulation, step *Step) (string, error) {
	entity, err := evaluateEntity(ctx, n, input, options, results)
	if err != nil {
		return "", at(nodePointer+"/entity", err)
	}
	value, err := cloneValue(ctx, entity.Value)
	if err != nil {
		return "", at(nodePointer+"/entity", err)
	}
	results[n.ID] = value
	raw, err := jsonx.Marshal(value)
	if err != nil {
		return "", err
	}
	// Outputs cross the browser boundary as JSON text so its parser never rounds IDs.
	if result.Results == nil {
		result.Results = map[string]string{}
	}
	result.Results[n.ID] = string(raw)
	step.EntityFound = new(entity.Found)
	count := 0
	if items, ok := value.([]any); ok {
		count = len(items)
	} else if entity.Found {
		count = 1
	}
	step.EntityCount = &count
	if n.Type == "entity_read" && n.Entity.Operation == "get" || n.Type == "entity_update" {
		if entity.Found {
			return "found", nil
		}
		return "missing", nil
	}
	return "next", nil
}
func evaluatedResponse(ctx context.Context, n Node, nodePointer string, input EvaluationInput, results map[string]any) (*Response, error) {
	if n.Response == nil {
		return nil, nil
	}
	response := *n.Response
	response.Headers = append([]Field{}, response.Headers...)
	if response.BodyJSON != nil {
		response.BodyJSON = new(*response.BodyJSON)
	}
	if response.BodyFrom != nil {
		value, err := resolveValue(ctx, *response.BodyFrom, input, results)
		if err != nil {
			return nil, at(nodePointer+"/response/bodyFrom", err)
		}
		raw, err := jsonx.Marshal(value)
		if err != nil {
			return nil, err
		}
		body := string(raw)
		if _, err := decodeBody(ctx, body); err != nil {
			return nil, at(nodePointer+"/response/bodyFrom", err)
		}
		response.BodyJSON = &body
		response.BodyFrom = nil
	}
	return &response, nil
}
