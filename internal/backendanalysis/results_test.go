package backendanalysis

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func snapshot(t *testing.T, j *Job, version int64, items string) PreparedSnapshot {
	t.Helper()
	p, err := prepareSnapshot(PreparedSnapshot{Manifest: ResultManifest{JobID: j.ID, AnalysisInputHash: j.AnalysisInputHash, ResultVersion: version, Complete: false, Verdict: "unknown"}, Chunks: []ResultChunk{{JobID: j.ID, Section: "changes", Sequence: 1, ItemsJSON: []byte(items)}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestAnalysisManifestImmutableAtomic(t *testing.T) {
	r, db := testRepo(t)
	j := mustStart(t, r, "start")
	if _, err := r.Claim(t.Context(), "token"); err != nil {
		t.Fatal(err)
	}
	s := snapshot(t, j, 1, `[{"id":"a","kind":"column"},{"id":"b","kind":"column"}]`)
	if _, err := r.Publish(t.Context(), projectID, j.ID, "token", s); err != nil {
		t.Fatal(err)
	}
	before, err := r.Results(t.Context(), projectID, j.ID, ResultQuery{ResultVersion: 1, Section: "changes", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(before.ItemsJSON) != `[{"id":"a","kind":"column"}]` || before.NextCursor == "" {
		t.Fatalf("page %+v", before)
	}
	if _, err = r.Publish(t.Context(), projectID, j.ID, "token", s); err != nil {
		t.Fatal("ambiguous replay", err)
	}
	s.Manifest.ResultVersion = 2
	s.Chunks = append(s.Chunks, ResultChunk{JobID: j.ID, Section: "changes", Sequence: 2, ItemsJSON: []byte(`[{"id":"c"}]`)})
	// A genuine SQL fault after chunk insert must roll back both data and pointer.
	if _, err = db.W.ExecContext(t.Context(), `CREATE TRIGGER analysis_test_fail BEFORE INSERT ON backend_analysis_manifests WHEN NEW.result_version=2 BEGIN SELECT RAISE(ABORT,'manifest fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Publish(t.Context(), projectID, j.ID, "token", s); err == nil {
		t.Fatal("fault did not fail")
	}
	var n int
	if err = db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_analysis_chunks WHERE job_id=?`, j.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("orphan chunk %d %v", n, err)
	}
	if _, err = db.W.ExecContext(t.Context(), `DROP TRIGGER analysis_test_fail`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Publish(t.Context(), projectID, j.ID, "token", s); err != nil {
		t.Fatal(err)
	}
	after, err := r.Results(t.Context(), projectID, j.ID, ResultQuery{ResultVersion: 1, Section: "changes", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("old page moved")
	}
	s.Chunks[0].ItemsJSON = []byte(`[{"id":"changed"}]`)
	_, err = r.Publish(t.Context(), projectID, j.ID, "token", s)
	requireStatus(t, err, 409)
}
func TestAnalysisCursorBoundFilters(t *testing.T) {
	r, _ := testRepo(t)
	j := mustStart(t, r, "start")
	_, err := r.Results(t.Context(), projectID, j.ID, ResultQuery{ResultVersion: 1, Section: "changes"})
	requireStatus(t, err, 409)
	if _, err = r.Claim(t.Context(), "token"); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Publish(t.Context(), projectID, j.ID, "token", snapshot(t, j, 1, `[{"id":"a"},{"id":"b"}]`)); err != nil {
		t.Fatal(err)
	}
	q := ResultQuery{ResultVersion: 1, Section: "changes", Limit: 1}
	p, err := r.Results(t.Context(), projectID, j.ID, q)
	if err != nil {
		t.Fatal(err)
	}
	q.Cursor = p.NextCursor
	q.Kind = "other"
	_, err = r.Results(t.Context(), projectID, j.ID, q)
	requireStatus(t, err, 400)
	q.Kind = ""
	p, err = r.Results(t.Context(), projectID, j.ID, q)
	if err != nil || string(p.ItemsJSON) != `[{"id":"b"}]` {
		t.Fatalf("next %v %v", p, err)
	}
	q.Cursor = "tampered"
	_, err = r.Results(t.Context(), projectID, j.ID, q)
	requireStatus(t, err, 400)
	_, err = r.Results(t.Context(), "other", j.ID, ResultQuery{ResultVersion: 1, Section: "changes"})
	requireStatus(t, err, 404)
}
func TestAnalysisManifestHashGrouping(t *testing.T) {
	j := &Job{ID: "one", AnalysisInputHash: "input"}
	a := snapshot(t, j, 1, `[{"id":"a"},{"id":"b"}]`)
	b := a
	b.Manifest.JobID = "two"
	b.Manifest.ResultVersion = 7
	b.Chunks = []ResultChunk{{Sequence: 1, Section: "changes", ItemsJSON: []byte(`[{"id":"a"}]`)}, {Sequence: 2, Section: "changes", ItemsJSON: []byte(`[{"id":"b"}]`)}}
	b, err := prepareSnapshot(b)
	if err != nil {
		t.Fatal(err)
	}
	if a.Manifest.SemanticResultHash != b.Manifest.SemanticResultHash {
		t.Fatal("publication grouping changed semantic hash")
	}
}

func TestAnalysisManifestReserveSurvivesExhaustion(t *testing.T) {
	r, _ := testRepo(t)
	p := testPrepared(t, "start")
	p.OutputReservation = terminalHeadroom + 1024
	j, err := r.Start(t.Context(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Claim(t.Context(), "token"); err != nil {
		t.Fatal(err)
	}
	s := snapshot(t, j, 1, `[{"id":"a","detail":"`+strings.Repeat("x", 2048)+`"}]`)
	_, err = r.Publish(t.Context(), projectID, j.ID, "token", s)
	requireStatus(t, err, 413)
	cancelled, err := r.Cancel(t.Context(), projectID, j.ID, CancelInput{IdempotencyKey: "cancel"})
	if err != nil {
		t.Fatal("terminal reserve unavailable", err)
	}
	if cancelled.ResultVersion == nil || *cancelled.ResultVersion != 1 {
		t.Fatal(cancelled)
	}
	page, err := r.Results(t.Context(), projectID, j.ID, ResultQuery{ResultVersion: 1, Section: "changes"})
	if err != nil || string(page.ItemsJSON) != "[]" {
		t.Fatalf("unaccepted output leaked %v %v", page, err)
	}
}
func TestAnalysisManifestPointerFaultRollsBack(t *testing.T) {
	r, db := testRepo(t)
	j := mustStart(t, r, "start")
	if _, err := r.Claim(t.Context(), "token"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.ExecContext(t.Context(), `CREATE TRIGGER analysis_pointer_fail BEFORE UPDATE OF result_version ON backend_analysis_jobs BEGIN SELECT RAISE(ABORT,'pointer fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Publish(t.Context(), projectID, j.ID, "token", snapshot(t, j, 1, `[{"id":"a"}]`)); err == nil {
		t.Fatal("pointer fault ignored")
	}
	for _, table := range []string{"backend_analysis_chunks", "backend_analysis_manifests"} {
		var n int
		if err := db.R.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("orphan %s %d %v", table, n, err)
		}
	}
	current, err := r.Get(t.Context(), projectID, j.ID)
	if err != nil || current.ResultVersion != nil {
		t.Fatalf("pointer advanced %v %v", current, err)
	}
}
