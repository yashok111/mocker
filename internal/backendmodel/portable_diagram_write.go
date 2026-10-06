package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
)

func replacePortableDiagramPin(pin *DiagramPin, known map[DiagramPin]DiagramPin, selfOld, selfNew DiagramPin) error {
	if pin == nil {
		return nil
	}
	if *pin == selfOld {
		*pin = selfNew
		return nil
	}
	value, ok := known[*pin]
	if !ok {
		return invalid("closure", "Missing exact diagram dependency")
	}
	*pin = value
	return nil
}
func (r *Repo) importPortableDiagramTx(ctx context.Context, tx *sql.Tx, v *DiagramVersion, known map[DiagramPin]DiagramPin) error {
	if err := v.Pin.Validate(); err != nil {
		return err
	}
	old := v.Pin
	v.receiptJSON = ""
	if v.Document.Interactions != nil {
		if err := replacePortableDiagramPin(v.Document.Interactions.Architecture, known, DiagramPin{}, DiagramPin{}); err != nil {
			return err
		}
	}
	if v.Document.BusinessMap != nil {
		if err := replacePortableDiagramPin(v.Document.BusinessMap.Architecture, known, DiagramPin{}, DiagramPin{}); err != nil {
			return err
		}
	}
	graph, err := resolveEffectiveGraph(ctx, tx, v.ProjectID, v.Document.Target)
	if err != nil {
		return err
	}
	ctx = r.portableDiagramContext(ctx, tx)
	if err := r.resolvePortableDiagramRows(ctx, tx, graph, &v.Document); err != nil {
		return err
	}
	v.Document, err = normalizeDiagram(v.Document)
	if err != nil {
		return err
	}
	v.Pin.ContentHash, err = requestDigest(v.Document)
	if err != nil {
		return err
	}
	v.TargetHash = graph.Pins.TargetHash
	if err := replacePortableDiagramPin(v.Provenance.Previous, known, DiagramPin{}, DiagramPin{}); err != nil {
		return err
	}
	if f := v.Provenance.Fork; f != nil {
		if err := replacePortableDiagramPin(&f.Source, known, DiagramPin{}, DiagramPin{}); err != nil {
			return err
		}
		source, err := loadDiagram(ctx, tx, v.ProjectID, f.Source.ID, f.Source.Version)
		if err != nil {
			return err
		}
		f.SourceProvenanceHash = source.ProvenanceHash
	}
	for i := range v.Provenance.Elements {
		p := &v.Provenance.Elements[i]
		for _, event := range []*DiagramProvenanceEvent{&p.Introduced, &p.LastEdited} {
			if err := replacePortableDiagramPin(&event.Pin, known, old, v.Pin); err != nil {
				return err
			}
		}
		if p.InheritedFrom != nil {
			if err := replacePortableDiagramPin(&p.InheritedFrom.Pin, known, old, v.Pin); err != nil {
				return err
			}
		}
	}
	if err := validatePortableProvenance(ctx, tx, v); err != nil {
		return err
	}
	var previous *DiagramVersion
	if v.Provenance.Previous != nil {
		pin := v.Provenance.Previous
		previous, err = loadDiagram(ctx, tx, v.ProjectID, pin.ID, pin.Version)
		if err != nil {
			return err
		}
		if err := validateDiagramSave(previous, v.Document); err != nil {
			return err
		}
		if err := rejectRetiredDiagramIDs(ctx, tx, v.ProjectID, v.Pin.ID, previous, v.Document); err != nil {
			return err
		}
	} else if v.Provenance.Fork != nil {
		pin := v.Provenance.Fork.Source
		previous, err = loadDiagram(ctx, tx, v.ProjectID, pin.ID, pin.Version)
		if err != nil {
			return err
		}
	}
	// Only earlier, validated included versions supply historical membership.
	// No unverified foreign provenance record enters this resolver.
	gaps, err := resolveDiagramEvidence(ctx, graph, v.Document, previous)
	if err != nil {
		return err
	}
	extra, err := resolveInteractionArchitectureRead(ctx, tx, v.ProjectID, graph, v.Document, previous)
	if err != nil {
		return err
	}
	gaps = append(gaps, extra...)
	extra, err = resolveBusinessMapArchitectureRead(ctx, tx, v.ProjectID, graph, v.Document, previous)
	if err != nil {
		return err
	}
	v.Gaps = append(gaps, extra...)
	v.ProvenanceHash, err = requestDigest(v.Provenance)
	return err
}
func validatePortableProvenance(ctx context.Context, tx *sql.Tx, v *DiagramVersion) error {
	p := v.Provenance
	if p.Format != "backend-diagram-provenance-v1" {
		return invalid("provenance", "Unknown provenance version")
	}
	switch p.Action {
	case "create":
		if v.Pin.Version != 1 || p.Previous != nil || p.Fork != nil {
			return invalid("provenance", "Invalid creation")
		}
	case "save":
		if p.Previous == nil || p.Previous.ID != v.Pin.ID || p.Previous.Version != v.Pin.Version-1 {
			return invalid("provenance", "Invalid save chain")
		}
		previous, err := loadDiagram(ctx, tx, v.ProjectID, p.Previous.ID, p.Previous.Version)
		if err != nil {
			return err
		}
		before, err := requestDigest(previous.Provenance.Fork)
		if err != nil {
			return err
		}
		after, err := requestDigest(p.Fork)
		if err != nil {
			return err
		}
		if before != after {
			return invalid("provenance", "Save changed inherited fork attribution")
		}

	case "fork":
		if v.Pin.Version != 1 || p.Previous != nil || p.Fork == nil || p.Fork.Source.ID == v.Pin.ID || !validAPIText(p.Fork.Reason, 1, 4096) {
			return invalid("provenance", "Invalid fork")
		}
	default:
		return invalid("provenance", "Unknown provenance action")
	}
	members := diagramSemanticRows(v.Document)
	seen := map[string]bool{}
	member := func(pin DiagramPin, id string) (*DiagramVersion, error) {
		source := v
		if pin != v.Pin {
			var err error
			source, err = loadDiagram(ctx, tx, v.ProjectID, pin.ID, pin.Version)
			if err != nil {
				return nil, err
			}
			if source.Pin != pin {
				return nil, diagramPinMismatch()
			}
		}
		if _, ok := diagramSemanticRows(source.Document)[id]; !ok {
			return nil, invalid("provenance", "Exact provenance member is missing")
		}
		return source, nil
	}
	for _, e := range p.Elements {
		if _, ok := members[e.ElementID]; !ok || seen[e.ElementID] {
			return invalid("provenance", "Duplicate or foreign member")
		}
		seen[e.ElementID] = true
		for _, event := range []DiagramProvenanceEvent{e.Introduced, e.LastEdited} {
			source, err := member(event.Pin, event.ElementID)
			if err != nil {
				return err
			}
			if source.Author != event.Author || source.CreatedAt != event.At {
				return invalid("provenance", "Author/time differ from exact historical version")
			}
		}
		if e.InheritedFrom != nil {
			if _, err := member(e.InheritedFrom.Pin, e.InheritedFrom.ElementID); err != nil {
				return err
			}
		}
	}
	if len(seen) != len(members) {
		return invalid("provenance", "Incomplete member provenance")
	}
	return nil
}
func insertPortableDiagramTx(ctx context.Context, tx *sql.Tx, v *DiagramVersion) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var count, versions int
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM backend_diagrams WHERE project_id=?),(SELECT count(*) FROM backend_diagram_versions WHERE project_id=? AND diagram_id=?),(SELECT coalesce(sum(length(CAST(document AS BLOB))),0) FROM backend_diagram_versions WHERE project_id=?)`, v.ProjectID, v.ProjectID, v.Pin.ID, v.ProjectID).Scan(&count, &versions, &total); err != nil {
		return err
	}
	if count >= 1000 && versions == 0 || versions >= 1000 || total+int64(len(raw)) > 256<<20 {
		return diagramQuota()
	}
	if versions == 0 {
		target, err := json.Marshal(v.Document.Target)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_diagrams(project_id,id,kind,version,target_json) VALUES(?,?,?,?,?)`, v.ProjectID, v.Pin.ID, v.Document.Kind, v.Pin.Version, string(target)); err != nil {
			return err
		}
	}
	provenance, err := json.Marshal(v.Provenance)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO backend_diagram_versions(project_id,diagram_id,version,content_hash,target_hash,document,author,created_at,provenance,provenance_hash) VALUES(?,?,?,?,?,?,?,?,?,?)`, v.ProjectID, v.Pin.ID, v.Pin.Version, v.Pin.ContentHash, v.TargetHash, string(raw), v.Author, v.CreatedAt, string(provenance), v.ProvenanceHash); err != nil {
		return err
	}
	if versions > 0 {
		result, err := tx.ExecContext(ctx, `UPDATE backend_diagrams SET version=? WHERE project_id=? AND id=? AND version=?`, v.Pin.Version, v.ProjectID, v.Pin.ID, v.Pin.Version-1)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return invalid("history", "Diagram version must follow its exact predecessor")
		}
	}
	return advanceDiagramCatalog(ctx, tx, v.ProjectID)
}
func (r *Repo) ImportPortableDiagramViewTx(ctx context.Context, tx *sql.Tx, pid string, v *DiagramView) error {
	if !ValidID(v.ID) || v.Version <= 0 {
		return invalid("view", "Exact view identity required")
	}
	v.receiptJSON = ""
	v.State = normalizeDiagramViewState(v.State)
	if err := validateDiagramViewRead(ctx, tx, pid, v.Name, v.State); err != nil {
		return err
	}
	diagram, err := loadDiagram(ctx, tx, pid, v.State.Diagram.ID, v.State.Diagram.Version)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var views, versions int
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM backend_diagram_views WHERE project_id=?),(SELECT count(*) FROM backend_diagram_view_versions WHERE project_id=? AND view_id=?),(SELECT coalesce(sum(length(CAST(document AS BLOB))),0) FROM backend_diagram_view_versions WHERE project_id=?)`, pid, pid, v.ID, pid).Scan(&views, &versions, &total); err != nil {
		return err
	}
	if views >= 1000 && versions == 0 || versions >= 1000 || total+int64(len(raw)) > 64<<20 {
		return diagramQuota()
	}
	if versions == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_diagram_views(project_id,id,version,kind,name) VALUES(?,?,?,?,?)`, pid, v.ID, v.Version, diagram.Document.Kind, v.Name); err != nil {
			return err
		}
	} else {
		var head int64
		var kind string
		if err := tx.QueryRowContext(ctx, `SELECT version,kind FROM backend_diagram_views WHERE project_id=? AND id=?`, pid, v.ID).Scan(&head, &kind); err != nil {
			return err
		}
		if v.Version > head || kind != diagram.Document.Kind {
			return invalid("view", "Import exact view versions newest first with a fixed kind")
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO backend_diagram_view_versions(project_id,view_id,version,document) VALUES(?,?,?,?)`, pid, v.ID, v.Version, string(raw)); err != nil {
		return err
	}
	return advanceDiagramCatalog(ctx, tx, pid)
}

