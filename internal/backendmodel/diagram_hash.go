package backendmodel

import (
	"cmp"
	"encoding/json/v2"
	"slices"
)

func normalizeDiagram(d DiagramDocument) (DiagramDocument, error) {
	// Detach slices before canonical ordering; caller buffers must remain unchanged.
	if err := d.Validate(); err != nil {
		return d, err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return d, err
	}
	var out DiagramDocument
	if err = json.Unmarshal(raw, &out); err != nil {
		return d, err
	}
	slices.SortFunc(out.Payload.Elements, func(a, b ArchitectureElement) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(out.Payload.Links, func(a, b ArchitectureLink) int { return cmp.Compare(a.ID, b.ID) })
	normalize := func(o *DiagramOrigin, refs []DiagramRef) {
		slices.SortFunc(o.Evidence, func(a, b DiagramEvidenceRef) int {
			aa, _ := requestDigest(a)
			bb, _ := requestDigest(b)
			return cmp.Compare(aa, bb)
		})
		slices.SortFunc(refs, func(a, b DiagramRef) int {
			aa, _ := requestDigest(a)
			bb, _ := requestDigest(b)
			return cmp.Compare(aa, bb)
		})
	}
	if out.Interactions != nil {
		normalizeInteractions(out.Interactions, normalize)
	}
	for i := range out.Payload.Elements {
		e := &out.Payload.Elements[i]
		normalize(&e.Origin, e.Refs)
	}
	for i := range out.Payload.Links {
		l := &out.Payload.Links[i]
		normalize(&l.Origin, l.Refs)
	}
	return out, nil
}
func diagramSemanticRows(d DiagramDocument) map[string]string {
	rows := map[string]string{}
	if d.Interactions != nil {
		for id, v := range interactionRows(d.Interactions) {
			rows[id], _ = requestDigest(v)
		}
		return rows
	}
	for _, e := range d.Payload.Elements {
		rows[e.ID], _ = requestDigest(e)
	}
	for _, e := range d.Payload.Links {
		rows[e.ID], _ = requestDigest(e)
	}
	return rows
}
func diagramProvenance(out *DiagramVersion, previous *DiagramVersion, action, reason string) {
	p := DiagramProvenance{Format: "backend-diagram-provenance-v1", Action: action, Elements: []DiagramElementProvenance{}}
	old := map[string]DiagramElementProvenance{}
	oldRows := map[string]string{}
	if previous != nil {
		oldRows = diagramSemanticRows(previous.Document)
		for _, e := range previous.Provenance.Elements {
			old[e.ElementID] = e
		}
		if action == "fork" {
			p.Fork = &DiagramForkProvenance{Source: previous.Pin, SourceProvenanceHash: previous.ProvenanceHash, Reason: reason}
		} else {
			p.Previous = new(previous.Pin)
			p.Fork = previous.Provenance.Fork
		}
	}
	rows := diagramSemanticRows(out.Document)
	for id, hash := range rows {
		event := DiagramProvenanceEvent{Pin: out.Pin, ElementID: id, Author: out.Author, At: out.CreatedAt}
		e, found := old[id]
		if !found {
			e = DiagramElementProvenance{ElementID: id, Introduced: event, LastEdited: event}
		} else if oldRows[id] != hash {
			e.LastEdited = event
		}
		if action == "fork" {
			e.InheritedFrom = &DiagramInherited{Pin: previous.Pin, ElementID: id}
		}
		p.Elements = append(p.Elements, e)
	}
	slices.SortFunc(p.Elements, func(a, b DiagramElementProvenance) int { return cmp.Compare(a.ElementID, b.ElementID) })
	out.Provenance = p
	out.ProvenanceHash, _ = requestDigest(p)
}
