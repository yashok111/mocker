package designscenario

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
)

const (
	maxParticipants = 200
	maxMessages     = 2_000
	maxFragments    = 500
	maxContracts    = 200
	maxText         = 50_000
	maxDrafts       = 1_000
)

var (
	participantKinds = []string{"user", "client", "service", "external", "database", "queue", "other"}
	messageKinds     = []string{"request", "response", "event", "note"}
	fragmentKinds    = []string{"opt", "loop"}
	operationMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}
)

func (r *Repo) Validate(_ context.Context, document Document) ([]Diagnostic, error) {
	diagnostics, err := r.validate(document, map[string]string{})
	return diagnostics, err
}

func (r *Repo) validate(document Document, formDrafts map[string]string) ([]Diagnostic, error) {
	diagnostics := validateDocument(document)
	diagnostics = append(diagnostics, validateDrafts(formDrafts)...)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return diagnostics, &InvalidError{Diagnostics: diagnostics}
		}
	}
	return diagnostics, nil
}

func validateDocument(document Document) []Diagnostic {
	diagnostics := executionDiagnostics(document)
	errorAt := func(pointer, message string) {
		diagnostics = append(diagnostics, Diagnostic{Pointer: pointer, Message: message, Severity: "error"})
	}
	if document.FormatVersion != 1 {
		errorAt("/formatVersion", "formatVersion must be 1")
	}
	checkText := func(pointer, value string, emptyOK bool) {
		if !emptyOK && value == "" {
			errorAt(pointer, "must not be empty")
		}
		if utf8.RuneCountInString(value) > maxText {
			errorAt(pointer, "is too long")
		}
	}
	checkColor := func(pointer string, color HexColor) {
		if color != "" && !hexColorPattern.MatchString(string(color)) {
			errorAt(pointer, "must be a #RRGGBB string")
		}
	}
	checkText("/title", document.Title, true)
	for _, field := range []struct {
		name    string
		missing bool
	}{
		{"participants", document.Participants == nil},
		{"messages", document.Messages == nil},
		{"fragments", document.Fragments == nil},
		{"contracts", document.Contracts == nil},
	} {
		if field.missing {
			errorAt("/"+field.name, "must be an array; use [] for an empty collection")
		}
	}
	if len(document.Participants) > maxParticipants {
		errorAt("/participants", "contains too many participants")
	}
	if len(document.Messages) > maxMessages {
		errorAt("/messages", "contains too many messages")
	}
	if len(document.Fragments) > maxFragments {
		errorAt("/fragments", "contains too many fragments")
	}
	if len(document.Contracts) > maxContracts {
		errorAt("/contracts", "contains too many contracts")
	}

	participants := map[string]int{}
	for index, participant := range document.Participants {
		pointer := fmt.Sprintf("/participants/%d", index)
		checkText(pointer+"/id", participant.ID, false)
		checkText(pointer+"/name", participant.Name, true)
		checkText(pointer+"/description", participant.Description, true)
		checkColor(pointer+"/color", participant.Color)
		if !slices.Contains(participantKinds, participant.Kind) {
			errorAt(pointer+"/kind", "unknown participant kind")
		}
		if _, exists := participants[participant.ID]; exists {
			errorAt(pointer+"/id", "duplicate participant id")
		}
		participants[participant.ID] = index
	}

	messages := map[string]int{}
	for index, message := range document.Messages {
		pointer := fmt.Sprintf("/messages/%d", index)
		checkText(pointer+"/id", message.ID, false)
		checkText(pointer+"/fromId", message.FromID, false)
		checkText(pointer+"/toId", message.ToID, false)
		checkText(pointer+"/label", message.Label, true)
		checkText(pointer+"/description", message.Description, true)
		checkColor(pointer+"/color", message.Color)
		checkColor(pointer+"/arrowColor", message.ArrowColor)
		if !slices.Contains(messageKinds, message.Kind) {
			errorAt(pointer+"/kind", "unknown message kind")
		}
		if _, exists := messages[message.ID]; exists {
			errorAt(pointer+"/id", "duplicate message id")
		}
		messages[message.ID] = index
	}

	contracts := map[string]map[string]struct{}{}
	linkedDesigns := map[int64]string{}
	for index, contract := range document.Contracts {
		pointer := fmt.Sprintf("/contracts/%d", index)
		checkText(pointer+"/id", contract.ID, false)
		checkText(pointer+"/name", contract.Name, true)
		if _, exists := contracts[contract.ID]; exists {
			errorAt(pointer+"/id", "duplicate contract id")
		}
		mode := contract.Mode
		if mode == "" {
			mode = "copy"
		}
		if mode != "copy" && mode != "linked" {
			errorAt(pointer+"/mode", "mode must be copy or linked")
		}
		if contract.Source != nil {
			if contract.Source.DesignID <= 0 {
				errorAt(pointer+"/source/designId", "must be positive")
			}
			if contract.Source.RevisionID <= 0 {
				errorAt(pointer+"/source/revisionId", "must be positive")
			}
			if contract.Source.Version < 0 || (mode == "linked" && contract.Source.Version == 0) {
				errorAt(pointer+"/source/version", "linked source version must be positive")
			}
		}
		if mode == "linked" {
			if contract.Source == nil {
				errorAt(pointer+"/source", "linked contract requires a source")
			} else if previous, exists := linkedDesigns[contract.Source.DesignID]; exists {
				errorAt(pointer+"/source/designId", "design is already linked by contract "+previous)
			} else {
				linkedDesigns[contract.Source.DesignID] = contract.ID
			}
		}
		keys, keyDiagnostics := operationKeys(contract.Document, pointer+"/document")
		diagnostics = append(diagnostics, keyDiagnostics...)
		contracts[contract.ID] = keys
	}

	for index, message := range document.Messages {
		pointer := fmt.Sprintf("/messages/%d", index)
		if _, exists := participants[message.FromID]; !exists {
			errorAt(pointer+"/fromId", "participant does not exist")
		}
		if _, exists := participants[message.ToID]; !exists {
			errorAt(pointer+"/toId", "participant does not exist")
		}
		if message.ReplyToID != "" {
			replyIndex, exists := messages[message.ReplyToID]
			switch {
			case message.Kind != "response":
				errorAt(pointer+"/replyToId", "only responses may reference a request")
			case !exists:
				errorAt(pointer+"/replyToId", "request does not exist")
			case document.Messages[replyIndex].Kind != "request":
				errorAt(pointer+"/replyToId", "must reference a request")
			case replyIndex >= index:
				errorAt(pointer+"/replyToId", "response must follow its request")
			default:
				request := document.Messages[replyIndex]
				if message.FromID != request.ToID || message.ToID != request.FromID {
					errorAt(pointer+"/replyToId", "response direction must reverse its request")
				}
			}
		}
		if message.Operation != nil {
			checkText(pointer+"/operation/operationKey", message.Operation.OperationKey, false)
			keys, exists := contracts[message.Operation.ContractID]
			if !exists {
				errorAt(pointer+"/operation/contractId", "contract does not exist")
			} else if _, exists = keys[message.Operation.OperationKey]; !exists {
				diagnostics = append(diagnostics, Diagnostic{Pointer: pointer + "/operation/operationKey", Message: "operation is missing from the pinned contract", Severity: "warning"})
			}
		}
	}

	fragments := map[string]struct{}{}
	for index, fragment := range document.Fragments {
		pointer := fmt.Sprintf("/fragments/%d", index)
		checkText(pointer+"/id", fragment.ID, false)
		checkText(pointer+"/label", fragment.Label, true)
		if !slices.Contains(fragmentKinds, fragment.Kind) {
			errorAt(pointer+"/kind", "unknown fragment kind")
		}
		if _, exists := fragments[fragment.ID]; exists {
			errorAt(pointer+"/id", "duplicate fragment id")
		}
		fragments[fragment.ID] = struct{}{}
		from, fromOK := messages[fragment.FromMessageID]
		to, toOK := messages[fragment.ToMessageID]
		if !fromOK {
			errorAt(pointer+"/fromMessageId", "message does not exist")
		}
		if !toOK {
			errorAt(pointer+"/toMessageId", "message does not exist")
		}
		if fromOK && toOK && from > to {
			errorAt(pointer, "fragment start must not follow its end")
		}
	}
	return diagnostics
}

