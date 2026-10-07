package backendportable

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/yashok111/mocker/internal/apidesign"
	bm "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/testkit"
)

type roundtripFixture struct {
	service   *Service
	artifacts *bm.ArtifactService
	selection Selection
	views     []bm.DiagramView
	diagrams  []bm.DiagramVersion
	sourceIDs map[string]string
	pin       bm.NamespacedArtifactPin
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func attrs(t *testing.T, raw string) map[string]jsontext.Value {
	t.Helper()
	var out map[string]jsontext.Value
	check(t, json.Unmarshal([]byte(raw), &out))
	return out
}
func fixedID(n int) string { return fmt.Sprintf("70000000-0000-4000-8000-%012d", n) }

func makeRoundtripFixture(t *testing.T) *roundtripFixture {
	t.Helper()
	db := testkit.NewDB(t)
	models := bm.NewRepo(db)
	s := NewService(db, models)
	p, err := models.Create(t.Context(), bm.CreateInput{Name: "Roundtrip", IdempotencyKey: "project"})
	check(t, err)
	inventory := []bm.InventoryItem{}
	for _, category := range []string{"files", "endpoints", "datastores", "migrations", "producers", "consumers", "jobs", "contracts", "tests"} {
		n := int64(0)
		if category == "files" {
			n = 1
		}
		inventory = append(inventory, bm.InventoryItem{Category: category, Status: "complete", KnownCount: n, Denominator: &n, DiscoverySource: "independent fixture", Gaps: []string{}})
	}
	begin := bm.BeginImportInput{ExpectedVersion: 1, BaseRevisionID: p.CurrentRevisionID, IdempotencyKey: "begin", Mode: "composed", Profile: bm.ComposedProfile, SourceScope: &bm.SourceScope{Kind: "add_repository"}, ScopeStatus: &bm.SourceScopeStatus{Status: "complete", Gaps: []string{}}, SyncPolicy: bm.WholeSourcePolicy, Inventory: inventory, Manifest: bm.SourceManifest{RepositoryName: "orders", Provider: bm.SourceProvider{Name: "fixture", Version: "1", Namespace: "fixture", Method: "ast", Profiles: []string{bm.GraphProfile, bm.RelationalProfile, bm.RuntimeProfile, bm.LineageProfile, bm.EventsProfile, bm.ComposedProfile}, Limitations: []string{}}, Snapshot: bm.SnapshotManifest{Consistency: "verified", CapturedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Files: []bm.ManifestFile{{Path: "orders.go", ContentHash: hash, FileType: "go", AnalysisStatus: "analyzed"}}}}}
	session, err := models.BeginImport(t.Context(), p.ID, begin)
	check(t, err)
	commands := []bm.ImportCommand{}
	proof := func(kind, key string) bm.ImportCommand {
		return bm.ImportCommand{Op: "upsert_evidence", Evidence: &bm.ImportEvidence{ExternalKey: "proof-" + key, SubjectType: kind, SubjectKey: key, Method: "ast", Status: "explicit", Source: bm.EvidenceSource{RepositoryID: session.RepositoryID, SnapshotID: session.SnapshotID, File: "orders.go", ContentHash: hash, StartLine: new(int64(1)), EndLine: new(int64(2))}}}
	}
	for _, node := range []struct{ key, kind, parent, attributes string }{
		{"service", "service", "", `{}`},
		{"entity", "domain_entity", "service", `{"qualifiedName":"orders.Order","analysisStatus":"complete","gaps":[]}`},
		{"status", "representation_field", "entity", `{"selector":[{"property":"status"}],"nativeType":{"status":"known","value":"string"},"nullable":{"status":"known","value":false},"cardinality":{"status":"known","value":"one"},"analysisStatus":"complete","gaps":[]}`},
		{"handler", "handler", "service", `{}`},
	} {
		n := &bm.ImportNode{ExternalKey: node.key, Kind: node.kind, Name: node.key, Attributes: attrs(t, node.attributes), EvidenceKeys: []string{"proof-" + node.key}}
		if node.parent != "" {
			n.ParentRef = &bm.ImportRecordRef{LocalKey: node.parent}
		}
		commands = append(commands, bm.ImportCommand{Op: "upsert_node", Node: n}, proof("node", node.key))
		if node.parent != "" {
			key := "contains-" + node.key
			commands = append(commands, bm.ImportCommand{Op: "upsert_edge", Edge: &bm.ImportEdge{ExternalKey: key, Kind: "contains", FromRef: &bm.ImportRecordRef{LocalKey: node.parent}, ToRef: &bm.ImportRecordRef{LocalKey: node.key}, Attributes: attrs(t, `{}`), EvidenceKeys: []string{"proof-" + key}}}, proof("edge", key))
		}
	}
	batchHash, err := bm.ImportBatchHash(commands)
	check(t, err)
	batch, err := models.PutImportBatch(t.Context(), p.ID, session.ID, "fixture", bm.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: batchHash, Commands: commands})
	check(t, err)
	candidate, err := models.PreviewImport(t.Context(), p.ID, session.ID, bm.PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	check(t, err)
	if candidate.CandidateHash == nil {
		t.Fatalf("source validation: %+v", candidate.Diagnostics)
	}
	source, err := models.CommitImport(t.Context(), p.ID, session.ID, bm.CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: candidate.Version, CandidateHash: *candidate.CandidateHash, IdempotencyKey: "commit"})
	check(t, err)
	ids := map[string]string{}
	for _, id := range batch.Identities {
		ids[id.ExternalKey] = id.ID
	}
	proposal, err := models.CreateChangeProposal(t.Context(), p.ID, bm.CreateChangeProposalInput{Name: "Desired", BaseRevisionID: source.Revision.ID, IdempotencyKey: "proposal"})
	check(t, err)
	cfg := &config.Config{MaxBody: 4 << 20}
	apis := apidesign.NewRepo(db, cfg)
	scenarios := designscenario.NewRepo(db, cfg, apis)
	owner, err := scenarios.Create(t.Context(), designscenario.CreateInput{Source: "ui", Document: designscenario.Document{FormatVersion: 1, Title: "Pinned sequence", Participants: []designscenario.Participant{{ID: "client", Name: "Client", Kind: "service"}, {ID: "orders", Name: "Orders", Kind: "service"}}, Messages: []designscenario.Message{}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{}}})
	check(t, err)
	pinCommand := bm.ChangeProposalCommand{Type: "set_artifact_pin", CommandID: uuid.NewV7().String(), Reason: "Explicit owner link", Artifact: &bm.ArtifactKey{Kind: "design_scenario", ID: strconv.FormatInt(owner.Scenario.ID, 10)}, RevisionID: strconv.FormatInt(owner.Draft.ID, 10), EditorBindings: []bm.EditorBindingInput{}}
	ownerPin := bm.ArtifactPin{Kind: "design_scenario", ID: pinCommand.Artifact.ID, RevisionID: pinCommand.RevisionID, ContentHash: owner.Draft.Hash}
	proposalCommands := []bm.ChangeProposalCommand{
		pinCommand,
		{Type: "create_node", CommandID: uuid.NewV7().String(), Reason: "Authored desired symbol", ID: fixedID(100), Kind: "symbol", Name: "Planned symbol", Attributes: attrs(t, `{}`)},
		{Type: "rename", CommandID: uuid.NewV7().String(), Reason: "Authored desired label", RecordType: "node", ID: ids["entity"], Name: "Order desired"},
		{Type: "set_criteria", CommandID: uuid.NewV7().String(), Reason: "Exact external attachments", Criteria: []bm.ChangeCriterion{
			{Key: "scenario", Kind: "test_attachment", Description: "Exact scenario, not execution proof", TargetIDs: []string{ids["handler"]}, Attachment: &bm.TestAttachmentRef{Kind: "artifact", Artifact: &ownerPin}},
			{Key: "source", Kind: "test_attachment", Description: "Exact source test locator", TargetIDs: []string{ids["handler"]}, Attachment: &bm.TestAttachmentRef{Kind: "source", RevisionID: source.Revision.ID, RepositoryID: session.RepositoryID, SnapshotID: session.SnapshotID, File: "orders.go", ContentHash: hash, StartLine: 1, EndLine: 2}},
		}},
	}
	preview, err := models.PreviewChangeProposal(t.Context(), p.ID, proposal.Proposal.ID, bm.PreviewChangeProposalInput{ExpectedVersion: 1, ProposalRevisionID: proposal.Revision.ID, Commands: proposalCommands})
	check(t, err)
	if preview.CandidateHash == nil {
		t.Fatal(preview.Diagnostics)
	}
	_, err = models.ApplyChangeProposal(t.Context(), p.ID, proposal.Proposal.ID, bm.ApplyChangeProposalInput{ExpectedVersion: 1, ProposalRevisionID: proposal.Revision.ID, Commands: proposalCommands, CandidateHash: *preview.CandidateHash, IdempotencyKey: "pin"})
	check(t, err)
	proposal, err = models.GetChangeProposal(t.Context(), p.ID, proposal.Proposal.ID, bm.GetChangeProposalInput{})
	check(t, err)
	target := bm.BackendReadTarget{ChangeProposal: &bm.ProposalReadTarget{ProposalID: proposal.Proposal.ID, ProposalRevisionID: proposal.Revision.ID}}
	artifacts := bm.NewArtifactService(models, apis, scenarios)
	page, err := artifacts.Query(t.Context(), p.ID, bm.ArtifactQueryInput{ChangeProposal: target.ChangeProposal, Artifact: *pinCommand.Artifact, View: "sequence", Limit: 100})
	check(t, err)
	if len(page.Items) == 0 {
		t.Fatal("missing API projection")
	}
	artifactRef := bm.DiagramRef{Kind: "artifact", Locator: &page.Items[0].Locator, RowID: page.Items[0].ID}
	origin := bm.DiagramOrigin{Kind: "authored", Reason: "Independent hand-authored fixture"}
	create := func(actor, key string, doc bm.DiagramDocument) *bm.DiagramVersion {
		t.Helper()
		ctx := artifacts.DiagramContext(bm.WithDiagramActor(t.Context(), actor))
		v, err := models.CreateDiagram(ctx, p.ID, bm.DiagramCreateInput{Document: doc, IdempotencyKey: key})
		check(t, err)
		return v
	}
	architecture := bm.DiagramDocument{Format: bm.DiagramDocumentVersion, Kind: "architecture", Target: target, Payload: bm.ArchitecturePayload{PrimarySystemID: fixedID(1), Elements: []bm.ArchitectureElement{{ID: fixedID(1), Label: "Orders X", Role: "software_system", Responsibility: "Orders", Technology: "Go", Origin: origin, Refs: []bm.DiagramRef{artifactRef}}, {ID: fixedID(2), Label: "Worker Y", Role: "application", ParentID: fixedID(1), Responsibility: "Background", Technology: "Go", Origin: origin, Refs: []bm.DiagramRef{{Kind: "record", RecordType: "node", ID: ids["handler"]}}}}, Links: []bm.ArchitectureLink{}}}
	a := create("Alice", "architecture", architecture)
	ctx := artifacts.DiagramContext(bm.WithDiagramActor(t.Context(), "Bob"))
	b, err := models.ForkDiagram(ctx, p.ID, bm.DiagramForkInput{Source: a.Pin, Target: target, Reason: "Review fork", IdempotencyKey: "fork"})
	check(t, err)
	architecture = b.Document
	architecture.Payload.Elements[1].Label = "Worker Y changed"
	c, err := models.SaveDiagram(artifacts.DiagramContext(bm.WithDiagramActor(t.Context(), "Carol")), p.ID, b.Pin.ID, bm.DiagramSaveInput{ExpectedVersion: 1, Document: architecture, IdempotencyKey: "edit-y"})
	check(t, err)
	interactions := bm.DiagramDocument{Format: bm.DiagramDocumentVersion, Kind: "interactions", Target: target, Interactions: &bm.InteractionPayload{Architecture: &c.Pin, ScopeRefs: []bm.DiagramRef{}, Participants: []bm.InteractionParticipant{{ID: fixedID(10), Label: "Client", Origin: origin, Refs: []bm.DiagramRef{}, ArchitectureElementID: fixedID(1)}, {ID: fixedID(11), Label: "Orders", Origin: origin, Refs: []bm.DiagramRef{}, ArchitectureElementID: fixedID(2)}}, Steps: []bm.InteractionStep{{ID: fixedID(12), Label: "Create", Kind: "request", From: fixedID(10), To: fixedID(11), Origin: origin, Refs: []bm.DiagramRef{artifactRef}, BranchPath: []string{}}, {ID: fixedID(13), Label: "Reply", Kind: "response", From: fixedID(11), To: fixedID(10), ReplyTo: fixedID(12), Origin: origin, Refs: []bm.DiagramRef{}, BranchPath: []string{}}}, Order: []bm.InteractionOrder{{ID: fixedID(14), From: fixedID(12), To: fixedID(13), Origin: origin}}, Branches: []bm.InteractionBranch{}}}
	lifecycle := bm.DiagramDocument{Format: bm.DiagramDocumentVersion, Kind: "lifecycle", Target: target, Lifecycle: &bm.LifecyclePayload{Entity: bm.DiagramRef{Kind: "record", RecordType: "node", ID: ids["entity"]}, StateFields: []bm.DiagramRef{{Kind: "record", RecordType: "node", ID: ids["status"]}}, States: []bm.LifecycleState{{ID: fixedID(20), Label: "Created", Origin: origin, Refs: []bm.DiagramRef{artifactRef}, Initial: true, Value: &bm.LifecycleValue{JSON: `"created"`}}, {ID: fixedID(21), Label: "Paid", Origin: origin, Refs: []bm.DiagramRef{}, Terminal: true, Value: &bm.LifecycleValue{JSON: `"paid"`}}}, Transitions: []bm.LifecycleTransition{{ID: fixedID(22), Label: "Pay", From: fixedID(20), To: fixedID(21), Origin: origin, Refs: []bm.DiagramRef{}, Triggers: []bm.DiagramRef{{Kind: "record", RecordType: "node", ID: ids["handler"]}}, Writes: []bm.DiagramRef{}, Events: []bm.DiagramRef{}, Guard: bm.LifecycleGuard{Kind: "opaque", Text: "external approval is unknown"}}}, Rules: []bm.LifecycleRule{{ID: fixedID(23), From: fixedID(21), To: fixedID(20), Trigger: bm.DiagramRef{Kind: "record", RecordType: "node", ID: ids["handler"]}, Verdict: "forbidden", Origin: origin}}, Coverage: "partial", CoverageOrigin: origin}}
	business := bm.DiagramDocument{Format: bm.DiagramDocumentVersion, Kind: "business_map", Target: target, BusinessMap: &bm.BusinessMapPayload{Architecture: &c.Pin, Elements: []bm.BusinessElement{{ID: fixedID(30), Label: "Create order", Role: "command", Responsibility: "Accept order", ArchitectureElementID: fixedID(1), Origin: origin, Refs: []bm.DiagramRef{artifactRef}}, {ID: fixedID(31), Label: "Order created", Role: "business_event", Responsibility: "Business fact", Origin: origin, Refs: []bm.DiagramRef{}}}, Links: []bm.BusinessLink{{ID: fixedID(32), From: fixedID(30), To: fixedID(31), Label: "produces", Relation: "produces", Origin: origin, Refs: []bm.DiagramRef{}}}}}
	diagrams := []bm.DiagramVersion{*c, *create("Dora", "interactions", interactions), *create("Eve", "lifecycle", lifecycle), *create("Frank", "business", business)}
	views := []bm.DiagramView{}
	for i, d := range diagrams {
		state := bm.DiagramViewState{Diagram: d.Pin, Origin: "all", Positions: []bm.DiagramPosition{}, CollapsedIDs: []string{}}
		if d.Document.Kind == "architecture" {
			state.Level = "containers"
			state.RootID = fixedID(1)
		}
		v, err := models.CreateDiagramView(t.Context(), p.ID, bm.DiagramCreateViewInput{Name: d.Document.Kind, State: state, IdempotencyKey: fmt.Sprint("view", i)})
		check(t, err)
		views = append(views, *v)
		if i == 0 {
			state.Positions = []bm.DiagramPosition{{ID: fixedID(2), X: 125, Y: 250}}
			next, err := models.SaveDiagramView(t.Context(), p.ID, v.ID, bm.DiagramSaveViewInput{Name: "Architecture layout v2", State: state, ExpectedVersion: 1, IdempotencyKey: "view2"})
			check(t, err)
			views = append(views, *next)
		}
	}
	graph, err := models.ResolveEffectiveGraph(t.Context(), p.ID, target)
	check(t, err)
	selection := Selection{ProjectID: p.ID, Target: target, TargetHash: graph.Pins.TargetHash, DiagramViews: []SVGInput{}}
	for _, v := range views {
		selection.DiagramViews = append(selection.DiagramViews, SVGInput{ViewID: v.ID, ViewVersion: v.Version})
	}
	installation, err := models.InstallationID(t.Context())
	check(t, err)
	return &roundtripFixture{service: s, artifacts: artifacts, selection: selection, views: views, diagrams: diagrams, sourceIDs: ids, pin: bm.NamespacedArtifactPin{Namespace: bm.ArtifactNamespace{Scope: "local", InstallationID: installation}, Pin: page.Items[0].Locator.Pin}}
}

