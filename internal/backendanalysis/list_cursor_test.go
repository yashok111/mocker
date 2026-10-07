package backendanalysis

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"
)

// review 2026-10-06, F142: the job list cursor was an OFFSET into the live
// status filter, so a job that left the filter between pages shifted every
// later row one place back and the next page skipped one. A keyset cursor
// (created_at, id of the last row returned) cannot skip or repeat.
func TestAnalysisJobListCursorSurvivesStatusChange(t *testing.T) {
	r, db := testRepo(t)
	seed := mustStart(t, r, "seed")
	base := time.Now().UTC().Add(time.Hour)
	err := db.Write(t.Context(), func(tx *sql.Tx) error {
		for i := range 5 {
			at := base.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano)
			if _, e := tx.ExecContext(t.Context(), `INSERT INTO backend_analysis_jobs(id,project_id,input_hash,kind,status,version,created_at,updated_at) VALUES(?,?,?,'impact','interrupted',1,?,?)`, fmt.Sprintf("job-%d", i), projectID, seed.AnalysisInputHash, at, at); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.List(t.Context(), projectID, ListQuery{Status: "interrupted", Limit: 2})
	if err != nil || len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page %+v %v", first, err)
	}
	// job-0 leaves the filter between pages (retention or any other row
	// change has the same effect on an offset).
	if err = db.Write(t.Context(), func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(t.Context(), `DROP TRIGGER IF EXISTS backend_analysis_jobs_terminal_immutable`); e != nil {
			return e
		}
		_, e := tx.ExecContext(t.Context(), `DELETE FROM backend_analysis_jobs WHERE id='job-0'`)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	second, err := r.List(t.Context(), projectID, ListQuery{Status: "interrupted", Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(first.Items)+len(second.Items))
	for _, j := range append(first.Items, second.Items...) {
		got = append(got, j.ID)
	}
	if want := []string{"job-0", "job-1", "job-2", "job-3"}; !slices.Equal(got, want) {
		t.Fatalf("pages %v, want %v (a row skipped or repeated)", got, want)
	}
	// A cursor stays bound to its query.
	if _, err = r.List(t.Context(), projectID, ListQuery{Status: "running", Limit: 2, Cursor: first.NextCursor}); err == nil {
		t.Fatal("cursor accepted under a different filter")
	}
}
