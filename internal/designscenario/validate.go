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

type documentValidator struct {
	diagnostics []Diagnostic
}

func validateDocument(document Document) []Diagnostic {
	validator := documentValidator{diagnostics: executionDiagnostics(document)}
	validator.validateMetadata(document)
	participants := validator.validateParticipants(document.Participants)
	messages := validator.validateMessages(document.Messages)
	contracts := validator.validateContracts(document.Contracts)
	validator.validateMessageReferences(document, participants, messages, contracts)
	validator.validateFragments(document.Fragments, messages)
	return validator.diagnostics
}

func (v *documentValidator) errorAt(pointer, message string) {
	v.diagnostics = append(v.diagnostics, Diagnostic{Pointer: pointer, Message: message, Severity: "error"})
}

func (v *documentValidator) checkText(pointer, value string, emptyOK bool) {
	if !emptyOK && value == "" {
		v.errorAt(pointer, "must not be empty")
	}
	if utf8.RuneCountInString(value) > maxText {
		v.errorAt(pointer, "is too long")
	}
}

func (v *documentValidator) checkColor(pointer string, color HexColor) {
	if color != "" && !hexColorPattern.MatchString(string(color)) {
		v.errorAt(pointer, "must be a #RRGGBB string")
	}
}

func (v *documentValidator) validateMetadata(document Document) {
	if document.FormatVersion != 1 {
		v.errorAt("/formatVersion", "formatVersion must be 1")
	}
	v.checkText("/title", document.Title, true)
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
			v.errorAt("/"+field.name, "must be an array; use [] for an empty collection")
		}
	}
	if len(document.Participants) > maxParticipants {
		v.errorAt("/participants", "contains too many participants")
	}
	if len(document.Messages) > maxMessages {
		v.errorAt("/messages", "contains too many messages")
	}
	if len(document.Fragments) > maxFragments {
		v.errorAt("/fragments", "contains too many fragments")
	}
	if len(document.Contracts) > maxContracts {
		v.errorAt("/contracts", "contains too many contracts")
	}
}

func (v *documentValidator) validateParticipants(items []Participant) map[string]int {
	participants := map[string]int{}
	for index, participant := range items {
		pointer := fmt.Sprintf("/participants/%d", index)
		v.checkText(pointer+"/id", participant.ID, false)
		v.checkText(pointer+"/name", participant.Name, true)
		v.checkText(pointer+"/description", participant.Description, true)
		v.checkColor(pointer+"/color", participant.Color)
		if !slices.Contains(participantKinds, participant.Kind) {
			v.errorAt(pointer+"/kind", "unknown participant kind")
		}
		if _, exists := participants[participant.ID]; exists {
			v.errorAt(pointer+"/id", "duplicate participant id")
		}
		participants[participant.ID] = index
	}

	return participants
}

func (v *documentValidator) validateMessages(items []Message) map[string]int {
	messages := map[string]int{}
	for index, message := range items {
		pointer := fmt.Sprintf("/messages/%d", index)
		v.checkText(pointer+"/id", message.ID, false)
		v.checkText(pointer+"/fromId", message.FromID, false)
		v.checkText(pointer+"/toId", message.ToID, false)
		v.checkText(pointer+"/label", message.Label, true)
		v.checkText(pointer+"/description", message.Description, true)
		v.checkColor(pointer+"/color", message.Color)
		v.checkColor(pointer+"/arrowColor", message.ArrowColor)
		if !slices.Contains(messageKinds, message.Kind) {
			v.errorAt(pointer+"/kind", "unknown message kind")
		}
		if _, exists := messages[message.ID]; exists {
			v.errorAt(pointer+"/id", "duplicate message id")
		}
		messages[message.ID] = index
	}

	return messages
}