func stageRoundtrip(t *testing.T, f *roundtripFixture) (*ExportResult, *Session) {
	t.Helper()
	export, err := f.service.Export(t.Context(), ExportInput{Selection: f.selection, IdempotencyKey: "export"})
	check(t, err)
	session, err := f.service.Begin(t.Context(), BeginInput{Manifest: export.Manifest, IdempotencyKey: "begin-portable"})
	check(t, err)
	for i := range export.Manifest.Chunks {
		body, err := f.service.ExportChunk(t.Context(), export.Session.ID, export.Session.ManifestHash, i)
		check(t, err)
		session, err = f.service.Put(t.Context(), session.ID, PutInput{ExpectedVersion: session.Version, Index: i, Body: string(body), IdempotencyKey: fmt.Sprint("put", i)})
		check(t, err)
	}
	return export, session
}

func TestDiagramPortableSemanticRoundtripAtomicReplay(t *testing.T) {
	t.Parallel()
	f := makeRoundtripFixture(t)
	_, session := stageRoundtrip(t, f)
	preview, err := f.service.Preview(t.Context(), session.ID, PreviewInput{ExpectedVersion: session.Version, Name: "Imported", ArtifactMappings: []bm.PortableArtifactMapping{}, IdempotencyKey: "preview"})
	check(t, err)
	if len(preview.Unresolved) != 1 || preview.Unresolved[0].Namespace.Scope != "foreign" {
		t.Fatal("numeric owner collision implicitly resolved")
	}
	if _, err := f.service.models.Get(t.Context(), preview.ProjectID); err == nil {
		t.Fatal("preview published a project")
	}
	in := CommitInput{ExpectedVersion: preview.Session.Version, CandidateHash: preview.CandidateHash, IdempotencyKey: "commit-portable"}
	for _, phase := range []string{"after_architecture", "before_views"} {
		sentinel := errors.New(phase)
		f.service.afterStage = func(at string) error {
			if at == phase {
				return sentinel
			}
			return nil
		}
		if _, err := f.service.Commit(t.Context(), session.ID, in); !errors.Is(err, sentinel) {
			t.Fatal(phase, err)
		}
		if _, err := f.service.models.Get(t.Context(), preview.ProjectID); err == nil {
			t.Fatal("partial project after", phase)
		}
	}
	f.service.afterStage = nil
	committed, err := f.service.Commit(t.Context(), session.ID, in)
	check(t, err)
	replay, err := f.service.Commit(t.Context(), session.ID, in)
	check(t, err)
	a, _ := json.Marshal(committed)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatal("commit replay changed IDs or bytes")
	}
	assertRoundtripSemantics(t, f, committed)
	// nil owner readers make any accidental foreign numeric resolution fail.
	reader := bm.NewArtifactService(f.service.models, nil, nil)
	namespace := f.pin.Namespace
	namespace.Scope = "foreign"
	page, err := reader.QueryNamespaced(t.Context(), committed.Project.ID, bm.NamespacedArtifactQueryInput{Target: committed.Target, TargetHash: committed.TargetHash, Namespace: namespace, Artifact: bm.ArtifactKey{Kind: f.pin.Pin.Kind, ID: f.pin.Pin.ID}, View: "sequence", Limit: 100})
	check(t, err)
	if page.Status != "foreign_unresolved" || page.Projection != nil {
		t.Fatal("foreign read reached owner")
	}

}

