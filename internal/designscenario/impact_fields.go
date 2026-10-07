package designscenario

import (
	"cmp"
	"context"
	"fmt"
	"math/big"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
)

func compareImpactFieldFindings(a, b apidesign.ImpactFieldImpact) int {
	ownerA, usageA := impactFieldIndices(a.UsagePointer)
	ownerB, usageB := impactFieldIndices(b.UsagePointer)
	for _, comparison := range []int{
		cmp.Compare(a.Locator.ScenarioID, b.Locator.ScenarioID),
		cmp.Compare(a.Locator.ScenarioRevision, b.Locator.ScenarioRevision),
		cmp.Compare(ownerA, ownerB), cmp.Compare(usageA, usageB),
		cmp.Compare(a.UsageKind, b.UsageKind), cmp.Compare(a.Field.Kind, b.Field.Kind),
		cmp.Compare(a.Field.Pointer, b.Field.Pointer), cmp.Compare(a.Field.Name, b.Field.Name),
		cmp.Compare(a.ID, b.ID),
	} {
		if comparison != 0 {
			return comparison
		}
	}
	return 0
}

func impactFieldIndices(pointer string) (int, int) {
	parts := strings.Split(pointer, "/")
	if len(parts) < 6 {
		return 0, 0
	}
	owner, _ := strconv.Atoi(parts[2])
	usage, _ := strconv.Atoi(parts[5])
	return owner, usage
}

func impactFieldState(presence, typ string) apidesign.ImpactFieldState {
	return apidesign.ImpactFieldState{Presence: presence, Type: typ}
}

