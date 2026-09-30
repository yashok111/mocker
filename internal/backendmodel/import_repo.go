package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"math"
	"time"
	"uuid"
)

type importReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadSession(ctx context.Context, q importReader, pid, sid string) (*ImportSession, error) {
	if !ValidID(pid) || !ValidID(sid) {
		return nil, notFound()
	}
	var doc string
	err := q.QueryRowContext(ctx, `SELECT document FROM backend_import_sessions WHERE project_id=? AND id=?`, pid, sid).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	var s ImportSession
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		return nil, err
	}
	return &s, nil
}
func saveSession(ctx context.Context, tx *sql.Tx, s *ImportSession) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE backend_import_sessions SET state=?,version=?,document=? WHERE id=?`, s.State, s.Version, string(b), s.ID)
	return err
}
func requireSessionVersion(s *ImportSession, v int64) error {
	if s.Version != v {
		return importConflict("backend_import_version_conflict", "Import session changed; read before retrying", s.Version)
	}
	if s.Version == math.MaxInt64 {
		return importConflict("backend_import_version_exhausted", "Import session version cannot be incremented", s.Version)
	}
	return nil
}
func requireCollectible(s *ImportSession) error {
	if s.State == "committed" || s.State == "aborted" {
		return importConflict("backend_import_state_conflict", "Import session is closed", s.Version)
	}
	return nil
}
func requireEmptyBase(ctx context.Context, q importReader, pid, rid string) error {
	var doc string
	err := q.QueryRowContext(ctx, `SELECT document FROM backend_revisions WHERE project_id=? AND id=?`, pid, rid).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound()
	}
	if err != nil {
		return err
	}
	var rev Revision
	if err := json.Unmarshal([]byte(doc), &rev); err != nil {
		return err
	}
	if len(rev.SourceSnapshotIDs) > 0 {
		return importConflict("backend_reimport_unsupported", "Only the first source import is supported", 0)
	}
	return nil
}
func requireProjectVersion(p *Project, v int64) error {
	if p.Version != v {
		return importConflict("backend_version_conflict", "Project changed; read before retrying", p.Version)
	}
	if p.Version == math.MaxInt64 {
		return importConflict("backend_version_exhausted", "Project version cannot be incremented", p.Version)
	}
	return nil
}
func requestDigest(v any) (string, error) {
	b, err := canonicalJSON(v)
	if err != nil {
		return "", err
	}
	return hashBytes(b), nil
}
func readImportReceipt(ctx context.Context, q importReader, scope, key, hash string, result any) (bool, error) {
	var prior, response string
	err := q.QueryRowContext(ctx, `SELECT request_hash,response FROM backend_command_receipts WHERE scope=? AND key=?`, scope, key).Scan(&prior, &response)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if prior != hash {
		return true, importConflict("backend_idempotency_conflict", "Idempotency key was already used with a different request", 0)
	}
	return true, json.Unmarshal([]byte(response), result)
}
func (r *Repo) importMutation(ctx context.Context, scope, key string, request, result any, apply func(*sql.Tx) error) error {
	if err := validateKey(key); err != nil {
		return err
	}
	hash, err := requestDigest(request)
	if err != nil {
		return err
	}
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		found, err := readImportReceipt(ctx, tx, scope, key, hash, result)
		if err != nil || found {
			return err
		}
		if err := apply(tx); err != nil {
			return err
		}
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_command_receipts(scope,key,request_hash,response) VALUES(?,?,?,?)`, scope, key, hash, string(b))
		return err
	})
}
func stagingBytes(ctx context.Context, tx *sql.Tx, pid string) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `SELECT
 (SELECT COALESCE(SUM(length(CAST(document AS BLOB))),0) FROM backend_import_sessions WHERE project_id=?) +
 (SELECT COALESCE(SUM(length(CAST(r.document AS BLOB))),0) FROM backend_import_records r JOIN backend_import_sessions s ON s.id=r.session_id WHERE s.project_id=?) +
 (SELECT COALESCE(SUM(length(CAST(b.receipt AS BLOB))+length(b.batch_id)+256),0) FROM backend_import_batches b JOIN backend_import_sessions s ON s.id=b.session_id WHERE s.project_id=?) +
 (SELECT COALESCE(SUM(length(CAST(i.external_key AS BLOB))+length(i.id)+64),0) FROM backend_import_identities i JOIN backend_import_sessions s ON s.id=i.session_id WHERE s.project_id=?) +
 (SELECT COALESCE(SUM(length(CAST(response AS BLOB))),0) FROM backend_command_receipts WHERE scope LIKE ?)`, pid, pid, pid, pid, "import:"+pid+":%").Scan(&n)
	return n, err
}
func checkStaging(ctx context.Context, tx *sql.Tx, pid string, reserved int64) error {
	n, err := stagingBytes(ctx, tx, pid)
	if err != nil {
		return err
	}
	if n+reserved > MaxProjectStagingBytes {
		return limitFault("Project staging byte limit exceeded")
	}
	return nil
}
func (r *Repo) BeginImport(ctx context.Context, pid string, in BeginImportInput) (*ImportSession, error) {
	result := new(ImportSession)
	err := r.importMutation(ctx, "import:"+pid+":begin", in.IdempotencyKey, in, result, func(tx *sql.Tx) error {
		if !ValidID(pid) {
			return notFound()
		}
		p, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id=?`, pid))
		if err != nil {
			return err
		}
		if err := requireProjectVersion(p, in.ExpectedVersion); err != nil {
			return err
		}
		if p.CurrentRevisionID != in.BaseRevisionID {
			return importConflict("backend_import_base_conflict", "Base must be the current project revision", p.Version)
		}
		if err := requireEmptyBase(ctx, tx, pid, p.CurrentRevisionID); err != nil {
			return err
		}
		if err := validateManifest(in.Manifest); err != nil {
			return err
		}
		if err := validateInventory(in.Inventory); err != nil {
			return err
		}
		var open int
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM backend_import_sessions WHERE project_id=? AND state NOT IN ('committed','aborted')`, pid).Scan(&open)
		if err != nil {
			return err
		}
		if open >= MaxOpenImportSessions {
			return limitFault("Open import session limit exceeded")
		}
		mh, err := requestDigest(in.Manifest)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		*result = ImportSession{ID: uuid.NewV7().String(), ProjectID: pid, BaseRevisionID: in.BaseRevisionID, RepositoryID: uuid.NewV7().String(), SnapshotID: uuid.NewV7().String(), ManifestHash: mh, Manifest: in.Manifest, Inventory: in.Inventory, State: "collecting", Version: 1, CreatedAt: now, UpdatedAt: now}
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_import_sessions(id,project_id,state,version,document) VALUES(?,?,?,?,?)`, result.ID, pid, result.State, result.Version, string(b))
		if err != nil {
			return err
		}
		return checkStaging(ctx, tx, pid, int64(len(b)))
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (r *Repo) PutImportBatch(ctx context.Context, pid, sid, bid string, in ImportBatchInput) (*BatchReceipt, error) {
	if err := validateKey(bid); err != nil {
		return nil, invalid("batchId", "Invalid batch ID")
	}
	requestHash, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	result := new(BatchReceipt)
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		s, err := loadSession(ctx, tx, pid, sid)
		if err != nil {
			return err
		}
		var prior, response string
		err = tx.QueryRowContext(ctx, `SELECT request_hash,receipt FROM backend_import_batches WHERE session_id=? AND batch_id=?`, sid, bid).Scan(&prior, &response)
		if err == nil {
			if prior != requestHash {
				return importConflict("backend_import_batch_conflict", "Batch ID was already used with a different request", s.Version)
			}
			return json.Unmarshal([]byte(response), result)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if len(in.Commands) == 0 {
			return semantic("commands", "Batch needs at least one command")
		}
		if len(in.Commands) > MaxImportCommands {
			return limitFault("Import command limit exceeded")
		}
		payload, err := canonicalJSON(in.Commands)
		if err != nil {
			return semantic("commands", err.Error())
		}
		requestBytes, err := canonicalJSON(in)
		if err != nil {
			return semantic("commands", err.Error())
		}
		if len(requestBytes) > MaxImportBatchBytes {
			return limitFault("Import batch byte limit exceeded")
		}
		if !validHash(in.PayloadHash) || hashBytes(payload) != in.PayloadHash {
			return semantic("payloadHash", "Payload hash does not match canonical commands")
		}
		if err := requireSessionVersion(s, in.ExpectedImportVersion); err != nil {
			return err
		}
		if err := requireCollectible(s); err != nil {
			return err
		}
		seen := map[string]bool{}
		result.Identities = []RecordIdentity{}
		for _, c := range in.Commands {
			typ, key, err := commandAddress(c)
			if err != nil {
				return err
			}
			address := typ + "\x00" + key
			if seen[address] {
				return semantic("commands", "Duplicate addressed command in one batch")
			}
			seen[address] = true
			if err := validateCommand(c, s); err != nil {
				return err
			}
			var id string
			err = tx.QueryRowContext(ctx, `SELECT id FROM backend_import_identities WHERE session_id=? AND record_type=? AND external_key=?`, sid, typ, key).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				id = uuid.NewV7().String()
				_, err = tx.ExecContext(ctx, `INSERT INTO backend_import_identities(session_id,record_type,external_key,id) VALUES(?,?,?,?)`, sid, typ, key, id)
			}
			if err != nil {
				return err
			}
			result.Identities = append(result.Identities, RecordIdentity{RecordType: typ, ExternalKey: key, ID: id})
			if c.Op == "remove" {
				_, err = tx.ExecContext(ctx, `DELETE FROM backend_import_records WHERE session_id=? AND record_type=? AND external_key=?`, sid, typ, key)
			} else {
				b, marshalErr := json.Marshal(c)
				if marshalErr != nil {
					return marshalErr
				}
				_, err = tx.ExecContext(ctx, `INSERT INTO backend_import_records(session_id,record_type,external_key,document) VALUES(?,?,?,?) ON CONFLICT(session_id,record_type,external_key) DO UPDATE SET document=excluded.document`, sid, typ, key, string(b))
			}
			if err != nil {
				return err
			}
		}
		s.Version++
		s.AcceptedBatchCount++
		s.State = "collecting"
		s.CandidateHash = nil
		s.UpdatedAt = time.Now().UTC()
		if err := saveSession(ctx, tx, s); err != nil {
			return err
		}
		result.BatchSummary = BatchSummary{BatchID: bid, PayloadHash: in.PayloadHash, AcceptedVersion: s.Version}
		receipt, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_import_batches(session_id,batch_id,payload_hash,request_hash,accepted_version,receipt) VALUES(?,?,?,?,?,?)`, sid, bid, in.PayloadHash, requestHash, s.Version, string(receipt))
		if err != nil {
			return err
		}
		var nodes, edges, evidence int
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(record_type='node'),0),COALESCE(SUM(record_type='edge'),0),COALESCE(SUM(record_type='evidence'),0) FROM backend_import_records WHERE session_id=?`, sid).Scan(&nodes, &edges, &evidence)
		if err != nil {
			return err
		}
		if nodes > MaxRevisionNodes || edges > MaxRevisionEdges || evidence > MaxRevisionEvidence {
			return limitFault("Revision record limit exceeded")
		}
		var total int64
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(CAST(document AS BLOB))),0) FROM backend_import_records WHERE session_id=?`, sid).Scan(&total)
		if err != nil {
			return err
		}
		manifestBytes, _ := canonicalJSON(s.Manifest)
		inventoryBytes, _ := canonicalJSON(s.Inventory)
		if total+int64(len(manifestBytes)+len(inventoryBytes)) > MaxRevisionBytes {
			return limitFault("Revision semantic byte limit exceeded")
		}
		return checkStaging(ctx, tx, pid, 0)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (r *Repo) AbortImport(ctx context.Context, pid, sid string, in AbortImportInput) (*ImportSession, error) {
	result := new(ImportSession)
	err := r.importMutation(ctx, "import:"+pid+":"+sid+":abort", in.IdempotencyKey, in, result, func(tx *sql.Tx) error {
		s, err := loadSession(ctx, tx, pid, sid)
		if err != nil {
			return err
		}
		if err := requireSessionVersion(s, in.ExpectedImportVersion); err != nil {
			return err
		}
		if err := requireCollectible(s); err != nil {
			return err
		}
		s.State = "aborted"
		s.CandidateHash = nil
		s.Version++
		s.UpdatedAt = time.Now().UTC()
		*result = *s
		if err := saveSession(ctx, tx, s); err != nil {
			return err
		}
		b, _ := json.Marshal(result)
		return checkStaging(ctx, tx, pid, int64(len(b)))
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
