package designscenario

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/store"
)

var (
	ErrRunConflict = errors.New("run ID already used with different input")
	ErrRunGone     = errors.New("run report is no longer retained")
	ErrRunBusy     = errors.New("a scenario run is already active")
)

// RunRepo stores reports independently of scenario revisions. Pruning removes
// only report bodies: the input fingerprint permanently prevents ID replay.
type RunRepo struct{ db *store.DB }

func NewRunRepo(db *store.DB) *RunRepo { return &RunRepo{db: db} }

func lookupRun(ctx context.Context, q queryer, scenarioID int64, runID string) (RunReport, string, error) {
	var hash string
	var raw sql.NullString
	err := q.QueryRowContext(ctx, `SELECT input_hash,report FROM design_scenario_runs WHERE scenario_id=? AND run_id=?`, scenarioID, runID).Scan(&hash, &raw)
	if err != nil {
		return RunReport{}, hash, notFound(err)
	}
	if !raw.Valid {
		return RunReport{}, hash, ErrRunGone
	}
	var report RunReport
	if err = jsonx.Unmarshal([]byte(raw.String), &report); err != nil {
		return RunReport{}, hash, fmt.Errorf("decode run report: %w", err)
	}
	return report, hash, nil
}

func (r *RunRepo) Lookup(ctx context.Context, scenarioID int64, runID string) (RunReport, string, error) {
	return lookupRun(ctx, r.db.R, scenarioID, runID)
}

func (r *RunRepo) Create(ctx context.Context, report RunReport, hash string) (RunReport, bool, error) {
	var result RunReport
	var created bool
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		stored, oldHash, err := lookupRun(ctx, tx, report.ScenarioID, report.ID)
		if err == nil || errors.Is(err, ErrRunGone) {
			if oldHash != hash {
				return ErrRunConflict
			}
			result = stored
			return err
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err = getRevision(ctx, tx, report.ScenarioID, report.RevisionID); err != nil {
			return err
		}
		var active int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM design_scenario_runs WHERE scenario_id=? AND status='running'`, report.ScenarioID).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return ErrRunBusy
		}
		raw, err := jsonx.Marshal(report)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO design_scenario_runs(scenario_id,run_id,revision_id,input_hash,version,name,source,status,started_at,finished_at,reason,report) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, report.ScenarioID, report.ID, report.RevisionID, hash, report.Version, report.Name, report.Source, report.Status, report.StartedAt, report.FinishedAt, report.Reason, string(raw))
		if err != nil {
			return err
		}
		result, created = report, true
		return nil
	})
	return result, created, err
}

func (r *RunRepo) SaveProgress(ctx context.Context, report RunReport) error {
	if report.Status != "running" {
		return errors.New("progress report must be running")
	}
	return r.db.Write(ctx, func(tx *sql.Tx) error { return saveRun(ctx, tx, report) })
}

func saveRun(ctx context.Context, tx *sql.Tx, report RunReport) error {
	raw, err := jsonx.Marshal(report)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE design_scenario_runs SET status=?,finished_at=?,reason=?,report=? WHERE scenario_id=? AND run_id=? AND status='running'`, report.Status, report.FinishedAt, report.Reason, string(raw), report.ScenarioID, report.ID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrRunConflict
	}
	return nil
}

func pruneRuns(ctx context.Context, tx *sql.Tx, scenarioID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE design_scenario_runs SET report=NULL WHERE scenario_id=? AND status!='running' AND report IS NOT NULL AND run_id NOT IN (SELECT run_id FROM design_scenario_runs WHERE scenario_id=? AND status!='running' AND report IS NOT NULL ORDER BY started_at DESC,run_id DESC LIMIT 50)`, scenarioID, scenarioID)
	return err
}

func (r *RunRepo) Finish(ctx context.Context, report RunReport) error {
	if report.Status != "passed" && report.Status != "failed" && report.Status != "cancelled" {
		return errors.New("final report must be terminal")
	}
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		if err := saveRun(ctx, tx, report); err != nil {
			return err
		}
		return pruneRuns(ctx, tx, report.ScenarioID)
	})
}

func (r *RunRepo) List(ctx context.Context, scenarioID int64) ([]RunSummary, error) {
	if _, err := getScenario(ctx, r.db.R, scenarioID); err != nil {
		return nil, err
	}
	rows, err := r.db.R.QueryContext(ctx, `SELECT run_id,scenario_id,revision_id,version,name,source,status,started_at,finished_at,reason FROM design_scenario_runs WHERE scenario_id=? AND report IS NOT NULL ORDER BY started_at DESC,run_id DESC LIMIT 50`, scenarioID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	runs := []RunSummary{}
	for rows.Next() {
		var summary RunSummary
		if err = rows.Scan(&summary.ID, &summary.ScenarioID, &summary.RevisionID, &summary.Version, &summary.Name, &summary.Source, &summary.Status, &summary.StartedAt, &summary.FinishedAt, &summary.Reason); err != nil {
			return nil, err
		}
		runs = append(runs, summary)
	}
	return runs, rows.Err()
}

// RecoverInterrupted is called before accepting traffic. A process restart
// cancels orphaned runs; it never resumes or dispatches their remaining steps.
func (r *RunRepo) RecoverInterrupted(ctx context.Context) error {
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT report FROM design_scenario_runs WHERE status='running'`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		var reports []RunReport
		for rows.Next() {
			var raw string
			if err = rows.Scan(&raw); err != nil {
				return err
			}
			var report RunReport
			if err = jsonx.Unmarshal([]byte(raw), &report); err != nil {
				return err
			}
			reports = append(reports, CancelRunReport(report, "server restarted during execution"))
		}
		iterationErr := rows.Err()
		if err = rows.Close(); err != nil {
			return err
		}
		if iterationErr != nil {
			return iterationErr
		}
		for _, report := range reports {
			if err = saveRun(ctx, tx, report); err != nil {
				return err
			}
			if err = pruneRuns(ctx, tx, report.ScenarioID); err != nil {
				return err
			}
		}
		return nil
	})
}
