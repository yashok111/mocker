package backendreplay

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"time"
	"uuid"

	"github.com/yashok111/mocker/internal/backendblob"

	"github.com/yashok111/mocker/internal/backendmodel"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"github.com/yashok111/mocker/internal/store"
)

type Repo struct{ db *store.DB }

func NewRepo(db *store.DB) *Repo { return &Repo{db: db} }

type replayReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func replayFault(status int, code, message string) error {
	return &backendmodel.FaultError{Status: status, Code: "backend_replay_" + code, Message: message}
}
func invalidReplay() error                { return replayFault(400, "invalid_request", "Invalid replay request") }
func missingReplay() error                { return replayFault(404, "not_found", "Replay record not found") }
func conflictReplay(message string) error { return replayFault(409, "conflict", message) }
func nowReplay() string                   { return time.Now().UTC().Format(time.RFC3339Nano) }
func newReplayID() string                 { return uuid.NewV7().String() }
func marshalReplay(v any) ([]byte, error) { return json.Marshal(v, json.Deterministic(true)) }
func replayHash(v any) (string, error)    { return p.Hash("backend-replay-request-v1", v) }
func receiptRead(ctx context.Context, q replayReader, pid, action, key, hash string, out any) (bool, error) {
	var stored string
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT input_hash,result_json FROM backend_replay_receipts WHERE project_id=? AND action=? AND key=?`, pid, action, key).Scan(&stored, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if stored != hash {
		return false, conflictReplay("Idempotency key was used for another input")
	}
	return true, json.Unmarshal(raw, out)
}
func receiptWrite(ctx context.Context, tx *sql.Tx, pid, action, key, hash string, out any) error {
	raw, err := marshalReplay(out)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_replay_receipts VALUES(?,?,?,?,?)`, pid, action, key, hash, string(raw))
	return err
}
func readProfile(ctx context.Context, q replayReader, pid, id string, version int64) (*Profile, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT document_json FROM backend_replay_profiles_documents WHERE project_id=? AND id=? AND version=?`, pid, id, version).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, missingReplay()
	}
	if err != nil {
		return nil, err
	}
	var v Profile
	err = json.Unmarshal(raw, &v)
	return &v, err
}
func readPackage(ctx context.Context, q replayReader, pid, id string, version int64) (*SavedPackage, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT document_json FROM backend_replay_packages_documents WHERE project_id=? AND id=? AND version=?`, pid, id, version).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, missingReplay()
	}
	if err != nil {
		return nil, err
	}
	var v SavedPackage
	err = json.Unmarshal(raw, &v)
	return &v, err
}
func readRun(ctx context.Context, q replayReader, pid, id string) (*Run, error) {
	var v Run
	var raw, report []byte
	var created, updated string
	err := q.QueryRowContext(ctx, `SELECT id,project_id,status,input_json,terminal_report_json,created_at,updated_at FROM backend_replay_runs WHERE project_id=? AND id=?`, pid, id).Scan(&v.ID, &v.ProjectID, &v.Status, &raw, &report, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, missingReplay()
	}
	if err != nil {
		return nil, err
	}
	var frozen struct {
		Input      RunInput   `json:"input"`
		Provenance Provenance `json:"provenance"`
	}
	if err = json.Unmarshal(raw, &frozen); err != nil {
		return nil, err
	}
	v.Input = frozen.Input
	v.Provenance = frozen.Provenance
	if report != nil {
		if err = json.Unmarshal(report, &v.Report); err != nil {
			return nil, err
		}
	}
	if v.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return nil, err
	}
	v.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	return &v, err
}
func (r *Repo) RecoverInterrupted(ctx context.Context) error {
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT project_id,id FROM backend_replay_runs WHERE status IN ('queued','running')`)
		if err != nil {
			return err
		}
		ids := [][2]string{}
		for rows.Next() {
			var pair [2]string
			if err = rows.Scan(&pair[0], &pair[1]); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, pair)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, pair := range ids {
			run, err := readRun(ctx, tx, pair[0], pair[1])
			if err != nil {
				return err
			}
			report := initialReport(run.Input, run.Provenance)
			report.Status = "interrupted"
			report.Reason = "Interrupted at startup; no automatic resume"
			raw, err := marshalReplay(report)
			if err != nil {
				return err
			}
			if err = fenceIfDispatched(ctx, tx, run.ID); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE backend_replay_runs SET status='interrupted',version=version+1,updated_at=?,terminal_report_json=? WHERE id=? AND status IN ('queued','running')`, nowReplay(), string(raw), run.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// fenceIfDispatched settles the lease of a run stopped without a verdict
// (restart recovery, cancellation of a running run). A step row is written
// in the same write that re-checks status='running' BEFORE any mutation is
// sent, so a run with no step row has sent nothing and never will: its lease
// is released, the rule finalize and Reconcile already apply. Marking every
// such lease uncertain (review 2026-10-06, F6/F128) fenced targets that no
// mutation had touched until the author acknowledged the run.
func fenceIfDispatched(ctx context.Context, tx *sql.Tx, runID string) error {
	var steps int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_replay_steps_documents WHERE run_id=?`, runID).Scan(&steps); err != nil {
		return err
	}
	if steps == 0 {
		_, err := tx.ExecContext(ctx, `DELETE FROM backend_replay_target_leases WHERE run_id=?`, runID)
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE backend_replay_target_leases SET state='uncertain' WHERE run_id=?`, runID)
	return err
}

