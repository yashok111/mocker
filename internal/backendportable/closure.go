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
	sourceMap, err := indexSources(model)
	if err != nil {
		return err
	}
	w := &closureWalker{ctx: ctx, model: model, sourceMap: sourceMap, sourceState: map[string]uint8{}, ordered: []bm.PortableSource{}, usedProposals: map[string]bool{}}
	base, ok := w.sourceMap[pins.BaseRevisionID]
	if !ok || (base.Revision.SchemaVersion != "5" && base.Revision.SchemaVersion != "6") {
		return fault(422, "Portable selected base requires source schema 5 or 6")
	}
	if err := w.target(model.Target); err != nil {
		return err
	}
	if err := w.diagrams(selection); err != nil {
		return err
	}
	if err := w.savedViews(selection); err != nil {
		return err
	}
	for _, a := range model.Annotations {
		if a.Target.RevisionID != "" {
			if err := w.source(a.Target.RevisionID, 0); err != nil {
				return err
			}
		}
	}
	if len(w.ordered) != len(model.Sources) || len(w.usedProposals) != len(model.Proposals) {
		return fault(422, "Extraneous source/proposal history")
	}
	model.Sources = w.ordered
	return nil
}

// indexSources indexes the model's revisions by id; each must be distinct and
// the project's own.
func indexSources(model *bm.PortableModel) (map[string]bm.PortableSource, error) {
	sourceMap := map[string]bm.PortableSource{}
	for _, s := range model.Sources {
		if _, ok := sourceMap[s.Revision.ID]; ok {
			return nil, fault(422, "Duplicate source revision")
		}
		if s.Revision.ProjectID != model.Project.ID {
			return nil, fault(422, "Source outside project")
		}
		sourceMap[s.Revision.ID] = s
	}
	return sourceMap, nil
}

// closureWalker collects the exact history the selection reaches: sources in
// dependency order, and each proposal once with its revisions sorted.
// Anything the walk does not reach is extraneous.
type closureWalker struct {
	ctx           context.Context
	model         *bm.PortableModel
	sourceMap     map[string]bm.PortableSource
	sourceState   map[string]uint8
	ordered       []bm.PortableSource
	usedProposals map[string]bool
}

// source visits a revision after its dependencies (depth-first, sorted),
// refusing a cycle and an unbounded chain.
func (w *closureWalker) source(id string, depth int) error {
	if w.sourceState[id] == 2 {
		return nil
	}
	if w.sourceState[id] == 1 {
		return fault(422, "Source history cycle")
	}
	if depth > 1000 {
		return fault(413, "Source dependency depth")
	}
	s, ok := w.sourceMap[id]
	if !ok {
		return fault(422, "Missing source history")
	}
	w.sourceState[id] = 1
	deps, err := bm.PortableRevisionDependencies(s)
	if err != nil {
		return err
	}
	slices.Sort(deps)
	for _, dep := range deps {
		if err := w.source(dep, depth+1); err != nil {
			return err
		}
	}
	if err := bm.ValidatePortableSource(w.ctx, &s); err != nil {
		return err
	}
	w.ordered = append(w.ordered, s)
	w.sourceState[id] = 2
	return nil
}

// target visits a revision target's source, or a proposal target's owner and
// the sources it depends on.
func (w *closureWalker) target(t bm.BackendReadTarget) error {
	if t.RevisionID != "" {
		return w.source(t.RevisionID, 0)
	}
	for i := range w.model.Proposals {
		p := &w.model.Proposals[i]
		matched := t.ChangeProposal != nil && p.Full != nil && p.Full.ID == t.ChangeProposal.ProposalID || t.Proposal != nil && p.Legacy != nil && p.Legacy.ID == t.Proposal.ProposalID
		if matched {
			return w.proposal(p)
		}
	}
	return fault(422, "Missing exact proposal owner")
}

func (w *closureWalker) proposal(p *bm.PortableProposal) error {
	var key string
	if p.Full != nil {
		key = "full:" + p.Full.ID
	} else {
		key = "legacy:" + p.Legacy.ID
	}
	if w.usedProposals[key] {
		return nil
	}
	w.usedProposals[key] = true
	var err error
	p.FullRevisions, err = sortPortableRevisions(p.FullRevisions, func(v bm.ChangeProposalRevision) string { return v.ID }, func(v bm.ChangeProposalRevision) *string { return v.ParentRevisionID })
	if err != nil {
		return err
	}
	p.LegacyRevisions, err = sortPortableRevisions(p.LegacyRevisions, func(v bm.ProposalRevision) string { return v.ID }, func(v bm.ProposalRevision) *string { return v.ParentRevisionID })
	if err != nil {
		return err
	}
	for _, dep := range bm.PortableProposalSourceDependencies(*p) {
		if err := w.source(dep, 0); err != nil {
			return err
		}
	}
	return nil
}

// diagrams requires exactly the selected views, and exactly the diagram
// history they reach, each diagram pinned to its own immutable target.
func (w *closureWalker) diagrams(selection Selection) error {
	model := w.model
	diagrams := memoryDiagrams{versions: map[bm.DiagramPin]bm.DiagramVersion{}}
	for _, v := range model.Diagrams {
		if _, ok := diagrams.versions[v.Pin]; ok {
			return fault(422, "Duplicate diagram version")
		}
		diagrams.versions[v.Pin] = v
	}
	roots, err := w.selectedViewRoots(diagrams, selection)
	if err != nil {
		return err
	}
	if len(roots) > 0 {
		closure, err := DiagramClosure(w.ctx, diagrams, model.Project.ID, roots)
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
		if err := w.target(d.Document.Target); err != nil {
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
	return nil
}

// selectedViewRoots returns the diagram each selected view shows; every view
// must be selected once and target the selected graph.
func (w *closureWalker) selectedViewRoots(diagrams memoryDiagrams, selection Selection) ([]bm.DiagramPin, error) {
	roots := []bm.DiagramPin{}
	views := map[SVGInput]bool{}
	for _, v := range w.model.DiagramViews {
		pin := SVGInput{ViewID: v.ID, ViewVersion: v.Version}
		if views[pin] || !slices.Contains(selection.DiagramViews, pin) {
			return nil, fault(422, "Duplicate or unselected diagram view")
		}
		views[pin] = true
		d, err := diagrams.GetDiagram(w.ctx, w.model.Project.ID, v.State.Diagram)
		if err != nil {
			return nil, err
		}
		if d.TargetHash != selection.TargetHash {
			return nil, fault(422, "Selected view targets incompatible graph")
		}
		roots = append(roots, v.State.Diagram)
	}
	if len(views) != len(selection.DiagramViews) {
		return nil, fault(422, "Selected view record is missing")
	}
	return roots, nil
}

func (w *closureWalker) savedViews(selection Selection) error {
	saved := map[SavedViewPin]bool{}
	for _, v := range w.model.SavedViews {
		pin := SavedViewPin{ID: v.ID, Version: v.Version}
		if saved[pin] || !slices.Contains(selection.SavedViews, pin) {
			return fault(422, "Duplicate/unselected legacy saved view")
		}
		saved[pin] = true
		if err := w.target(v.Target); err != nil {
			return err
		}
		pins, err := bm.PortableTargetPins(*w.model, v.Target)
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
