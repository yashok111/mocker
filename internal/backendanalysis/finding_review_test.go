package backendanalysis

import (
	"bytes"
	"database/sql"
	"encoding/json/v2"
	"strings"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func findingReport(t *testing.T, r *Repo, key, basis, status string) (*Job, *backendmodel.Finding) {
	t.Helper()
	p := testPrepared(t, key)
	var in ImmutableInput
	_ = json.Unmarshal(p.InputJSON, &in)
	in.Kind = "diagnostics"
	in.RuleSetVersion = "diagnostics-v1"
	p.InputJSON, _ = canonical(in)
	p.InputHash = digest(p.InputJSON)
	j, e := r.Start(t.Context(), p, nil)
	if e != nil {
		t.Fatal(e)
	}
	claim, e := r.Claim(t.Context(), "finding-token")
	if e != nil || claim == nil {
		t.Fatal(e)
	}
	f := backendmodel.Finding{Fingerprint: strings.Repeat("a", 64), BasisHash: basis, Rule: "unused_table", RuleVersion: "1", Certainty: "confirmed", Subjects: []backendmodel.ChangeRecordRef{}, EvidenceIDs: []string{}, Prerequisites: []string{}, Gaps: []string{}}
	b := newReport(&in)
	c := backendmodel.FindingCheck{Fingerprint: f.Fingerprint, ScopeKey: "same-scope", Status: status}
	b.add("checks", ObjectAddress{RecordType: "diagnostic", ID: f.Fingerprint}, "diagnostic_check", "unknown", 0, c)
	if status == "present" {
		b.add("findings", ObjectAddress{RecordType: "diagnostic", ID: f.Fingerprint}, f.Rule, f.Certainty, 0, f)
	}
	snapshot, e := b.snapshot(ResultManifest{Complete: true, ResultVersion: 1, AnalysisInputHash: p.InputHash})
	if e != nil {
		t.Fatal(e)
	}
	j, e = r.Finalize(t.Context(), projectID, j.ID, claim.Token, TerminalSnapshot{Status: "completed", Snapshot: snapshot})
	if e != nil {
		t.Fatal(e)
	}
	return j, &f
}
func TestFindingReviewReceiptRecurrenceAndResolution(t *testing.T) {
	r, db := testRepo(t)
	model := backendmodel.NewRepo(db)
	j, f := findingReport(t, r, "first", strings.Repeat("b", 64), "present")
	var original []byte
	if e := db.R.QueryRow(`SELECT document FROM backend_analysis_manifests_documents WHERE job_id=?`, j.ID).Scan(&original); e != nil {
		t.Fatal(e)
	}
	in := backendmodel.FindingReviewInput{ExpectedVersion: 1, BasisHash: f.BasisHash, Status: "accepted_risk", Reason: "Reviewed static risk", IdempotencyKey: "review"}
	reviewed, e := model.ReviewFinding(t.Context(), projectID, f.Fingerprint, in)
	if e != nil || reviewed.Version != 2 {
		t.Fatal(reviewed, e)
	}
	raw, _ := json.Marshal(reviewed)
	next, _ := findingReport(t, r, "next", strings.Repeat("c", 64), "present")
	page, e := model.ListBackendFindings(t.Context(), projectID, backendmodel.FindingAnalysisRef{JobID: next.ID, ResultVersion: 1}, "", 100)
	if e != nil || len(page.Items) != 1 || page.Items[0].Review.Status != "open" || page.Items[0].Review.Version != 3 {
		t.Fatal(page, e)
	}
	replay, e := model.ReviewFinding(t.Context(), projectID, f.Fingerprint, in)
	again, _ := json.Marshal(replay)
	if e != nil || !bytes.Equal(raw, again) {
		t.Fatal("receipt changed", e)
	}
	in.Reason = "different"
	_, e = model.ReviewFinding(t.Context(), projectID, f.Fingerprint, in)
	requireStatus(t, e, 409)
	incomplete, _ := findingReport(t, r, "incomplete", strings.Repeat("c", 64), "unknown")
	in = backendmodel.FindingReviewInput{ExpectedVersion: 3, BasisHash: strings.Repeat("c", 64), Status: "resolved", Reason: "Rechecked", IdempotencyKey: "resolve", ResolutionAnalysis: &backendmodel.FindingAnalysisRef{JobID: incomplete.ID, ResultVersion: 1}}
	_, e = model.ReviewFinding(t.Context(), projectID, f.Fingerprint, in)
	requireStatus(t, e, 409)
	absent, _ := findingReport(t, r, "absent", strings.Repeat("c", 64), "absent")
	in.ResolutionAnalysis.JobID = absent.ID
	resolved, e := model.ReviewFinding(t.Context(), projectID, f.Fingerprint, in)
	if e != nil || resolved.Status != "resolved" {
		t.Fatal(resolved, e)
	}
	var after []byte
	_ = db.R.QueryRow(`SELECT document FROM backend_analysis_manifests_documents WHERE job_id=?`, j.ID).Scan(&after)
	if !bytes.Equal(original, after) {
		t.Fatal("immutable report changed")
	}
}
func TestFindingReviewRaceRollbackAndImmutableHistory(t *testing.T) {
	r, db := testRepo(t)
	model := backendmodel.NewRepo(db)
	_, f := findingReport(t, r, "first", strings.Repeat("b", 64), "present")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, key := range []string{"A", "B"} {
		wg.Go(func() {
			_, e := model.ReviewFinding(t.Context(), projectID, f.Fingerprint, backendmodel.FindingReviewInput{ExpectedVersion: 1, BasisHash: f.BasisHash, Status: "false_positive", Reason: "Human decision", IdempotencyKey: key})
			results <- e
		})
	}
	wg.Wait()
	close(results)
	passed := 0
	for e := range results {
		if e == nil {
			passed++
		} else {
			requireStatus(t, e, 409)
		}
	}
	if passed != 1 {
		t.Fatal(passed)
	}
	for _, q := range []string{`DELETE FROM backend_finding_events`, `UPDATE backend_finding_occurrences SET basis_hash='forged'`, `DELETE FROM backend_finding_receipts`} {
		if _, e := db.W.Exec(q); e == nil {
			t.Fatal("immutable write accepted", q)
		}
	}
	if _, e := db.W.Exec(`CREATE TRIGGER reject_finding_receipt BEFORE INSERT ON backend_finding_receipts BEGIN SELECT RAISE(ABORT,'injected'); END`); e != nil {
		t.Fatal(e)
	}
	_, e := model.ReviewFinding(t.Context(), projectID, f.Fingerprint, backendmodel.FindingReviewInput{ExpectedVersion: 2, BasisHash: f.BasisHash, Status: "open", Reason: "Rollback", IdempotencyKey: "fail"})
	if e == nil {
		t.Fatal("failure not injected")
	}
	var version int
	_ = db.R.QueryRow(`SELECT version FROM backend_finding_reviews`).Scan(&version)
	if version != 2 {
		t.Fatal("partial commit", version)
	}
	_ = db.Write(t.Context(), func(tx *sql.Tx) error { _, e := tx.Exec(`DROP TRIGGER reject_finding_receipt`); return e })
}
