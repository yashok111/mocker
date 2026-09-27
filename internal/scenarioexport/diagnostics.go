package scenarioexport

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

func diagnostic(code, severity, message, kind, id, pointer string) Diagnostic {
	return Diagnostic{Code: code, Severity: severity, Message: message, Target: &Target{Kind: kind, ID: id}, Pointer: pointer}
}

func diagramDiagnostics(doc designscenario.Document) []Diagnostic {
	result := []Diagnostic{}
	if len(doc.Participants) == 0 {
		result = append(result, Diagnostic{Code: "diagram_empty", Severity: "error", Message: "Добавьте участников сценария"})
	}
	participants := make(map[string]bool, len(doc.Participants))
	styled := false
	for _, p := range doc.Participants {
		participants[p.ID] = true
		styled = styled || p.Color != ""
	}
	contractKeys := make(map[string]map[string]bool, len(doc.Contracts))
	for _, contract := range doc.Contracts {
		contractKeys[contract.ID] = operationKeys(contract.Document)
	}
	for i, m := range doc.Messages {
		if !participants[m.FromID] || !participants[m.ToID] {
			result = append(result, diagnostic("participant_missing", "error", "У сообщения отсутствует участник", "message", m.ID, fmt.Sprintf("/messages/%d", i)))
		}
		styled = styled || m.Color != "" || m.ArrowColor != ""
		if m.Operation != nil && !contractKeys[m.Operation.ContractID][m.Operation.OperationKey] {
			result = append(result, diagnostic("binding_missing", "warning", "Привязка API недоступна; сообщение сохранено как описание", "message", m.ID, fmt.Sprintf("/messages/%d/operation", i)))
		}
	}
	_, issues := sequenceIntervals(doc)
	result = append(result, issues...)
	if styled {
		result = append(result, Diagnostic{Code: "style_not_preserved", Severity: "info", Message: "Цвета канваса сохраняются в SVG/PNG; текстовый формат использует своё оформление"})
	}
	return result
}

func (s *Service) contractDiagnostics(rev designscenario.Revision, contract designscenario.Contract) ([]Diagnostic, error) {
	result := []Diagnostic{}
	budget := jsonBudget{remaining: s.maxBytes}
	add := func(d Diagnostic) error {
		if err := budget.value(reflect.ValueOf(d)); err != nil {
			return err
		}
		result = append(result, d)
		return nil
	}
	if s.validate == nil {
		return nil, unavailableValidator()
	}
	diagnostics, err := s.validate(string(contract.Document))
	if err != nil {
		return nil, err
	}
	for _, d := range diagnostics {
		if err := add(diagnostic("contract_invalid", d.Severity, d.Message, "contract", contract.ID, d.Pointer)); err != nil {
			return nil, err
		}
	}
	if hasPendingForms(rev.FormDrafts, contract.ID) {
		if err := add(diagnostic("api_forms_pending", "error", "Завершите редактирование API", "contract", contract.ID, "")); err != nil {
			return nil, err
		}
	}
	keys := operationKeys(contract.Document)
	for i, m := range rev.Document.Messages {
		if m.Kind != "request" {
			continue
		}
		pointer := fmt.Sprintf("/messages/%d/operation", i)
		if m.Operation == nil {
			if err := add(diagnostic("descriptive_messages_omitted", "info", "Описательное сообщение не входит в этот API-контракт", "message", m.ID, pointer)); err != nil {
				return nil, err
			}
		} else if m.Operation.ContractID == contract.ID && !keys[m.Operation.OperationKey] {
			if err := add(diagnostic("binding_missing", "error", "Операция сообщения отсутствует в контракте", "message", m.ID, pointer)); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func hasPendingForms(envelope map[string]string, contractID string) bool {
	prefix := "/canvas-contract/" + contractID
	for key, raw := range envelope {
		if key != "all" {
			return true
		}
		var drafts map[string]jsonx.RawMessage
		if err := jsonx.Unmarshal([]byte(raw), &drafts); err != nil || drafts == nil {
			return true
		}
		for pointer, value := range drafts {
			var field map[string]jsonx.RawMessage
			if err := jsonx.Unmarshal(value, &field); err != nil || field == nil {
				return true
			}
			var source, property string
			if field["source"] == nil || field["propertySource"] == nil {
				return true
			}
			if jsonx.Unmarshal(field["source"], &source) != nil || jsonx.Unmarshal(field["propertySource"], &property) != nil {
				return true
			}
			if !strings.HasPrefix(pointer, "/canvas-contract/") {
				return true
			}
			if pointer == prefix || strings.HasPrefix(pointer, prefix+"/") {
				return true
			}
		}
	}
	return false
}

// Canvas bindings refer to operations under paths, matching the editor's resolver.
func operationKeys(raw []byte) map[string]bool {
	keys := map[string]bool{}
	decoder := jsonx.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil {
		return keys
	}
	paths, _ := value["paths"].(map[string]any)
	for path, item := range paths {
		if !strings.HasPrefix(path, "/") {
			continue
		}
		pathItem, _ := item.(map[string]any)
		for _, method := range []string{"get", "put", "post", "patch", "delete", "options", "head", "trace"} {
			operation, _ := pathItem[method].(map[string]any)
			if key, ok := operation["x-mocker-canvas-operation-id"].(string); ok && key != "" {
				keys[key] = true
			}
		}
	}
	return keys
}
