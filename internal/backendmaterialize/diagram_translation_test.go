package backendmaterialize

import (
	bm "github.com/yashok111/mocker/internal/backendmodel"
	"testing"
)

func TestDiagramTranslationExactScopeAndExplicitCoverage(t *testing.T) {
	t.Parallel()
	s, pid, in := fixture(t)
	id := "10000000-0000-4000-8000-000000000002"
	doc := bm.DiagramDocument{Format: bm.DiagramDocumentVersion, Kind: "architecture", Target: in.Target, Payload: bm.ArchitecturePayload{PrimarySystemID: id, Elements: []bm.ArchitectureElement{{ID: id, Label: "API", Role: "software_system", Responsibility: "HTTP", Technology: "Go", Origin: bm.DiagramOrigin{Kind: "authored", Reason: "Explicit boundary"}, Refs: []bm.DiagramRef{}}}, Links: []bm.ArchitectureLink{}}}
	v, err := s.models.CreateDiagram(t.Context(), pid, bm.DiagramCreateInput{Document: doc, IdempotencyKey: "diagram"})
	must(t, err)
	in.DiagramScope = &bm.DiagramScopeInput{Pin: v.Pin, Selectors: []bm.DiagramScopeSelector{{Kind: "semantic", ID: id}}}
	p, err := s.Preview(t.Context(), pid, in)
	must(t, err)
	if p.CanApply || len(p.Coverage) != 2 || p.Coverage[1].Status != "unsupported" {
		t.Fatal(p)
	}
	in.Translations = append(in.Translations, Translation{SourceID: id, TargetKey: "api", Selector: "", Reason: "Authored boundary mapping"})
	request := applyInput(t, s, pid, in, "scope")
	// Editing head cannot silently change an exact selected old diagram version.
	doc.Payload.Elements[0].Label = "New head"
	_, err = s.models.SaveDiagram(t.Context(), pid, v.Pin.ID, bm.DiagramSaveInput{ExpectedVersion: 1, Document: doc, IdempotencyKey: "edit"})
	must(t, err)
	receipt, err := s.Apply(t.Context(), pid, request)
	must(t, err)
	if receipt.Coverage[1].DiagramPin == nil || *receipt.Coverage[1].DiagramPin != v.Pin || receipt.Coverage[1].TargetOwnerRef == nil {
		t.Fatal("exact diagram/owner coverage missing")
	}
	in.DiagramScope.Pin.ContentHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := s.Preview(t.Context(), pid, in); err == nil {
		t.Fatal("forged diagram pin accepted")
	}
}
