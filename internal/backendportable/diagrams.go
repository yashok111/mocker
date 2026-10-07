package backendportable

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"

	bm "github.com/yashok111/mocker/internal/backendmodel"
)

type DiagramVersionReader interface {
	GetDiagram(context.Context, string, bm.DiagramPin) (*bm.DiagramVersion, error)
}

// DiagramClosure returns dependency-first immutable versions, with original
// provenance intact. It neither allocates local IDs nor authorizes imported orphans.
func DiagramClosure(ctx context.Context, r DiagramVersionReader, project string, roots []bm.DiagramPin) ([]bm.DiagramVersion, error) {
	if !bm.ValidID(project) || len(roots) == 0 {
		return nil, fault(422, "Project and selected diagrams required")
	}
	state := map[bm.DiagramPin]uint8{}
	versions := map[bm.DiagramPin]*bm.DiagramVersion{}
	identities := map[struct {
		ID      string
		Version int64
	}]string{}
	out := []bm.DiagramVersion{}
	total := 0
	var visit func(bm.DiagramPin, int) error
	visit = func(pin bm.DiagramPin, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := pin.Validate(); err != nil {
			return err
		}
		if state[pin] == 1 {
			return fault(422, "Distinct-version dependency cycle")
		}
		if state[pin] == 2 {
			return nil
		}
		if depth > 1000 || len(state) >= 100000 {
			return fault(413, "Diagram closure quota")
		}
		key := struct {
			ID      string
			Version int64
		}{pin.ID, pin.Version}
		if h, ok := identities[key]; ok && h != pin.ContentHash {
			return fault(422, "Conflicting exact diagram hash")
		}
		identities[key] = pin.ContentHash
		state[pin] = 1
		v, err := detachedDiagram(ctx, r, project, pin, &total)
		if err != nil {
			return err
		}
		versions[pin] = v
		for _, dep := range diagramDependencies(v) {
			if dep == pin {
				continue
			}
			if err = visit(dep, depth+1); err != nil {
				return err
			}
		}
		if err = validateProvenanceReferences(v, versions); err != nil {
			return err
		}
		state[pin] = 2
		out = append(out, *v)
		return nil
	}
	for _, pin := range roots {
		if err := visit(pin, 0); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// detachedDiagram reads one pinned version owned by the project, charges its
// encoded size to *total and returns a validated private copy of it.
func detachedDiagram(ctx context.Context, r DiagramVersionReader, project string, pin bm.DiagramPin, total *int) (*bm.DiagramVersion, error) {
	v, err := r.GetDiagram(ctx, project, pin)
	if err != nil {
		return nil, err
	}
	if v.Pin != pin || v.ProjectID != project {
		return nil, fault(422, "Closure pin ownership mismatch")
	}
	// Detach server receipt bytes and pointers; no caller mutation during traversal.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	*total += len(raw)
	if *total > MaxBundleBytes {
		return nil, fault(413, "Diagram closure byte quota")
	}
	v = new(bm.DiagramVersion)
	if err = json.Unmarshal(raw, v, json.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err = validateDiagramVersion(v); err != nil {
		return nil, err
	}
	return v, nil
}
func diagramDependencies(v *bm.DiagramVersion) []bm.DiagramPin {
	deps := []bm.DiagramPin{}
	add := func(p *bm.DiagramPin) {
		if p != nil {
			deps = append(deps, *p)
		}
	}
	if v.Document.Interactions != nil {
		add(v.Document.Interactions.Architecture)
	}
	if v.Document.BusinessMap != nil {
		add(v.Document.BusinessMap.Architecture)
	}
	add(v.Provenance.Previous)
	if v.Provenance.Fork != nil {
		deps = append(deps, v.Provenance.Fork.Source)
	}
	for _, e := range v.Provenance.Elements {
		deps = append(deps, e.Introduced.Pin, e.LastEdited.Pin)
		if e.InheritedFrom != nil {
			deps = append(deps, e.InheritedFrom.Pin)
		}
	}
	return deps
}
func diagramMemberIDs(d bm.DiagramDocument) (map[string]bool, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	var wire struct {
		Payload map[string]jsontext.Value `json:"payload"`
	}
	if err = json.Unmarshal(b, &wire); err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, field := range []string{"elements", "links", "participants", "steps", "order", "branches", "states", "transitions", "rules"} {
		raw, ok := wire.Payload[field]
		if !ok {
			continue
		}
		var rows []struct {
			ID string `json:"id"`
		}
		if err = json.Unmarshal(raw, &rows); err != nil {
			return nil, err
		}
		for _, row := range rows {
			if ids[row.ID] {
				return nil, fault(422, "Duplicate diagram member")
			}
			ids[row.ID] = true
		}
	}
	return ids, nil
}
func validateDiagramVersion(v *bm.DiagramVersion) error {
	if err := v.Document.Validate(); err != nil {
		return err
	}
	h, err := DocumentHash(v.Document)
	if err != nil {
		return err
	}
	if h != v.Pin.ContentHash {
		return fault(422, "Diagram content hash mismatch")
	}
	h, err = DocumentHash(v.Provenance)
	if err != nil {
		return err
	}
	if h != v.ProvenanceHash {
		return fault(422, "Diagram provenance hash mismatch")
	}
	p := v.Provenance
	if err = validateProvenanceAction(v); err != nil {
		return err
	}
	ids, err := diagramMemberIDs(v.Document)
	if err != nil {
		return err
	}
	if len(ids) != len(p.Elements) {
		return fault(422, "Incomplete provenance membership")
	}
	seen := map[string]bool{}
	for _, e := range p.Elements {
		if !ids[e.ElementID] || seen[e.ElementID] {
			return fault(422, "Invalid provenance member")
		}
		seen[e.ElementID] = true
	}
	return nil
}

// validateProvenanceAction: a create has no predecessor, a save follows the
// previous version of the same diagram, a fork names another diagram and why.
func validateProvenanceAction(v *bm.DiagramVersion) error {
	p := v.Provenance
	if p.Format != "backend-diagram-provenance-v1" || !slices.Contains([]string{"create", "save", "fork"}, p.Action) {
		return fault(422, "Unsupported provenance")
	}
	switch p.Action {
	case "create":
		if p.Previous != nil || p.Fork != nil {
			return fault(422, "Invalid create provenance")
		}
	case "save":
		if p.Previous == nil || p.Previous.ID != v.Pin.ID || p.Previous.Version != v.Pin.Version-1 {
			return fault(422, "Invalid previous provenance")
		}
	case "fork":
		if p.Previous != nil || p.Fork == nil || p.Fork.Source.ID == v.Pin.ID || p.Fork.Reason == "" {
			return fault(422, "Invalid fork provenance")
		}
	}
	return nil
}
func validateProvenanceReferences(v *bm.DiagramVersion, versions map[bm.DiagramPin]*bm.DiagramVersion) error {
	member := func(pin bm.DiagramPin, id string) (*bm.DiagramVersion, error) {
		source := versions[pin]
		if source == nil {
			return nil, fault(422, "Missing provenance dependency")
		}
		ids, err := diagramMemberIDs(source.Document)
		if err != nil {
			return nil, err
		}
		if !ids[id] {
			return nil, fault(422, "Missing provenance member")
		}
		return source, nil
	}
	if f := v.Provenance.Fork; f != nil {
		source := versions[f.Source]
		if source == nil || source.ProvenanceHash != f.SourceProvenanceHash {
			return fault(422, "Fork provenance hash mismatch")
		}
	}
	for _, e := range v.Provenance.Elements {
		for _, event := range []bm.DiagramProvenanceEvent{e.Introduced, e.LastEdited} {
			source, err := member(event.Pin, event.ElementID)
			if err != nil {
				return err
			}
			if source.Author != event.Author || source.CreatedAt != event.At {
				return fault(422, "Provenance author/time mismatch")
			}
		}
		if e.InheritedFrom != nil {
			if _, err := member(e.InheritedFrom.Pin, e.InheritedFrom.ElementID); err != nil {
				return err
			}
		}
	}
	return nil
}