func assertRoundtripSemantics(t *testing.T, f *roundtripFixture, result *CommitResult) {
	t.Helper()
	lookup := func(kind, id, parent string) string {
		t.Helper()
		for _, entry := range result.IDMap {
			if entry.Origin.Kind == kind && entry.Origin.ID == id && (parent == "" && entry.Parent == nil || entry.Parent != nil && entry.Parent.ID == parent) {
				return entry.Local.ID
			}
		}
		t.Fatalf("missing %s %s in map", kind, id)
		return ""
	}
	for _, original := range f.views {
		id := lookup("diagram_view_version", original.ID, "")
		view, err := f.service.models.GetDiagramView(t.Context(), result.Project.ID, id, original.Version)
		check(t, err)
		diagram, err := f.service.models.GetDiagram(t.Context(), result.Project.ID, view.State.Diagram)
		check(t, err)
		if diagram.ImportOrigin == nil || diagram.ImportOrigin.InstallationID != f.pin.Namespace.InstallationID {
			t.Fatal("missing imported attribution")
		}
		if original.Version == 2 {
			if len(view.State.Positions) != 1 || view.State.Positions[0].X != 125 || view.State.Positions[0].Y != 250 {
				t.Fatal("saved geometry/version lost")
			}
		}
		switch diagram.Document.Kind {
		case "architecture":
			if len(diagram.Document.Payload.Elements) != 2 {
				t.Fatal("architecture membership")
			}
			labels := map[string]bool{}
			for _, e := range diagram.Document.Payload.Elements {
				labels[e.Label] = true
			}
			if !labels["Orders X"] || !labels["Worker Y changed"] {
				t.Fatal(labels)
			}
			for _, p := range diagram.Provenance.Elements {
				if p.ElementID == lookup("diagram_element", fixedID(1), f.diagrams[0].Pin.ID) {
					if p.Introduced.Author != "Alice" || p.LastEdited.Author != "Alice" {
						t.Fatal("X authorship changed", p)
					}
				}
			}
			if diagram.Provenance.Previous == nil {
				t.Fatal("history dropped")
			}
			previous, err := f.service.models.GetDiagram(t.Context(), result.Project.ID, *diagram.Provenance.Previous)
			check(t, err)
			if previous.Provenance.Fork == nil || previous.Provenance.Fork.Reason != "Review fork" {
				t.Fatal("fork provenance lost")
			}
		case "interactions":
			p := diagram.Document.Interactions
			if p.Architecture == nil || len(p.Steps) != 2 || len(p.Order) != 1 || p.Steps[0].Kind == "send" {
				t.Fatal("interaction semantics")
			}
			byLabel := map[string]bm.InteractionStep{}
			for _, step := range p.Steps {
				byLabel[step.Label] = step
			}
			if byLabel["Reply"].ReplyTo != byLabel["Create"].ID {
				t.Fatal("replyTo lost")
			}
		case "lifecycle":
			p := diagram.Document.Lifecycle
			if len(p.States) != 2 || p.Transitions[0].Guard.Text != "external approval is unknown" || p.Rules[0].Verdict != "forbidden" || p.Coverage != "partial" {
				t.Fatal("lifecycle intent changed")
			}
		case "business_map":
			p := diagram.Document.BusinessMap
			if p.Architecture == nil || len(p.Elements) != 2 || p.Links[0].Relation != "produces" {
				t.Fatal("business semantics changed")
			}
		default:
			t.Fatal("unexpected diagram kind")
		}
		raw, _ := json.Marshal(diagram.Document)
		if !strings.Contains(string(raw), `"scope":"foreign"`) {
			t.Fatal("foreign reference vanished", diagram.Document.Kind)
		}
	}
}

