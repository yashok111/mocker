package backendmodel

import (
	"encoding/json/v2"
	"slices"
	"testing"
)

func TestDiagramScopeProjectedRelation(t *testing.T) {
	v, g, want := ordersProjectionFixture(t)
	q := DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "links", Limit: 1}
	page, err := ProjectArchitecture(t.Context(), v, g, q)
	if err != nil {
		t.Fatal(err)
	}
	in := DiagramScopeInput{Pin: v.Pin, Selectors: []DiagramScopeSelector{{Kind: "architecture_relation", ID: page.Items[0].ArchitectureLink.ID, Projection: page.Projection}}}
	s, err := resolveDiagramScope(t.Context(), v, g, in)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, ref := range s.SourceRefs {
		ids = append(ids, ref.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, want.PaymentMembers) {
		t.Fatalf("members: %v", ids)
	}
	in.Selectors[0].Projection = &ArchitectureProjectionContext{Policy: "architecture-v1", Level: "containers", RootID: q.RootID}
	if _, err = resolveDiagramScope(t.Context(), v, g, in); err == nil {
		t.Fatal("forged projection accepted")
	}
}
func TestDiagramScopeDoesNotFanOut(t *testing.T) {
	v, g, _ := ordersProjectionFixture(t)
	e := &v.Document.Payload.Elements[0]
	e.Refs = []DiagramRef{{Kind: "record", RecordType: "node", ID: "10000000-0000-4000-8000-000000000091"}, {Kind: "record", RecordType: "node", ID: "10000000-0000-4000-8000-000000000092"}}
	e.Refs = append(e.Refs, e.Refs...)
	in := DiagramScopeInput{Pin: v.Pin, Selectors: []DiagramScopeSelector{{Kind: "semantic", ID: e.ID}, {Kind: "semantic", ID: e.ID}}}
	s, err := resolveDiagramScope(t.Context(), v, g, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.SourceRefs) != 2 || len(s.Selectors) != 1 {
		t.Fatalf("scope: %+v", s)
	}
	in.Selectors = in.Selectors[:1]
	again, err := resolveDiagramScope(t.Context(), v, g, in)
	if err != nil || again.ScopeHash != s.ScopeHash {
		t.Fatal("set normalization changed hash", err)
	}
}
func TestDiagramScopeStrictWire(t *testing.T) {
	for _, raw := range []string{`{"kind":"semantic","id":"10000000-0000-4000-8000-000000000091","projection":null}`, `{"kind":"semantic","id":"10000000-0000-4000-8000-000000000091","projection":{"policy":"architecture-v1","level":"context","rootId":"10000000-0000-4000-8000-000000000092"}}`} {
		var s DiagramScopeSelector
		if json.Unmarshal([]byte(raw), &s) == nil {
			t.Fatal("mixed selector accepted")
		}
	}
}

func TestDiagramScopeAllPayloadKinds(t *testing.T) {
	const id = "10000000-0000-4000-8000-000000000091"
	ref := DiagramRef{Kind: "record", RecordType: "node", ID: "10000000-0000-4000-8000-000000000092"}
	for _, kind := range []string{"architecture", "interactions", "lifecycle", "business_map"} {
		t.Run(kind, func(t *testing.T) {
			v, g, _ := ordersProjectionFixture(t)
			v.Document.Kind = kind
			v.Document.Payload = ArchitecturePayload{}
			switch kind {
			case "architecture":
				v.Document.Payload.Elements = []ArchitectureElement{{ID: id, Refs: []DiagramRef{ref}}}
			case "interactions":
				v.Document.Interactions = &InteractionPayload{Steps: []InteractionStep{{ID: id, Refs: []DiagramRef{ref}}}}
			case "lifecycle":
				v.Document.Lifecycle = &LifecyclePayload{Transitions: []LifecycleTransition{{ID: id, Refs: []DiagramRef{ref}, Triggers: []DiagramRef{ref}}}}
			case "business_map":
				v.Document.BusinessMap = &BusinessMapPayload{Elements: []BusinessElement{{ID: id, Role: "question", Refs: []DiagramRef{ref}}}}
			}
			scope, err := resolveDiagramScope(t.Context(), v, g, DiagramScopeInput{Pin: v.Pin, Selectors: []DiagramScopeSelector{{Kind: "semantic", ID: id}}})
			if err != nil || len(scope.SourceRefs) != 1 || scope.SourceRefs[0].ID != ref.ID {
				t.Fatal(scope, err)
			}
		})
	}
}

func TestDiagramScopeHistoricalPinAndForeignOwnership(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "scope-history")
	other := createProject(t, r, "scope-foreign")
	doc := diagramTestDocument(p.CurrentRevisionID)
	v, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	in := DiagramScopeInput{Pin: v.Pin, Selectors: []DiagramScopeSelector{{Kind: "semantic", ID: doc.Payload.PrimarySystemID}}}
	old, err := r.ResolveDiagramScope(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	doc.Payload.Elements[0].Label = "New label"
	if _, err = r.SaveDiagram(t.Context(), p.ID, v.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: doc, IdempotencyKey: "save"}); err != nil {
		t.Fatal(err)
	}
	again, err := r.ResolveDiagramScope(t.Context(), p.ID, in)
	if err != nil || again.ScopeHash != old.ScopeHash {
		t.Fatal("latest mapping substituted", err)
	}
	if _, err = r.ResolveDiagramScope(t.Context(), other.ID, in); err == nil {
		t.Fatal("foreign diagram accepted")
	}
}
