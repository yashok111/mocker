package designscenario

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/jsonx"
)

const (
	MaxExecutionEntries = 100
	MaxExecutionBody    = 1 << 20
)

var executionVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,99}$`)

type Execution struct {
	Variables ExecutionValues `json:"variables"`
}

// ExecutionValues rejects JSON null values instead of silently turning them
// into empty strings, preserving the same contract as the browser editor.
type ExecutionValues map[string]string

func (v *ExecutionValues) UnmarshalJSON(data []byte) error {
	var values map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &values); err != nil {
		return err
	}
	if values == nil {
		return fmt.Errorf("execution parameters must be an object")
	}
	out := make(ExecutionValues, len(values))
	for key, raw := range values {
		if isJSONNull(raw) {
			return fmt.Errorf("execution parameter %q must be a string", key)
		}
		var value string
		if err := jsonx.Unmarshal(raw, &value); err != nil {
			return err
		}
		out[key] = value
	}
	*v = out
	return nil
}

type StepExecution struct {
	Enabled        bool                  `json:"enabled"`
	PathParams     ExecutionValues       `json:"pathParams"`
	Query          ExecutionValues       `json:"query"`
	Headers        ExecutionValues       `json:"headers"`
	Body           string                `json:"body"`
	ExpectedStatus *int                  `json:"expectedStatus,omitempty"`
	Assertions     []ExecutionAssertion  `json:"assertions"`
	Extract        []ExecutionExtraction `json:"extract"`
}

type ExecutionAssertion struct {
	Pointer string           `json:"pointer"`
	Equals  jsonx.RawMessage `json:"equals"`
}

type ExecutionExtraction struct {
	Name    string `json:"name"`
	Pointer string `json:"pointer"`
}

func (e *Execution) UnmarshalJSON(data []byte) error {
	type wire Execution
	var out wire
	if err := decodeExecutionObject(data, &out, "variables"); err != nil {
		return err
	}
	*e = Execution(out)
	return nil
}

func (e *StepExecution) UnmarshalJSON(data []byte) error {
	type wire StepExecution
	var out wire
	if err := decodeExecutionObject(data, &out, "enabled", "pathParams", "query", "headers", "body", "assertions", "extract"); err != nil {
		return err
	}
	*e = StepExecution(out)
	return nil
}

func (e *ExecutionAssertion) UnmarshalJSON(data []byte) error {
	type wire ExecutionAssertion
	var out wire
	// equals may deliberately be JSON null, but must be present.
	var fields map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, ok := fields["equals"]; !ok {
		return fmt.Errorf("execution assertion equals is required")
	}
	if err := decodeExecutionObject(data, &out, "pointer"); err != nil {
		return err
	}
	*e = ExecutionAssertion(out)
	return nil
}

func (e *ExecutionExtraction) UnmarshalJSON(data []byte) error {
	type wire ExecutionExtraction
	var out wire
	if err := decodeExecutionObject(data, &out, "name", "pointer"); err != nil {
		return err
	}
	*e = ExecutionExtraction(out)
	return nil
}

func decodeExecutionObject(data []byte, out any, required ...string) error {
	var fields map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range required {
		if raw, ok := fields[key]; !ok || isJSONNull(raw) {
			return fmt.Errorf("execution field %q is required", key)
		}
	}
	if raw, ok := fields["expectedStatus"]; ok && isJSONNull(raw) {
		return fmt.Errorf("expectedStatus must be an integer")
	}
	decoder := jsonx.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}

func executionDiagnostics(document Document) []Diagnostic {
	out := []Diagnostic{}
	add := func(pointer, message string) {
		out = append(out, Diagnostic{Pointer: pointer, Message: message, Severity: "error"})
	}
	if document.Execution != nil {
		checkExecutionValues("/execution/variables", document.Execution.Variables, true, add)
	}
	for i, message := range document.Messages {
		e := message.Execution
		if e == nil {
			continue
		}
		pointer := fmt.Sprintf("/messages/%d/execution", i)
		checkStepExecution(pointer, e, add)
	}
	return out
}

func checkExecutionValues(pointer string, values map[string]string, variables bool, add func(string, string)) {
	if values == nil {
		add(pointer, "must be an object")
		return
	}
	if len(values) > MaxExecutionEntries {
		add(pointer, "contains too many entries")
	}
	for key, value := range values {
		if key == "" || utf8.RuneCountInString(key) > 256 || (variables && !executionVariableName.MatchString(key)) {
			add(pointer+"/"+escapePointer(key), "invalid key")
		}
		if utf8.RuneCountInString(value) > maxText {
			add(pointer+"/"+escapePointer(key), "value is too long")
		}
	}
}

func checkStepExecution(pointer string, e *StepExecution, add func(string, string)) {
	checkExecutionValues(pointer+"/pathParams", e.PathParams, false, add)
	checkExecutionValues(pointer+"/query", e.Query, false, add)
	checkExecutionValues(pointer+"/headers", e.Headers, false, add)
	if len(e.Body) > MaxExecutionBody {
		add(pointer+"/body", "body is too large")
	}
	if e.ExpectedStatus != nil && (*e.ExpectedStatus < 100 || *e.ExpectedStatus > 599) {
		add(pointer+"/expectedStatus", "must be an HTTP status between 100 and 599")
	}
	if e.Assertions == nil || len(e.Assertions) > MaxExecutionEntries {
		add(pointer+"/assertions", "must be an array of at most 100 assertions")
	}
	if e.Extract == nil || len(e.Extract) > MaxExecutionEntries {
		add(pointer+"/extract", "must be an array of at most 100 extractions")
	}
	for j, assertion := range e.Assertions {
		p := fmt.Sprintf("%s/assertions/%d", pointer, j)
		if !validExecutionPointer(assertion.Pointer) {
			add(p+"/pointer", "invalid JSON Pointer")
		}
		if len(assertion.Equals) == 0 || len(assertion.Equals) > MaxExecutionBody || !jsonx.Valid(assertion.Equals) {
			add(p+"/equals", "must be a JSON value of at most 1 MiB")
		}
	}
	names := map[string]bool{}
	for j, extraction := range e.Extract {
		p := fmt.Sprintf("%s/extract/%d", pointer, j)
		if !executionVariableName.MatchString(extraction.Name) || names[extraction.Name] {
			add(p+"/name", "invalid or duplicate variable name")
		}
		names[extraction.Name] = true
		if !validExecutionPointer(extraction.Pointer) {
			add(p+"/pointer", "invalid JSON Pointer")
		}
	}
}

func validExecutionPointer(pointer string) bool {
	if utf8.RuneCountInString(pointer) > 2000 || (pointer != "" && !strings.HasPrefix(pointer, "/")) {
		return false
	}
	for i := 0; i < len(pointer); i++ {
		if pointer[i] == '~' {
			i++
			if i >= len(pointer) || (pointer[i] != '0' && pointer[i] != '1') {
				return false
			}
		}
	}
	return true
}
