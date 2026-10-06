package backendportable

import (
	"context"
	"slices"

	bm "github.com/yashok111/mocker/internal/backendmodel"
)

type memoryDiagrams struct {
	versions map[bm.DiagramPin]bm.DiagramVersion
}

func (r memoryDiagrams) GetDiagram(_ context.Context, pid string, pin bm.DiagramPin) (*bm.DiagramVersion, error) {
	v, ok := r.versions[pin]
	if !ok || v.ProjectID != pid {
		return nil, fault(422, "Missing exact diagram closure member")
	}
	return &v, nil
}
func normalizePortableClosure(ctx context.Context, model *bm.PortableModel, selection Selection) error {
	if model.Project.Version <= 0 || model.Project.CreatedAt.IsZero() || model.Project.UpdatedAt.IsZero() {
		return fault(422, "Complete project metadata required")
	}
	if model.Project.ID != selection.ProjectID {
		return fault(422, "Project identity differs from selection")
	}
	pins, err := bm.PortableTargetPins(*model, model.Target)
	if err != nil {
		return err
	}
	if pins.TargetHash != selection.TargetHash {
		return fault(409, "Portable targetHash does not match its exact source/proposal pins")
	}
	if model.Project.CurrentRevisionID != pins.BaseRevisionID {
		return fault(422, "Project source head must equal selected immutable source")
	}
	sourceMap := map[string]bm.PortableSource{}
	for _, s := range model.Sources {
		if _, ok := sourceMap[s.Revision.ID]; ok {
			return fault(422, "Duplicate source revision")
		}
		if s.Revision.ProjectID != model.Project.ID {
			return fault(422, "Source outside project")
		}
		sourceMap[s.Revision.ID] = s
	}
	base, ok := sourceMap[pins.BaseRevisionID]
	if !ok || (base.Revision.SchemaVersion != "5" && base.Revision.SchemaVersion != "6") {
		return fault(422, "Portable selected base requires source schema 5 or 6")
	}
	sourceState := map[string]uint8{}
	ordered := []bm.PortableSource{}
	var source func(string, int) error
	source = func(id string, depth int) error {
		if sourceState[id] == 2 {
			return nil
		}
		if sourceState[id] == 1 {
			return fault(422, "Source history cycle")
		}
		if depth > 1000 {
			return fault(413, "Source dependency depth")
		}
		s, ok := sourceMap[id]
		if !ok {
			return fault(422, "Missing source history")
		}
		sourceState[id] = 1
		deps, err := bm.PortableRevisionDependencies(s)
		if err != nil {
			return err
		}
		slices.Sort(deps)
		for _, dep := range deps {
			if err := source(dep, depth+1); err != nil {
				return err
			}
		}
		if err := bm.ValidatePortableSource(ctx, &s); err != nil {
			return err
		}
		ordered = append(ordered, s)
		sourceState[id] = 2
		return nil
	}
	usedProposals := map[string]bool{}
	target := func(t bm.BackendReadTarget) error {
		if t.RevisionID != "" {
			return source(t.RevisionID, 0)
		}
		for i := range model.Proposals {
			p := &model.Proposals[i]
			matched := t.ChangeProposal != nil && p.Full != nil && p.Full.ID == t.ChangeProposal.ProposalID || t.Proposal != nil && p.Legacy != nil && p.Legacy.ID == t.Proposal.ProposalID
			if !matched {
				continue
			}
			key := ""
			if p.Full != nil {
				key = "full:" + p.Full.ID
			} else {
				key = "legacy:" + p.Legacy.ID
			}
			if usedProposals[key] {
				return nil
			}
			usedProposals[key] = true
			p.FullRevisions, err = sortPortableRevisions(p.FullRevisions, func(v bm.ChangeProposalRevision) string { return v.ID }, func(v bm.ChangeProposalRevision) *string { return v.ParentRevisionID })
			if err != nil {
				return err
			}
			p.LegacyRevisions, err = sortPortableRevisions(p.LegacyRevisions, func(v bm.ProposalRevision) string { return v.ID }, func(v bm.ProposalRevision) *string { return v.ParentRevisionID })
			if err != nil {
				return err
			}
			for _, dep := range bm.PortableProposalSourceDependencies(*p) {
				if err := source(dep, 0); err != nil {
					return err
				}
			}
			return nil
		}
		return fault(422, "Missing exact proposal owner")
	}
	if err := target(model.Target); err != nil {
		return err
	}
	diagrams := memoryDiagrams{versions: map[bm.DiagramPin]bm.DiagramVersion{}}
	for _, v := range model.Diagrams {
		if _, ok := diagrams.versions[v.Pin]; ok {
			return fault(422, "Duplicate diagram version")
		}
		diagrams.versions[v.Pin] = v
	}
	roots := []bm.DiagramPin{}
	views := map[SVGInput]bool{}
	for _, v := range model.DiagramViews {
		pin := SVGInput{ViewID: v.ID, ViewVersion: v.Version}
		if views[pin] || !slices.Contains(selection.DiagramViews, pin) {
			return fault(422, "Duplicate or unselected diagram view")
		}
		views[pin] = true
		d, err := diagrams.GetDiagram(ctx, model.Project.ID, v.State.Diagram)
		if err != nil {
			return err
		}
		if d.TargetHash != selection.TargetHash {
			return fault(422, "Selected view targets incompatible graph")
		}
		roots = append(roots, v.State.Diagram)
	}
	if len(views) != len(selection.DiagramViews) {
		return fault(422, "Selected view record is missing")
	}
	if len(roots) > 0 {
		closure, err := DiagramClosure(ctx, diagrams, model.Project.ID, roots)
		if err != nil {
			return err
		}
		if len(closure) != len(model.Diagrams) {
			return fault(422, "Extraneous diagram history")
		}
		model.Diagrams = closure
	} else if len(model.Diagrams) > 0 {
		return fault(422, "Unselected diagram records")
	}
	for _, d := range model.Diagrams {
		if err := target(d.Document.Target); err != nil {
			return err
		}
		pins, err := bm.PortableTargetPins(*model, d.Document.Target)
		if err != nil {
			return err
		}
		if pins.TargetHash != d.TargetHash {
			return fault(422, "Diagram target hash differs from its immutable source")
		}
	}
	saved := map[SavedViewPin]bool{}
	for _, v := range model.SavedViews {
		pin := SavedViewPin{ID: v.ID, Version: v.Version}
		if saved[pin] || !slices.Contains(selection.SavedViews, pin) {
			return fault(422, "Duplicate/unselected legacy saved view")
		}
		saved[pin] = true
		if err := target(v.Target); err != nil {
			return err
		}
		pins, err := bm.PortableTargetPins(*model, v.Target)
		if err != nil {
			return err
		}
		if pins.TargetHash != selection.TargetHash {
			return fault(422, "Saved view target differs")
		}
	}
	if len(saved) != len(selection.SavedViews) {
		return fault(422, "Missing selected saved view")
	}
	for _, a := range model.Annotations {
		if a.Target.RevisionID != "" {
			if err := source(a.Target.RevisionID, 0); err != nil {
				return err
			}
		}
	}
	if len(ordered) != len(model.Sources) || len(usedProposals) != len(model.Proposals) {
		return fault(422, "Extraneous source/proposal history")
	}
	model.Sources = ordered
	return nil
}

func validateManifestSchemas(model bm.PortableModel, m Manifest) error {
	required := []string{bm.ArtifactContextV3Version}
	for _, source := range model.Sources {
		required = append(required, source.Revision.SchemaVersion)
	}
	for _, p := range model.Proposals {
		if p.Full != nil {
			required = append(required, bm.ChangeProposalDocumentVersion)
		} else {
			required = append(required, bm.ProposalDocumentVersion)
		}
	}
	if len(model.Diagrams) > 0 {
		required = append(required, bm.DiagramDocumentVersion, "backend-diagram-provenance-v1")
	}
	if len(model.DiagramViews) > 0 {
		required = append(required, "diagram-view-v1")
	}
	for _, view := range model.SavedViews {
		required = append(required, view.DocumentVersion)
	}
	for _, version := range required {
		if !slices.Contains(m.Schemas, version) {
			return fault(422, "Record uses an undeclared schema version: "+version)
		}
	}
	return nil
}
