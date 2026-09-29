package designscenario

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/yashok111/mocker/internal/apidesign"
)

// ImpactScenarioResult describes current saved drafts, independently of the API
// snapshot transaction. Complete does not establish compatibility of step data.
type ImpactScenarioResult struct {
	Affected    []apidesign.ImpactEntity
	Evidence    []apidesign.ImpactEvidence
	Diagnostics []apidesign.ImpactDiagnostic
	Coverage    apidesign.ImpactCoverage
	Complete    bool
}

type impactOperationUsage struct {
	Locator  apidesign.ImpactLocator
	Evidence apidesign.ImpactEvidence
}

type impactContractUsage struct {
	Contract Contract
	Index    int
	Keys     map[string]impactEmbeddedOperation
	Invalid  bool
}

type impactEmbeddedOperation struct {
	Count   int
	Pointer string
	Method  string
	Path    string
}

type impactUsageCollector struct {
	ctx        context.Context
	report     apidesign.ImpactReport
	operations map[string][]impactOperationUsage
	ambiguous  map[string]bool
	result     ImpactScenarioResult
}

// ImpactUsages joins messages to operation evidence through their exact contract
// source and stable key. It does not refresh or execute embedded contracts.
func (r *Repo) ImpactUsages(ctx context.Context, report apidesign.ImpactReport) (ImpactScenarioResult, error) {
	collector := impactUsageCollector{
		ctx: ctx, report: report, operations: impactOperationUsages(report),
		ambiguous: impactAmbiguousOperationKeys(report),
		result: ImpactScenarioResult{
			Affected: []apidesign.ImpactEntity{}, Evidence: []apidesign.ImpactEvidence{},
			Diagnostics: []apidesign.ImpactDiagnostic{},
			Coverage:    apidesign.ImpactCoverage{TruncatedReasons: []string{}}, Complete: true,
		},
	}
	if err := ctx.Err(); err != nil {
		return collector.result, err
	}
	if len(report.Changes) == 0 {
		return collector.result, nil
	}
	read, err := r.readDesignUsageDrafts(ctx, collector.visit)
	if err != nil {
		return collector.result, err
	}
	collector.result.Coverage.ScenariosScanned = read.Scanned
	for _, reason := range read.TruncatedReasons {
		collector.truncate(reason)
	}
	collector.result.Coverage.ScenarioUsagesReturned = len(collector.result.Affected)
	collector.result.Coverage.EntitiesReturned = len(collector.result.Affected)
	collector.result.Coverage.EvidenceReturned = len(collector.result.Evidence)
	return collector.result, nil
}

func impactOperationUsages(report apidesign.ImpactReport) map[string][]impactOperationUsage {
	entities := map[string]apidesign.ImpactEntity{}
	for _, entity := range report.Affected {
		if entity.Kind == "operation" {
			entities[entity.ID] = entity
		}
	}
	result := map[string][]impactOperationUsage{}
	for _, evidence := range report.Evidence {
		entity, exists := entities[evidence.EntityID]
		if !exists {
			continue
		}
		locator := entity.Before
		if evidence.Side == "after" {
			locator = entity.After
		}
		if locator == nil || locator.OperationKey == "" {
			continue
		}
		result[locator.OperationKey] = append(result[locator.OperationKey], impactOperationUsage{
			Locator: *locator, Evidence: evidence,
		})
	}
	return result
}

func impactAmbiguousOperationKeys(report apidesign.ImpactReport) map[string]bool {
	seen := map[string]string{}
	ambiguous := map[string]bool{}
	for _, entity := range report.Affected {
		if entity.Kind != "operation" {
			continue
		}
		for side, locator := range []*apidesign.ImpactLocator{entity.Before, entity.After} {
			if locator == nil || locator.OperationKey == "" {
				continue
			}
			identity := strconv.Itoa(side) + ":" + locator.OperationKey
			if previous, exists := seen[identity]; exists && previous != entity.ID {
				ambiguous[locator.OperationKey] = true
			}
			seen[identity] = entity.ID
		}
	}
	return ambiguous
}

func (c *impactUsageCollector) visit(draft designUsageDraft) (bool, error) {
	contracts := map[string]impactContractUsage{}
	for index, contract := range draft.Document.Contracts {
		if err := c.ctx.Err(); err != nil {
			return false, err
		}
		if contract.Source == nil || contract.Source.DesignID != c.report.DesignID {
			continue
		}
		keys, invalid, err := impactEmbeddedOperationKeys(c.ctx, contract)
		if err != nil {
			return false, err
		}
		if _, duplicate := contracts[contract.ID]; duplicate {
			invalid = true
		}
		contracts[contract.ID] = impactContractUsage{Contract: contract, Index: index, Keys: keys, Invalid: invalid}
	}
	for index, message := range draft.Document.Messages {
		if err := c.ctx.Err(); err != nil {
			return false, err
		}
		if message.Operation == nil {
			continue
		}
		contract, exists := contracts[message.Operation.ContractID]
		if !exists {
			continue
		}
		key := message.Operation.OperationKey
		uniqueEmbeddedKey := key != "" && contract.Keys[key].Count == 1
		known := uniqueEmbeddedKey && !contract.Invalid && !c.ambiguous[key]
		usages := c.operations[key]
		if known && len(usages) == 0 {
			continue
		}
		if len(c.result.Affected) >= resourceMapUsageLimit {
			c.truncate("scenario_usages")
			return true, nil
		}
		c.addMessage(draft, index, contract, known)
		if slices.Contains(c.result.Coverage.TruncatedReasons, "evidence") {
			return true, nil
		}
	}
	return false, nil
}

