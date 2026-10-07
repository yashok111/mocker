package designscenario

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/jsonx"
)

type DataBindingTarget struct {
	Kind    string `json:"kind"`
	Name    string `json:"name,omitempty"`
	Pointer string `json:"pointer,omitempty"`
}

// MarshalJSON preserves the required empty pointer for a whole-body target.
func (t DataBindingTarget) MarshalJSON() ([]byte, error) {
	if t.Kind == "body" {
		return jsonx.Marshal(struct {
			Kind    string `json:"kind"`
			Pointer string `json:"pointer"`
			Name    string `json:"name,omitempty"`
		}{t.Kind, t.Pointer, t.Name})
	}
	type wire DataBindingTarget
	return jsonx.Marshal(wire(t))
}
func (t *DataBindingTarget) UnmarshalJSON(data []byte) error {
	var fields map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &fields); err != nil {
		return err
	}
	var kind string
	if err := jsonx.Unmarshal(fields["kind"], &kind); err != nil {
		return err
	}
	required := []string{"kind", "name"}
	forbidden := "pointer"
	if kind == "body" {
		required = []string{"kind", "pointer"}
		forbidden = "name"
	} else if kind != "path" && kind != "query" && kind != "header" {
		return fmt.Errorf("invalid binding target kind")
	}
	if _, ok := fields[forbidden]; ok {
		return fmt.Errorf("binding target field %q is not allowed", forbidden)
	}
	type wire DataBindingTarget
	var out wire
	if err := decodeExecutionObject(data, &out, required...); err != nil {
		return err
	}
	*t = DataBindingTarget(out)
	return nil
}

type DataBinding struct {
	ID              string                 `json:"id"`
	SourceMessageID string                 `json:"sourceMessageId"`
	SourcePointer   string                 `json:"sourcePointer"`
	Target          DataBindingTarget      `json:"target"`
	Prefix          string                 `json:"prefix,omitempty"`
	Transforms      []DataBindingTransform `json:"transforms,omitempty"`
}

const MaxBindingTransforms = 8

type DataBindingTransform struct {
	Kind string `json:"kind"`
}

func validBindingTransformKind(kind string) bool {
	switch kind {
	case "trim", "lower", "upper", "to_string", "to_number", "to_integer":
		return true
	}
	return false
}

func (transform *DataBindingTransform) UnmarshalJSON(data []byte) error {
	type wire DataBindingTransform
	var decoded wire
	if err := decodeExecutionObject(data, &decoded, "kind"); err != nil {
		return err
	}
	if !validBindingTransformKind(decoded.Kind) {
		return fmt.Errorf("неизвестный вид преобразования %q", decoded.Kind)
	}
	*transform = DataBindingTransform(decoded)
	return nil
}

func (b *DataBinding) UnmarshalJSON(data []byte) error {
	type wire DataBinding
	var out wire
	var fields map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &fields); err != nil {
		return err
	}
	if raw, ok := fields["prefix"]; ok && isJSONNull(raw) {
		return fmt.Errorf("binding prefix cannot be null")
	}
	if raw, ok := fields["transforms"]; ok && isJSONNull(raw) {
		return fmt.Errorf("преобразования не могут быть null")
	}
	if err := decodeExecutionObject(data, &out, "id", "sourceMessageId", "sourcePointer", "target"); err != nil {
		return err
	}
	if len(out.Transforms) > MaxBindingTransforms {
		return fmt.Errorf("допускается не более %d преобразований", MaxBindingTransforms)
	}
	*b = DataBinding(out)
	return nil
}

type BindingResult struct {
	BindingID            string            `json:"bindingId"`
	SourceMessageID      string            `json:"sourceMessageId"`
	SourcePointer        string            `json:"sourcePointer"`
	SourceOccurrence     int               `json:"sourceOccurrence"`
	SourceIterations     []LoopIteration   `json:"sourceIterations,omitempty"`
	Target               DataBindingTarget `json:"target"`
	ValueJSON            string            `json:"valueJson"`
	TransformedValueJSON *string           `json:"transformedValueJson,omitempty"`
}

