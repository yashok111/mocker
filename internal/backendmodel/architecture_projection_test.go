package backendmodel

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

func ordersProjectionFixture(t *testing.T) (*DiagramVersion, *EffectiveGraphSnapshot, diagramExpected) {
	t.Helper()
	var f diagramFixture
	var expected diagramExpected
	loadDiagramFixture(t, "orders.json", &f)
	loadDiagramFixture(t, "orders_expected.json", &expected)
	d := diagramTestDocument("10000000-0000-4000-8000-000000000090")
	d.Payload.Elements = []ArchitectureElement{}
	g := &EffectiveGraphSnapshot{Pins: EffectiveGraphPins{BaseRevisionID: d.Target.RevisionID, TargetHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}
	for _, e := range f.Elements {
		refs := []DiagramRef{}
		for _, id := range e.SourceIDs {
			refs = append(refs, DiagramRef{Kind: "record", RecordType: "node", ID: id})
			g.State.Nodes = append(g.State.Nodes, Node{ID: id, Name: e.Label})
		}
		d.Payload.Elements = append(d.Payload.Elements, ArchitectureElement{ID: e.ID, Label: e.Label, Role: e.Role, ParentID: e.ParentID, Origin: DiagramOrigin{Kind: "authored", Reason: "Explicit responsibility boundary"}, Refs: refs})
	}
	for _, dep := range f.Dependencies {
		g.State.Edges = append(g.State.Edges, Edge{ID: dep.ID, From: dep.From, To: dep.To, Kind: dep.Relation, EvidenceIDs: []string{dep.EvidenceID}})
		g.State.Evidence = append(g.State.Evidence, Evidence{ID: dep.EvidenceID, SubjectID: dep.ID})
	}
	hash, _ := requestDigest(d)
	v := &DiagramVersion{Pin: DiagramPin{ID: "10000000-0000-4000-8000-000000000080", Version: 1, ContentHash: hash}, Document: d, TargetHash: g.Pins.TargetHash, Gaps: []DiagramGap{}}
	return v, g, expected
}
func TestArchitectureGoldenThreeLevelsAndMembers(t *testing.T) {
	v, g, expected := ordersProjectionFixture(t)
	for _, level := range []string{"context", "containers", "components"} {
		t.Run(level, func(t *testing.T) {
			root := v.Document.Payload.PrimarySystemID
			if level == "components" {
				root = "10000000-0000-4000-8000-000000000004"
			}
			q := DiagramQueryInput{Pin: v.Pin, Level: level, RootID: root, Search: "", Origin: "all", Section: "links", Limit: 100}
			page, err := ProjectArchitecture(context.Background(), v, g, q)
			if err != nil {
				t.Fatal(err)
			}
			if page.Projection == nil || page.Projection.Level != level || page.Projection.RootID != root || page.Projection.Policy != "architecture-v1" {
				t.Fatalf("projection context lost: %+v", page)
			}
			members := []string{}
			proofs := []string{}
			for _, row := range page.Items {
				q.Section = "members"
				q.SubjectID = row.ArchitectureLink.ID
				q.Limit = 1
				q.Cursor = ""
				for {
					p, err := ProjectArchitecture(t.Context(), v, g, q)
					if err != nil {
						t.Fatal(err)
					}
					for _, member := range p.Items {
						members = append(members, member.Member.Ref.ID)
						for _, e := range member.Member.Origin.Evidence {
							proofs = append(proofs, e.EvidenceID)
						}
					}
					if p.NextCursor == "" {
						break
					}
					q.Cursor = p.NextCursor
				}
			}
			slices.Sort(members)
			slices.Sort(proofs)
			if !slices.Equal(members, expected.PaymentMembers) || !slices.Equal(proofs, expected.PaymentEvidence) {
				t.Fatalf("members=%v proofs=%v", members, proofs)
			}
			if len(page.Gaps) == 0 {
				t.Fatal("unresolved delivery was hidden")
			}
		})
	}
}
func TestArchitectureAmbiguousMembershipDoesNotDoubleCount(t *testing.T) {
	v, g, _ := ordersProjectionFixture(t)
	e := v.Document.Payload.Elements[6]
	e.ID = "10000000-0000-4000-8000-000000000009"
	v.Document.Payload.Elements = append(v.Document.Payload.Elements, e)
	page, err := ProjectArchitecture(t.Context(), v, g, DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "links", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("links=%d", len(page.Items))
	}
	q := DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "members", SubjectID: page.Items[0].ArchitectureLink.ID, Limit: 100}
	members, err := ProjectArchitecture(t.Context(), v, g, q)
	if err != nil {
		t.Fatal(err)
	}
	if members.Total != 1 {
		t.Fatalf("ambiguous mapping invented a second member: %d", members.Total)
	}
}
func TestDiagramCursorBindsProjection(t *testing.T) {
	v, g, _ := ordersProjectionFixture(t)
	q := DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "elements", Limit: 1}
	p, err := ProjectArchitecture(t.Context(), v, g, q)
	if err != nil || p.NextCursor == "" {
		t.Fatalf("page=%+v, %v", p, err)
	}
	q.Cursor = p.NextCursor
	q.Level = "containers"
	_, err = ProjectArchitecture(t.Context(), v, g, q)
	assertFault(t, err, "backend_diagram_cursor_mismatch")
}