func TestDiagramPortableExplicitMappingAndStaleCandidate(t *testing.T) {
	t.Parallel()
	f := makeRoundtripFixture(t)
	_, session := stageRoundtrip(t, f)
	bad := f.pin
	bad.Pin.ContentHash = strings.Repeat("b", 64)
	if _, err := f.service.Preview(t.Context(), session.ID, PreviewInput{ExpectedVersion: session.Version, ArtifactMappings: []bm.PortableArtifactMapping{{Origin: f.pin, Local: bad}}, IdempotencyKey: "bad-mapping"}); err == nil {
		t.Fatal("forged local owner hash accepted")
	}
	preview, err := f.service.Preview(t.Context(), session.ID, PreviewInput{ExpectedVersion: session.Version, ArtifactMappings: []bm.PortableArtifactMapping{{Origin: f.pin, Local: f.pin}}, IdempotencyKey: "mapped-preview"})
	check(t, err)
	if len(preview.Unresolved) != 0 {
		t.Fatal("explicit exact local mapping remains unresolved")
	}
	if _, err := f.service.Commit(t.Context(), session.ID, CommitInput{ExpectedVersion: preview.Session.Version, CandidateHash: strings.Repeat("b", 64), IdempotencyKey: "wrong-candidate"}); err == nil {
		t.Fatal("wrong candidate accepted")
	}
	committed, err := f.service.Commit(t.Context(), session.ID, CommitInput{ExpectedVersion: preview.Session.Version, CandidateHash: preview.CandidateHash, IdempotencyKey: "mapped-commit"})
	check(t, err)

	page, err := f.artifacts.QueryNamespaced(t.Context(), committed.Project.ID, bm.NamespacedArtifactQueryInput{Target: committed.Target, TargetHash: committed.TargetHash, Namespace: f.pin.Namespace, Artifact: bm.ArtifactKey{Kind: f.pin.Pin.Kind, ID: f.pin.Pin.ID}, View: "sequence", Limit: 100})
	check(t, err)
	if page.Status != "local_resolved" || page.Projection == nil || len(page.Projection.Items) == 0 {
		t.Fatal("explicit local reader lost projection")
	}
	for _, entry := range committed.IDMap {
		if entry.Origin.Kind != "diagram_version" {
			continue
		}
		version, err := strconv.ParseInt(entry.Local.Version, 10, 64)
		check(t, err)
		// Read through ordinary exact version APIs; the stored hash is recomputed
		// from local document identities, not copied from the export.
		var raw string
		check(t, f.service.db.R.QueryRow(`SELECT document FROM backend_diagram_versions_documents WHERE project_id=? AND diagram_id=? AND version=?`, committed.Project.ID, entry.Local.ID, version).Scan(&raw))
		var diagram bm.DiagramVersion
		check(t, json.Unmarshal([]byte(raw), &diagram))
		_, err = f.service.models.GetDiagram(t.Context(), committed.Project.ID, diagram.Pin)
		check(t, err)
		document, _ := json.Marshal(diagram.Document)
		if !strings.Contains(string(document), `"scope":"local"`) {
			t.Fatal("explicit local ref missing")
		}
	}
}

