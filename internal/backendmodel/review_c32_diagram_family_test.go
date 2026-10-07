package backendmodel

import (
	"errors"
	"testing"
	"uuid"
)

// Review 2026-10-06, cluster C32: diagram family determinism and status codes.

// F101: a stale writer was validated against the newer head before the
// version comparison, so it got a content error instead of the documented
// version conflict that tells it to reread or fork.
func TestDiagramStaleSaveIsVersionConflictBeforeValidation(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "stale-save")
	doc := diagramTestDocument(p.CurrentRevisionID)
	v1, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	doc.Payload.Elements[0].Label = "Writer A"
	if _, err = r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: doc, IdempotencyKey: "a"}); err != nil {
		t.Fatal(err)
	}
	// Writer B still edits v1; what it sends cannot be checked against a head
	// it never read.
	stale := diagramTestDocument(uuid.NewV7().String())
	_, err = r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: stale, IdempotencyKey: "b"})
	assertFault(t, err, "backend_diagram_version_conflict")
}

// F104: BuildInteractions listed each unresolved_receiver gap twice (once from
// the builder, once from evidence resolution).
func TestInteractionsBuildGapsAreUnique(t *testing.T) {
	t.Parallel()
	// The unresolved-send fixture of TestInteractionsBuildUnresolvedSend,
	// through the same two steps BuildInteractions runs.
	s := eventsRuntimeState(t)
	s.Nodes[3].Attributes = runtimeQueryAttrs(t, map[string]any{"stepKind": "emit", "analysisStatus": "complete", "gaps": []string{}})
	runtimeQueryAddNode(t, s, 30, "message", 0, nil)
	runtimeQueryAddEdge(t, s, 130, "emits", 4, 30, map[string]any{"channelId": runtimeQueryID(31), "deliveryStatus": "declared"})
	g := &EffectiveGraphSnapshot{State: *s, Pins: EffectiveGraphPins{BaseRevisionID: s.Revision.ID, TargetHash: s.Revision.SemanticHash}}
	candidate, err := buildInteractions(t.Context(), g, DiagramInteractionBuildInput{Target: BackendReadTarget{RevisionID: s.Revision.ID}, EntrypointID: runtimeQueryID(1), MaxSteps: 200}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = resolveInteractionCandidateGaps(t.Context(), g, candidate); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	receivers := 0
	for _, gap := range candidate.Gaps {
		if seen[gap.ID] {
			t.Fatalf("duplicate gap %s (%s)", gap.ID, gap.Code)
		}
		seen[gap.ID] = true
		if gap.Code == "unresolved_receiver" {
			receivers++
		}
	}
	if receivers == 0 {
		t.Fatal("fixture no longer has an unresolved receiver; the test proves nothing")
	}
	if got := uniqueDiagramGaps([]DiagramGap{{ID: "b"}, {ID: "a"}, {ID: "b"}}); len(got) != 2 || got[0].ID != "b" || got[1].ID != "a" {
		t.Fatalf("read-side de-duplication: %+v", got)
	}
}

// F105: transition roles were checked in map order, so a transition with two
// invalid roles failed with a different message on identical requests.
func TestLifecycleRoleErrorIsDeterministic(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "lifecycle-roles")
	s := runtimeQueryFixture(t)
	runtimeQueryAddRelationalFacets(t, s)
	s.Revision.ProjectID = p.ID
	s.Revision.ParentRevisionID = new(p.CurrentRevisionID)
	runtimeQueryPersist(t, r, s)
	d := lifecycleFixture(t)
	d.Target = BackendReadTarget{RevisionID: s.Revision.ID}
	d.Lifecycle.Entity.ID = runtimeQueryID(10)
	d.Lifecycle.StateFields[0].ID = runtimeQueryID(11)
	for i := range d.Lifecycle.Transitions {
		d.Lifecycle.Transitions[i].Triggers[0].ID = runtimeQueryID(1)
	}
	d.Lifecycle.Rules[0].Trigger.ID = runtimeQueryID(1)
	// Both roles are wrong: an entity as trigger, an operation as write.
	d.Lifecycle.Transitions[0].Triggers[0].ID = runtimeQueryID(10)
	d.Lifecycle.Transitions[0].Writes = []DiagramRef{{Kind: "record", RecordType: "node", ID: runtimeQueryID(1)}}
	messages := map[string]bool{}
	for range 24 {
		_, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: d, IdempotencyKey: "roles"})
		fault, ok := errors.AsType[*FaultError](err)
		if !ok {
			t.Fatalf("want a fault, got %v", err)
		}
		messages[fault.Message] = true
	}
	if len(messages) != 1 {
		t.Fatalf("identical requests failed differently: %v", messages)
	}
}

// F107: diagram and view listings shared one catalog counter, so writing a
// view invalidated an in-flight diagram listing and vice versa.
func TestDiagramAndViewCursorsAreIndependent(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "catalogs")
	doc := diagramTestDocument(p.CurrentRevisionID)
	first, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "two"}); err != nil {
		t.Fatal(err)
	}
	state := DiagramViewState{Diagram: first.Pin, Level: "context", RootID: doc.Payload.PrimarySystemID, Origin: "all", Positions: []DiagramPosition{}, CollapsedIDs: []string{}}
	for _, key := range []string{"v1", "v2"} {
		if _, err = r.CreateDiagramView(t.Context(), p.ID, DiagramCreateViewInput{Name: key, State: state, IdempotencyKey: key}); err != nil {
			t.Fatal(err)
		}
	}
	diagrams, err := r.ListDiagrams(t.Context(), p.ID, DiagramListInput{Limit: 1})
	if err != nil || diagrams.NextCursor == "" {
		t.Fatalf("diagram page: %+v %v", diagrams, err)
	}
	views, err := r.ListDiagramViews(t.Context(), p.ID, DiagramListInput{Limit: 1})
	if err != nil || views.NextCursor == "" {
		t.Fatalf("view page: %+v %v", views, err)
	}
	// A view write leaves the diagram catalog unchanged.
	if _, err = r.CreateDiagramView(t.Context(), p.ID, DiagramCreateViewInput{Name: "v3", State: state, IdempotencyKey: "v3"}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ListDiagrams(t.Context(), p.ID, DiagramListInput{Limit: 1, Cursor: diagrams.NextCursor}); err != nil {
		t.Fatalf("view write invalidated the diagram cursor: %v", err)
	}
	// A diagram write leaves the view catalog unchanged; the view cursor was
	// taken before v3, so re-page from a fresh one.
	views, err = r.ListDiagramViews(t.Context(), p.ID, DiagramListInput{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "three"}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ListDiagramViews(t.Context(), p.ID, DiagramListInput{Limit: 1, Cursor: views.NextCursor}); err != nil {
		t.Fatalf("diagram write invalidated the view cursor: %v", err)
	}
	// Its own catalog's change still rejects a stale cursor.
	_, err = r.ListDiagrams(t.Context(), p.ID, DiagramListInput{Limit: 1, Cursor: diagrams.NextCursor})
	if err == nil {
		t.Fatal("diagram write kept a stale diagram cursor valid")
	}
}