// Match the scenario resolver's direct paths operations. Inherited Path Items
// cannot be confirmed by the runtime resolver and remain unknown here too.
func impactEmbeddedOperationKeys(
	ctx context.Context,
	contract Contract,
) (map[string]impactEmbeddedOperation, bool, error) {
	keys := map[string]impactEmbeddedOperation{}
	value, err := decodeJSONValue(contract.Document)
	root := object(value)
	if err != nil || root == nil {
		return keys, true, nil
	}
	paths := object(root["paths"])
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		item := object(paths[path])
		for _, method := range operationMethods {
			operation := object(item[method])
			if key := stringValue(operation[apidesign.OperationKey]); key != "" {
				previous := keys[key]
				keys[key] = impactEmbeddedOperation{
					Count: previous.Count + 1, Pointer: "/paths/" + escapePointer(path) + "/" + method,
					Method: method, Path: path,
				}
			}
		}
	}
	return keys, false, nil
}

func (c *impactUsageCollector) addMessage(
	draft designUsageDraft,
	index int,
	contract impactContractUsage,
	known bool,
) {
	message := draft.Document.Messages[index]
	pointer := "/messages/" + strconv.Itoa(index) + "/operation"
	locator := apidesign.ImpactLocator{
		Pointer: pointer, OperationKey: message.Operation.OperationKey,
		ScenarioID: draft.ScenarioID, ScenarioName: draft.ScenarioName, ScenarioRevision: draft.RevisionID,
		ContractID: contract.Contract.ID, PinnedRevisionID: contract.Contract.Source.RevisionID,
		Mode: cmp.Or(contract.Contract.Mode, "copy"), MessageID: message.ID,
	}
	entity := apidesign.ImpactEntity{
		ID: fmt.Sprintf(
			"scenario:%d:%d:%d",
			draft.ScenarioID,
			draft.RevisionID,
			index,
		),
		Kind:  "scenario_message",
		Label: draft.ScenarioName + ": " + cmp.Or(message.Label, message.ID),
	}
	if !known {
		entity.After = new(locator)
		c.result.Affected = append(c.result.Affected, entity)
		c.result.Complete = false
		c.diagnostic(entity, "scenario_binding_unknown", "Не удалось подтвердить привязку в сохранённом контракте шага.")
		c.caveats(entity, locator)
		return
	}
	seen := map[string]bool{}
	for _, usage := range c.operations[message.Operation.OperationKey] {
		identity := usage.Evidence.ChangeID + ":" + usage.Evidence.Side + ":" + usage.Evidence.Direction
		if seen[identity] {
			continue
		}
		seen[identity] = true
		if len(c.result.Evidence) >= 10000 {
			c.truncate("evidence")
			break
		}
		sideLocator := locator
		embedded := contract.Keys[message.Operation.OperationKey]
		sideLocator.Method, sideLocator.Path = embedded.Method, embedded.Path
		sideLocator.SourcePointer = "/contracts/" + strconv.Itoa(contract.Index) + "/document" + embedded.Pointer
		if usage.Evidence.Side == "before" {
			entity.Before = new(sideLocator)
		} else {
			entity.After = new(sideLocator)
		}
		sites := append(slices.Clone(usage.Evidence.ReferenceSites), apidesign.ImpactReferenceSite{
			Pointer: pointer, TargetPointer: usage.Locator.Pointer, Kind: "scenario_operation",
		})
		c.result.Evidence = append(c.result.Evidence, apidesign.ImpactEvidence{
			ID: entity.ID + ":" + usage.Evidence.ID, EntityID: entity.ID,
			ChangeID: usage.Evidence.ChangeID, Side: usage.Evidence.Side, Direction: usage.Evidence.Direction,
			ReferenceSites: sites,
			Explanation:    "Шаг использует затронутую операцию; при обновлении контракта проверьте данные шага.",
		})
	}
	if entity.Before == nil && entity.After == nil {
		return
	}
	c.result.Affected = append(c.result.Affected, entity)
	c.caveats(entity, locator)
}

func (c *impactUsageCollector) caveats(entity apidesign.ImpactEntity, locator apidesign.ImpactLocator) {
	if locator.Mode == "copy" {
		c.diagnostic(entity, "scenario_copy",
			"Копия контракта могла быть изменена; последствия обновления требуют отдельной проверки.")
	}
	if locator.PinnedRevisionID != c.report.FromRevisionID {
		c.diagnostic(entity, "scenario_revision_mismatch", "Ревизия контракта шага отличается от базы сравнения.")
	}
}

func (c *impactUsageCollector) diagnostic(entity apidesign.ImpactEntity, code, message string) {
	locator, side := entity.After, "after"
	if locator == nil {
		locator, side = entity.Before, "before"
	}
	c.result.Diagnostics = append(c.result.Diagnostics, apidesign.ImpactDiagnostic{
		Code: code, Severity: "warning", Side: side, Pointer: locator.Pointer, EntityID: entity.ID, Message: message,
	})
}

func (c *impactUsageCollector) truncate(reason string) {
	if slices.Contains(c.result.Coverage.TruncatedReasons, reason) {
		return
	}
	c.result.Complete = false
	c.result.Coverage.TruncatedReasons = append(c.result.Coverage.TruncatedReasons, reason)
	c.result.Diagnostics = append(c.result.Diagnostics, apidesign.ImpactDiagnostic{
		Code: reason, Severity: "warning", Side: "after", Pointer: "",
		Message: "Достигнут лимит анализа сценариев; часть зависимостей могла не попасть в результат.",
	})
}
