package backendmodel

import (
	"encoding/json/v2"
	"os"
	"slices"
	"testing"
)

func interactionFixture(t *testing.T) DiagramDocument {
	t.Helper()
	raw, err := os.ReadFile("testdata/diagrams/interaction.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc DiagramDocument
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestInteractionsBranchesDoNotBecomeSequence(t *testing.T) {
	doc := interactionFixture(t)
	r, _ := testRepo(t)
	p := createProject(t, r, "interaction-oracle")
	doc.Target = BackendReadTarget{RevisionID: p.CurrentRevisionID}
	v, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.QueryDiagram(t.Context(), p.ID, DiagramQueryInput{Pin: v.Pin, Search: "", Origin: "all", Section: "links", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/diagrams/interaction_expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		Links []string `json:"links"`
	}
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(page.Items))
	for _, row := range page.Items {
		b, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			RowType string `json:"rowType"`
			Data    struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err = json.Unmarshal(b, &wire); err != nil {
			t.Fatal(err)
		}
		if wire.RowType != "order" {
			t.Fatalf("unexpected row: %s", b)
		}
		ids = append(ids, wire.Data.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, expected.Links) {
		t.Fatalf("invented or lost order: %v", ids)
	}
}

func TestInteractionsNoSyntheticReceiver(t *testing.T) {
	doc := interactionFixture(t)
	r, _ := testRepo(t)
	p := createProject(t, r, "unresolved-interaction")
	doc.Target = BackendReadTarget{RevisionID: p.CurrentRevisionID}
	v, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(v.Gaps, func(g DiagramGap) bool {
		return g.SubjectID == "00000000-0000-4000-8000-000000000013" && g.Code == "unresolved_receiver"
	}) {
		t.Fatal("unresolved send lost its gap")
	}
}

func TestInteractionsPartialOrder(t *testing.T) {
	doc := interactionFixture(t)
	before, err := normalizeDiagram(doc)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(doc.Interactions.Steps)
	slices.Reverse(doc.Interactions.Order)
	slices.Reverse(doc.Interactions.Branches)
	after, err := normalizeDiagram(doc)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := requestDigest(before)
	b, _ := requestDigest(after)
	if a != b {
		t.Fatal("display order changed semantic hash")
	}
	if len(after.Interactions.Order) != 2 {
		t.Fatal("unordered peers gained order")
	}
}
func TestInteractionsOrderDuplicates(t *testing.T) {
	for _, kind := range []string{"pair", "collision", "cycle", "branch-crossing", "missing-participant", "branch-chain", "reply"} {
		t.Run(kind, func(t *testing.T) {
			doc := interactionFixture(t)
			p := doc.Interactions
			switch kind {
			case "pair":
				v := p.Order[0]
				v.ID = "00000000-0000-4000-8000-000000000099"
				p.Order = append(p.Order, v)
			case "collision":
				p.Order[0].ID = p.Steps[0].ID
			case "cycle":
				v := p.Order[0]
				v.ID = "00000000-0000-4000-8000-000000000099"
				v.From, v.To = v.To, v.From
				p.Order = append(p.Order, v)
			case "branch-crossing":
				p.Order[0].From = p.Steps[1].ID
				p.Order[0].To = p.Steps[2].ID
			case "missing-participant":
				p.Steps[0].From = p.Steps[0].ID
			case "branch-chain":
				p.Steps[1].BranchPath = append(p.Steps[1].BranchPath, p.Branches[1].ID)
			case "reply":
				p.Steps[1].ReplyTo = p.Steps[3].ID
			}
			if err := doc.Validate(); err == nil {
				t.Fatal("invalid payload accepted")
			}
		})
	}
}
func TestInteractionsHistoricalCompare(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "interaction-history")
	doc := interactionFixture(t)
	doc.Target = BackendReadTarget{RevisionID: p.CurrentRevisionID}
	v1, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	edge := doc.Interactions.Order[0].ID
	view, err := r.CreateDiagramView(t.Context(), p.ID, DiagramCreateViewInput{Name: "Old order", IdempotencyKey: "view", State: DiagramViewState{Diagram: v1.Pin, Origin: "all", Selection: &DiagramSelection{Type: "link", ID: edge}, Positions: []DiagramPosition{}, CollapsedIDs: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	doc.Interactions.Order = doc.Interactions.Order[1:]
	v2, err := r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: doc, IdempotencyKey: "remove"})
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := r.CompareDiagrams(t.Context(), p.ID, DiagramCompareInput{Before: v1.Pin, After: v2.Pin, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(comparison.Items) != 1 || comparison.Items[0].ID != edge || comparison.Items[0].Kind != "removed" {
		t.Fatalf("comparison=%+v", comparison)
	}
	old, err := r.GetDiagramView(t.Context(), p.ID, view.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if old.State.Diagram != v1.Pin || old.State.Selection.ID != edge {
		t.Fatal("historical selection changed")
	}
	page, err := r.QueryDiagram(t.Context(), p.ID, DiagramQueryInput{Pin: v1.Pin, Origin: "all", Section: "links", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].Order.ID != edge {
		t.Fatal("historical order unavailable")
	}
	_, err = r.QueryDiagram(t.Context(), p.ID, DiagramQueryInput{Pin: v2.Pin, Origin: "all", Section: "links", Limit: 1, Cursor: page.NextCursor})
	assertFault(t, err, "backend_diagram_cursor_mismatch")
}

func TestInteractionsOrderIdentity(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "order-identity")
	doc := interactionFixture(t)
	doc.Target = BackendReadTarget{RevisionID: p.CurrentRevisionID}
	v1, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	edge := doc.Interactions.Order[0].ID
	doc.Interactions.Order[0].Origin.Reason = "Explicitly revised causal claim"
	v2, err := r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: doc, IdempotencyKey: "origin"})
	if err != nil {
		t.Fatal(err)
	}
	delta, err := r.CompareDiagrams(t.Context(), p.ID, DiagramCompareInput{Before: v1.Pin, After: v2.Pin, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Items) != 1 || delta.Items[0].ID != edge || !slices.Equal(delta.Items[0].Fields, []string{"origin"}) {
		t.Fatalf("origin changed identity: %+v", delta)
	}
	added := InteractionOrder{ID: "00000000-0000-4000-8000-000000000098", From: doc.Interactions.Steps[0].ID, To: doc.Interactions.Steps[3].ID, Origin: doc.Interactions.Order[0].Origin}
	doc.Interactions.Order = append(doc.Interactions.Order, added)
	v3, err := r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, DiagramSaveInput{ExpectedVersion: 2, Document: doc, IdempotencyKey: "add"})
	if err != nil {
		t.Fatal(err)
	}
	delta, err = r.CompareDiagrams(t.Context(), p.ID, DiagramCompareInput{Before: v2.Pin, After: v3.Pin, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Items) != 1 || delta.Items[0].ID != added.ID || delta.Items[0].Kind != "added" {
		t.Fatalf("added order lost: %+v", delta)
	}
}

func TestInteractionsCompareScopeAndArchitecture(t *testing.T) {
	doc := interactionFixture(t)
	v := &DiagramVersion{Document: doc, TargetHash: "same"}
	g := &EffectiveGraphSnapshot{Pins: EffectiveGraphPins{TargetHash: "same"}}
	before, err := diagramComparisonRows(t.Context(), v, g, diagramIdentity("interaction-document-metadata-v1"))
	if err != nil {
		t.Fatal(err)
	}
	doc.Interactions.ScopeRefs = []DiagramRef{{Kind: "record", RecordType: "node", ID: "00000000-0000-4000-8000-000000000080"}}
	after, err := diagramComparisonRows(t.Context(), &DiagramVersion{Document: doc, TargetHash: "same"}, g, diagramIdentity("interaction-document-metadata-v1"))
	if err != nil {
		t.Fatal(err)
	}
	diffs := diagramDifferences(before, after)
	if len(diffs) != 1 || !slices.Contains(diffs[0].Fields, "scopeRefs") {
		t.Fatal("scope-only edit missing", diffs)
	}
	doc.Interactions.Architecture = &DiagramPin{ID: "00000000-0000-4000-8000-000000000081", Version: 1, ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	next, err := diagramComparisonRows(t.Context(), &DiagramVersion{Document: doc, TargetHash: "same"}, g, diagramIdentity("interaction-document-metadata-v1"))
	if err != nil {
		t.Fatal(err)
	}
	diffs = diagramDifferences(after, next)
	if len(diffs) != 1 || !slices.Contains(diffs[0].Fields, "architecture") {
		t.Fatal("dependency-only fork missing", diffs)
	}
}

func TestInteractionsForkMembershipGapSurvivesSave(t *testing.T) {
	r, _ := testRepo(t)
	project := createProject(t, r, "membership-history")
	architecture := diagramTestDocument(project.CurrentRevisionID)
	first, err := r.CreateDiagram(t.Context(), project.ID, DiagramCreateInput{Document: architecture, IdempotencyKey: "arch1"})
	if err != nil {
		t.Fatal(err)
	}
	architecture.Payload.PrimarySystemID = "10000000-0000-4000-8000-000000000003"
	architecture.Payload.Elements[0].ID = architecture.Payload.PrimarySystemID
	second, err := r.CreateDiagram(t.Context(), project.ID, DiagramCreateInput{Document: architecture, IdempotencyKey: "arch2"})
	if err != nil {
		t.Fatal(err)
	}
	doc := interactionFixture(t)
	doc.Target = BackendReadTarget{RevisionID: project.CurrentRevisionID}
	doc.Interactions.Architecture = &first.Pin
	doc.Interactions.Participants[0].ArchitectureElementID = first.Document.Payload.PrimarySystemID
	original, err := r.CreateDiagram(t.Context(), project.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "interaction"})
	if err != nil {
		t.Fatal(err)
	}
	fork, err := r.ForkDiagram(t.Context(), project.ID, DiagramForkInput{Source: original.Pin, Target: doc.Target, Architecture: &second.Pin, Reason: "Revised explicit membership", IdempotencyKey: "fork"})
	if err != nil {
		t.Fatal(err)
	}
	fork.Document.Interactions.Steps[0].Label = "Unrelated edit"
	saved, err := r.SaveDiagram(t.Context(), project.ID, fork.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: fork.Document, IdempotencyKey: "save"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(saved.Gaps, func(g DiagramGap) bool { return g.Code == "unresolved_membership" }) {
		t.Fatal("historical membership gap lost")
	}
}

func TestInteractionsComparisonMetadataCannotShadowSemanticIdentity(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	project := createProject(t, r, "metadata-collision")
	doc := interactionFixture(t)
	doc.Target = BackendReadTarget{RevisionID: project.CurrentRevisionID}
	id := diagramIdentity("interaction-document-metadata-v1")
	doc.Interactions.Order[0].ID = id
	v1, err := r.CreateDiagram(t.Context(), project.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	doc.Interactions.Order = doc.Interactions.Order[1:]
	v2, err := r.SaveDiagram(t.Context(), project.ID, v1.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: doc, IdempotencyKey: "remove"})
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := r.CompareDiagrams(t.Context(), project.ID, DiagramCompareInput{Before: v1.Pin, After: v2.Pin, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(comparison.Items) != 1 || comparison.Items[0].ID != id || comparison.Items[0].Kind != "removed" {
		t.Fatalf("metadata shadowed semantic removal: %+v", comparison.Items)
	}
	reverse, err := r.CompareDiagrams(t.Context(), project.ID, DiagramCompareInput{Before: v2.Pin, After: v1.Pin, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(reverse.Items) != 1 || reverse.Items[0].ID != id || reverse.Items[0].Kind != "added" {
		t.Fatalf("metadata shadowed semantic addition: %+v", reverse.Items)
	}
}

func TestInteractionsLocalActionHasNoReceiverGap(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "local-action")
	doc := interactionFixture(t)
	doc.Target = BackendReadTarget{RevisionID: p.CurrentRevisionID}
	action := &doc.Interactions.Steps[3]
	action.Kind, action.To, action.ReplyTo = "action", "", ""
	action.Label = "Проверить, настроена ли ссылка"
	v, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "action"})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(v.Gaps, func(g DiagramGap) bool { return g.SubjectID == action.ID && g.Code == "unresolved_receiver" }) {
		t.Fatal("local action gained a receiver gap")
	}
	for _, field := range []string{"to", "replyTo"} {
		t.Run(field, func(t *testing.T) {
			bad := doc
			bad.Interactions = new(*doc.Interactions)
			bad.Interactions.Steps = slices.Clone(doc.Interactions.Steps)
			if field == "to" {
				bad.Interactions.Steps[3].To = doc.Interactions.Participants[0].ID
			} else {
				bad.Interactions.Steps[3].ReplyTo = doc.Interactions.Steps[0].ID
			}
			if err := bad.Validate(); err == nil {
				t.Fatal("action accepted message fields")
			}
		})
	}
}
