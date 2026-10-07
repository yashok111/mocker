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

// impactFieldScope is what every field check of one scenario draft reads: the
// draft, its selected contracts and the binding schemas before and after the
// proposal.
type impactFieldScope struct {
	draft         designUsageDraft
	contracts     map[string]impactContractUsage
	before, after []bindingSchema
	positions     map[string]int
	provenance    map[string]bool
}

// impactFieldUsage is one field reference: a binding end, an assertion or an
// extraction, read through the operation of message operationIndex.
type impactFieldUsage struct {
	ownerIndex, operationIndex, usageIndex int
	kind, pointer                          string
	field                                  apidesign.ImpactFieldSelector
	binding                                *DataBinding
}

func (c *impactUsageCollector) visitFields(draft designUsageDraft, contracts map[string]impactContractUsage) (bool, error) {
	if c.pair.Before == "" || c.pair.Proposed == "" {
		return false, nil
	}
	before, err := bindingSchemas(c.ctx, draft.Document)
	if err != nil {
		return false, err
	}
	provenance, err := c.contractProvenance(contracts)
	if err != nil {
		return false, err
	}
	after, err := bindingSchemas(c.ctx, c.proposedDocument(draft.Document))
	if err != nil {
		return false, err
	}
	s := &impactFieldScope{draft: draft, contracts: contracts, before: before, after: after, positions: make(map[string]int, len(draft.Document.Messages)), provenance: provenance}
	for i, message := range draft.Document.Messages {
		s.positions[message.ID] = i
	}
	for ownerIndex, owner := range draft.Document.Messages {
		if err := c.ctx.Err(); err != nil {
			return false, err
		}
		if owner.Execution == nil {
			continue
		}
		if stop, err := c.visitMessageFields(s, ownerIndex, owner.Execution); stop || err != nil {
			return stop, err
		}
	}
	return false, nil
}

// contractProvenance marks the contracts whose embedded copy equals the
// saved base revision, the precondition for a definitive verdict.
func (c *impactUsageCollector) contractProvenance(contracts map[string]impactContractUsage) (map[string]bool, error) {
	provenance := map[string]bool{}
	for id, contract := range contracts {
		if err := c.ctx.Err(); err != nil {
			return nil, err
		}
		value, err := decodeJSONValue(contract.Contract.Document)
		provenance[id] = err == nil && c.baseValue != nil && impactJSONEqual(c.ctx, value, c.baseValue)
		if err := c.ctx.Err(); err != nil {
			return nil, err
		}
	}
	return provenance, nil
}

// proposedDocument swaps the proposed contract into every contract linked to
// this design, leaving the draft itself untouched.
func (c *impactUsageCollector) proposedDocument(document Document) Document {
	projected := document
	projected.Contracts = slices.Clone(document.Contracts)
	for i, contract := range projected.Contracts {
		if contract.Source != nil && contract.Source.DesignID == c.report.DesignID {
			projected.Contracts[i].Document = jsonx.RawMessage(c.pair.Proposed)
		}
	}
	return projected
}

