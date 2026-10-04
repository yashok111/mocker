package backendmodel

import (
	"encoding/json/v2"
	"fmt"
	"testing"
)

func TestAnnotationDoesNotCreateSemanticRevision(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	p := createProject(t, r, "annotation-create")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	preview := previewFixture(t, r, p, s)
	committed, err := commitFixture(t, r, p, s, preview, "annotation-source")
	if err != nil {
		t.Fatal(err)
	}
	graph, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: committed.Revision.ID, RecordType: "nodes"})
	if err != nil || len(graph.Nodes) != 1 {
		t.Fatalf("graph: %+v %v", graph, err)
	}
	var input CommandsInput
	raw := fmt.Sprintf(`{"expectedVersion":%d,"idempotencyKey":"annotation-note","commands":[{"type":"create_annotation","annotationId":"0197aaf9-5555-7000-8000-000000000099","target":{"recordType":"node","id":%q},"body":"Owner confirmed this boundary"}]}`, committed.Project.Version, graph.Nodes[0].ID)
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	updated, err := r.Apply(t.Context(), p.ID, input)
	if err != nil {
		t.Fatalf("annotation command must succeed: %v", err)
	}
	if updated.Version != committed.Project.Version+1 || updated.CurrentRevisionID != committed.Revision.ID {
		t.Fatalf("metadata changed semantic head: %+v", updated)
	}
	after, err := r.Revision(t.Context(), p.ID, committed.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(committed.Revision)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("annotation changed source revision")
	}
	var body, author string
	if err := db.R.QueryRowContext(t.Context(), `SELECT body,author FROM backend_annotations WHERE project_id=?`, p.ID).Scan(&body, &author); err != nil {
		t.Fatal(err)
	}
	if body != "Owner confirmed this boundary" || author != "system" {
		t.Fatalf("note body/author: %q/%q", body, author)
	}
}

func annotationSemanticBytes(t *testing.T, r *Repo, pid string) string {
	t.Helper()
	queries := []string{
		`SELECT document FROM backend_revisions WHERE project_id=? ORDER BY id`,
		`SELECT document FROM backend_graph_records WHERE project_id=? ORDER BY revision_id,record_type,id`,
		`SELECT document FROM backend_revision_sources WHERE revision_id IN (SELECT id FROM backend_revisions WHERE project_id=?) ORDER BY revision_id`,
		`SELECT document FROM backend_revision_api_artifacts WHERE revision_id IN (SELECT id FROM backend_revisions WHERE project_id=?) ORDER BY revision_id`,
	}
	all := make([][]string, 0, len(queries))
	for _, query := range queries {
		all = append(all, annotationDocuments(t, r, query, pid))
	}
	data, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func annotationDocuments(t *testing.T, r *Repo, query string, args ...any) []string {
	t.Helper()
	rows, err := r.db.R.QueryContext(t.Context(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		out = append(out, raw)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAnnotationEdgeAndArtifactPinIsolation(t *testing.T) {
	t.Parallel()
	service, base, ids, api := apiPinFixture(t)
	pinned, _ := applyPinTest(t, service, base.Project.ID, pinTestInput(base, ids, api), "pin")
	r := service.repo
	graph, err := r.QueryGraph(t.Context(), base.Project.ID, GraphQueryInput{RevisionID: pinned.Revision.ID, RecordType: "edges"})
	if err != nil || len(graph.Edges) == 0 {
		t.Fatalf("edge graph: %+v %v", graph, err)
	}
	target := AnnotationTarget{RecordType: "edge", ID: graph.Edges[0].ID, RevisionID: pinned.Revision.ID}
	before := annotationSemanticBytes(t, r, base.Project.ID)
	ownersBefore := annotationDocuments(t, r, `SELECT document FROM api_design_revisions ORDER BY id`)
	pinChange := PreviewAPIPinsInput{BaseRevisionID: pinned.Revision.ID, ExpectedVersion: pinned.Project.Version, Commands: []APIPinCommand{{Type: "remove_api_pin", ArtifactID: pinned.Revision.ArtifactPins[0].ID, Reason: "Detach"}}}
	preview, err := service.Preview(t.Context(), base.Project.ID, pinChange)
	if err != nil || !preview.CanApply {
		t.Fatalf("pin preview: %+v %v", preview, err)
	}
	note := annotationCommand(target, "manual edge note")
	updated := annotationApply(t, r, &pinned.Project, "note", note)
	_, err = service.Apply(t.Context(), base.Project.ID, ApplyAPIPinsInput{BaseRevisionID: pinChange.BaseRevisionID, ExpectedVersion: pinChange.ExpectedVersion, Commands: pinChange.Commands, CandidateHash: preview.CandidateHash, IdempotencyKey: "stale-pin"})
	assertFault(t, err, "backend_version_conflict")
	page, err := r.ListAnnotations(t.Context(), base.Project.ID, AnnotationListInput{TargetID: target.ID, RecordType: "edge", RevisionID: target.RevisionID, Orphaned: new(false)})
	if err != nil || len(page.Items) != 1 || page.Items[0].Target != target || page.Items[0].TargetStatus != "current" {
		t.Fatalf("edge annotation %+v %v", page, err)
	}
	edit := note
	edit.Type = "update_annotation"
	edit.Body = "second opinion"
	updated = annotationApply(t, r, updated, "edit", edit)
	// A semantic no-op is still one accepted metadata batch.
	updated = annotationApply(t, r, updated, "same-body", edit)
	annotationApply(t, r, updated, "remove", Command{Type: "remove_annotation", AnnotationID: note.AnnotationID})
	if annotationSemanticBytes(t, r, base.Project.ID) != before {
		t.Fatal("annotations changed source or artifact context")
	}
	ownersAfter := annotationDocuments(t, r, `SELECT document FROM api_design_revisions ORDER BY id`)
	a, _ := json.Marshal(ownersBefore)
	b, _ := json.Marshal(ownersAfter)
	if string(a) != string(b) {
		t.Fatal("annotations changed artifact owner documents")
	}
}