func (v *documentValidator) validateContracts(items []Contract) map[string]map[string]struct{} {
	contracts := map[string]map[string]struct{}{}
	linkedDesigns := map[int64]string{}
	for index, contract := range items {
		pointer := fmt.Sprintf("/contracts/%d", index)
		v.checkText(pointer+"/id", contract.ID, false)
		v.checkText(pointer+"/name", contract.Name, true)
		if _, exists := contracts[contract.ID]; exists {
			v.errorAt(pointer+"/id", "duplicate contract id")
		}
		mode := contract.Mode
		if mode == "" {
			mode = "copy"
		}
		if mode != "copy" && mode != "linked" {
			v.errorAt(pointer+"/mode", "mode must be copy or linked")
		}
		if contract.Source != nil {
			if contract.Source.DesignID <= 0 {
				v.errorAt(pointer+"/source/designId", "must be positive")
			}
			if contract.Source.RevisionID <= 0 {
				v.errorAt(pointer+"/source/revisionId", "must be positive")
			}
			if contract.Source.Version < 0 || (mode == "linked" && contract.Source.Version == 0) {
				v.errorAt(pointer+"/source/version", "linked source version must be positive")
			}
		}
		if mode == "linked" {
			if contract.Source == nil {
				v.errorAt(pointer+"/source", "linked contract requires a source")
			} else if previous, exists := linkedDesigns[contract.Source.DesignID]; exists {
				v.errorAt(pointer+"/source/designId", "design is already linked by contract "+previous)
			} else {
				linkedDesigns[contract.Source.DesignID] = contract.ID
			}
		}
		keys, keyDiagnostics := operationKeys(contract.Document, pointer+"/document")
		v.diagnostics = append(v.diagnostics, keyDiagnostics...)
		contracts[contract.ID] = keys
	}

	return contracts
}

func (v *documentValidator) validateMessageReferences(document Document, participants, messages map[string]int, contracts map[string]map[string]struct{}) {
	for index, message := range document.Messages {
		pointer := fmt.Sprintf("/messages/%d", index)
		if _, exists := participants[message.FromID]; !exists {
			v.errorAt(pointer+"/fromId", "participant does not exist")
		}
		if _, exists := participants[message.ToID]; !exists {
			v.errorAt(pointer+"/toId", "participant does not exist")
		}
		if message.ReplyToID != "" {
			replyIndex, exists := messages[message.ReplyToID]
			switch {
			case message.Kind != "response":
				v.errorAt(pointer+"/replyToId", "only responses may reference a request")
			case !exists:
				v.errorAt(pointer+"/replyToId", "request does not exist")
			case document.Messages[replyIndex].Kind != "request":
				v.errorAt(pointer+"/replyToId", "must reference a request")
			case replyIndex >= index:
				v.errorAt(pointer+"/replyToId", "response must follow its request")
			default:
				request := document.Messages[replyIndex]
				if message.FromID != request.ToID || message.ToID != request.FromID {
					v.errorAt(pointer+"/replyToId", "response direction must reverse its request")
				}
			}
		}
		if message.Operation != nil {
			v.checkText(pointer+"/operation/operationKey", message.Operation.OperationKey, false)
			keys, exists := contracts[message.Operation.ContractID]
			if !exists {
				v.errorAt(pointer+"/operation/contractId", "contract does not exist")
			} else if _, exists = keys[message.Operation.OperationKey]; !exists {
				v.diagnostics = append(v.diagnostics, Diagnostic{Pointer: pointer + "/operation/operationKey", Message: "operation is missing from the pinned contract", Severity: "warning"})
			}
		}
	}
}

func (v *documentValidator) validateFragments(items []Fragment, messages map[string]int) {
	fragments := map[string]struct{}{}
	for index, fragment := range items {
		pointer := fmt.Sprintf("/fragments/%d", index)
		v.checkText(pointer+"/id", fragment.ID, false)
		v.checkText(pointer+"/label", fragment.Label, true)
		if !slices.Contains(fragmentKinds, fragment.Kind) {
			v.errorAt(pointer+"/kind", "unknown fragment kind")
		}
		if _, exists := fragments[fragment.ID]; exists {
			v.errorAt(pointer+"/id", "duplicate fragment id")
		}
		fragments[fragment.ID] = struct{}{}
		from, fromOK := messages[fragment.FromMessageID]
		to, toOK := messages[fragment.ToMessageID]
		if !fromOK {
			v.errorAt(pointer+"/fromMessageId", "message does not exist")
		}
		if !toOK {
			v.errorAt(pointer+"/toMessageId", "message does not exist")
		}
		if fromOK && toOK && from > to {
			v.errorAt(pointer, "fragment start must not follow its end")
		}
	}
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
