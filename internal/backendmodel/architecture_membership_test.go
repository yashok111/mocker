package backendmodel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
)

func TestArchitectureMembershipCatalogFindsExactMember(t *testing.T) {
	r, pid, q := storedArchitectureFixture(t, 3, 3, 3)
	v, err := r.GetDiagram(t.Context(), pid, q.Pin)
	if err != nil {
		t.Fatal(err)
	}
	d := v.Document
	e := &d.Payload.Elements[2]
	subject := e.Refs[0].ID
	e.Refs = []DiagramRef{}
	e.Membership = &ArchitectureMembership{Format: ArchitectureMembershipVersion, NodeIDs: []string{subject}}
	// This read fixture deliberately has no source-owner bootstrap. Persist its
	// next immutable document through the canonical writer, like fixture setup.
	v.Document, err = normalizeDiagram(d)
	if err != nil {
		t.Fatal(err)
	}
	v.Pin.Version++
	v.receiptJSON = ""
	v.Pin.ContentHash, err = requestDigest(v.Document)
	if err != nil {
		t.Fatal(err)
	}
	diagramProvenance(v, nil, "create", "")
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		return persistDiagramVersion(t.Context(), tx, diagramMutation{pid: pid, id: v.Pin.ID, op: "save", key: "membership", digest: v.Pin.ContentHash, document: v.Document}, v)
	})
	if err != nil {
		t.Fatal(err)
	}
	saved := v
	page, err := r.ListDiagrams(t.Context(), pid, DiagramListInput{SubjectID: subject, TargetHash: saved.TargetHash, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Pin != saved.Pin {
		t.Fatalf("member absent from catalog: %+v", page)
	}
}

func TestArchitectureHistoricalMembershipChecksCancellation(t *testing.T) {
	t.Parallel()
	refs := make([]DiagramRef, 5000)
	for i := range refs {
		refs[i] = DiagramRef{Kind: "record", RecordType: "node", ID: fmt.Sprintf("20000000-0000-4000-8000-%012d", i+1)}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	resolver := newDiagramArtifactResolver(ctx, &EffectiveGraphSnapshot{})
	_, err := diagramReferenceGaps(resolver, "element", refs, refs, map[string]bool{}, map[string]bool{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("historical membership ignored cancellation: %v", err)
	}
}

func TestArchitectureExactMembershipReportedScopes(t *testing.T) {
	t.Parallel()
	d := diagramTestDocument("10000000-0000-4000-8000-000000000090")
	const app = "10000000-0000-4000-8000-000000000003"
	const target = "10000000-0000-4000-8000-000000000004"
	const targetNode = "20000000-0000-4000-8000-000000000004"
	origin := DiagramOrigin{Kind: "authored", Reason: "Exact responsibility mapping"}
	d.Payload.Elements = append(d.Payload.Elements, ArchitectureElement{ID: app, Role: "application", ParentID: d.Payload.PrimarySystemID, Label: "App", Origin: origin, Refs: []DiagramRef{}}, ArchitectureElement{ID: target, Role: "component", ParentID: app, Label: "Adapter", Origin: origin, Refs: []DiagramRef{{Kind: "record", RecordType: "node", ID: targetNode}}})
	g := &EffectiveGraphSnapshot{Pins: EffectiveGraphPins{BaseRevisionID: d.Target.RevisionID, TargetHash: fixtureHash}}
	for scope, count := range []int{771, 646, 278, 137, 103} {
		e := ArchitectureElement{ID: fmt.Sprintf("10000000-0000-4000-8000-%012d", scope+10), Role: "component", ParentID: app, Label: fmt.Sprint("Scope ", scope), Origin: origin, Refs: []DiagramRef{}, Membership: &ArchitectureMembership{Format: ArchitectureMembershipVersion, NodeIDs: []string{}}}
		for i := range count {
			node := fmt.Sprintf("20000000-0000-4000-8000-%012d", scope*1000+i+100)
			edge := fmt.Sprintf("30000000-0000-4000-8000-%012d", scope*1000+i+100)
			proof := fmt.Sprintf("40000000-0000-4000-8000-%012d", scope*1000+i+100)
			e.Membership.NodeIDs = append(e.Membership.NodeIDs, node)
			g.State.Nodes = append(g.State.Nodes, Node{ID: node})
			g.State.Edges = append(g.State.Edges, Edge{ID: edge, From: node, To: targetNode, Kind: "calls", EvidenceIDs: []string{proof}})
			g.State.Evidence = append(g.State.Evidence, Evidence{ID: proof, SubjectID: edge})
		}
		d.Payload.Elements = append(d.Payload.Elements, e)
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	hash, _ := requestDigest(d)
	v := &DiagramVersion{Document: d, TargetHash: fixtureHash, Pin: DiagramPin{ID: "10000000-0000-4000-8000-000000000080", Version: 1, ContentHash: hash}}
	q := DiagramQueryInput{Pin: v.Pin, Level: "components", RootID: app, Section: "members", Origin: "all", Limit: 100, ResponseMode: "compact-v1"}
	for _, e := range d.Payload.Elements[3:] {
		q.SubjectID, q.Cursor = e.ID, ""
		got := []string{}
		for {
			page, err := ProjectArchitecture(t.Context(), v, g, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range page.Items {
				got = append(got, row.Member.Ref.ID)
			}
			if page.NextCursor == "" {
				break
			}
			q.Cursor = page.NextCursor
		}
		if !slices.Equal(got, e.Membership.NodeIDs) {
			t.Fatalf("scope %s lost exact members", e.Label)
		}
	}
	q.Section, q.SubjectID, q.Cursor = "links", "", ""
	page, err := ProjectArchitecture(t.Context(), v, g, q)
	if err != nil || len(page.Items) != 5 || page.GapSummary.Total != 0 {
		t.Fatalf("relationships/gaps changed: %+v %v", page, err)
	}
	for _, row := range page.Items {
		q.Section, q.SubjectID, q.Cursor = "members", row.ArchitectureLink.ID, ""
		count := 0
		for {
			members, err := ProjectArchitecture(t.Context(), v, g, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, member := range members.Items {
				if len(member.Member.Origin.Evidence) != 1 {
					t.Fatal("lost proof")
				}
				count++
			}
			if members.NextCursor == "" {
				break
			}
			q.Cursor = members.NextCursor
		}
		if !slices.Contains([]int{771, 646, 278, 137, 103}, count) {
			t.Fatalf("lost relationship members: %d", count)
		}
	}
}

func TestArchitectureMembershipIsExplicitBoundedAndCanonical(t *testing.T) {
	t.Parallel()
	d := diagramTestDocument("10000000-0000-4000-8000-000000000090")
	e := &d.Payload.Elements[0]
	e.Membership = &ArchitectureMembership{Format: ArchitectureMembershipVersion, NodeIDs: []string{"20000000-0000-4000-8000-000000000002", "20000000-0000-4000-8000-000000000001"}}
	normalized, err := normalizeDiagram(d)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Payload.Elements[0].Membership.NodeIDs[0] == e.Membership.NodeIDs[0] {
		t.Fatal("membership order not canonicalized or input changed")
	}
	e.Refs = []DiagramRef{{Kind: "record", RecordType: "node", ID: e.Membership.NodeIDs[0]}}
	if d.Validate() == nil {
		t.Fatal("duplicate exact owner admitted")
	}
	e.Refs = []DiagramRef{}
	e.Membership.NodeIDs = append(e.Membership.NodeIDs, e.Membership.NodeIDs[0])
	if d.Validate() == nil {
		t.Fatal("duplicate set member admitted")
	}
	e.Membership = &ArchitectureMembership{Format: ArchitectureMembershipVersion, NodeIDs: make([]string, 5001)}
	if d.Validate() == nil {
		t.Fatal("unbounded membership admitted")
	}
}