// Decoded JSON equality preserves arbitrarily large numbers while treating
// numerically equal spellings such as 1 and 1.0 as the same content.
func impactJSONEqual(ctx context.Context, a, b any) bool {
	if ctx.Err() != nil {
		return false
	}
	switch left := a.(type) {
	case jsonx.Number:
		right, ok := b.(jsonx.Number)
		if !ok {
			return false
		}
		leftSign, leftDigits, leftExponent := impactNumberParts(string(left))
		rightSign, rightDigits, rightExponent := impactNumberParts(string(right))
		return leftSign == rightSign && leftDigits == rightDigits && leftExponent.Cmp(rightExponent) == 0
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for key, value := range left {
			other, exists := right[key]
			if !exists || !impactJSONEqual(ctx, value, other) {
				return false
			}
		}
		return true
	case []any:
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for i, value := range left {
			if !impactJSONEqual(ctx, value, right[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

func impactNumberParts(raw string) (bool, string, *big.Int) {
	negative := strings.HasPrefix(raw, "-")
	raw = strings.TrimPrefix(raw, "-")
	mantissa, exponentText, hasExponent := strings.Cut(raw, "e")
	if !hasExponent {
		mantissa, exponentText, hasExponent = strings.Cut(raw, "E")
	}
	exponent := new(big.Int)
	if hasExponent {
		exponent.SetString(exponentText, 10)
	}
	integer, fractional, hasFraction := strings.Cut(mantissa, ".")
	digits := integer
	if hasFraction {
		digits += fractional
		exponent.Sub(exponent, big.NewInt(int64(len(fractional))))
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return false, "0", new(big.Int)
	}
	trimmed := strings.TrimRight(digits, "0")
	exponent.Add(exponent, big.NewInt(int64(len(digits)-len(trimmed))))
	return negative, trimmed, exponent
}

func impactBindingCompatible(source, target apidesign.ImpactFieldState, binding DataBinding) (bool, bool) {
	if source.Presence != "present" || target.Presence != "present" || source.Type == "" || target.Type == "" {
		return false, false
	}
	projected, err := ProjectBindingType(source.Type, binding.Transforms)
	if err != nil {
		return false, true
	}
	if binding.Target.Kind != "body" {
		if projected == "object" || projected == "array" || projected == "null" {
			return false, true
		}
		if binding.Prefix != "" && target.Type != "string" {
			return false, true
		}
		if target.Type == "string" {
			return true, true
		}
	}
	return compatibleBindingTypes(projected, target.Type), true
}

func (c *impactUsageCollector) visitFields(draft designUsageDraft, contracts map[string]impactContractUsage) (bool, error) {
	if c.pair.Before == "" || c.pair.Proposed == "" {
		return false, nil
	}
	before, err := bindingSchemasContext(c.ctx, draft.Document)
	if err != nil {
		return false, err
	}
	provenance := map[string]bool{}
	for id, contract := range contracts {
		if err := c.ctx.Err(); err != nil {
			return false, err
		}
		value, err := decodeJSONValue(contract.Contract.Document)
		provenance[id] = err == nil && c.baseValue != nil && impactJSONEqual(c.ctx, value, c.baseValue)
		if err := c.ctx.Err(); err != nil {
			return false, err
		}
	}
	projected := draft.Document
	projected.Contracts = slices.Clone(draft.Document.Contracts)
	for i, contract := range projected.Contracts {
		if contract.Source != nil && contract.Source.DesignID == c.report.DesignID {
			projected.Contracts[i].Document = jsonx.RawMessage(c.pair.Proposed)
		}
	}
	after, err := bindingSchemasContext(c.ctx, projected)
	if err != nil {
		return false, err
	}
	positions := make(map[string]int, len(draft.Document.Messages))
	for i, message := range draft.Document.Messages {
		positions[message.ID] = i
	}
	for ownerIndex, owner := range draft.Document.Messages {
		if err := c.ctx.Err(); err != nil {
			return false, err
		}
		if owner.Execution == nil {
			continue
		}
		base := "/messages/" + strconv.Itoa(ownerIndex) + "/execution"
		for usageIndex, binding := range owner.Execution.Bindings {
			if sourceIndex, ok := positions[binding.SourceMessageID]; ok {
				stop, err := c.checkField(draft, contracts, before, after, ownerIndex, sourceIndex, usageIndex,
					"binding_source", base+"/bindings/"+strconv.Itoa(usageIndex)+"/sourcePointer",
					apidesign.ImpactFieldSelector{Kind: "response", Pointer: binding.SourcePointer}, &binding, positions, provenance)
				if stop || err != nil {
					return stop, err
				}
			}
			stop, err := c.checkField(draft, contracts, before, after, ownerIndex, ownerIndex, usageIndex,
				"binding_target", base+"/bindings/"+strconv.Itoa(usageIndex)+"/target/"+impactTargetFieldName(binding.Target),
				apidesign.ImpactFieldSelector{Kind: binding.Target.Kind, Pointer: binding.Target.Pointer, Name: binding.Target.Name}, &binding, positions, provenance)
			if stop || err != nil {
				return stop, err
			}
		}
		for usageIndex, assertion := range owner.Execution.Assertions {
			stop, err := c.checkField(draft, contracts, before, after, ownerIndex, ownerIndex, usageIndex,
				"assertion", base+"/assertions/"+strconv.Itoa(usageIndex)+"/pointer",
				apidesign.ImpactFieldSelector{Kind: "response", Pointer: assertion.Pointer}, nil, positions, provenance)
			if stop || err != nil {
				return stop, err
			}
		}
		for usageIndex, extract := range owner.Execution.Extract {
			stop, err := c.checkField(draft, contracts, before, after, ownerIndex, ownerIndex, usageIndex,
				"extract", base+"/extract/"+strconv.Itoa(usageIndex)+"/pointer",
				apidesign.ImpactFieldSelector{Kind: "response", Pointer: extract.Pointer}, nil, positions, provenance)
			if stop || err != nil {
				return stop, err
			}
		}
	}
	return false, nil
}

func impactTargetFieldName(target DataBindingTarget) string {
	if target.Kind == "body" {
		return "pointer"
	}
	return "name"
}

func (c *impactUsageCollector) checkField(
	draft designUsageDraft, contracts map[string]impactContractUsage, before, after []bindingSchema,
	ownerIndex, operationIndex, usageIndex int, kind, usagePointer string, field apidesign.ImpactFieldSelector,
	binding *DataBinding, positions map[string]int, provenance map[string]bool,
) (bool, error) {
	if err := c.ctx.Err(); err != nil {
		return false, err
	}
	operationMessage := draft.Document.Messages[operationIndex]
	if operationMessage.Operation == nil {
		return false, nil
	}
	contract, selected := contracts[operationMessage.Operation.ContractID]
	if !selected {
		return false, nil
	}
	if c.result.Coverage.FieldUsagesChecked >= apidesign.MaxImpactFieldUsages {
		c.truncate("scenario_fields")
		return true, nil
	}
	c.result.Coverage.FieldUsagesChecked++
	key := operationMessage.Operation.OperationKey
	identityKnown := !contract.Invalid && contract.Keys[key].Count == 1 && !c.ambiguous[key]
	var bp, bt, ap, at string
	if field.Kind == "response" {
		bp, bt = before[operationIndex].responseField(c.ctx, field.Pointer)
		ap, at = after[operationIndex].responseField(c.ctx, field.Pointer)
	} else {
		target := DataBindingTarget{Kind: field.Kind, Pointer: field.Pointer, Name: field.Name}
		bp, bt = before[operationIndex].targetField(c.ctx, target)
		ap, at = after[operationIndex].targetField(c.ctx, target)
	}
	if !identityKnown {
		bp, bt, ap, at = "unknown", "", "unknown", ""
	}
	previous, next := impactFieldState(bp, bt), impactFieldState(ap, at)
	changed := previous != next
	compatChanged := false
	newCompatible, newKnown := false, false
	if binding != nil {
		sourceIndex, sourceExists := positions[binding.SourceMessageID]
		if sourceExists {
			oldSourcePresence, oldSourceType := before[sourceIndex].responseField(c.ctx, binding.SourcePointer)
			newSourcePresence, newSourceType := after[sourceIndex].responseField(c.ctx, binding.SourcePointer)
			oldTargetPresence, oldTargetType := before[ownerIndex].targetField(c.ctx, binding.Target)
			newTargetPresence, newTargetType := after[ownerIndex].targetField(c.ctx, binding.Target)
			oldCompatible, oldKnown := impactBindingCompatible(impactFieldState(oldSourcePresence, oldSourceType), impactFieldState(oldTargetPresence, oldTargetType), *binding)
			newCompatible, newKnown = impactBindingCompatible(impactFieldState(newSourcePresence, newSourceType), impactFieldState(newTargetPresence, newTargetType), *binding)
			compatChanged = oldKnown && newKnown && oldCompatible != newCompatible
		}
	}
	unknown := bp == "unknown" || ap == "unknown"
	relevant := len(c.operations[key]) > 0 || c.ambiguous[key] || changed || compatChanged
	if unknown && relevant {
		c.result.Complete = false
		if len(c.result.Diagnostics) < 10000 {
			c.result.Diagnostics = append(c.result.Diagnostics, apidesign.ImpactDiagnostic{
				Code: "scenario_field_unknown", Severity: "warning", Side: "after", Pointer: usagePointer,
				Message: "Не удалось определить поле по сохранённому или предлагаемому контракту.",
			})
		}
	}
	if !changed && !compatChanged && (!unknown || !relevant) {
		return false, nil
	}
	owner := draft.Document.Messages[ownerIndex]
	locator := apidesign.ImpactLocator{
		Pointer: "/messages/" + strconv.Itoa(operationIndex) + "/operation", OperationKey: key,
		ScenarioID: draft.ScenarioID, ScenarioName: draft.ScenarioName, ScenarioRevision: draft.RevisionID,
		ContractID: contract.Contract.ID, Mode: cmp.Or(contract.Contract.Mode, "copy"),
		PinnedRevisionID: contract.Contract.Source.RevisionID, MessageID: owner.ID,
	}
	if embedded := contract.Keys[key]; embedded.Count == 1 {
		locator.Method, locator.Path = embedded.Method, embedded.Path
	}
	definitive := c.contractDefinitive(contract, provenance)
	if binding != nil {
		sourceIndex, sourceExists := positions[binding.SourceMessageID]
		if !sourceExists {
			definitive = false
		}
		indices := []int{ownerIndex}
		if sourceExists {
			indices = append(indices, sourceIndex)
		}
		for _, index := range indices {
			message := draft.Document.Messages[index]
			if message.Operation == nil {
				continue
			}
			if contributor, selected := contracts[message.Operation.ContractID]; selected && !c.contractDefinitive(contributor, provenance) {
				definitive = false
			}
		}
	}
	verdict, reason, explanation := "review", "scenario_field_changed", "Поле изменилось; проверьте сохранённый шаг с предлагаемым контрактом."
	if unknown {
		reason, explanation = "scenario_field_unknown", "Поле или операция не определены однозначно; проверьте совместимость вручную."
	} else if ap == "absent" && bp == "present" {
		if definitive {
			verdict, reason, explanation = "broken", "scenario_field_absent", "Поле объявлено в сохранённом контракте и отсутствует в закрытой схеме предлагаемого контракта."
		} else {
			reason, explanation = "scenario_field_hypothetical", "Поле отсутствует в предлагаемом контракте; сравните с встроенным контрактом этого шага."
		}
	} else if compatChanged && newKnown && !newCompatible {
		if definitive {
			verdict, reason, explanation = "broken", "scenario_binding_incompatible", "После изменения типы источника и получателя binding несовместимы."
		} else {
			reason, explanation = "scenario_binding_hypothetical", "Типы binding могут стать несовместимыми; сравните встроенный контракт шага с предложением."
		}
	}
	if !definitive && !strings.Contains(explanation, "встроенн") {
		explanation += " Сравните встроенный контракт шага с предложением."
	}
	id := fmt.Sprintf("scenario:%d:%d:%s:%s:%d:%s:%s:%s:%s", draft.ScenarioID, draft.RevisionID,
		escapePointer(owner.ID), kind, usageIndex, escapePointer(operationMessage.ID), field.Kind,
		escapePointer(field.Pointer), escapePointer(field.Name))
	if len(c.result.FieldImpacts) >= apidesign.MaxImpactFieldFindings {
		c.truncate("scenario_fields")
		return true, nil
	}
	c.result.FieldImpacts = append(c.result.FieldImpacts, apidesign.ImpactFieldImpact{
		ID: id, Locator: locator, OperationMessageID: operationMessage.ID, UsageKind: kind,
		UsagePointer: usagePointer, Field: field, Before: previous, After: next,
		Verdict: verdict, ReasonCode: reason, Explanation: explanation,
	})
	return false, nil
}

func (c *impactUsageCollector) contractDefinitive(contract impactContractUsage, provenance map[string]bool) bool {
	return contract.Contract.Mode == "linked" && contract.Contract.Source != nil &&
		contract.Contract.Source.RevisionID == c.report.FromRevisionID && provenance[contract.Contract.ID]
}
