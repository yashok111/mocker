package backendportable

import (
	"context"
	"database/sql"
	"slices"
	"strings"

	bm "github.com/yashok111/mocker/internal/backendmodel"
)

type transactionDiagrams struct {
	repo *bm.Repo
	tx   *sql.Tx
}

func (r transactionDiagrams) GetDiagram(ctx context.Context, pid string, pin bm.DiagramPin) (*bm.DiagramVersion, error) {
	return r.repo.GetDiagramTx(ctx, r.tx, pid, pin)
}

func (s *Service) Export(ctx context.Context, in ExportInput) (*ExportResult, error) {
	return portableMutation(ctx, s, "installation", "export", in.IdempotencyKey, in.Selection, func(tx *sql.Tx) (*ExportResult, string, error) {
		model, err := s.exportModelTx(ctx, tx, in.Selection)
		if err != nil {
			return nil, "", err
		}
		installation, err := s.models.InstallationIDTx(ctx, tx)
		if err != nil {
			return nil, "", err
		}
		records, err := modelRecords(*model, installation)
		if err != nil {
			return nil, "", err
		}
		chunks, descriptors, err := splitRecords(records)
		if err != nil {
			return nil, "", err
		}
		schemas := []string{bm.ArtifactContextV3Version}
		for _, source := range model.Sources {
			if !slices.Contains(schemas, source.Revision.SchemaVersion) {
				schemas = append(schemas, source.Revision.SchemaVersion)
			}
		}
		for _, p := range model.Proposals {
			version := bm.ProposalDocumentVersion
			if p.Full != nil {
				version = bm.ChangeProposalDocumentVersion
			}
			if !slices.Contains(schemas, version) {
				schemas = append(schemas, version)
			}
		}
		if len(model.Diagrams) > 0 {
			schemas = append(schemas, bm.DiagramDocumentVersion, "backend-diagram-provenance-v1", "diagram-view-v1")
		}
		for _, v := range model.SavedViews {
			if !slices.Contains(schemas, v.DocumentVersion) {
				schemas = append(schemas, v.DocumentVersion)
			}
		}
		slices.Sort(schemas)
		manifest := Manifest{Format: Format, OriginInstallationID: installation, Schemas: schemas, Selection: in.Selection, Chunks: descriptors}
		if err := manifest.Validate(); err != nil {
			return nil, "", err
		}
		session, err := insertSession(ctx, tx, "export", manifest)
		if err != nil {
			return nil, "", err
		}
		for i, chunk := range chunks {
			if err := putChunk(ctx, tx, session.ID, manifest, i, chunk); err != nil {
				return nil, "", err
			}
		}
		session.State = "ready"
		if err := advanceSession(ctx, tx, session); err != nil {
			return nil, "", err
		}
		return &ExportResult{Session: *session, Manifest: manifest}, session.ID, nil
	})
}
func (s *Service) exportModelTx(ctx context.Context, tx *sql.Tx, selection Selection) (*bm.PortableModel, error) {
	if !bm.ValidID(selection.ProjectID) || !validHash(selection.TargetHash) || selection.DiagramViews == nil || selection.Target.ImportCandidate != nil {
		return nil, fault(422, "Exact immutable project/target and view list required")
	}
	graph, err := s.models.ResolveEffectiveGraphTx(ctx, tx, selection.ProjectID, selection.Target)
	if err != nil {
		return nil, err
	}
	if graph.Pins.TargetHash != selection.TargetHash {
		return nil, fault(409, "Exact export target hash differs")
	}
	project, err := s.models.PortableProjectTx(ctx, tx, selection.ProjectID)
	if err != nil {
		return nil, err
	}
	project.CurrentRevisionID = graph.Pins.BaseRevisionID
	model := &bm.PortableModel{Project: *project, Target: selection.Target, Sources: []bm.PortableSource{}, Proposals: []bm.PortableProposal{}, Diagrams: []bm.DiagramVersion{}, DiagramViews: []bm.DiagramView{}, SavedViews: []bm.SavedView{}, Annotations: []bm.Annotation{}}
	roots := []bm.DiagramPin{}
	views := map[SVGInput]bool{}
	for _, pin := range selection.DiagramViews {
		if views[pin] {
			return nil, fault(422, "Duplicate exact selected view")
		}
		views[pin] = true
		view, err := s.models.GetDiagramViewTx(ctx, tx, project.ID, pin.ViewID, pin.ViewVersion)
		if err != nil {
			return nil, err
		}
		diagram, err := s.models.GetDiagramTx(ctx, tx, project.ID, view.State.Diagram)
		if err != nil {
			return nil, err
		}
		if diagram.TargetHash != selection.TargetHash {
			return nil, fault(422, "Selected view is incompatible with export target")
		}
		model.DiagramViews = append(model.DiagramViews, *view)
		roots = append(roots, view.State.Diagram)
	}
	if len(roots) > 0 {
		model.Diagrams, err = DiagramClosure(ctx, transactionDiagrams{s.models, tx}, project.ID, roots)
		if err != nil {
			return nil, err
		}
	}
	saved := map[SavedViewPin]bool{}
	for _, pin := range selection.SavedViews {
		if saved[pin] {
			return nil, fault(422, "Duplicate saved view")
		}
		saved[pin] = true
		v, err := s.models.GetSavedViewTx(ctx, tx, project.ID, pin.ID, pin.Version)
		if err != nil {
			return nil, err
		}
		a, _ := DocumentHash(v.Target)
		b, _ := DocumentHash(selection.Target)
		if a != b {
			return nil, fault(422, "Saved view targets another exact snapshot")
		}
		model.SavedViews = append(model.SavedViews, *v)
	}
	sourceStates := map[string]uint8{}
	proposals := map[string]bool{}
	var source func(string, int) error
	source = func(id string, depth int) error {
		if sourceStates[id] == 2 {
			return nil
		}
		if sourceStates[id] == 1 {
			return fault(422, "Source history cycle")
		}
		if depth > 1000 || len(sourceStates) >= 1000 {
			return fault(413, "Source closure quota")
		}
		sourceStates[id] = 1
		value, err := s.models.ExportPortableSourceTx(ctx, tx, project.ID, id)
		if err != nil {
			return err
		}
		if err := bm.ValidatePortableSource(ctx, value); err != nil {
			return err
		}
		deps, err := bm.PortableRevisionDependencies(*value)
		if err != nil {
			return err
		}
		slices.Sort(deps)
		for _, dep := range deps {
			if err := source(dep, depth+1); err != nil {
				return err
			}
		}
		model.Sources = append(model.Sources, *value)
		sourceStates[id] = 2
		return nil
	}
	target := func(target bm.BackendReadTarget) error {
		if target.RevisionID != "" {
			return source(target.RevisionID, 0)
		}
		pin := target.ChangeProposal
		kind := "full"
		if pin == nil {
			pin = target.Proposal
			kind = "legacy"
		}
		if pin == nil {
			return fault(422, "Unsupported portable target")
		}
		key := kind + ":" + pin.ProposalID + ":" + pin.ProposalRevisionID
		if proposals[key] {
			return nil
		}
		proposals[key] = true
		p, err := s.models.ExportPortableProposalTx(ctx, tx, project.ID, target)
		if err != nil {
			return err
		}
		for _, rid := range bm.PortableProposalSourceDependencies(*p) {
			if err := source(rid, 0); err != nil {
				return err
			}
		}
		// Merge shared histories once when multiple diagram ancestors select the same owner.
		for i := range model.Proposals {
			old := &model.Proposals[i]
			same := old.Full != nil && p.Full != nil && old.Full.ID == p.Full.ID || old.Legacy != nil && p.Legacy != nil && old.Legacy.ID == p.Legacy.ID
			if same {
				mergePortableProposals(old, p)
				return nil
			}
		}
		model.Proposals = append(model.Proposals, *p)
		return nil
	}
	if err := target(selection.Target); err != nil {
		return nil, err
	}
	for _, d := range model.Diagrams {
		if err := target(d.Document.Target); err != nil {
			return nil, err
		}
	}
	members := map[string]bool{}
	for _, source := range model.Sources {
		for _, n := range source.Nodes {
			members["node:"+n.ID] = true
		}
		for _, e := range source.Edges {
			members["edge:"+e.ID] = true
		}
	}
	annotations, err := s.models.PortableAnnotationsTx(ctx, tx, project.ID)
	if err != nil {
		return nil, err
	}
	for _, a := range annotations {
		if members[a.Target.RecordType+":"+a.Target.ID] && (a.Target.RevisionID == "" || sourceStates[a.Target.RevisionID] == 2) {
			model.Annotations = append(model.Annotations, a)
		}
	}
	if err := normalizePortableClosure(ctx, model, selection); err != nil {
		return nil, err
	}
	return model, nil
}
func mergePortableProposals(dst, src *bm.PortableProposal) {
	for _, v := range src.FullRevisions {
		if !slices.ContainsFunc(dst.FullRevisions, func(old bm.ChangeProposalRevision) bool { return old.ID == v.ID }) {
			dst.FullRevisions = append(dst.FullRevisions, v)
		}
	}
	for _, v := range src.LegacyRevisions {
		if !slices.ContainsFunc(dst.LegacyRevisions, func(old bm.ProposalRevision) bool { return old.ID == v.ID }) {
			dst.LegacyRevisions = append(dst.LegacyRevisions, v)
		}
	}
	for _, v := range src.Batches {
		if !slices.ContainsFunc(dst.Batches, func(old bm.ChangeAppliedBatch) bool { return old.RevisionID == v.RevisionID }) {
			dst.Batches = append(dst.Batches, v)
		}
	}
	for _, v := range src.Identities {
		if !slices.ContainsFunc(dst.Identities, func(old bm.ChangeObjectIdentity) bool { return old.ID == v.ID }) {
			dst.Identities = append(dst.Identities, v)
		}
	}
}
func sortPortableRevisions[T any](items []T, id func(T) string, parent func(T) *string) ([]T, error) {
	byID := map[string]T{}
	for _, v := range items {
		key := id(v)
		if _, ok := byID[key]; ok {
			return nil, fault(422, "Duplicate revision")
		}
		byID[key] = v
	}
	state := map[string]uint8{}
	out := []T{}
	var visit func(string) error
	visit = func(key string) error {
		if state[key] == 2 {
			return nil
		}
		if state[key] == 1 {
			return fault(422, "Revision history cycle")
		}
		v, ok := byID[key]
		if !ok {
			return fault(422, "Missing revision history dependency")
		}
		state[key] = 1
		if p := parent(v); p != nil {
			if err := visit(*p); err != nil {
				return err
			}
		}
		out = append(out, v)
		state[key] = 2
		return nil
	}
	keys := []string{}
	for k := range byID {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, strings.Compare)
	for _, key := range keys {
		if err := visit(key); err != nil {
			return nil, err
		}
	}
	return out, nil
}
