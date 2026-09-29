package designscenario

import (
	"fmt"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

type DataFlowField struct {
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	Pointer  string `json:"pointer,omitempty"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}
type DataFlowMessage struct {
	MessageID      string          `json:"messageId"`
	ResponseFields []DataFlowField `json:"responseFields"`
	RequestFields  []DataFlowField `json:"requestFields"`
}
type DataFlowBinding struct {
	MessageID string      `json:"messageId"`
	Binding   DataBinding `json:"binding"`
}
type DataFlowAnalysis struct {
	Messages    []DataFlowMessage `json:"messages"`
	Bindings    []DataFlowBinding `json:"bindings"`
	Diagnostics []Diagnostic      `json:"diagnostics"`
}

// AnalyzeDataFlow inspects only the document's pinned contracts and never mutates it.
func AnalyzeDataFlow(document Document) DataFlowAnalysis {
	out := analyzeDataFlow(document)
	if len(dataFlowInputDiagnostics(document)) > 0 {
		return out
	}
	for i, m := range document.Messages {
		if m.Execution != nil {
			checkDataBindings(fmt.Sprintf("/messages/%d/execution/bindings", i), m.Execution.Bindings, func(pointer, message string) {
				if len(out.Diagnostics) < 2000 {
					out.Diagnostics = append(out.Diagnostics, Diagnostic{Pointer: pointer, Message: message, Severity: "error"})
				} else if len(out.Diagnostics) == 2000 {
					out.Diagnostics = append(out.Diagnostics, Diagnostic{Pointer: "/messages", Message: "data-flow analysis diagnostic limit exceeded", Severity: "error"})
				}
			})
		}
	}
	sortDataFlowDiagnostics(out.Diagnostics)
	if encoded, _ := jsonx.Marshal(out); len(encoded) > 4<<20 {
		out.Messages = []DataFlowMessage{}
		out.Bindings = []DataFlowBinding{}
		out.Diagnostics = append(out.Diagnostics, Diagnostic{Pointer: "/messages", Message: "data-flow analysis output limit exceeded", Severity: "error"})
	}
	return out
}
func analyzeDataFlow(document Document) DataFlowAnalysis {
	out := DataFlowAnalysis{Messages: []DataFlowMessage{}, Bindings: []DataFlowBinding{}, Diagnostics: []Diagnostic{}}
	if diagnostics := dataFlowInputDiagnostics(document); len(diagnostics) > 0 {
		out.Diagnostics = diagnostics
		return out
	}
	schemas := bindingSchemas(document)
	positions := map[string]int{}
	for i, m := range document.Messages {
		positions[m.ID] = i
	}
	scopes := bindingScopes(document, positions)
	ambiguous := document.FormatVersion == 1 && ambiguousBindingFragments(document, positions)
	outputBytes, fieldCount := 0, 0
	limited := false
	limit := func() {
		if !limited {
			limited = true
			out.Diagnostics = append(out.Diagnostics, Diagnostic{Pointer: "/messages", Message: "data-flow analysis output limit exceeded", Severity: "error"})
		}
	}
	fits := func(value any) bool {
		encoded, _ := jsonx.Marshal(value)
		if outputBytes+len(encoded) > 4<<20 {
			limit()
			return false
		}
		outputBytes += len(encoded)
		return true
	}
	add := func(pointer, message, severity string) {
		diagnostic := Diagnostic{Pointer: pointer, Message: message, Severity: severity}
		if len(out.Diagnostics) >= 2000 {
			limit()
			return
		}
		if fits(diagnostic) {
			out.Diagnostics = append(out.Diagnostics, diagnostic)
		}
	}
	for i, m := range document.Messages {
		if limited {
			break
		}
		if m.Kind == "request" && m.Operation != nil {
			fields, truncated, unknown := schemaCatalog(schemas[i], m.ID, 20000-fieldCount)
			fieldCount += len(fields.ResponseFields) + len(fields.RequestFields)
			if fits(fields) {
				out.Messages = append(out.Messages, fields)
			}
			if truncated {
				add(fmt.Sprintf("/messages/%d/operation", i), "field catalog truncated (maximum depth 20, 2000 fields per message and 20000 total)", "warning")
			}
			if unknown {
				add(fmt.Sprintf("/messages/%d/operation", i), "тип не удалось определить: unsupported or unresolved schema", "warning")
			}
		}
		if m.Execution == nil {
			continue
		}
		for j, b := range m.Execution.Bindings {
			entry := DataFlowBinding{MessageID: m.ID, Binding: b}
			if fits(entry) {
				out.Bindings = append(out.Bindings, entry)
			}
			pointer := fmt.Sprintf("/messages/%d/execution/bindings/%d", i, j)
			if m.Kind != "request" || m.Operation == nil {
				add(pointer, "binding recipient must be an HTTP request with an operation", "error")
			}
			source, exists := positions[b.SourceMessageID]
			if !exists {
				add(pointer+"/sourceMessageId", "source message does not exist", "error")
				continue
			}
			from := document.Messages[source]
			if from.Kind != "request" || from.Operation == nil || (from.Execution != nil && !from.Execution.Enabled) {
				add(pointer+"/sourceMessageId", "source must be an enabled HTTP request with an operation", "error")
			}
			if source >= i {
				add(pointer+"/sourceMessageId", "source must precede the recipient", "error")
			}
			for scope := range scopes[source] {
				if !scopes[i][scope] {
					add(pointer+"/sourceMessageId", "source is outside the recipient control-flow scope", "error")
					break
				}
			}
			if ambiguous {
				add(pointer+"/sourceMessageId", "ambiguous formatVersion 1 fragments", "error")
			}
			if !schemas[source].available {
				add(pointer+"/sourceMessageId", "source operation is missing from the pinned contract; "+OperationKeyDescription, "error")
			}
			if !schemas[i].available {
				add(pointer+"/target", "recipient operation is missing from the pinned contract; "+OperationKeyDescription, "error")
			}
			st, tt := schemas[source].responseType(b.SourcePointer), schemas[i].targetType(b.Target)
			if st == "unknown" || tt == "unknown" {
				add(pointer, "тип не удалось определить: compatibility will be checked during execution", "warning")
			}
			projected, err := ProjectBindingType(st, b.Transforms)
			if err != nil {
				add(pointer+"/transforms", err.Error(), "error")
				continue
			}
			st = projected
			if b.Target.Kind != "body" {
				if st == "object" || st == "array" || st == "null" {
					add(pointer+"/sourcePointer", "text targets require a non-null scalar source", "error")
				}
				if b.Prefix != "" && tt != "unknown" && tt != "string" {
					add(pointer+"/prefix", "prefix requires a string target", "error")
				}
				// A string HTTP parameter can receive any scalar's textual representation.
				if tt == "string" {
					continue
				}
			}
			if !compatibleBindingTypes(st, tt) {
				add(pointer+"/target", bindingTypeError(st, tt), "error")
			}
		}
	}
	sortDataFlowDiagnostics(out.Diagnostics)
	return out
}
func sortDataFlowDiagnostics(diagnostics []Diagnostic) {
	slices.SortFunc(diagnostics, func(a, b Diagnostic) int {
		if n := strings.Compare(a.Pointer, b.Pointer); n != 0 {
			return n
		}
		if n := strings.Compare(a.Severity, b.Severity); n != 0 {
			return n
		}
		return strings.Compare(a.Message, b.Message)
	})
}
func bindingScopes(document Document, positions map[string]int) []map[flowScope]bool {
	out := make([]map[flowScope]bool, len(document.Messages))
	for i := range out {
		out[i] = map[flowScope]bool{}
	}
	mark := func(from, to string, key flowScope) {
		first, ok := positions[from]
		last, okLast := positions[to]
		if !ok || !okLast || first > last {
			return
		}
		for i := first; i <= last; i++ {
			out[i][key] = true
		}
	}
	for _, f := range document.Fragments {
		if f.Kind == "alt" {
			for _, b := range f.Branches {
				mark(b.FromMessageID, b.ToMessageID, flowScope{f.ID, b.ID})
			}
		} else {
			mark(f.FromMessageID, f.ToMessageID, flowScope{f.ID, ""})
		}
	}
	return out
}
func ambiguousBindingFragments(document Document, positions map[string]int) bool {
	for i, a := range document.Fragments {
		ar, ok := messageRange(a.FromMessageID, a.ToMessageID, positions)
		if !ok {
			continue
		}
		for _, b := range document.Fragments[i+1:] {
			br, ok := messageRange(b.FromMessageID, b.ToMessageID, positions)
			if ok && ar.start <= br.end && br.start <= ar.end {
				return true
			}
		}
	}
	return false
}

func hasDataBindings(document Document) bool {
	for _, message := range document.Messages {
		if message.Execution != nil && len(message.Execution.Bindings) > 0 {
			return true
		}
	}
	return false
}

func dataFlowInputDiagnostics(document Document) []Diagnostic {
	out := []Diagnostic{}
	add := func(pointer, message string) {
		out = append(out, Diagnostic{Pointer: pointer, Message: message, Severity: "error"})
	}
	if len(document.Messages) > maxMessages {
		add("/messages", "too many messages for data-flow analysis")
	}
	if len(document.Contracts) > maxContracts {
		add("/contracts", "too many contracts for data-flow analysis")
	}
	if len(document.Fragments) > maxFragments {
		add("/fragments", "too many fragments for data-flow analysis")
	}
	if len(out) > 0 {
		return out
	}
	branches := 0
	for _, f := range document.Fragments {
		branches += len(f.Branches)
	}
	if branches > 1000 {
		add("/fragments", "too many branches for data-flow analysis")
		return out
	}
	total := 0
	for _, c := range document.Contracts {
		total += len(c.Document)
	}
	if total > MaxRunRetainedData {
		add("/contracts", "contracts exceed data-flow analysis size limit")
	}
	for i, m := range document.Messages {
		if m.Execution != nil && len(m.Execution.Bindings) > MaxExecutionEntries {
			add(fmt.Sprintf("/messages/%d/execution/bindings", i), "must contain at most 100 bindings")
		}
	}
	return out
}