func checkDataBindings(pointer string, bindings []DataBinding, add func(string, string)) {
	if len(bindings) > MaxExecutionEntries {
		add(pointer, "must contain at most 100 bindings")
	}
	ids := map[string]bool{}
	for i, b := range bindings {
		p := fmt.Sprintf("%s/%d", pointer, i)
		if !runIDPattern.MatchString(b.ID) || ids[b.ID] {
			add(p+"/id", "invalid or duplicate binding ID")
		}
		ids[b.ID] = true
		if b.SourceMessageID == "" || utf8.RuneCountInString(b.SourceMessageID) > maxText {
			add(p+"/sourceMessageId", "invalid source message ID")
		}
		if !validExecutionPointer(b.SourcePointer) {
			add(p+"/sourcePointer", "invalid JSON Pointer")
		}
		checkDataBindingTarget(p, b, add)
		if utf8.RuneCountInString(b.Prefix) > maxText {
			add(p+"/prefix", "prefix is too long")
		}
		if len(b.Transforms) > MaxBindingTransforms {
			add(p+"/transforms", "допускается не более 8 преобразований")
		}
		for j, transform := range b.Transforms {
			if !validBindingTransformKind(transform.Kind) {
				add(fmt.Sprintf("%s/transforms/%d/kind", p, j), "неизвестный вид преобразования")
			}
		}
		for _, prior := range bindings[:i] {
			if bindingTargetsOverlap(prior.Target, b.Target) {
				add(p+"/target", "duplicate or overlapping binding target")
				break
			}
		}
	}
}

// checkDataBindingTarget admits a name for text targets and a pointer (and no
// prefix) for body targets.
func checkDataBindingTarget(p string, b DataBinding, add func(string, string)) {
	switch b.Target.Kind {
	case "path", "query", "header":
		if b.Target.Name == "" || utf8.RuneCountInString(b.Target.Name) > 256 {
			add(p+"/target/name", "invalid target name")
		}
		if b.Target.Pointer != "" {
			add(p+"/target/pointer", "pointer is only allowed for body targets")
		}
	case "body":
		if b.Target.Name != "" {
			add(p+"/target/name", "name is not allowed for body targets")
		}
		if !validExecutionPointer(b.Target.Pointer) {
			add(p+"/target/pointer", "invalid JSON Pointer")
		}
		if b.Prefix != "" {
			add(p+"/prefix", "prefix is only allowed for text targets")
		}
	default:
		add(p+"/target/kind", "invalid target kind")
	}
}
func bindingTargetsOverlap(a, b DataBindingTarget) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Kind == "body" {
		return a.Pointer == b.Pointer || strings.HasPrefix(a.Pointer, b.Pointer+"/") || strings.HasPrefix(b.Pointer, a.Pointer+"/")
	}
	if a.Kind == "header" {
		return strings.EqualFold(a.Name, b.Name)
	}
	return a.Name == b.Name
}
func applyDataBindingCommand(document *Document, command Command) error {
	m := findMessage(document.Messages, command.MessageID)
	if m == nil {
		return invalidAt("/messageId", "message does not exist")
	}
	if command.Type == "remove_data_binding" {
		if m.Execution != nil {
			for i, b := range m.Execution.Bindings {
				if b.ID == command.ID {
					m.Execution.Bindings = slices.Delete(m.Execution.Bindings, i, i+1)
					return nil
				}
			}
		}
		return invalidAt("/id", "binding does not exist")
	}
	if command.Binding == nil {
		return invalidAt("/binding", "binding is required")
	}
	if m.Execution == nil {
		m.Execution = &StepExecution{Enabled: true, PathParams: ExecutionValues{}, Query: ExecutionValues{}, Headers: ExecutionValues{}, Assertions: []ExecutionAssertion{}, Extract: []ExecutionExtraction{}}
	}
	for i, b := range m.Execution.Bindings {
		if b.ID == command.Binding.ID {
			m.Execution.Bindings[i] = *command.Binding
			return nil
		}
	}
	m.Execution.Bindings = append(m.Execution.Bindings, *command.Binding)
	return nil
}