func TestArchitectureProposalIntentIsNotSourceProof(t *testing.T) {
	v, g, _ := ordersProjectionFixture(t)
	g.Origins = []EffectiveFieldOrigin{{RecordType: "edge", SubjectID: g.State.Edges[0].ID, Kind: "authored", Reason: "Explicit changed desired relation"}}
	page, err := ProjectArchitecture(t.Context(), v, g, DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "links", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	authored, source := 0, 0
	for _, row := range page.Items {
		switch row.ArchitectureLink.Origin.Kind {
		case "authored":
			authored++
		case "source_assertion":
			source++
		}
	}
	if authored != 1 || source != 1 {
		t.Fatalf("proposal intent merged into source proof: authored=%d source=%d", authored, source)
	}
}

func TestArchitectureExplicitSourceCannotReverseOrBypassAmbiguity(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			v, g, expected := ordersProjectionFixture(t)
			source := g.State.Edges[0]
			v.Document.Payload.Links = []ArchitectureLink{{ID: "10000000-0000-4000-8000-000000000900", Label: "Forged reverse", From: "10000000-0000-4000-8000-000000000003", To: "10000000-0000-4000-8000-000000000007", Relation: "calls", Origin: DiagramOrigin{Kind: "source_assertion", Evidence: []DiagramEvidenceRef{{RevisionID: g.Pins.BaseRevisionID, EvidenceID: expected.PaymentEvidence[0], SubjectID: source.ID}}}, Refs: []DiagramRef{{Kind: "record", RecordType: "edge", ID: source.ID}}}}
			if ambiguous {
				duplicate := v.Document.Payload.Elements[6]
				duplicate.ID = "10000000-0000-4000-8000-000000000009"
				v.Document.Payload.Elements = append(v.Document.Payload.Elements, duplicate)
			}
			q := DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "links", Limit: 100}
			page, err := ProjectArchitecture(t.Context(), v, g, q)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 1 {
				t.Fatalf("explicit assertion fabricated links: %+v", page.Items)
			}
			link := page.Items[0].ArchitectureLink
			if link.From != "10000000-0000-4000-8000-000000000002" || link.To != "10000000-0000-4000-8000-000000000003" {
				t.Fatalf("source direction spoofed: %+v", link)
			}
			q.Section = "members"
			q.SubjectID = link.ID
			members, err := ProjectArchitecture(t.Context(), v, g, q)
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if ambiguous {
				want = 1
			}
			if members.Total != want {
				t.Fatalf("explicit source bypassed ownership ambiguity: %d want%d", members.Total, want)
			}
		})
	}
}

func TestArchitectureAggregateMembersBeyondCanvasLimit(t *testing.T) {
	v, g, _ := ordersProjectionFixture(t)
	edge := g.State.Edges[0]
	g.State.Edges = []Edge{}
	g.State.Evidence = []Evidence{}
	expected := make([]string, 0, 601)
	for i := range 601 {
		id := fmt.Sprintf("20000000-0000-4000-8000-%012d", i+1)
		proof := fmt.Sprintf("30000000-0000-4000-8000-%012d", i+1)
		e := edge
		e.ID = id
		e.EvidenceIDs = []string{proof}
		g.State.Edges = append(g.State.Edges, e)
		g.State.Evidence = append(g.State.Evidence, Evidence{ID: proof, SubjectID: id})
		expected = append(expected, id)
	}
	q := DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "links", Limit: 100}
	page, err := ProjectArchitecture(t.Context(), v, g, q)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("aggregate: %+v %v", page, err)
	}
	q.Section = "members"
	q.SubjectID = page.Items[0].ArchitectureLink.ID
	q.Limit = 100
	got := []string{}
	for {
		members, err := ProjectArchitecture(t.Context(), v, g, q)
		if err != nil {
			t.Fatal(err)
		}
		if members.Truncated || members.Total != 601 {
			t.Fatal("canvas/page cap leaked into member traversal")
		}
		for _, row := range members.Items {
			got = append(got, row.Member.Ref.ID)
		}
		if members.NextCursor == "" {
			break
		}
		q.Cursor = members.NextCursor
	}
	if !slices.Equal(got, expected) {
		t.Fatal("lost or duplicate aggregate members beyond canvas limit")
	}
}

func TestArchitectureRejectsProjectedIdentityCollision(t *testing.T) {
	v, g, _ := ordersProjectionFixture(t)
	// Independent architecture-v1 Context tuple SHA-256/UUIDv8 test vector.
	collision := "7c75ea25-b97d-8a47-a636-f74cf87f3662"
	v.Document.Payload.Elements = append(v.Document.Payload.Elements, ArchitectureElement{ID: collision, Label: "Distinct person", Role: "person", Origin: DiagramOrigin{Kind: "authored", Reason: "Must not share a namespace with a projected relation"}, Refs: []DiagramRef{}})
	_, err := ProjectArchitecture(t.Context(), v, g, DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "links", Limit: 100})
	assertFault(t, err, "backend_diagram_identity_conflict")
}

func TestArchitectureSameNamedApplicationsKeepExactIdentities(t *testing.T) {
	v, g, _ := ordersProjectionFixture(t)
	v.Document.Payload.Elements[4].Label = v.Document.Payload.Elements[3].Label
	page, err := ProjectArchitecture(t.Context(), v, g, DiagramQueryInput{Pin: v.Pin, Level: "containers", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "elements", Search: "Orders API", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, row := range page.Items {
		if row.ArchitectureElement.Role == "application" {
			ids = append(ids, row.ArchitectureElement.ID)
		}
	}
	if !slices.Equal(ids, []string{"10000000-0000-4000-8000-000000000004", "10000000-0000-4000-8000-000000000005"}) {
		t.Fatalf("same names merged: %v", ids)
	}
}
