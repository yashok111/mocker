package backendmodel

import (
	"encoding/json/v2"
	"slices"
	"strconv"
	"testing"
)

// Review 2026-10-06, cluster C11: no-op writes and receipts outside quotas.

// F100: a save equal to the head returned before the quota check yet stored
// the whole version JSON as a fresh receipt per idempotency key, outside every
// cap and undeletable. Receipts now name the immutable version they answered.
func TestDiagramNoopSaveReceiptIsCompactAndReplaysExactBytes(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	p := createProject(t, r, "noop-receipts")
	doc := diagramTestDocument(p.CurrentRevisionID)
	v1, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(v1)
	in := DiagramSaveInput{ExpectedVersion: 1, Document: doc, IdempotencyKey: "noop-1"}
	noop, err := r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, in)
	if err != nil || noop.Pin != v1.Pin {
		t.Fatalf("noop: %+v %v", noop, err)
	}
	state := DiagramViewState{Diagram: v1.Pin, Level: "context", RootID: doc.Payload.PrimarySystemID, Origin: "all", Positions: []DiagramPosition{}, CollapsedIDs: []string{}}
	view, err := r.CreateDiagramView(t.Context(), p.ID, DiagramCreateViewInput{Name: "View", State: state, IdempotencyKey: "view"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.SaveDiagramView(t.Context(), p.ID, view.ID, DiagramSaveViewInput{Name: "View", State: state, ExpectedVersion: 1, IdempotencyKey: "view-noop"}); err != nil {
		t.Fatal(err)
	}
	var largest int
	if err = db.R.QueryRowContext(t.Context(), `SELECT max(length(CAST(receipt AS BLOB))) FROM backend_diagram_receipts WHERE project_id=?`, p.ID).Scan(&largest); err != nil {
		t.Fatal(err)
	}
	if largest > 256 {
		t.Fatalf("a receipt stores %d bytes; it must reference the immutable version, not copy it", largest)
	}
	for _, key := range []string{"create", "noop-1"} {
		var replay *DiagramVersion
		if key == "create" {
			replay, err = r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: key})
		} else {
			replay, err = r.SaveDiagram(t.Context(), p.ID, v1.Pin.ID, in)
		}
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := json.Marshal(replay); string(got) != string(original) {
			t.Fatalf("%s replay bytes changed", key)
		}
	}
	replayedView, err := r.SaveDiagramView(t.Context(), p.ID, view.ID, DiagramSaveViewInput{Name: "View", State: state, ExpectedVersion: 1, IdempotencyKey: "view-noop"})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(view)
	b, _ := json.Marshal(replayedView)
	if string(a) != string(b) {
		t.Fatal("view no-op replay bytes changed")
	}
}

// F185: an identical saved-view save appended a new immutable version and
// consumed the per-view version cap; diagram saves already answer a no-op with
// the current version.
func TestSavedViewIdenticalSaveKeepsVersion(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	base, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	view, err := r.CreateSavedView(t.Context(), base.Project.ID, savedDatabaseInput(base, ids, "create"))
	if err != nil {
		t.Fatal(err)
	}
	same, err := r.SaveSavedView(t.Context(), base.Project.ID, view.ID, SaveSavedViewInput{ExpectedVersion: view.Version, Name: view.Name, State: view.State, IdempotencyKey: "same"})
	if err != nil {
		t.Fatal(err)
	}
	if same.Version != view.Version {
		t.Fatalf("identical save made version %d", same.Version)
	}
	var versions int
	if err = r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_saved_view_versions_documents WHERE view_id=?`, view.ID).Scan(&versions); err != nil || versions != 1 {
		t.Fatalf("versions = %d %v", versions, err)
	}
	replay, err := r.SaveSavedView(t.Context(), base.Project.ID, view.ID, SaveSavedViewInput{ExpectedVersion: view.Version, Name: view.Name, State: view.State, IdempotencyKey: "same"})
	if err != nil || replay.Version != view.Version {
		t.Fatalf("no-op replay: %+v %v", replay, err)
	}
	renamed, err := r.SaveSavedView(t.Context(), base.Project.ID, view.ID, SaveSavedViewInput{ExpectedVersion: view.Version, Name: "Renamed", State: view.State, IdempotencyKey: "rename"})
	if err != nil || renamed.Version != view.Version+1 {
		t.Fatalf("real change: %+v %v", renamed, err)
	}
}

// F95: remove_api_pin of an artifact with no pin previewed as an applicable
// empty change, and Apply wrote a phantom revision.
func TestAPIPinRemoveOfUnpinnedArtifactBlocks(t *testing.T) {
	t.Parallel()
	s, base, _, api := apiPinFixture(t)
	in := PreviewAPIPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []APIPinCommand{{Type: "remove_api_pin", ArtifactID: strconv.FormatInt(api.Design.ID, 10), Reason: "Detach a typo"}}}
	preview, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CanApply || !slices.ContainsFunc(preview.Diagnostics, func(d APIArtifactDiagnostic) bool { return d.Code == "backend_api_pin_absent" }) {
		t.Fatalf("removal of an unpinned artifact is applicable: %+v", preview)
	}
}

// F61: the same hole for editor/API artifact groups.
func TestArtifactPinRemoveOfUnpinnedGroupBlocks(t *testing.T) {
	t.Parallel()
	s, base, _, scenario := artifactServiceFixture(t)
	in := PreviewArtifactPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []ArtifactPinCommand{{Type: "remove_artifact_pin", Artifact: ArtifactKey{"design_scenario", strconv.FormatInt(scenario.Scenario.ID, 10)}, Reason: "Remove a group never pinned"}}}
	preview, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CanApply || !slices.ContainsFunc(preview.Diagnostics, func(d ArtifactDiagnostic) bool { return d.Code == "backend_artifact_pin_absent" }) {
		t.Fatalf("removal of an unpinned group is applicable: %+v", preview)
	}
}
