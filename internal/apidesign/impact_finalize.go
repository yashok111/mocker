package apidesign

import (
	"cmp"
	"context"
	"slices"

	"github.com/yashok111/mocker/internal/jsonx"
)

const impactResponseBytes = 4 << 20

// FinalizeImpactReport bounds the combined API/scenario response and retains
// only evidence whose change and entity are present. It never upgrades completeness.
func FinalizeImpactReport(ctx context.Context, report *ImpactReport) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	source := *report
	report.Changes = []ImpactChange{}
	report.Affected = []ImpactEntity{}
	report.Evidence = []ImpactEvidence{}
	report.Diagnostics = []ImpactDiagnostic{}
	report.Coverage.TruncatedReasons = append([]string{}, source.Coverage.TruncatedReasons...)
	budget := impactResponseBytes - 4096 // Snapshot fields, counts and the final limit diagnostic.
	mark := func(reason string) {
		report.Complete = false
		if !slices.Contains(report.Coverage.TruncatedReasons, reason) {
			report.Coverage.TruncatedReasons = append(report.Coverage.TruncatedReasons, reason)
		}
	}
	size := func(value any) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		b, err := jsonx.Marshal(value)
		return len(b) + 1, err
	}
	changes := make(map[string]bool)
	for _, change := range source.Changes {
		if len(report.Changes) >= 500 {
			mark("changes")
			break
		}
		n, err := size(change)
		if err != nil {
			return err
		}
		if n > budget {
			mark("output")
			break
		}
		budget -= n
		report.Changes = append(report.Changes, change)
		changes[change.ID] = true
	}
	entities := make(map[string]ImpactEntity, len(source.Affected))
	for _, entity := range source.Affected {
		if err := ctx.Err(); err != nil {
			return err
		}
		entities[entity.ID] = entity
	}
	retained := make(map[string]bool)
	addEntity := func(id string, extraBytes int) (bool, error) {
		if retained[id] {
			if extraBytes > budget {
				mark("output")
				return false, nil
			}
			return true, nil
		}
		entity, exists := entities[id]
		if !exists {
			return false, nil
		}
		if len(report.Affected) >= 5000 {
			mark("entities")
			return false, nil
		}
		n, err := size(entity)
		if err != nil {
			return false, err
		}
		if n+extraBytes > budget {
			mark("output")
			return false, nil
		}
		budget -= n
		report.Affected = append(report.Affected, entity)
		retained[id] = true
		return true, nil
	}
	for _, evidence := range source.Evidence {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !changes[evidence.ChangeID] {
			continue
		}
		if len(report.Evidence) >= 10000 {
			mark("evidence")
			break
		}
		if evidence.ReferenceSites == nil {
			evidence.ReferenceSites = []ImpactReferenceSite{}
		}
		n, err := size(evidence)
		if err != nil {
			return err
		}
		ok, err := addEntity(evidence.EntityID, n)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		budget -= n
		report.Evidence = append(report.Evidence, evidence)
	}
	for _, diagnostic := range source.Diagnostics {
		n, err := size(diagnostic)
		if err != nil {
			return err
		}
		if diagnostic.EntityID != "" {
			ok, err := addEntity(diagnostic.EntityID, n)
			if err != nil {
				return err
			}
			if !ok {
				diagnostic.EntityID = ""
			}
		}
		if n > budget {
			mark("output")
			break
		}
		budget -= n
		report.Diagnostics = append(report.Diagnostics, diagnostic)
	}
	if slices.Contains(report.Coverage.TruncatedReasons, "output") {
		report.Diagnostics = append(report.Diagnostics, ImpactDiagnostic{
			Code: "output_limit", Severity: "warning", Side: "after", Pointer: "",
			Message: "Отчёт сокращён до 4 MiB; показана только найденная часть зависимостей",
		})
	}
	slices.SortFunc(report.Affected, func(a, b ImpactEntity) int { return cmp.Compare(a.ID, b.ID) })
	slices.Sort(report.Coverage.TruncatedReasons)
	report.Coverage.TruncatedReasons = slices.Compact(report.Coverage.TruncatedReasons)
	if len(report.Coverage.TruncatedReasons) > 0 {
		report.Complete = false
	}
	report.Coverage.ChangesReturned = len(report.Changes)
	report.Coverage.EntitiesReturned = len(report.Affected)
	report.Coverage.EvidenceReturned = len(report.Evidence)
	report.Coverage.ScenarioUsagesReturned = 0
	for _, entity := range report.Affected {
		if entity.Kind == "scenario_message" {
			report.Coverage.ScenarioUsagesReturned++
		}
	}
	return ctx.Err()
}