func (c *impactUsageCollector) visitMessageFields(s *impactFieldScope, ownerIndex int, execution *StepExecution) (bool, error) {
	base := "/messages/" + strconv.Itoa(ownerIndex) + "/execution"
	for usageIndex, binding := range execution.Bindings {
		if sourceIndex, ok := s.positions[binding.SourceMessageID]; ok {
			stop, err := c.checkField(s, impactFieldUsage{ownerIndex, sourceIndex, usageIndex,
				"binding_source", base + "/bindings/" + strconv.Itoa(usageIndex) + "/sourcePointer",
				apidesign.ImpactFieldSelector{Kind: "response", Pointer: binding.SourcePointer}, &binding})
			if stop || err != nil {
				return stop, err
			}
		}
		stop, err := c.checkField(s, impactFieldUsage{ownerIndex, ownerIndex, usageIndex,
			"binding_target", base + "/bindings/" + strconv.Itoa(usageIndex) + "/target/" + impactTargetFieldName(binding.Target),
			apidesign.ImpactFieldSelector{Kind: binding.Target.Kind, Pointer: binding.Target.Pointer, Name: binding.Target.Name}, &binding})
		if stop || err != nil {
			return stop, err
		}
	}
	for usageIndex, assertion := range execution.Assertions {
		stop, err := c.checkField(s, impactFieldUsage{ownerIndex, ownerIndex, usageIndex,
			"assertion", base + "/assertions/" + strconv.Itoa(usageIndex) + "/pointer",
			apidesign.ImpactFieldSelector{Kind: "response", Pointer: assertion.Pointer}, nil})
		if stop || err != nil {
			return stop, err
		}
	}
	for usageIndex, extract := range execution.Extract {
		stop, err := c.checkField(s, impactFieldUsage{ownerIndex, ownerIndex, usageIndex,
			"extract", base + "/extract/" + strconv.Itoa(usageIndex) + "/pointer",
			apidesign.ImpactFieldSelector{Kind: "response", Pointer: extract.Pointer}, nil})
		if stop || err != nil {
			return stop, err
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

// impactBindingShift is how a binding's source/target compatibility moves
// across the proposal.
type impactBindingShift struct {
	changed, newCompatible, newKnown bool
}

func (c *impactUsageCollector) checkField(s *impactFieldScope, u impactFieldUsage) (bool, error) {
	if err := c.ctx.Err(); err != nil {
		return false, err
	}
	operationMessage := s.draft.Document.Messages[u.operationIndex]
	if operationMessage.Operation == nil {
		return false, nil
	}
	contract, selected := s.contracts[operationMessage.Operation.ContractID]
	if !selected {
		return false, nil
	}
	if c.result.Coverage.FieldUsagesChecked >= apidesign.MaxImpactFieldUsages {
		c.truncate("scenario_fields")
		return true, nil
	}
	c.result.Coverage.FieldUsagesChecked++
	key := operationMessage.Operation.OperationKey
	bp, bt, ap, at := c.fieldPresence(s, u)
	if contract.Invalid || contract.Keys[key].Count != 1 || c.ambiguous[key] {
		bp, bt, ap, at = "unknown", "", "unknown", ""
	}
	previous, next := impactFieldState(bp, bt), impactFieldState(ap, at)
	changed := previous != next
	shift := c.bindingShift(s, u)
	unknown := bp == "unknown" || ap == "unknown"
	relevant := len(c.operations[key]) > 0 || c.ambiguous[key] || changed || shift.changed
	if unknown && relevant {
		c.noteUnknownField(u.pointer)
	}
	if !changed && !shift.changed && (!unknown || !relevant) {
		return false, nil
	}
	owner := s.draft.Document.Messages[u.ownerIndex]
	locator := impactFieldLocator(s.draft, u.operationIndex, key, contract, owner.ID)
	definitive := c.fieldDefinitive(s, u, contract)
	verdict, reason, explanation := impactFieldVerdict(unknown, bp, ap, definitive, shift)
	id := fmt.Sprintf("scenario:%d:%d:%s:%s:%d:%s:%s:%s:%s", s.draft.ScenarioID, s.draft.RevisionID,
		escapePointer(owner.ID), u.kind, u.usageIndex, escapePointer(operationMessage.ID), u.field.Kind,
		escapePointer(u.field.Pointer), escapePointer(u.field.Name))
	if len(c.result.FieldImpacts) >= apidesign.MaxImpactFieldFindings {
		c.truncate("scenario_fields")
		return true, nil
	}
	c.result.FieldImpacts = append(c.result.FieldImpacts, apidesign.ImpactFieldImpact{
		ID: id, Locator: locator, OperationMessageID: operationMessage.ID, UsageKind: u.kind,
		UsagePointer: u.pointer, Field: u.field, Before: previous, After: next,
		Verdict: verdict, ReasonCode: reason, Explanation: explanation,
	})
	return false, nil
}

// noteUnknownField marks the report incomplete and, within the diagnostic
// cap, says which usage could not be resolved.
func (c *impactUsageCollector) noteUnknownField(pointer string) {
	c.result.Complete = false
	if len(c.result.Diagnostics) < 10000 {
		c.result.Diagnostics = append(c.result.Diagnostics, apidesign.ImpactDiagnostic{
			Code: "scenario_field_unknown", Severity: "warning", Side: "after", Pointer: pointer,
			Message: "Не удалось определить поле по сохранённому или предлагаемому контракту.",
		})
	}
}

func impactFieldLocator(draft designUsageDraft, operationIndex int, key string, contract impactContractUsage, ownerID string) apidesign.ImpactLocator {
	locator := apidesign.ImpactLocator{
		Pointer: "/messages/" + strconv.Itoa(operationIndex) + "/operation", OperationKey: key,
		ScenarioID: draft.ScenarioID, ScenarioName: draft.ScenarioName, ScenarioRevision: draft.RevisionID,
		ContractID: contract.Contract.ID, Mode: cmp.Or(contract.Contract.Mode, "copy"),
		PinnedRevisionID: contract.Contract.Source.RevisionID, MessageID: ownerID,
	}
	if embedded := contract.Keys[key]; embedded.Count == 1 {
		locator.Method, locator.Path = embedded.Method, embedded.Path
	}
	return locator
}

// fieldPresence reads the field's presence and type before and after.
func (c *impactUsageCollector) fieldPresence(s *impactFieldScope, u impactFieldUsage) (bp, bt, ap, at string) {
	if u.field.Kind == "response" {
		bp, bt = s.before[u.operationIndex].responseField(c.ctx, u.field.Pointer)
		ap, at = s.after[u.operationIndex].responseField(c.ctx, u.field.Pointer)
		return bp, bt, ap, at
	}
	target := DataBindingTarget{Kind: u.field.Kind, Pointer: u.field.Pointer, Name: u.field.Name}
	bp, bt = s.before[u.operationIndex].targetField(c.ctx, target)
	ap, at = s.after[u.operationIndex].targetField(c.ctx, target)
	return bp, bt, ap, at
}

func (c *impactUsageCollector) bindingShift(s *impactFieldScope, u impactFieldUsage) impactBindingShift {
	if u.binding == nil {
		return impactBindingShift{}
	}
	sourceIndex, sourceExists := s.positions[u.binding.SourceMessageID]
	if !sourceExists {
		return impactBindingShift{}
	}
	oldSourcePresence, oldSourceType := s.before[sourceIndex].responseField(c.ctx, u.binding.SourcePointer)
	newSourcePresence, newSourceType := s.after[sourceIndex].responseField(c.ctx, u.binding.SourcePointer)
	oldTargetPresence, oldTargetType := s.before[u.ownerIndex].targetField(c.ctx, u.binding.Target)
	newTargetPresence, newTargetType := s.after[u.ownerIndex].targetField(c.ctx, u.binding.Target)
	oldCompatible, oldKnown := impactBindingCompatible(impactFieldState(oldSourcePresence, oldSourceType), impactFieldState(oldTargetPresence, oldTargetType), *u.binding)
	newCompatible, newKnown := impactBindingCompatible(impactFieldState(newSourcePresence, newSourceType), impactFieldState(newTargetPresence, newTargetType), *u.binding)
	return impactBindingShift{changed: oldKnown && newKnown && oldCompatible != newCompatible, newCompatible: newCompatible, newKnown: newKnown}
}

// fieldDefinitive holds only when every contract the usage reads through is a
// linked copy of the saved base revision.
func (c *impactUsageCollector) fieldDefinitive(s *impactFieldScope, u impactFieldUsage, contract impactContractUsage) bool {
	definitive := c.contractDefinitive(contract, s.provenance)
	if u.binding == nil {
		return definitive
	}
	sourceIndex, sourceExists := s.positions[u.binding.SourceMessageID]
	if !sourceExists {
		definitive = false
	}
	indices := []int{u.ownerIndex}
	if sourceExists {
		indices = append(indices, sourceIndex)
	}
	for _, index := range indices {
		message := s.draft.Document.Messages[index]
		if message.Operation == nil {
			continue
		}
		if contributor, selected := s.contracts[message.Operation.ContractID]; selected && !c.contractDefinitive(contributor, s.provenance) {
			definitive = false
		}
	}
	return definitive
}

func impactFieldVerdict(unknown bool, bp, ap string, definitive bool, shift impactBindingShift) (verdict, reason, explanation string) {
	verdict, reason, explanation = "review", "scenario_field_changed", "Поле изменилось; проверьте сохранённый шаг с предлагаемым контрактом."
	if unknown {
		reason, explanation = "scenario_field_unknown", "Поле или операция не определены однозначно; проверьте совместимость вручную."
	} else if ap == "absent" && bp == "present" {
		if definitive {
			verdict, reason, explanation = "broken", "scenario_field_absent", "Поле объявлено в сохранённом контракте и отсутствует в закрытой схеме предлагаемого контракта."
		} else {
			reason, explanation = "scenario_field_hypothetical", "Поле отсутствует в предлагаемом контракте; сравните с встроенным контрактом этого шага."
		}
	} else if shift.changed && shift.newKnown && !shift.newCompatible {
		if definitive {
			verdict, reason, explanation = "broken", "scenario_binding_incompatible", "После изменения типы источника и получателя binding несовместимы."
		} else {
			reason, explanation = "scenario_binding_hypothetical", "Типы binding могут стать несовместимыми; сравните встроенный контракт шага с предложением."
		}
	}
	if !definitive && !strings.Contains(explanation, "встроенн") {
		explanation += " Сравните встроенный контракт шага с предложением."
	}
	return verdict, reason, explanation
}

func (c *impactUsageCollector) contractDefinitive(contract impactContractUsage, provenance map[string]bool) bool {
	return contract.Contract.Mode == "linked" && contract.Contract.Source != nil &&
		contract.Contract.Source.RevisionID == c.report.FromRevisionID && provenance[contract.Contract.ID]
}