func validateDrafts(formDrafts map[string]string) []Diagnostic {
	diagnostics := []Diagnostic{}
	if len(formDrafts) > maxDrafts {
		diagnostics = append(diagnostics, Diagnostic{Pointer: "/formDrafts", Message: "contains too many entries", Severity: "error"})
	}
	for key, value := range formDrafts {
		if key == "" {
			diagnostics = append(diagnostics, Diagnostic{Pointer: "/formDrafts", Message: "draft key must not be empty", Severity: "error"})
		}
		if utf8.RuneCountInString(key) > maxText || int64(len(value)) > 2_000_000 {
			diagnostics = append(diagnostics, Diagnostic{Pointer: "/formDrafts/" + key, Message: "draft is too large", Severity: "error"})
		}
	}
	return diagnostics
}

func operationKeys(raw jsonx.RawMessage, pointer string) (map[string]struct{}, []Diagnostic) {
	keys := map[string]struct{}{}
	diagnostics := []Diagnostic{}
	value, err := decodeJSONValue(raw)
	document, ok := value.(map[string]any)
	if len(raw) == 0 || err != nil || !ok || document == nil {
		return keys, []Diagnostic{{Pointer: pointer, Message: "API document must be a JSON object", Severity: "error"}}
	}
	paths, _ := document["paths"].(map[string]any)
	for path, pathValue := range paths {
		pathItem, _ := pathValue.(map[string]any)
		for _, method := range operationMethods {
			operation, _ := pathItem[method].(map[string]any)
			if operation == nil {
				continue
			}
			value, exists := operation[apidesign.OperationKey]
			if !exists {
				continue
			}
			key, ok := value.(string)
			operationPointer := pointer + "/paths/" + escapePointer(path) + "/" + method + "/" + apidesign.OperationKey
			if !ok || strings.TrimSpace(key) == "" {
				diagnostics = append(diagnostics, Diagnostic{Pointer: operationPointer, Message: "operation key must be a non-empty string", Severity: "error"})
				continue
			}
			if _, duplicate := keys[key]; duplicate {
				diagnostics = append(diagnostics, Diagnostic{Pointer: operationPointer, Message: "duplicate operation key", Severity: "error"})
			}
			keys[key] = struct{}{}
		}
	}
	return keys, diagnostics
}

func escapePointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
