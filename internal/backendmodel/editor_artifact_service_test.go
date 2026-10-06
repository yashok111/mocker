package backendmodel

import (
	"encoding/json/v2"
	"errors"
	"github.com/yashok111/mocker/internal/testkit"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
)

func artifactServiceFixture(t *testing.T) (*ArtifactService, *ImportCommitResult, map[string]string, *designscenario.Detail) {
	t.Helper()
	legacy, base, ids, _ := apiPinFixture(t)
	owner := designscenario.NewRepo(legacy.repo.db, &config.Config{MaxBody: 2 << 20}, legacy.artifacts.(*apidesign.Repo))
	doc := designscenario.Document{FormatVersion: 3, Title: "Frozen scenario", Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{}, Participants: []designscenario.Participant{{ID: "p", Name: "Service", Kind: "service"}, {ID: "c", Name: "Client", Kind: "client"}}, Messages: []designscenario.Message{{ID: "m", FromID: "c", ToID: "p", Kind: "request", Label: "Request"}}}
	detail, err := owner.Create(t.Context(), designscenario.CreateInput{Document: doc, FormDrafts: map[string]string{"draft": "inert unfinished form"}, Source: "ui"})
	if err != nil {
		if invalid, ok := errors.AsType[*designscenario.InvalidError](err); ok {
			t.Fatalf("scenario fixture: %+v", invalid.Diagnostics)
		}
		t.Fatal(err)
	}
	return NewArtifactService(legacy.repo, legacy.artifacts, owner), base, ids, detail
}

func scenarioSet(base *ImportCommitResult, ids map[string]string, d *designscenario.Detail) PreviewArtifactPinsInput {
	return PreviewArtifactPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []ArtifactPinCommand{{Type: "set_artifact_pin", Artifact: ArtifactKey{"design_scenario", strconv.FormatInt(d.Scenario.ID, 10)}, RevisionID: strconv.FormatInt(d.Draft.ID, 10), EditorBindings: []EditorBindingInput{{Selector: EditorSelector{Kind: "participant", ParticipantID: "p"}, SourceNodeIDs: []string{ids["http"]}}, {Selector: EditorSelector{Kind: "sequence_message", MessageID: "m"}, SourceNodeIDs: []string{ids["http"]}}}, Reason: "Explicit authored associations"}}}
}

func TestArtifactServicePreviewFullReplacement(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	in := scenarioSet(base, ids, d)
	before, _ := s.repo.Get(t.Context(), base.Project.ID)
	p, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if !p.CanApply || len(p.Pins) != 1 || len(p.EditorBindings) != 2 || len(p.APIBindings) != 0 {
		t.Fatalf("full vector: %+v", p)
	}
	for _, b := range p.EditorBindings {
		if b.Origin != "manual" || b.ObjectHash == "" || b.LastKnownLabel == "" || len(b.SourceLabels) != 1 || b.SourceLabels[0] == "" || b.Reason != in.Commands[0].Reason {
			t.Fatalf("server fields: %+v", b)
		}
	}
	if p.EditorBindings[0].SourceNodeIDs[0] != p.EditorBindings[1].SourceNodeIDs[0] {
		t.Fatal("many objects cannot share source")
	}
	again, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil || again.CandidateHash != p.CandidateHash {
		t.Fatalf("unstable preview: %v", err)
	}
	after, _ := s.repo.Get(t.Context(), base.Project.ID)
	if before.Version != after.Version || before.CurrentRevisionID != after.CurrentRevisionID {
		t.Fatal("preview wrote project")
	}
}