func (r *Repo) importPortableSavedViewTx(ctx context.Context, tx *sql.Tx, pid string, v *SavedView) error {
	if v.ProjectID != pid || !ValidID(v.ID) || v.Version <= 0 {
		return invalid("view", "Invalid saved view scope")
	}
	v.receiptJSON = ""
	if err := validateSavedViewVersionTarget(v.DocumentVersion, v.Target); err != nil {
		return err
	}
	if err := validateSavedViewState(v.State); err != nil {
		return err
	}
	pins, err := resolveSavedVersionReferences(ctx, tx, pid, v.DocumentVersion, v.Target, v.State)
	if err != nil {
		return err
	}
	v.Pins = *pins
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(raw) > MaxSavedViewBodyBytes {
		return limitFault("Saved view exceeds portable owner bound")
	}
	if err := checkSavedViewQuota(ctx, tx, pid, v.ID, int64(len(raw))); err != nil {
		return err
	}
	target, err := json.Marshal(v.Target)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO backend_saved_views(id,project_id,version,name,kind,target_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET version=max(version,excluded.version)`, v.ID, pid, v.Version, v.Name, v.State.kind(), string(target), v.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), v.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00")); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_saved_view_versions(view_id,version,document) VALUES(?,?,?)`, v.ID, v.Version, string(raw))
	return err
}
func importPortableAnnotationsTx(ctx context.Context, tx *sql.Tx, pid string, annotations []Annotation) error {
	if len(annotations) > MaxAnnotationIdentities {
		return limitFault("Annotation identity quota")
	}
	for i := range annotations {
		a := &annotations[i]
		a.Author = fmt.Sprintf("imported: %s", a.Author)
		if !ValidID(a.ID) || len(a.Body) > MaxAnnotationBodyBytes {
			return invalid("annotation", "Invalid bounded annotation")
		}
		if err := validateAnnotationTarget(a.Target); err != nil {
			return err
		}
		var revision any
		if a.Target.RevisionID != "" {
			revision = a.Target.RevisionID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_annotations(id,project_id,record_type,target_id,revision_id,body,author,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, a.ID, pid, a.Target.RecordType, a.Target.ID, revision, a.Body, a.Author, a.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), a.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00")); err != nil {
			return err
		}
	}
	return checkAnnotationQuota(ctx, tx, pid)
}