func (r *Repo) registerConfigs(ctx context.Context, targets []Target) error {
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		for _, t := range targets {
			var aliased int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_replay_target_leases l JOIN backend_replay_runs r ON r.id=l.run_id WHERE json_extract(r.input_json,'$.input.profile.identity.isolationId')=? AND l.target_id<>?`, t.IsolationID, t.ID).Scan(&aliased); err != nil {
				return err
			}
			if aliased != 0 {
				return conflictReplay("Target alias cannot bypass an existing isolation lease")
			}

			var latest int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(version),0) FROM backend_replay_configs WHERE target_id=?`, t.ID).Scan(&latest); err != nil {
				return err
			}
			if t.Version < latest {
				return conflictReplay("Operator config version cannot go backwards")
			}
			var old string
			err := tx.QueryRowContext(ctx, `SELECT fingerprint FROM backend_replay_configs WHERE target_id=? AND version=?`, t.ID, t.Version).Scan(&old)
			if err == nil {
				if old != t.ConfigFingerprint {
					return conflictReplay("Operator target changed without a new config version")
				}
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO backend_replay_configs VALUES(?,?,?)`, t.ID, t.Version, t.ConfigFingerprint); err != nil {
				return err
			}
		}
		return nil
	})
}
func authorizeProfile(ctx context.Context, q replayReader, pid, actor string, profile Profile) error {
	var author string
	var revoked int
	err := q.QueryRowContext(ctx, `SELECT author,(SELECT count(*) FROM backend_replay_revocations WHERE project_id=p.project_id AND profile_id=p.id AND profile_version=p.version) FROM backend_replay_profiles_documents p WHERE project_id=? AND id=? AND version=? AND content_hash=?`, pid, profile.Pin.ID, profile.Pin.Version, profile.Pin.ContentHash).Scan(&author, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return missingReplay()
	}
	if err != nil {
		return err
	}
	if author != actor || revoked != 0 {
		return replayFault(403, "authorization_revoked", "Reset consent is not current for this actor")
	}
	return nil
}
func (r *Repo) beforeMutation(ctx context.Context, pid, actor string, in RunInput, endpoint p.Endpoint, f p.Fence, hash string, body []byte) error {
	if len(body) > p.BodyLimit {
		return invalidReplay()
	}
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM backend_replay_runs WHERE project_id=? AND id=?`, pid, in.RunID).Scan(&status); err != nil {
			return err
		}
		if status != "running" {
			return conflictReplay("Replay stopped before dispatch")
		}
		if err := authorizeProfile(ctx, tx, pid, actor, in.Profile); err != nil {
			return err
		}
		_, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_replay_steps(run_id,step_id,request_key,endpoint,request_hash,request_json) VALUES(?,?,?,?,?,?)`, in.RunID, f.StepID, f.RequestKey, string(endpoint), hash, body)
		return err
	})
}
func (r *Repo) evidence(ctx context.Context, id, kind string, body []byte) error {
	if len(body) > p.ReportLimit {
		return conflictReplay("Replay evidence exceeds limit")
	}
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		var n, size int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(sequence),0),COALESCE(sum(length(body)),0) FROM backend_replay_evidence_documents WHERE run_id=?`, id).Scan(&n, &size); err != nil {
			return err
		}
		if size+int64(len(body)) > 16*p.ReportLimit {
			return conflictReplay("Replay evidence budget exceeded")
		}
		_, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_replay_evidence(run_id,sequence,kind,content_hash,body) VALUES(?,?,?,?,?)`, id, n+1, kind, p.HashBytes(body), body)
		return err
	})
}