func TestArtifactDiscriminatorLoadAndQualifiedQuery(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	p, err := s.Preview(t.Context(), base.Project.ID, scenarioSet(base, ids, d))
	if err != nil {
		t.Fatal(err)
	}
	c := ArtifactContext{DocumentVersion: EditorArtifactDocumentVersion, SourceContentHash: strings.Repeat("b", 64), SourceSemanticHash: base.Revision.SemanticHash, APIBindings: p.APIBindings, EditorBindings: p.EditorBindings}
	doc, err := EncodeArtifactContext(c, p.Pins)
	if err != nil {
		t.Fatal(err)
	}
	base.Revision.ArtifactPins = p.Pins
	raw, _ := json.Marshal(base.Revision)
	if _, err = s.repo.db.W.ExecContext(t.Context(), `UPDATE backend_revisions SET document=? WHERE id=?`, string(raw), base.Revision.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = testkit.ExecBackendOwner(t.Context(), s.repo.db.W, `INSERT INTO backend_revision_api_artifacts(revision_id,source_content_hash,source_semantic_hash,document) VALUES(?,?,?,?)`, base.Revision.ID, c.SourceContentHash, c.SourceSemanticHash, string(doc)); err != nil {
		t.Fatal(err)
	}
	state, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ArtifactContext == nil || len(state.ArtifactContext.EditorBindings) != 2 {
		t.Fatal("v2 bindings lost during revision load")
	}
	page, err := s.Query(t.Context(), base.Project.ID, ArtifactQueryInput{RevisionID: base.Revision.ID, Artifact: ArtifactKey{"design_scenario", strconv.FormatInt(d.Scenario.ID, 10)}, View: "sequence", Limit: 1})
	if err != nil || !page.BindingsComplete || len(page.EditorBindings) != 2 || len(page.Items) != 1 {
		t.Fatalf("query: %+v %v", page, err)
	}
	unavailable := NewArtifactService(s.repo, nil, nil)
	page, err = unavailable.Query(t.Context(), base.Project.ID, ArtifactQueryInput{RevisionID: base.Revision.ID, Artifact: ArtifactKey{"design_scenario", strconv.FormatInt(d.Scenario.ID, 10)}, View: "sequence"})
	if err != nil || !page.BindingsComplete || len(page.EditorBindings) != 2 || page.Resolution.Status != "unavailable" {
		t.Fatalf("broken roster: %+v %v", page, err)
	}
	if _, err := s.repo.db.W.ExecContext(t.Context(), `DROP TRIGGER backend_revision_api_artifacts_immutable_update`); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(string(doc), EditorArtifactDocumentVersion, "future-v3", 1), strings.Replace(string(doc), `"documentVersion":"`+EditorArtifactDocumentVersion+`"`, `"documentVersion":null`, 1), strings.Replace(string(doc), c.SourceContentHash, strings.Repeat("a", 64), 1)} {
		if _, err := s.repo.db.W.ExecContext(t.Context(), `UPDATE backend_revision_api_artifacts SET document=? WHERE revision_id=?`, bad, base.Revision.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, base.Revision.ID); err == nil {
			t.Fatal("invalid discriminator or row anchor accepted")
		}
	}
}

func TestArtifactPersistentMixedVersionRejected(t *testing.T) {
	t.Parallel()
	s, base, ids, api := apiPinFixture(t)
	pinned, _ := applyPinTest(t, s, base.Project.ID, pinTestInput(base, ids, api), "v1")
	var original string
	if err := s.repo.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revision_api_artifacts_documents WHERE revision_id=?`, pinned.Revision.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if _, err := s.repo.db.W.ExecContext(t.Context(), `DROP TRIGGER backend_revision_api_artifacts_immutable_update`); err != nil {
		t.Fatal(err)
	}
	mixed := strings.TrimSuffix(original, "}") + `,"editorBindings":[]}`
	if _, err := s.repo.db.W.ExecContext(t.Context(), `UPDATE backend_revision_api_artifacts SET document=? WHERE revision_id=?`, mixed, pinned.Revision.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, pinned.Revision.ID); err == nil {
		t.Fatal("untagged row silently discarded editor context")
	}
}

func TestArtifactForeignOrMissingBaseIsNotFound(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	foreign, err := s.repo.Create(t.Context(), CreateInput{Name: "Foreign", IdempotencyKey: "foreign"})
	if err != nil {
		t.Fatal(err)
	}
	for _, rid := range []string{foreign.CurrentRevisionID, "90000000-0000-4000-8000-000000000001"} {
		in := scenarioSet(base, ids, d)
		in.BaseRevisionID = rid
		_, err = s.Preview(t.Context(), base.Project.ID, in)
		fault, ok := errors.AsType[*FaultError](err)
		if !ok || fault.Status != 404 {
			t.Fatalf("foreign/missing base classified as head conflict: %v", err)
		}
	}
}
