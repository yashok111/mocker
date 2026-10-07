package scenarioexport

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/designscenario"
)

// postmanBindingPreflight's only error is ctx's: the data-flow analysis and
// the schema walk stop for a cancelled request.
func postmanBindingPreflight(ctx context.Context, document designscenario.Document) (map[string]map[string]string, []Diagnostic, error) {
	// Keep original positions for diagnostics. Bindings on omitted recipients
	// do not execute, but an active consumer still sees a disabled source.
	document.Messages = slices.Clone(document.Messages)
	hasBindings := false
	for i, message := range document.Messages {
		if message.Execution == nil || len(message.Execution.Bindings) == 0 {
			continue
		}
		if !message.Execution.Enabled || message.Kind != "request" {
			config := *message.Execution
			config.Bindings = nil
			document.Messages[i].Execution = &config
			continue
		}
		hasBindings = true
	}
	if !hasBindings {
		return nil, nil, nil
	}
	analysis, err := designscenario.AnalyzeDataFlow(ctx, document)
	if err != nil {
		return nil, nil, err
	}
	diagnostics := []Diagnostic{}
	for _, diagnostic := range analysis.Diagnostics {
		mapped := Diagnostic{Code: "data_binding_invalid", Severity: diagnostic.Severity, Message: diagnostic.Message, Pointer: diagnostic.Pointer}
		if mapped.Severity != "error" {
			mapped.Code = "data_binding_warning"
		}
		if rest, ok := strings.CutPrefix(diagnostic.Pointer, "/messages/"); ok {
			index, _, _ := strings.Cut(rest, "/")
			if i, err := strconv.Atoi(index); err == nil && i >= 0 && i < len(document.Messages) {
				mapped.Target = &Target{Kind: "message", ID: document.Messages[i].ID}
				message := document.Messages[i]
				if message.Kind != "request" || (message.Execution != nil && !message.Execution.Enabled) {
					continue
				}
			}
		}
		diagnostics = append(diagnostics, mapped)
	}
	// In particular, honor analysis input limits before resolving schemas again.
	// A blocked export has no executable request that needs target metadata.
	if slices.ContainsFunc(diagnostics, func(diagnostic Diagnostic) bool { return diagnostic.Severity == "error" }) {
		return nil, diagnostics, nil
	}
	types, err := designscenario.BindingTargetTypes(ctx, document)
	if err != nil {
		return nil, nil, err
	}
	return types, diagnostics, nil
}

func omitBoundHTTPInputs(config designscenario.StepExecution, add func(string, string, string)) designscenario.StepExecution {
	config.PathParams, config.Query, config.Headers = maps.Clone(config.PathParams), maps.Clone(config.Query), maps.Clone(config.Headers)
	for _, binding := range config.Bindings {
		target := binding.Target
		if target.Kind != "body" && strings.ContainsRune(target.Name, 0) {
			add("input_invalid", "error", "NUL в имени параметра не поддерживается")
		}
		switch target.Kind {
		case "path":
			delete(config.PathParams, target.Name)
		case "query":
			delete(config.Query, target.Name)
		case "header":
			if !httpHeaderName.MatchString(target.Name) {
				add("header_invalid", "error", "HTTP-заголовок содержит недопустимое имя")
			}
			maps.DeleteFunc(config.Headers, func(key, _ string) bool { return strings.EqualFold(key, target.Name) })
		}
	}
	return config
}

func bindingHTTPValidation(config designscenario.StepExecution) designscenario.StepExecution {
	// These placeholders only establish presence for required-input validation.
	// The emitted request retains its authored body and receives actual bound
	// values at runtime.
	config.PathParams, config.Query, config.Headers = maps.Clone(config.PathParams), maps.Clone(config.Query), maps.Clone(config.Headers)
	for _, binding := range config.Bindings {
		target := binding.Target
		var values *designscenario.ExecutionValues
		switch target.Kind {
		case "path":
			values = &config.PathParams
		case "query":
			values = &config.Query
		case "header":
			values = &config.Headers
		case "body":
			if target.Pointer == "" && config.Body == "" {
				config.Body = "null"
			}
		}
		if values != nil {
			if *values == nil {
				*values = designscenario.ExecutionValues{}
			}
			(*values)[target.Name] = "binding"
		}
	}
	return config
}
