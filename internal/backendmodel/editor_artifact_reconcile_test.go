package backendmodel

import (
	"encoding/json/v2"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/designscenario"
)

func TestArtifactCarryKeepsFrozenEditorsAndSourceAnchor(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	a, _ := applyArtifactTest(t, s, base.Project.ID, scenarioSet(base, ids, d), "scenario")
	state, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, a.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	session := &ImportSession{ProjectID: base.Project.ID, BaseRevisionID: a.Revision.ID, Version: 1, Inventory: state.Inventory}
	g := &graphCandidate{Nodes: []Node{}, Edges: []Edge{}, Evidence: []Evidence{}, Sources: state.Sources, Coverage: state.Revision.Coverage}
	anchor, err := candidateJSON(session, g)
	if err != nil {
		t.Fatal(err)
	}
	if err = carryAPIArtifactContext(t.Context(), session, state, g); err != nil {
		t.Fatal(err)
	}
	if g.ArtifactContext == nil || g.ArtifactContext.SourceSemanticHash != hashBytes(anchor) || len(g.ArtifactContext.EditorBindings) != 2 {
		t.Fatalf("carry lost frozen roster: %+v", g)
	}
	before, _ := json.Marshal(state.ArtifactContext.EditorBindings)
	after, _ := json.Marshal(g.ArtifactContext.EditorBindings)
	if string(before) != string(after) {
		t.Fatal("owner-free orphan carry changed labels/identities")
	}
	raw, err := candidateJSON(session, g)
	if err != nil || !strings.Contains(string(raw), `"editorBindings"`) {
		t.Fatal("candidate drops editor context", err)
	}
}
func TestArtifactComparisonManyToOneEmbeddedAndGroups(t *testing.T) {
	c := ArtifactContext{DocumentVersion: EditorArtifactDocumentVersion, SourceContentHash: strings.Repeat("b", 64), SourceSemanticHash: strings.Repeat("c", 64), APIBindings: []APIArtifactBinding{}, EditorBindings: []EditorBinding{}}
	pin := ArtifactPin{Kind: "design_scenario", ID: "1", RevisionID: "1", ContentHash: strings.Repeat("a", 64)}
	before := RevisionState{Revision: Revision{ArtifactPins: []ArtifactPin{pin}}, ArtifactContext: &c}
	afterContext := c
	for _, scope := range []string{"linked", "copy"} {
		afterContext.EditorBindings = append(afterContext.EditorBindings, EditorBinding{ArtifactKind: "design_scenario", ArtifactID: "1", Selector: EditorSelector{Kind: "state_transition", DiagramID: "d", TransitionID: "tr", EmbeddedContractID: scope}, SourceNodeIDs: []string{editorSourceID}, SourceLabels: []string{"Source"}, ObjectHash: strings.Repeat("f", 64), LastKnownLabel: "Transition", Origin: "manual", Reason: "Explicit"})
	}
	after := RevisionState{Revision: before.Revision, ArtifactContext: &afterContext}
	delta, err := CompareRevisionStates(t.Context(), before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Changes) != 2 || delta.Changes[0].ID == delta.Changes[1].ID || delta.Changes[0].After.EditorArtifact == nil || delta.Changes[1].After.EditorArtifact == nil {
		t.Fatalf("full typed identities collide: %+v", delta)
	}
	nextPin := pin
	nextPin.RevisionID = "2"
	nextPin.ContentHash = strings.Repeat("f", 64)
	after = RevisionState{Revision: Revision{ArtifactPins: []ArtifactPin{nextPin}}, ArtifactContext: &c}
	delta, err = CompareRevisionStates(t.Context(), before, after)
	if err != nil || len(delta.Changes) != 1 || delta.Changes[0].After.ArtifactGroup == nil || !delta.Changes[0].ContextChanged {
		t.Fatalf("projection-only pin omitted: %+v %v", delta, err)
	}
}
func TestArtifactLegacyWrappersGuardAndRetainV2(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	scenario, _ := applyArtifactTest(t, s, base.Project.ID, scenarioSet(base, ids, d), "scenario")
	owner := s.api.(*apidesign.Repo)
	api, err := owner.Create(t.Context(), apidesign.CreateInput{Name: "State API", Document: editorAPIDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	key := ArtifactKey{"api_design", strconv.FormatInt(api.Design.ID, 10)}
	in := PreviewArtifactPinsInput{BaseRevisionID: scenario.Revision.ID, ExpectedVersion: scenario.Project.Version, Commands: []ArtifactPinCommand{{Type: "set_artifact_pin", Artifact: key, RevisionID: strconv.FormatInt(api.Draft.ID, 10), APIBindings: []APIPinBindingInput{{SourceNodeID: ids["http"], Selector: APIArtifactSelector{ObjectKey: "op"}}}, EditorBindings: []EditorBindingInput{{Selector: EditorSelector{Kind: "state_diagram", DiagramID: "d"}, SourceNodeIDs: []string{ids["http"]}}}, Reason: "API and editor"}}}
	both, _ := applyArtifactTest(t, s, base.Project.ID, in, "both")
	legacy := NewAPIArtifactService(s.repo, s.api)
	remove := PreviewAPIPinsInput{BaseRevisionID: both.Revision.ID, ExpectedVersion: both.Project.Version, Commands: []APIPinCommand{{Type: "remove_api_pin", ArtifactID: key.ID, Reason: "Explicit"}}}
	blocked, err := legacy.Preview(t.Context(), base.Project.ID, remove)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.CanApply || !slices.ContainsFunc(blocked.Diagnostics, func(d APIArtifactDiagnostic) bool { return d.Code == "backend_api_editor_generic_required" }) {
		t.Fatalf("hidden removal allowed: %+v", blocked)
	}
	changed, err := owner.Save(t.Context(), api.Design.ID, apidesign.SaveInput{ExpectedVersion: api.Design.Version, Document: strings.Replace(editorAPIDocument, `"Lifecycle"`, `"Changed lifecycle"`, 1), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	set := PreviewAPIPinsInput{BaseRevisionID: both.Revision.ID, ExpectedVersion: both.Project.Version, Commands: []APIPinCommand{{Type: "set_api_pin", ArtifactID: key.ID, RevisionID: strconv.FormatInt(changed.Draft.ID, 10), Bindings: []APIPinBindingInput{{SourceNodeID: ids["http"], Selector: APIArtifactSelector{ObjectKey: "op"}}}, Reason: "Explicit"}}}
	blocked, err = legacy.Preview(t.Context(), base.Project.ID, set)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.CanApply {
		t.Fatal("legacy update invisibly changed editor object hash")
	}
	same, err := owner.Save(t.Context(), api.Design.ID, apidesign.SaveInput{ExpectedVersion: changed.Design.Version, Document: strings.Replace(editorAPIDocument, `"Historical API"`, `"Context only"`, 1), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	set.Commands[0].RevisionID = strconv.FormatInt(same.Draft.ID, 10)
	result, _ := applyPinTest(t, legacy, base.Project.ID, set, "allowed")
	state, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, result.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Revision.ArtifactPins) != 2 || len(state.ArtifactContext.EditorBindings) != 3 || len(state.ArtifactContext.APIBindings) != 1 {
		t.Fatal("legacy apply lost v2 context")
	}
	before, _ := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, both.Revision.ID)
	a, _ := json.Marshal(before.ArtifactContext.EditorBindings)
	b, _ := json.Marshal(state.ArtifactContext.EditorBindings)
	if string(a) != string(b) {
		t.Fatal("legacy changed frozen editor semantics/labels")
	}
}

func TestArtifactReimportCommitCarriesV2WithoutOwners(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	a, _ := applyArtifactTest(t, s, base.Project.ID, scenarioSet(base, ids, d), "scenario")
	input := lineageOrdersInput(t, &a.Project)
	input.Mode = "reconcile"
	input.RepositoryID = new(base.Project.Repositories[0].ID)
	input.GraphScope = &GraphScope{Profile: LineageProfile, Status: "partial", Gaps: []string{"Partial observation"}}
	input.IdempotencyKey = "v2-reimport"
	session, err := s.repo.BeginImport(t.Context(), base.Project.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	preview, _ := stageRelational(t, s.repo, &a.Project, session, lineageOrdersCommands(t, session), "v2-reimport")
	if preview.State != "ready" {
		t.Fatal(preview.Diagnostics)
	}
	s.api = nil
	s.scenarios = nil
	result, err := commitFixture(t, s.repo, &a.Project, session, preview, "v2-commit")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.Revision.ArtifactPins, result.Revision.ArtifactPins) {
		t.Fatal("reimport cleared scenario vector")
	}
	state, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, result.Revision.ID)
	if err != nil || len(state.ArtifactContext.EditorBindings) != 2 {
		t.Fatal("reimport cleared editor roster", err)
	}
	old, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, a.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(old.ArtifactContext.EditorBindings)
	after, _ := json.Marshal(state.ArtifactContext.EditorBindings)
	if string(before) != string(after) {
		t.Fatal("reimport changed frozen labels/associations")
	}
	removed := PreviewArtifactPinsInput{result.Revision.ID, result.Project.Version, []ArtifactPinCommand{{Type: "remove_artifact_pin", Artifact: artifactKey(result.Revision.ArtifactPins[0]), Reason: "Explicit"}}}
	cleared, _ := applyArtifactTest(t, s, base.Project.ID, removed, "v2-reimport-clear")
	if cleared.Revision.SemanticHash != state.ArtifactContext.SourceSemanticHash {
		t.Fatal("reimport lost original source candidate anchor")
	}
}

func TestArtifactOptionalLinkedOriginAndSeparateCopies(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	apiOwner := s.api.(*apidesign.Repo)
	api, err := apiOwner.Create(t.Context(), apidesign.CreateInput{Name: "Exact state API", Document: editorAPIDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := apiOwner.ArtifactSnapshot(t.Context(), api.Design.ID, api.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner := s.scenarios.(*designscenario.Repo)
	doc := d.Draft.Document
	doc.Contracts = []designscenario.Contract{{ID: "linked", Mode: "linked", Name: "Saved linked", Document: []byte(snapshot.IdentityDocument), Source: &designscenario.ContractSource{DesignID: snapshot.DesignID, RevisionID: snapshot.RevisionID, Version: snapshot.Version}}, {ID: "copy", Mode: "copy", Name: "Independent copy", Document: []byte(strings.Replace(snapshot.IdentityDocument, "Lifecycle", "Independent lifecycle", 1)), Source: &designscenario.ContractSource{DesignID: snapshot.DesignID, RevisionID: snapshot.RevisionID, Version: snapshot.Version}}}
	saved, err := owner.Save(t.Context(), d.Scenario.ID, designscenario.SaveInput{ExpectedVersion: d.Scenario.Version, Document: doc, FormDrafts: map[string]string{"copy": "inert draft"}, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	in := scenarioSet(base, ids, saved)
	in.Commands[0].EditorBindings = []EditorBindingInput{}
	for _, scope := range []string{"linked", "copy"} {
		in.Commands[0].EditorBindings = append(in.Commands[0].EditorBindings, EditorBindingInput{Selector: EditorSelector{Kind: "state_diagram", DiagramID: "d", EmbeddedContractID: scope}, SourceNodeIDs: []string{ids["http"]}})
	}
	// An unavailable optional origin must not discard the saved embedded content.
	s.api = nil
	p, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil || !p.CanApply || len(p.EditorBindings) != 2 {
		t.Fatalf("optional origin prevented explicit authored pin: %+v %v", p, err)
	}
	if p.EditorBindings[0].ObjectHash == p.EditorBindings[1].ObjectHash || !slices.ContainsFunc(p.Diagnostics, func(d ArtifactDiagnostic) bool { return strings.HasPrefix(d.Code, "linked_origin_") }) {
		t.Fatal("copy/linked authored hashes or qualification lost")
	}
	result, _ := applyArtifactTest(t, s, base.Project.ID, in, "linked-copy")
	page, err := s.Query(t.Context(), base.Project.ID, ArtifactQueryInput{RevisionID: result.Revision.ID, Artifact: artifactKey(result.Revision.ArtifactPins[0]), View: "states", EmbeddedContractID: "linked"})
	if err != nil || len(page.Items) == 0 || !page.BindingsComplete {
		t.Fatal("saved linked projection unavailable", err)
	}
}

func TestArtifactReceiptScopesAreIndependent(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	legacy := NewAPIArtifactService(s.repo, s.api)
	pin, err := NewEditorArtifactRequest(t.Context(), s.api, nil).SnapshotPin(ArtifactKey{"api_design", "1"}, "1")
	if err != nil {
		t.Fatal(err)
	}
	legacyIn := PreviewAPIPinsInput{base.Revision.ID, base.Project.Version, []APIPinCommand{{Type: "set_api_pin", ArtifactID: pin.ID, RevisionID: pin.RevisionID, Bindings: []APIPinBindingInput{{SourceNodeID: ids["http"], Selector: APIArtifactSelector{ObjectKey: "orders-read"}}}, Reason: "Explicit"}}}
	pinned, _ := applyPinTest(t, legacy, base.Project.ID, legacyIn, "shared-key")
	in := scenarioSet(base, ids, d)
	in.BaseRevisionID = pinned.Revision.ID
	in.ExpectedVersion = pinned.Project.Version
	result, _ := applyArtifactTest(t, s, base.Project.ID, in, "shared-key")
	if result.Project.Version != pinned.Project.Version+1 || len(result.Revision.ArtifactPins) != 2 {
		t.Fatal("old API receipt collided with generic scope")
	}
}
