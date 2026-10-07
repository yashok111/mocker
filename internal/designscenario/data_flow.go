package designscenario

import (
	"context"
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

// AnalyzeDataFlow inspects only the document's pinned contracts and never
// mutates it. Its only error is ctx's, once the request is cancelled.
func AnalyzeDataFlow(ctx context.Context, document Document) (DataFlowAnalysis, error) {
	out, err := analyzeDataFlow(ctx, document)
	if err != nil {
		return DataFlowAnalysis{}, err
	}
	if len(dataFlowInputDiagnostics(document)) > 0 {
		return out, nil
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
	return out, nil
}
func analyzeDataFlow(ctx context.Context, document Document) (DataFlowAnalysis, error) {
	out := DataFlowAnalysis{Messages: []DataFlowMessage{}, Bindings: []DataFlowBinding{}, Diagnostics: []Diagnostic{}}
	if diagnostics := dataFlowInputDiagnostics(document); len(diagnostics) > 0 {
		out.Diagnostics = diagnostics
		return out, nil
	}
	schemas, err := bindingSchemas(ctx, document)
	if err != nil {
		return DataFlowAnalysis{}, err
	}
	a := dataFlowAnalyzer{document: document, out: &out, schemas: schemas, positions: map[string]int{}}
	for i, m := range document.Messages {
		a.positions[m.ID] = i
	}
	a.scopes = bindingScopes(document, a.positions)
	a.ambiguous = document.FormatVersion == 1 && ambiguousBindingFragments(document, a.positions)
	for i, m := range document.Messages {
		if a.limited {
			break
		}
		// Up to maxMessages messages, each cataloguing its schemas: the
		// walk a cancelled request must not finish.
		if err := ctx.Err(); err != nil {
			return DataFlowAnalysis{}, err
		}
		if m.Kind == "request" && m.Operation != nil {
			a.catalog(i, m)
		}
		if m.Execution == nil {
			continue
		}
		for j, b := range m.Execution.Bindings {
			a.binding(i, j, m, b)
		}
	}
	sortDataFlowDiagnostics(out.Diagnostics)
	return out, nil
}

// dataFlowAnalyzer carries the output budget shared by every message: once
// either limit trips, one error diagnostic is added and the walk stops.
type dataFlowAnalyzer struct {
	document                Document
	out                     *DataFlowAnalysis
	schemas                 []bindingSchema
	positions               map[string]int
	scopes                  []map[flowScope]bool
	ambiguous, limited      bool
	outputBytes, fieldCount int
}

func (a *dataFlowAnalyzer) limit() {
	if !a.limited {
		a.limited = true
		a.out.Diagnostics = append(a.out.Diagnostics, Diagnostic{Pointer: "/messages", Message: "data-flow analysis output limit exceeded", Severity: "error"})
	}
}

func (a *dataFlowAnalyzer) fits(value any) bool {
	encoded, _ := jsonx.Marshal(value)
	if a.outputBytes+len(encoded) > 4<<20 {
		a.limit()
		return false
	}
	a.outputBytes += len(encoded)
	return true
}

func (a *dataFlowAnalyzer) add(pointer, message, severity string) {
	diagnostic := Diagnostic{Pointer: pointer, Message: message, Severity: severity}
	if len(a.out.Diagnostics) >= 2000 {
		a.limit()
		return
	}
	if a.fits(diagnostic) {
		a.out.Diagnostics = append(a.out.Diagnostics, diagnostic)
	}
}

func (a *dataFlowAnalyzer) catalog(i int, m Message) {
	fields, truncated, unknown := schemaCatalog(a.schemas[i], m.ID, 20000-a.fieldCount)
	a.fieldCount += len(fields.ResponseFields) + len(fields.RequestFields)
	if a.fits(fields) {
		a.out.Messages = append(a.out.Messages, fields)
	}
	if truncated {
		a.add(fmt.Sprintf("/messages/%d/operation", i), "field catalog truncated (maximum depth 20, 2000 fields per message and 20000 total)", "warning")
	}
	if unknown {
		a.add(fmt.Sprintf("/messages/%d/operation", i), "тип не удалось определить: unsupported or unresolved schema", "warning")
	}
}

func (a *dataFlowAnalyzer) binding(i, j int, m Message, b DataBinding) {
	entry := DataFlowBinding{MessageID: m.ID, Binding: b}
	if a.fits(entry) {
		a.out.Bindings = append(a.out.Bindings, entry)
	}
	pointer := fmt.Sprintf("/messages/%d/execution/bindings/%d", i, j)
	if m.Kind != "request" || m.Operation == nil {
		a.add(pointer, "binding recipient must be an HTTP request with an operation", "error")
	}
	source, exists := a.positions[b.SourceMessageID]
	if !exists {
		a.add(pointer+"/sourceMessageId", "source message does not exist", "error")
		return
	}
	a.bindingSource(pointer, i, source)
	if !a.schemas[i].available {
		a.add(pointer+"/target", "recipient operation is missing from the pinned contract; "+OperationKeyDescription, "error")
	}
	a.bindingTypes(pointer, b, a.schemas[source].responseType(b.SourcePointer), a.schemas[i].targetType(b.Target))
}

// bindingSource checks that the source message can feed recipient i.
func (a *dataFlowAnalyzer) bindingSource(pointer string, i, source int) {
	from := a.document.Messages[source]
	if from.Kind != "request" || from.Operation == nil || (from.Execution != nil && !from.Execution.Enabled) {
		a.add(pointer+"/sourceMessageId", "source must be an enabled HTTP request with an operation", "error")
	}
	if source >= i {
		a.add(pointer+"/sourceMessageId", "source must precede the recipient", "error")
	}
	for scope := range a.scopes[source] {
		if !a.scopes[i][scope] {
			a.add(pointer+"/sourceMessageId", "source is outside the recipient control-flow scope", "error")
			break
		}
	}
	if a.ambiguous {
		a.add(pointer+"/sourceMessageId", "ambiguous formatVersion 1 fragments", "error")
	}
	if !a.schemas[source].available {
		a.add(pointer+"/sourceMessageId", "source operation is missing from the pinned contract; "+OperationKeyDescription, "error")
	}
}

// bindingTypes compares the (transformed) source type st with the target type tt.
func (a *dataFlowAnalyzer) bindingTypes(pointer string, b DataBinding, st, tt string) {
	if st == "unknown" || tt == "unknown" {
		a.add(pointer, "тип не удалось определить: compatibility will be checked during execution", "warning")
	}
	projected, err := ProjectBindingType(st, b.Transforms)
	if err != nil {
		a.add(pointer+"/transforms", err.Error(), "error")
		return
	}
	st = projected
	if b.Target.Kind != "body" {
		if st == "object" || st == "array" || st == "null" {
			a.add(pointer+"/sourcePointer", "text targets require a non-null scalar source", "error")
		}
		if b.Prefix != "" && tt != "unknown" && tt != "string" {
			a.add(pointer+"/prefix", "prefix requires a string target", "error")
		}
		// A string HTTP parameter can receive any scalar's textual representation.
		if tt == "string" {
			return
		}
	}
	if !compatibleBindingTypes(st, tt) {
		a.add(pointer+"/target", bindingTypeError(st, tt), "error")
	}
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
