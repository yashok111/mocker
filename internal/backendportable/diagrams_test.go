package backendportable

import (
	"context"
	"fmt"
	bm "github.com/yashok111/mocker/internal/backendmodel"
	"testing"
)

type closureReader map[bm.DiagramPin]*bm.DiagramVersion

func (r closureReader) GetDiagram(_ context.Context, _ string, p bm.DiagramPin) (*bm.DiagramVersion, error) {
	v, ok := r[p]
	if !ok {
		return nil, fmt.Errorf("missing pin")
	}
	return v, nil
}
func provenanceFixture(t *testing.T, id string, version int64, author string) *bm.DiagramVersion {
	t.Helper()
	v := &bm.DiagramVersion{ProjectID: pid, Pin: bm.DiagramPin{ID: id, Version: version}, Author: author, CreatedAt: "2026-10-06T00:00:00Z", TargetHash: hash, Gaps: []bm.DiagramGap{}, Document: bm.DiagramDocument{Format: bm.DiagramDocumentVersion, Kind: "architecture", Target: bm.BackendReadTarget{RevisionID: pid}, Payload: bm.ArchitecturePayload{PrimarySystemID: nid, Elements: []bm.ArchitectureElement{{ID: nid, Label: "Orders", Role: "software_system", Responsibility: "Orders", Technology: "Go", Origin: bm.DiagramOrigin{Kind: "authored", Reason: "intent"}, Refs: []bm.DiagramRef{}}}, Links: []bm.ArchitectureLink{}}}}
	v.Pin.ContentHash, _ = DocumentHash(v.Document)
	event := bm.DiagramProvenanceEvent{Pin: v.Pin, ElementID: nid, Author: author, At: v.CreatedAt}
	v.Provenance = bm.DiagramProvenance{Format: "backend-diagram-provenance-v1", Action: "create", Elements: []bm.DiagramElementProvenance{{ElementID: nid, Introduced: event, LastEdited: event}}}
	v.ProvenanceHash, _ = DocumentHash(v.Provenance)
	return v
}
func TestDiagramPortableClosureForkEdit(t *testing.T) {
	a := provenanceFixture(t, did, 1, "A")
	b := provenanceFixture(t, vid, 1, "B")
	b.Provenance.Action = "fork"
	b.Provenance.Fork = &bm.DiagramForkProvenance{Source: a.Pin, SourceProvenanceHash: a.ProvenanceHash, Reason: "alternate"}
	b.Provenance.Elements[0] = a.Provenance.Elements[0]
	b.Provenance.Elements[0].InheritedFrom = &bm.DiagramInherited{Pin: a.Pin, ElementID: nid}
	b.ProvenanceHash, _ = DocumentHash(b.Provenance)
	c := provenanceFixture(t, vid, 2, "C")
	c.Provenance.Action = "save"
	c.Provenance.Previous = &b.Pin
	c.Provenance.Fork = b.Provenance.Fork
	c.Provenance.Elements[0].Introduced = a.Provenance.Elements[0].Introduced
	c.Provenance.Elements[0].InheritedFrom = b.Provenance.Elements[0].InheritedFrom
	c.ProvenanceHash, _ = DocumentHash(c.Provenance)
	r := closureReader{a.Pin: a, b.Pin: b, c.Pin: c}
	out, err := DiagramClosure(t.Context(), r, pid, []bm.DiagramPin{c.Pin, a.Pin})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0].Pin != a.Pin || out[1].Pin != b.Pin || out[2].Pin != c.Pin {
		t.Fatal("closure not dependency ordered/deduplicated")
	}
	if out[2].Provenance.Elements[0].Introduced.Author != "A" || out[2].Provenance.Fork.Reason != "alternate" {
		t.Fatal("original provenance changed")
	}
	c.ProvenanceHash = hash
	if _, err = DiagramClosure(t.Context(), r, pid, []bm.DiagramPin{c.Pin}); err == nil {
		t.Fatal("tampered provenance accepted")
	}
}
func TestDiagramPortableMissingAndDistinctCycle(t *testing.T) {
	a := provenanceFixture(t, did, 1, "A")
	b := provenanceFixture(t, vid, 1, "B")
	a.Provenance.Previous = &b.Pin
	a.Provenance.Action = "save"
	a.ProvenanceHash, _ = DocumentHash(a.Provenance)
	if _, err := DiagramClosure(t.Context(), closureReader{a.Pin: a}, pid, []bm.DiagramPin{a.Pin}); err == nil {
		t.Fatal("missing dependency")
	}
	b.Provenance.Previous = &a.Pin
	b.Provenance.Action = "save"
	b.ProvenanceHash, _ = DocumentHash(b.Provenance)
	if _, err := DiagramClosure(t.Context(), closureReader{a.Pin: a, b.Pin: b}, pid, []bm.DiagramPin{a.Pin}); err == nil {
		t.Fatal("cycle accepted")
	}
}
