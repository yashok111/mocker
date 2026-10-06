package backendmodel

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"math"
	"slices"
	"uuid"
)

type DiagramSelection struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
type DiagramPosition struct {
	ID string  `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
}
type DiagramViewState struct {
	Diagram      DiagramPin        `json:"diagram"`
	Level        string            `json:"level,omitempty"`
	RootID       string            `json:"rootId,omitempty"`
	Search       string            `json:"search"`
	Origin       string            `json:"origin"`
	Selection    *DiagramSelection `json:"selection"`
	Positions    []DiagramPosition `json:"positions"`
	CollapsedIDs []string          `json:"collapsedIds"`
}
type DiagramCreateViewInput struct {
	Name           string           `json:"name"`
	State          DiagramViewState `json:"state"`
	IdempotencyKey string           `json:"idempotencyKey"`
}
type DiagramSaveViewInput struct {
	Name            string           `json:"name"`
	State           DiagramViewState `json:"state"`
	IdempotencyKey  string           `json:"idempotencyKey"`
	ExpectedVersion int64            `json:"expectedVersion"`
}
type DiagramView struct {
	receiptJSON string
	ID          string           `json:"id"`
	Version     int64            `json:"version"`
	Name        string           `json:"name"`
	State       DiagramViewState `json:"state"`
}

func (v DiagramView) MarshalJSON() ([]byte, error) {
	if v.receiptJSON != "" {
		return []byte(v.receiptJSON), nil
	}
	type plain DiagramView
	return json.Marshal(plain(v))
}
func (v *DiagramSelection) UnmarshalJSON(b []byte) error {
	type plain DiagramSelection
	return strictAPIObject(b, []string{"type", "id"}, nil, (*plain)(v))
}
func (v *DiagramPosition) UnmarshalJSON(b []byte) error {
	type plain DiagramPosition
	return strictAPIObject(b, []string{"id", "x", "y"}, nil, (*plain)(v))
}
func (v *DiagramViewState) UnmarshalJSON(b []byte) error {
	m, err := relationalObject(b)
	if err != nil {
		return err
	}
	if err = relationalFields(m, []string{"diagram", "search", "origin", "selection", "positions", "collapsedIds"}, []string{"level", "rootId"}); err != nil {
		return err
	}
	for k, raw := range m {
		if k != "selection" && string(raw) == "null" {
			return invalid(k, "Null is forbidden")
		}
	}
	type plain DiagramViewState
	*v = DiagramViewState{}
	return json.Unmarshal(b, (*plain)(v), json.RejectUnknownMembers(true))
}
func (v *DiagramCreateViewInput) UnmarshalJSON(b []byte) error {
	type plain DiagramCreateViewInput
	*v = DiagramCreateViewInput{}
	return strictAPIObject(b, []string{"name", "state", "idempotencyKey"}, nil, (*plain)(v))
}
func (v *DiagramSaveViewInput) UnmarshalJSON(b []byte) error {
	type plain DiagramSaveViewInput
	*v = DiagramSaveViewInput{}
	return strictAPIObject(b, []string{"name", "state", "idempotencyKey", "expectedVersion"}, nil, (*plain)(v))
}
func normalizeDiagramViewState(s DiagramViewState) DiagramViewState {
	s.Positions = slices.Clone(s.Positions)
	s.CollapsedIDs = slices.Clone(s.CollapsedIDs)
	slices.SortFunc(s.Positions, func(a, b DiagramPosition) int { return cmp.Compare(a.ID, b.ID) })
	slices.Sort(s.CollapsedIDs)
	return s
}
func (r *Repo) validateDiagramView(ctx context.Context, pid, name string, s DiagramViewState) error {
	return validateDiagramViewRead(ctx, r.db.R, pid, name, s)
}
func validateDiagramViewRead(ctx context.Context, qry importReader, pid, name string, s DiagramViewState) error {
	if !validAPIText(name, 1, 256) || s.Positions == nil || s.CollapsedIDs == nil {
		return invalid("view", "Name and nonnull layout arrays required")
	}
	q := DiagramQueryInput{Pin: s.Diagram, Level: s.Level, RootID: s.RootID, Search: s.Search, Origin: s.Origin, Section: "elements", Limit: 100}
	if err := q.Validate(); err != nil {
		return err
	}
	v, err := loadDiagram(ctx, qry, pid, s.Diagram.ID, s.Diagram.Version)
	if err == nil && v.Pin != s.Diagram {
		return diagramPinMismatch()
	}
	if err != nil {
		return err
	}
	graph, err := resolveEffectiveGraph(ctx, qry, pid, v.Document.Target)
	if err != nil {
		return err
	}
	var p *architectureProjection
	if v.Document.BusinessMap != nil {
		if q.Level != "" || q.RootID != "" {
			return invalid("projection", "Architecture selectors forbidden for business map")
		}
		p = &architectureProjection{elements: map[string]ArchitectureElement{}, links: map[string]ArchitectureLink{}}
		for _, e := range v.Document.BusinessMap.Elements {
			p.elements[e.ID] = ArchitectureElement{ID: e.ID}
		}
		for _, e := range v.Document.BusinessMap.Links {
			p.links[e.ID] = ArchitectureLink{ID: e.ID}
		}
	} else if v.Document.Lifecycle != nil {
		if q.Level != "" || q.RootID != "" {
			return invalid("projection", "Architecture selectors forbidden for lifecycle")
		}
		p = &architectureProjection{elements: map[string]ArchitectureElement{}, links: map[string]ArchitectureLink{}}
		for id, value := range lifecycleRows(v.Document.Lifecycle) {
			p.elements[id] = ArchitectureElement{ID: id}
			if _, ok := value.(LifecycleTransition); ok {
				p.links[id] = ArchitectureLink{ID: id}
			}
		}
	} else if v.Document.Interactions != nil {
		if q.Level != "" || q.RootID != "" {
			return invalid("projection", "Architecture projection forbidden for interactions")
		}
		p = &architectureProjection{elements: map[string]ArchitectureElement{}, links: map[string]ArchitectureLink{}}
		for id, value := range interactionRows(v.Document.Interactions) {
			if _, ok := value.(InteractionOrder); ok {
				p.links[id] = ArchitectureLink{ID: id}
			} else {
				p.elements[id] = ArchitectureElement{ID: id}
			}
		}
	} else {
		p, err = projectArchitecture(ctx, v, graph, q)
	}
	if err != nil {
		return err
	}
	if err := validateDiagramLayout(p, s); err != nil {
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(raw) > 128<<10 {
		return &FaultError{Status: 413, Code: "backend_diagram_view_limit", Message: "View exceeds 128 KiB"}
	}
	return nil
}
func loadDiagramView(ctx context.Context, q importReader, pid, id string, version int64) (*DiagramView, error) {
	if !ValidID(pid) || !ValidID(id) || version <= 0 {
		return nil, notFound()
	}
	var raw string
	err := q.QueryRowContext(ctx, `SELECT document FROM backend_diagram_view_versions WHERE project_id=? AND view_id=? AND version=?`, pid, id, version).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	return decodeDiagramView(raw)
}
func decodeDiagramView(raw string) (*DiagramView, error) {
	v := new(DiagramView)
	if err := json.Unmarshal([]byte(raw), v, json.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	v.receiptJSON = raw
	return v, nil
}
func (r *Repo) GetDiagramView(ctx context.Context, pid, id string, version int64) (*DiagramView, error) {
	return loadDiagramView(ctx, r.db.R, pid, id, version)
}
func (r *Repo) CreateDiagramView(ctx context.Context, pid string, in DiagramCreateViewInput) (*DiagramView, error) {
	in.State = normalizeDiagramViewState(in.State)
	hash, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	return r.mutateDiagramView(ctx, pid, "", 0, in.Name, in.State, in.IdempotencyKey, hash)
}
func (r *Repo) SaveDiagramView(ctx context.Context, pid, id string, in DiagramSaveViewInput) (*DiagramView, error) {
	if !ValidID(id) || in.ExpectedVersion <= 0 {
		return nil, invalid("view", "Exact view identity and expected version required")
	}
	in.State = normalizeDiagramViewState(in.State)
	hash, err := requestDigest(struct {
		ID    string
		Input DiagramSaveViewInput
	}{id, in})
	if err != nil {
		return nil, err
	}
	return r.mutateDiagramView(ctx, pid, id, in.ExpectedVersion, in.Name, in.State, in.IdempotencyKey, hash)
}
func (r *Repo) mutateDiagramView(ctx context.Context, pid, id string, expected int64, name string, state DiagramViewState, key, hash string) (*DiagramView, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	op := "view-create"
	if id != "" {
		op = "view-save"
	}
	raw, err := readDiagramReceipt(ctx, r.db.R, pid, op, key, hash)
	if err != nil {
		return nil, err
	}
	if raw != "" {
		return decodeDiagramView(raw)
	}
	if err = r.validateDiagramView(ctx, pid, name, state); err != nil {
		return nil, err
	}
	var out *DiagramView
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		raw, err := readDiagramReceipt(ctx, tx, pid, op, key, hash)
		if err != nil {
			return err
		}
		if raw != "" {
			out, err = decodeDiagramView(raw)
			return err
		}
		if _, err = loadDiagram(ctx, tx, pid, state.Diagram.ID, state.Diagram.Version); err != nil {
			return err
		}
		version := int64(1)
		newID := id
		if id != "" {
			var head int64
			if err = tx.QueryRowContext(ctx, `SELECT version FROM backend_diagram_views WHERE project_id=? AND id=?`, pid, id).Scan(&head); errors.Is(err, sql.ErrNoRows) {
				return notFound()
			} else if err != nil {
				return err
			}
			current, err := loadDiagramView(ctx, tx, pid, id, head)
			if err != nil {
				return err
			}
			if current.State.Diagram != state.Diagram {
				return invalid("diagram", "View diagram pin is immutable; create a new view")
			}
			if head != expected {
				return diagramConflict()
			}
			a, _ := requestDigest(current.State)
			b, _ := requestDigest(state)
			if current.Name == name && a == b {
				out = current
				return writeDiagramReceipt(ctx, tx, pid, op, key, hash, current.receiptJSON)
			}
			version = head + 1
		} else {
			newID = uuid.NewV7().String()
		}
		out = &DiagramView{ID: newID, Version: version, Name: name, State: state}
		return persistDiagramView(ctx, tx, pid, id, op, key, hash, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func validateDiagramLayout(p *architectureProjection, s DiagramViewState) error {
	seen := map[string]bool{}
	for _, pos := range s.Positions {
		_, ok := p.elements[pos.ID]
		if !ok || seen[pos.ID] || math.IsInf(pos.X, 0) || math.IsNaN(pos.X) || math.IsInf(pos.Y, 0) || math.IsNaN(pos.Y) {
			return invalid("positions", "Unique projected member and finite coordinates required")
		}
		seen[pos.ID] = true
	}
	clear(seen)
	for _, id := range s.CollapsedIDs {
		if _, ok := p.elements[id]; !ok || seen[id] {
			return invalid("collapsedIds", "Unique projected elements required")
		}
		seen[id] = true
	}
	if s.Selection != nil {
		switch s.Selection.Type {
		case "element":
			if _, ok := p.elements[s.Selection.ID]; !ok {
				return invalid("selection", "Element not in pinned projection")
			}
		case "link":
			if _, ok := p.links[s.Selection.ID]; !ok {
				return invalid("selection", "Link not in pinned projection with this level/root")
			}
		default:
			return invalid("selection", "Unknown selection type")
		}
	}

	return nil
}

func persistDiagramView(ctx context.Context, tx *sql.Tx, pid, id, op, key, hash string, out *DiagramView) error {
	newID, version, name := out.ID, out.Version, out.Name
	bytes, err := json.Marshal(out)
	if err != nil {
		return err
	}
	raw := string(bytes)
	if len(bytes) > 128<<10 {
		return &FaultError{Status: 413, Code: "backend_diagram_view_limit", Message: "View exceeds 128 KiB"}
	}
	var views, versions int
	var total int64
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM backend_diagram_views WHERE project_id=?),(SELECT count(*) FROM backend_diagram_view_versions WHERE project_id=? AND view_id=?),(SELECT coalesce(sum(length(CAST(document AS BLOB))),0) FROM backend_diagram_view_versions WHERE project_id=?)`, pid, pid, newID, pid).Scan(&views, &versions, &total); err != nil {
		return err
	}
	if id == "" && views >= 1000 || versions >= 1000 || total+int64(len(bytes)) > 64<<20 {
		return diagramQuota()
	}
	if id == "" {
		diagram, loadErr := loadDiagram(ctx, tx, pid, out.State.Diagram.ID, out.State.Diagram.Version)
		if loadErr != nil {
			return loadErr
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO backend_diagram_views(project_id,id,version,kind,name) VALUES(?,?,1,?,?)`, pid, newID, diagram.Document.Kind, name); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO backend_diagram_view_versions(project_id,view_id,version,document) VALUES(?,?,?,?)`, pid, newID, version, raw); err != nil {
		return err
	}
	if id != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE backend_diagram_views SET version=?,name=? WHERE project_id=? AND id=?`, version, name, pid, id); err != nil {
			return err
		}
	}
	if err = advanceDiagramCatalog(ctx, tx, pid); err != nil {
		return err
	}
	if err = writeDiagramReceipt(ctx, tx, pid, op, key, hash, raw); err != nil {
		return err
	}

	out.receiptJSON = raw
	return nil
}
