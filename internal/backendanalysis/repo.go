package backendanalysis

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"github.com/yashok111/mocker/internal/backendblob"
	"time"
	"uuid"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/store"
)

type Repo struct{ db *store.DB }

func NewRepo(db *store.DB) *Repo { return &Repo{db: db} }

type reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func opaqueID() string { return uuid.NewV7().String() }

const jobColumns = `id,project_id,input_hash,kind,status,version,result_version,progress_states,progress_dependency_visits,progress_findings,progress_records,diagnostic,created_at,updated_at`

func readJob(ctx context.Context, q reader, pid, id string) (*Job, error) {
	var j Job
	var diagnostic *string
	var created, updated string
	err := q.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM backend_analysis_jobs WHERE project_id=? AND id=?`, pid, id).Scan(&j.ID, &j.ProjectID, &j.AnalysisInputHash, &j.Kind, &j.Status, &j.Version, &j.ResultVersion, &j.Progress.States, &j.Progress.DependencyVisits, &j.Progress.Findings, &j.Progress.Records, &diagnostic, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fault(404, "not_found", "Analysis not found")
	}
	if err != nil {
		return nil, err
	}
	j.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, err
	}
	j.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return nil, err
	}
	if diagnostic != nil {
		if err = json.Unmarshal([]byte(*diagnostic), &j.Diagnostic); err != nil {
			return nil, err
		}
	}
	j.RecommendedPollIntervalMs = 2000
	return &j, nil
}
func (r *Repo) Get(ctx context.Context, pid, id string) (*Job, error) {
	return readJob(ctx, r.db.R, pid, id)
}
func lookupReceipt(ctx context.Context, q reader, pid, action, key string) (*Receipt, error) {
	out := Receipt{ProjectID: pid, Action: action, Key: key}
	err := q.QueryRowContext(ctx, `SELECT job_id,request_hash,response FROM backend_analysis_receipts WHERE project_id=? AND action=? AND key=?`, pid, action, key).Scan(&out.JobID, &out.RequestHash, &out.Response)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}
func (r *Repo) LookupReceipt(ctx context.Context, pid, action, key string) (*Receipt, error) {
	return lookupReceipt(ctx, r.db.R, pid, action, key)
}
func replay(rec *Receipt, hash string) (*Job, error) {
	if rec.RequestHash != hash {
		return nil, fault(409, "idempotency_conflict", "Key was used for another request")
	}
	var j Job
	if err := json.Unmarshal(rec.Response, &j); err != nil {
		return nil, err
	}
	return &j, nil
}
func saveReceipt(ctx context.Context, tx *sql.Tx, action, key, hash string, j *Job) error {
	raw, err := canonical(j)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_analysis_receipts(project_id,action,key,job_id,request_hash,response,response_bytes) VALUES(?,?,?,?,?,?,?)`, j.ProjectID, action, key, j.ID, hash, string(raw), len(raw))
	return err
}
func (r *Repo) Start(ctx context.Context, p PreparedStart, check AdmissionCheck) (*Job, error) {
	if rec, err := r.LookupReceipt(ctx, p.ProjectID, "start", p.Key); err != nil {
		return nil, err
	} else if rec != nil {
		return replay(rec, p.RequestHash)
	}
	return r.admit(ctx, p, "start", check)
}
func (r *Repo) admit(ctx context.Context, p PreparedStart, action string, check AdmissionCheck) (*Job, error) {
	var out *Job
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		rec, err := lookupReceipt(ctx, tx, p.ProjectID, action, p.Key)
		if err != nil {
			return err
		}
		if rec != nil {
			out, err = replay(rec, p.RequestHash)
			return err
		}
		out, err = admitTx(ctx, tx, p, action, check)
		return err
	})
	return out, err
}
func admitTx(ctx context.Context, tx *sql.Tx, p PreparedStart, action string, check AdmissionCheck) (*Job, error) {
	in, err := validatePrepared(p)
	if err != nil {
		return nil, err
	}
	if err = checkJobSlots(ctx, tx, p.ProjectID); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	j := &Job{ID: opaqueID(), ProjectID: p.ProjectID, Kind: in.Kind, Status: "queued", AnalysisInputHash: p.InputHash, Version: 1, RecommendedPollIntervalMs: 2000, CreatedAt: now, UpdatedAt: now}
	response, err := canonical(j)
	if err != nil {
		return nil, err
	}
	inputCost := int64(len(p.InputJSON))
	var old string
	var oldBytes int64
	err = tx.QueryRowContext(ctx, `SELECT document,input_bytes FROM backend_analysis_inputs_documents WHERE project_id=? AND input_hash=?`, p.ProjectID, p.InputHash).Scan(&old, &oldBytes)
	if err == nil {
		if old != string(p.InputJSON) || oldBytes != inputCost {
			return nil, fault(409, "input_conflict", "Immutable input differs")
		}
		inputCost = 0
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err = checkBytes(ctx, tx, p.ProjectID, inputCost+int64(len(response))+p.OutputReservation); err != nil {
		return nil, err
	}
	if check != nil {
		if err = check(ctx, tx); err != nil {
			return nil, err
		}
	}
	if inputCost > 0 {
		if _, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_analysis_inputs(project_id,input_hash,document,input_bytes) VALUES(?,?,?,?)`, p.ProjectID, p.InputHash, string(p.InputJSON), len(p.InputJSON)); err != nil {
			return nil, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_analysis_jobs(id,project_id,input_hash,kind,status,version,reserved_output_bytes,reserved_terminal_bytes,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?,?,?)`, j.ID, p.ProjectID, p.InputHash, j.Kind, j.Status, p.OutputReservation, terminalHeadroom, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	if err = saveReceipt(ctx, tx, action, p.Key, p.RequestHash, j); err != nil {
		return nil, err
	}
	return j, nil
}
func checkBytes(ctx context.Context, q reader, pid string, additional int64) error {
	var retained, reserved int64
	err := q.QueryRowContext(ctx, `SELECT COALESCE((SELECT sum(input_bytes) FROM backend_analysis_inputs_documents WHERE project_id=?),0)+COALESCE((SELECT sum(result_bytes) FROM backend_analysis_jobs WHERE project_id=?),0)+COALESCE((SELECT sum(response_bytes) FROM backend_analysis_receipts WHERE project_id=?),0),COALESCE((SELECT sum(reserved_output_bytes) FROM backend_analysis_jobs WHERE project_id=?),0)`, pid, pid, pid, pid).Scan(&retained, &reserved)
	if err != nil {
		return err
	}
	if additional > maxProjectBytes-retained-reserved {
		return &backendmodel.FaultError{Status: 409, Code: "backend_analysis_byte_quota", Message: "Retained evidence and reservations exceed project quota", Details: map[string]any{"retainedBytes": retained, "reservedBytes": reserved, "requestedBytes": additional, "allowedBytes": maxProjectBytes}}
	}
	return nil
}
func inputBytes(ctx context.Context, q reader, pid, id string) ([]byte, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT i.document FROM backend_analysis_inputs_documents i JOIN backend_analysis_jobs j ON j.project_id=i.project_id AND j.input_hash=i.input_hash WHERE j.project_id=? AND j.id=?`, pid, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fault(404, "not_found", "Analysis not found")
	}
	return raw, err
}
func (r *Repo) Input(ctx context.Context, pid, id string) (*ImmutableInput, error) {
	raw, err := inputBytes(ctx, r.db.R, pid, id)
	if err != nil {
		return nil, err
	}
	var in ImmutableInput
	if err = json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	return &in, nil
}
func (r *Repo) Claim(ctx context.Context, token string) (*ClaimedJob, error) {
	if token == "" {
		return nil, malformed("Empty worker token")
	}
	var out *ClaimedJob
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		var running int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_analysis_jobs WHERE status='running'`).Scan(&running); err != nil {
			return err
		}
		if running >= 2 {
			return nil
		}
		var pid, id string
		err := tx.QueryRowContext(ctx, `SELECT project_id,id FROM backend_analysis_jobs WHERE status='queued' ORDER BY created_at,id LIMIT 1`).Scan(&pid, &id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE backend_analysis_jobs SET status='running',worker_token=?,version=version+1,updated_at=? WHERE project_id=? AND id=? AND status='queued'`, token, time.Now().UTC().Format(time.RFC3339Nano), pid, id)
		if err != nil {
			return err
		}
		j, err := readJob(ctx, tx, pid, id)
		if err != nil {
			return err
		}
		raw, err := inputBytes(ctx, tx, pid, id)
		if err != nil {
			return err
		}
		var in ImmutableInput
		if err = json.Unmarshal(raw, &in); err != nil {
			return err
		}
		out = &ClaimedJob{Job: *j, Input: in, Token: token}
		return nil
	})
	return out, err
}

func mutationHash(id string) string { return digest([]byte(id)) }
func (r *Repo) Retry(ctx context.Context, pid, id string, in RetryInput) (*Job, error) {
	hash := mutationHash(id)
	if rec, err := r.LookupReceipt(ctx, pid, "retry", in.IdempotencyKey); err != nil {
		return nil, err
	} else if rec != nil {
		return replay(rec, hash)
	}
	var out *Job
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		rec, err := lookupReceipt(ctx, tx, pid, "retry", in.IdempotencyKey)
		if err != nil {
			return err
		}
		if rec != nil {
			out, err = replay(rec, hash)
			return err
		}
		original, err := readJob(ctx, tx, pid, id)
		if err != nil {
			return err
		}
		if original.Status == "queued" || original.Status == "running" {
			return fault(409, "not_terminal", "Retry requires a terminal job")
		}
		raw, err := inputBytes(ctx, tx, pid, id)
		if err != nil {
			return err
		}
		var input ImmutableInput
		if err = json.Unmarshal(raw, &input); err != nil {
			return err
		}
		out, err = admitTx(ctx, tx, PreparedStart{ProjectID: pid, Key: in.IdempotencyKey, RequestHash: hash, InputHash: original.AnalysisInputHash, InputJSON: raw, OutputReservation: input.Limits.ResultBytes}, "retry", nil)
		return err
	})
	return out, err
}

// Read and prepare the accepted prefix without holding the writer. The short
// terminal transaction rejects a moved job version and repeats this read.
func (r *Repo) acceptedPrefix(ctx context.Context, pid, id, code string) (*Job, PreparedSnapshot, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, PreparedSnapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	j, err := readJob(ctx, tx, pid, id)
	if err != nil {
		return nil, PreparedSnapshot{}, err
	}
	s := PreparedSnapshot{Manifest: ResultManifest{JobID: id, AnalysisInputHash: j.AnalysisInputHash, ResultVersion: 1, Verdict: "unknown"}, Progress: j.Progress}
	rawInput, err := inputBytes(ctx, tx, pid, id)
	if err != nil {
		return nil, s, err
	}
	var saved ImmutableInput
	if err = json.Unmarshal(rawInput, &saved); err != nil {
		return nil, s, err
	}
	s.Manifest.Scope = saved.Scope
	s.Manifest.RuleSetVersion = saved.RuleSetVersion
	s.Manifest.TraversalVersion = saved.TraversalVersion

	if j.ResultVersion != nil {
		var raw []byte
		if err = tx.QueryRowContext(ctx, `SELECT document FROM backend_analysis_manifests_documents WHERE project_id=? AND job_id=? AND result_version=?`, pid, id, *j.ResultVersion).Scan(&raw); err != nil {
			return nil, s, err
		}
		if err = json.Unmarshal(raw, &s.Manifest); err != nil {
			return nil, s, err
		}
		s.Manifest.ResultVersion++
	}
	rows, err := tx.QueryContext(ctx, `SELECT sequence,section,content_hash,items_json FROM backend_analysis_chunks_documents WHERE project_id=? AND job_id=? ORDER BY sequence`, pid, id)
	if err != nil {
		return nil, s, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		c := ResultChunk{JobID: id}
		if err = rows.Scan(&c.Sequence, &c.Section, &c.ContentHash, &c.ItemsJSON); err != nil {
			return nil, s, err
		}
		s.Chunks = append(s.Chunks, c)
	}
	if err = rows.Err(); err != nil {
		return nil, s, err
	}
	s.Manifest.Complete = false
	s.Manifest.Verdict = "unknown"
	s.Manifest.Gaps = append(s.Manifest.Gaps, Diagnostic{ID: code, Code: code, Message: "Analysis ended before completion"})
	prepared, err := prepareSnapshot(s)
	if isFault(err, "backend_analysis_manifest_limit") {
		// This prefix is how a job that cannot finish is CLOSED -- by the
		// worker, by Cancel and by startup recovery -- so it must always fit.
		// An oversized scope admitted before the admission bound, or a last
		// published manifest that left no room for one more gap, made it answer
		// 413 forever: recovery returned the error and the server could not
		// start (review 2026-10-06, F149). The immutable input and the chunks
		// keep everything dropped here; the gap says it was dropped.
		prepared, err = prepareSnapshot(compactPrefix(s, code))
	}
	return j, prepared, err
}

// compactPrefix keeps the identity, versions and verdict of a prefix manifest
// and drops every caller- or graph-sized field. Sections are recomputed from
// the chunks by prepareSnapshot.
func compactPrefix(s PreparedSnapshot, code string) PreparedSnapshot {
	m := s.Manifest
	s.Manifest = ResultManifest{
		JobID: m.JobID, AnalysisInputHash: m.AnalysisInputHash, ResultVersion: m.ResultVersion,
		RuleSetVersion: m.RuleSetVersion, TraversalVersion: m.TraversalVersion, Verdict: "unknown",
		ChangedIDs: []ObjectAddress{}, CoveredChangedIDs: []ObjectAddress{}, TruncationReasons: []Diagnostic{},
		Gaps: []Diagnostic{
			{ID: code, Code: code, Message: "Analysis ended before completion"},
			{ID: "manifest_compacted", Code: "manifest_compacted", Message: "Scope, diagram scope, changed ids, coverage and earlier gaps were omitted to fit the manifest bound; the immutable input keeps them"},
		},
	}
	return s
}

func isFault(err error, code string) bool {
	f, ok := errors.AsType[*backendmodel.FaultError](err)
	return ok && f.Code == code
}

var errTransitionMoved = errors.New("analysis transition moved")

func (r *Repo) Cancel(ctx context.Context, pid, id string, in CancelInput) (*Job, error) {
	return r.stop(ctx, pid, id, "cancelled", in.IdempotencyKey)
}
func (r *Repo) stop(ctx context.Context, pid, id, status, key string) (*Job, error) {
	hash := mutationHash(id)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if status == "cancelled" {
			if rec, err := r.LookupReceipt(ctx, pid, "cancel", key); err != nil {
				return nil, err
			} else if rec != nil {
				return replay(rec, hash)
			}
		}
		previous, s, err := r.acceptedPrefix(ctx, pid, id, status)
		if err != nil {
			return nil, err
		}
		var out *Job
		err = r.db.Write(ctx, func(tx *sql.Tx) error { var e error; out, e = stopTx(ctx, tx, previous, s, status, key); return e })
		if errors.Is(err, errTransitionMoved) {
			continue
		}
		return out, err
	}
}
func (r *Repo) RecoverInterrupted(ctx context.Context) error {
	rows, err := r.db.R.QueryContext(ctx, `SELECT project_id,id FROM backend_analysis_jobs WHERE status IN ('queued','running') ORDER BY created_at,id`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	type ref struct{ pid, id string }
	var jobs []ref
	for rows.Next() {
		var j ref
		if err = rows.Scan(&j.pid, &j.id); err != nil {
			return err
		}
		jobs = append(jobs, j)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, j := range jobs {
		if _, err = r.stop(ctx, j.pid, j.id, "interrupted", ""); err != nil {
			return err
		}
	}
	return nil
}

func validatePrepared(p PreparedStart) (*ImmutableInput, error) {
	if len(p.InputJSON) > maxInputBytes {
		return nil, fault(413, "input_limit", "Normalized input exceeds 2 MiB")
	}
	if digest(p.InputJSON) != p.InputHash {
		return nil, malformed("Input hash differs")
	}
	var in ImmutableInput
	if err := json.Unmarshal(p.InputJSON, &in); err != nil {
		return nil, err
	}
	if in.ProjectID != p.ProjectID || in.Kind != "diff" && in.Kind != "impact" && in.Kind != "diagnostics" && !b43Kind(in.Kind) && !measurementKind(in.Kind) {
		return nil, malformed("Invalid immutable input")
	}
	if p.OutputReservation < terminalHeadroom || p.OutputReservation > maxResultBytes {
		return nil, malformed("Invalid output reservation")
	}
	return &in, nil
}

func checkJobSlots(ctx context.Context, tx *sql.Tx, pid string) error {
	var count, waiting int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_analysis_jobs WHERE project_id=?`, pid).Scan(&count); err != nil {
		return err
	}
	if count >= 1000 {
		return fault(409, "job_quota", "Project already retains 1000 jobs")
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_analysis_jobs WHERE status='queued'`).Scan(&waiting); err != nil {
		return err
	}
	if waiting >= 20 {
		return &backendmodel.FaultError{Status: 429, Code: "backend_analysis_queue_full", Message: "Twenty jobs are waiting", Retryable: true, Details: map[string]any{"retryAfterSeconds": 2, "waitingJobs": waiting, "allowedWaitingJobs": 20}}
	}
	return nil
}

func stopTx(ctx context.Context, tx *sql.Tx, previous *Job, s PreparedSnapshot, status, key string) (*Job, error) {
	pid, id := previous.ProjectID, previous.ID
	if status == "cancelled" {
		rec, err := lookupReceipt(ctx, tx, pid, "cancel", key)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			return replay(rec, mutationHash(id))
		}
	}
	current, err := readJob(ctx, tx, pid, id)
	if err != nil {
		return nil, err
	}
	if current.Status != "running" && current.Status != "queued" {
		if status == "interrupted" {
			return current, nil
		}
		return nil, fault(409, "terminal_conflict", "Analysis is already terminal")
	}
	if current.Version != previous.Version {
		return nil, errTransitionMoved
	}
	token := opaqueID()
	if _, err = tx.ExecContext(ctx, `UPDATE backend_analysis_jobs SET status='running',worker_token=? WHERE project_id=? AND id=?`, token, pid, id); err != nil {
		return nil, err
	}
	if err = publishTx(ctx, tx, pid, id, token, s, true); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE backend_analysis_jobs SET status=?,worker_token=NULL,reserved_output_bytes=0,reserved_terminal_bytes=0,version=version+1 WHERE project_id=? AND id=?`, status, pid, id); err != nil {
		return nil, err
	}
	out, err := readJob(ctx, tx, pid, id)
	if err != nil {
		return nil, err
	}
	if status == "cancelled" {
		raw, e := canonical(out)
		if e != nil {
			return nil, e
		}
		if e = checkBytes(ctx, tx, pid, int64(len(raw))); e != nil {
			return nil, e
		}
		err = saveReceipt(ctx, tx, "cancel", key, mutationHash(id), out)
	}
	return out, err
}