func TestPortableReexportMatchesCommittedLocalHashes(t *testing.T) {
	t.Parallel()
	f := makeRoundtripFixture(t)
	p, err := f.service.models.Get(t.Context(), f.selection.ProjectID)
	check(t, err)
	_, err = f.service.models.ApplyAs(t.Context(), p.ID, bm.CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "annotation", Commands: []bm.Command{{Type: "create_annotation", AnnotationID: fixedID(900), Target: &bm.AnnotationTarget{RecordType: "node", ID: f.sourceIDs["entity"]}, Body: "Preserved authorship"}}}, "Alice")
	check(t, err)
	_, session := stageRoundtrip(t, f)
	preview, err := f.service.Preview(t.Context(), session.ID, PreviewInput{ExpectedVersion: session.Version, ArtifactMappings: []bm.PortableArtifactMapping{}, IdempotencyKey: "preview"})
	check(t, err)
	committed, err := f.service.Commit(t.Context(), session.ID, CommitInput{ExpectedVersion: preview.Session.Version, CandidateHash: preview.CandidateHash, IdempotencyKey: "commit"})
	check(t, err)
	selection := Selection{ProjectID: committed.Project.ID, Target: committed.Target, TargetHash: committed.TargetHash, DiagramViews: []SVGInput{}}
	expected := map[string]string{}
	for _, entry := range committed.IDMap {
		if entry.LocalHash != "" {
			key, err := mapIdentityKey(entry.Local, entry.LocalParent)
			check(t, err)
			expected[key] = entry.LocalHash
		}
		if entry.Origin.Kind == "diagram_view_version" {
			v, err := strconv.ParseInt(entry.Local.Version, 10, 64)
			check(t, err)
			selection.DiagramViews = append(selection.DiagramViews, SVGInput{ViewID: entry.Local.ID, ViewVersion: v})
		}
	}
	exported, err := f.service.Export(t.Context(), ExportInput{Selection: selection, IdempotencyKey: "reexport"})
	check(t, err)
	for i, d := range exported.Manifest.Chunks {
		body, err := f.service.ExportChunk(t.Context(), exported.Session.ID, exported.Session.ManifestHash, i)
		check(t, err)
		records, err := DecodeChunk(d, body)
		check(t, err)
		for _, record := range records {
			key, err := record.key()
			check(t, err)
			var parent *Identity
			if key.Parent.ID != "" {
				parent = &key.Parent
			}
			identity, err := mapIdentityKey(record.Identity, parent)
			check(t, err)
			if expected[identity] != record.ContentHash {
				t.Fatalf("persisted %s differs from LocalHash: %s != %s", record.Kind, record.ContentHash, expected[identity])
			}
		}
	}
	graph, err := f.service.models.ResolveEffectiveGraph(t.Context(), committed.Project.ID, committed.Target)
	check(t, err)
	if len(graph.Criteria) != 2 {
		t.Fatal("criteria lost")
	}
	for _, c := range graph.Criteria {
		if c.Key == "scenario" && (c.Attachment.Kind != "artifact_v3" || c.Attachment.NamespacedArtifact.Namespace.Scope != "foreign") {
			t.Fatal("attachment rebound by numeric collision")
		}
		if c.Key == "source" && c.Attachment.RevisionID == f.selection.Target.RevisionID {
			t.Fatal("source attachment was not remapped")
		}
	}
}

