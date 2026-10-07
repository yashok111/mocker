package apidesign

import (
	"cmp"
	"context"
	"slices"

	"github.com/yashok111/mocker/internal/jsonx"
)

const impactResponseBytes = 4 << 20

// impactFinalizer carries the one byte budget every section of the report
// draws from, so the sections can be admitted by separate methods.
type impactFinalizer struct {
	ctx      context.Context
	report   *ImpactReport
	budget   int
	changes  map[string]bool
	entities map[string]ImpactEntity
	retained map[string]bool
}

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
	report.FieldImpacts = []ImpactFieldImpact{}
	report.Diagnostics = []ImpactDiagnostic{}
	report.Coverage.TruncatedReasons = append([]string{}, source.Coverage.TruncatedReasons...)
	f := &impactFinalizer{
		ctx:    ctx,
		report: report,
		budget: impactResponseBytes - 4096, // Snapshot fields, counts and the final limit diagnostic.
	}
	if err := f.keepChanges(source.Changes); err != nil {
		return err
	}
	f.entities = make(map[string]ImpactEntity, len(source.Affected))
	for _, entity := range source.Affected {
		if err := ctx.Err(); err != nil {
			return err
		}
		f.entities[entity.ID] = entity
	}
	f.retained = make(map[string]bool)
	if err := f.keepEvidence(source.Evidence); err != nil {
		return err
	}
	if err := f.keepFieldImpacts(source.FieldImpacts); err != nil {
		return err
	}
	if err := f.keepDiagnostics(source.Diagnostics); err != nil {
		return err
	}
	f.finishCoverage()
	return ctx.Err()
}

func (f *impactFinalizer) mark(reason string) {
	f.report.Complete = false
	if !slices.Contains(f.report.Coverage.TruncatedReasons, reason) {
		f.report.Coverage.TruncatedReasons = append(f.report.Coverage.TruncatedReasons, reason)
	}
}

func (f *impactFinalizer) size(value any) (int, error) {
	if err := f.ctx.Err(); err != nil {
		return 0, err
	}
	b, err := jsonx.Marshal(value)
	return len(b) + 1, err
}

func (f *impactFinalizer) keepChanges(changes []ImpactChange) error {
	f.changes = make(map[string]bool)
	for _, change := range changes {
		if len(f.report.Changes) >= 500 {
			f.mark("changes")
			break
		}
		n, err := f.size(change)
		if err != nil {
			return err
		}
		if n > f.budget {
			f.mark("output")
			break
		}
		f.budget -= n
		f.report.Changes = append(f.report.Changes, change)
		f.changes[change.ID] = true
	}
	return nil
}

// addEntity admits the entity an evidence row or diagnostic names, reserving
// extraBytes for that row too; false means the row must go without it.
func (f *impactFinalizer) addEntity(id string, extraBytes int) (bool, error) {
	if f.retained[id] {
		if extraBytes > f.budget {
			f.mark("output")
			return false, nil
		}
		return true, nil
	}
	entity, exists := f.entities[id]
	if !exists {
		return false, nil
	}
	if len(f.report.Affected) >= 5000 {
		f.mark("entities")
		return false, nil
	}
	n, err := f.size(entity)
	if err != nil {
		return false, err
	}
	if n+extraBytes > f.budget {
		f.mark("output")
		return false, nil
	}
	f.budget -= n
	f.report.Affected = append(f.report.Affected, entity)
	f.retained[id] = true
	return true, nil
}

func (f *impactFinalizer) keepEvidence(evidenceRows []ImpactEvidence) error {
	for _, evidence := range evidenceRows {
		if err := f.ctx.Err(); err != nil {
			return err
		}
		if !f.changes[evidence.ChangeID] {
			continue
		}
		if len(f.report.Evidence) >= 10000 {
			f.mark("evidence")
			break
		}
		if evidence.ReferenceSites == nil {
			evidence.ReferenceSites = []ImpactReferenceSite{}
		}
		n, err := f.size(evidence)
		if err != nil {
			return err
		}
		ok, err := f.addEntity(evidence.EntityID, n)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		f.budget -= n
		f.report.Evidence = append(f.report.Evidence, evidence)
	}
	return nil
}

func (f *impactFinalizer) keepFieldImpacts(findings []ImpactFieldImpact) error {
	for _, finding := range findings {
		if len(f.report.FieldImpacts) >= MaxImpactFieldFindings {
			f.mark("scenario_fields")
			break
		}
		n, err := f.size(finding)
		if err != nil {
			return err
		}
		if n > f.budget {
			f.mark("output")
			break
		}
		f.budget -= n
		f.report.FieldImpacts = append(f.report.FieldImpacts, finding)
	}
	return nil
}

func (f *impactFinalizer) keepDiagnostics(diagnostics []ImpactDiagnostic) error {
	for _, diagnostic := range diagnostics {
		n, err := f.size(diagnostic)
		if err != nil {
			return err
		}
		if diagnostic.EntityID != "" {
			ok, err := f.addEntity(diagnostic.EntityID, n)
			if err != nil {
				return err
			}
			if !ok {
				diagnostic.EntityID = ""
			}
		}
		if n > f.budget {
			f.mark("output")
			break
		}
		f.budget -= n
		f.report.Diagnostics = append(f.report.Diagnostics, diagnostic)
	}
	return nil
}

func (f *impactFinalizer) finishCoverage() {
	report := f.report
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
	report.Coverage.FieldImpactsReturned = len(report.FieldImpacts)
	report.Coverage.ScenarioUsagesReturned = 0
	for _, entity := range report.Affected {
		if entity.Kind == "scenario_message" {
			report.Coverage.ScenarioUsagesReturned++
		}
	}
}
