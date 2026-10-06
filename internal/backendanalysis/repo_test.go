package backendanalysis

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
	"github.com/yashok111/mocker/internal/testleak"
)

func TestMain(m *testing.M) { testleak.VerifyTestMain(m) }

const projectID = "33333333-3333-4333-8333-333333333333"
const revisionID = "11111111-1111-4111-8111-111111111111"

func testRepo(t *testing.T) (*Repo, *store.DB) {
	t.Helper()
	db := testkit.NewDB(t)
	err := db.Write(t.Context(), func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(t.Context(), `INSERT INTO backend_projects VALUES (?,?,1,?,?,?)`, projectID, "Analysis", revisionID, "now", "now"); e != nil {
			return e
		}
		_, e := testkit.ExecBackendOwner(t.Context(), tx, `INSERT INTO backend_revisions VALUES (?,?,?)`, revisionID, projectID, `{}`)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewRepo(db), db
}
func testPrepared(t *testing.T, key string) PreparedStart {
	t.Helper()
	in := ImmutableInput{DocumentVersion: "backend-analysis-input/v1", ProjectID: projectID, Kind: "impact", From: backendmodel.BackendReadTarget{RevisionID: revisionID}, To: &backendmodel.BackendReadTarget{RevisionID: revisionID}, Limits: defaultLimits(), ObservationMode: "none"}
	raw, err := canonical(in)
	if err != nil {
		t.Fatal(err)
	}
	return PreparedStart{ProjectID: projectID, InputJSON: raw, InputHash: digest(raw), RequestHash: digest([]byte(key)), Key: key, OutputReservation: 1 << 20}
}
func mustStart(t *testing.T, r *Repo, key string) *Job {
	t.Helper()
	j, err := r.Start(t.Context(), testPrepared(t, key), nil)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func requireStatus(t *testing.T, err error, status int) {
	t.Helper()
	var f *backendmodel.FaultError
	if !errors.As(err, &f) || f.Status != status {
		t.Fatalf("want %d, got %v", status, err)
	}
}
func TestAnalysisReceiptReplayBeforeAdmission(t *testing.T) {
	r, _ := testRepo(t)
	p := testPrepared(t, "start")
	j, err := r.Start(t.Context(), p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Claim(t.Context(), "token"); err != nil {
		t.Fatal(err)
	}
	j2, err := r.Start(t.Context(), p, func(context.Context, *sql.Tx) error { t.Fatal("replay ran admission"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(j)
	b, _ := json.Marshal(j2)
	if string(a) != string(b) {
		t.Fatal("replay changed original response")
	}
	p.RequestHash = "different"
	_, err = r.Start(t.Context(), p, nil)
	requireStatus(t, err, 409)
}
func TestAnalysisAtomicAdmission(t *testing.T) {
	r, db := testRepo(t)
	sentinel := errors.New("reject admission")
	_, err := r.Start(t.Context(), testPrepared(t, "fail"), func(context.Context, *sql.Tx) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	for _, table := range []string{"backend_analysis_inputs", "backend_analysis_jobs", "backend_analysis_receipts"} {
		var n int
		if err = db.R.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s %d %v", table, n, err)
		}
	}
}
func TestAnalysisQuotaWaitingAndRunning(t *testing.T) {
	r, _ := testRepo(t)
	first := mustStart(t, r, "first")
	for i := 1; i < 20; i++ {
		mustStart(t, r, string(rune('a'+i)))
	}
	_, err := r.Start(t.Context(), testPrepared(t, "overflow"), nil)
	requireStatus(t, err, 429)
	if _, err = r.Start(t.Context(), testPrepared(t, "first"), nil); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"a", "b"} {
		c, e := r.Claim(t.Context(), token)
		if e != nil || c == nil {
			t.Fatalf("claim %v %v", c, e)
		}
	}
	if c, e := r.Claim(t.Context(), "third"); e != nil || c != nil {
		t.Fatalf("third slot %v %v", c, e)
	}
	if first.Status != "queued" {
		t.Fatal(first)
	}
}

func TestAnalysisQuotaBytesAndJobCount(t *testing.T) {
	t.Run("bytes", func(t *testing.T) {
		r, db := testRepo(t)
		p := testPrepared(t, "first")
		p.OutputReservation = maxResultBytes
		j, err := r.Start(t.Context(), p, nil)
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < 7; i++ {
			p.Key = fmt.Sprint(i)
			p.RequestHash = digest([]byte(p.Key))
			if _, err = r.Start(t.Context(), p, nil); err != nil {
				t.Fatal(err)
			}
		}
		p.Key = "eighth"
		p.RequestHash = digest([]byte(p.Key))
		_, err = r.Start(t.Context(), p, nil)
		requireStatus(t, err, 409)
		if _, err = r.Cancel(t.Context(), projectID, j.ID, CancelInput{IdempotencyKey: "release"}); err != nil {
			t.Fatal(err)
		}
		if _, err = r.Start(t.Context(), p, nil); err != nil {
			t.Fatal("terminal reservation not released", err)
		}
		var retained, reserved int64
		if err = db.R.QueryRowContext(t.Context(), `SELECT result_bytes,reserved_output_bytes FROM backend_analysis_jobs WHERE id=?`, j.ID).Scan(&retained, &reserved); err != nil || retained == 0 || reserved != 0 {
			t.Fatalf("retained=%d reserved=%d %v", retained, reserved, err)
		}
	})
	t.Run("jobs", func(t *testing.T) {
		r, db := testRepo(t)
		j := mustStart(t, r, "first")
		if _, err := r.Cancel(t.Context(), projectID, j.ID, CancelInput{IdempotencyKey: "cancel"}); err != nil {
			t.Fatal(err)
		}
		err := db.Write(t.Context(), func(tx *sql.Tx) error {
			for i := 1; i < 1000; i++ {
				_, e := tx.ExecContext(t.Context(), `INSERT INTO backend_analysis_jobs(id,project_id,input_hash,kind,status,version,created_at,updated_at) VALUES(?,?,?,'impact','interrupted',1,?,?)`, fmt.Sprintf("retained-%d", i), projectID, j.AnalysisInputHash, j.CreatedAt.Format(time.RFC3339Nano), j.CreatedAt.Format(time.RFC3339Nano))
				if e != nil {
					return e
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = r.Start(t.Context(), testPrepared(t, "1001"), nil)
		requireStatus(t, err, 409)
		if _, err = r.Start(t.Context(), testPrepared(t, "first"), nil); err != nil {
			t.Fatal("quota blocked receipt", err)
		}
	})
	t.Run("input", func(t *testing.T) {
		r, _ := testRepo(t)
		p := testPrepared(t, "large")
		p.InputJSON = bytes.Repeat([]byte(" "), maxInputBytes+1)
		p.InputHash = digest(p.InputJSON)
		_, err := r.Start(t.Context(), p, nil)
		requireStatus(t, err, 413)
	})
}
func TestAnalysisAtomicReceiptFailure(t *testing.T) {
	r, db := testRepo(t)
	if _, err := db.W.ExecContext(t.Context(), `CREATE TRIGGER analysis_test_receipt_fail BEFORE INSERT ON backend_analysis_receipts BEGIN SELECT RAISE(ABORT,'receipt fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Start(t.Context(), testPrepared(t, "fail"), nil); err == nil {
		t.Fatal("receipt fault ignored")
	}
	for _, table := range []string{"backend_analysis_jobs", "backend_analysis_inputs", "backend_analysis_receipts"} {
		var n int
		if err := db.R.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s leaked %d %v", table, n, err)
		}
	}
}

func TestAnalysisReceiptRestartReplay(t *testing.T) {
	r, db := testRepo(t)
	j := mustStart(t, r, "start")
	if _, err := r.Cancel(t.Context(), projectID, j.ID, CancelInput{IdempotencyKey: "cancel"}); err != nil {
		t.Fatal(err)
	}
	var sequence int
	var name, path string
	if err := db.R.QueryRowContext(t.Context(), `PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if err = reopened.Migrate(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	next := NewRepo(reopened)
	replay, err := next.Start(t.Context(), testPrepared(t, "start"), func(context.Context, *sql.Tx) error { t.Fatal("restarted replay ran admission"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(j)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatal("restart changed receipt")
	}
	current, err := next.Get(t.Context(), projectID, j.ID)
	if err != nil || current.Status != "cancelled" {
		t.Fatalf("current %v %v", current, err)
	}
	page, err := next.Results(t.Context(), projectID, j.ID, ResultQuery{ResultVersion: 1, Section: "changes"})
	if err != nil || page.Manifest.Complete || string(page.ItemsJSON) != "[]" {
		t.Fatalf("restart partial manifest %v %v", page, err)
	}
}