// review 2026-10-06, F75: the imported project is created at Commit, not at
// Preview. Preview used to stamp createdAt/updatedAt into the prepared input
// and Commit inserted them verbatim, so a project reviewed for a day was
// dated before its own receipt, id maps and origins.
func TestPortableCommitStampsProjectAtCommitTime(t *testing.T) {
	t.Parallel()
	f := makeRoundtripFixture(t)
	_, session := stageRoundtrip(t, f)
	preview, err := f.service.Preview(t.Context(), session.ID, PreviewInput{ExpectedVersion: session.Version, ArtifactMappings: []bm.PortableArtifactMapping{}, IdempotencyKey: "preview"})
	check(t, err)
	afterPreview := time.Now().UTC()
	committed, err := f.service.Commit(t.Context(), session.ID, CommitInput{ExpectedVersion: preview.Session.Version, CandidateHash: preview.CandidateHash, IdempotencyKey: "commit"})
	check(t, err)
	if committed.Project.CreatedAt.Before(afterPreview) || !committed.Project.UpdatedAt.Equal(committed.Project.CreatedAt) {
		t.Fatal("project stamped at Preview time", committed.Project.CreatedAt, afterPreview)
	}
	stored, err := f.service.models.Get(t.Context(), committed.Project.ID)
	check(t, err)
	if !stored.CreatedAt.Equal(committed.Project.CreatedAt) {
		t.Fatal("stored project time differs from the receipt", stored.CreatedAt, committed.Project.CreatedAt)
	}
}

// review 2026-10-06, F74: the guide says to export with the resolved
// selection unchanged, so the resolver must refuse what export refuses. A
// duplicated view pin used to resolve with 200 and fail only at export.
func TestPortableResolveSelectionRejectsWhatExportRejects(t *testing.T) {
	t.Parallel()
	f := makeRoundtripFixture(t)
	in := SelectionInput{Target: f.selection.Target, DiagramViews: f.selection.DiagramViews}
	if _, err := f.service.ResolveSelection(t.Context(), f.selection.ProjectID, in); err != nil {
		t.Fatal(err)
	}
	in.DiagramViews = append(slices.Clone(in.DiagramViews), in.DiagramViews[0])
	if _, err := f.service.ResolveSelection(t.Context(), f.selection.ProjectID, in); err == nil {
		t.Fatal("duplicate view pin resolved")
	}
}
