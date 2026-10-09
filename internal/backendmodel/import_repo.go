package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"log/slog"
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
	if s.Mode == "" {
		s.Mode = "initial"
	}
	s.Profile = selectedProfile(s.Profile)
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
	err := q.QueryRowContext(ctx, `SELECT document FROM backend_revisions_documents WHERE project_id=? AND id=?`, pid, rid).Scan(&doc)
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
	if err := json.Unmarshal([]byte(response), result); err != nil {
		return true, err
	}
	if session, ok := result.(*ImportSession); ok {
		session.legacyReceiptJSON = response
		if session.Mode == "" {
			session.Mode = "initial"
		}
		session.Profile = selectedProfile(session.Profile)
	}
	return true, nil
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

// openSessionFilter restricts a staging sum to the sessions that still hold
// staging. Review 2026-10-06, F83/F172: the sums used to join on project_id
// alone, so every committed and aborted session (whose rows nothing deletes)
// and every import receipt counted forever. A project that reconciled often
// enough crossed MaxProjectStagingBytes for good: Begin, Batch, Preview,
// Commit and even Abort answered 413, and the transient budgets of proposals,
// change proposals, rebase and analysis jobs (which use this sum as their
// baseline) shrank with every old import. A closed session's rows stay
// readable as history; they are simply no longer staging.
const openSessionFilter = `s.project_id=? AND s.state NOT IN ('committed','aborted')`

// stagingBytes is the durable staging the project's OPEN import sessions hold.
// Command receipts are not counted: they are idempotency records retained for
// the project's life (recovery.md: receipts survive restart), so counting them
// made the cap a lifetime one, and an open session's begin receipt is a copy
// of the session document that is already counted here.
func stagingBytes(ctx context.Context, tx *sql.Tx, pid string) (int64, error) {
	return importSessionBytes(ctx, tx, pid, false)
}

func importSessionBytes(ctx context.Context, tx *sql.Tx, pid string, closed bool) (int64, error) {
	stateFilter := openSessionFilter
	if closed {
		stateFilter = `s.project_id=? AND s.state IN ('committed','aborted')`
	}

	var n int64
	err := tx.QueryRowContext(ctx, `SELECT
 (SELECT COALESCE(SUM(length(CAST(s.document AS BLOB))),0) FROM backend_import_sessions s WHERE `+stateFilter+`) +
 (SELECT COALESCE(SUM(length(CAST(r.document AS BLOB))),0) FROM backend_import_records r JOIN backend_import_sessions s ON s.id=r.session_id WHERE `+stateFilter+`) +
 (SELECT COALESCE(SUM(length(CAST(b.receipt AS BLOB))+length(b.batch_id)+256),0) FROM backend_import_batches b JOIN backend_import_sessions s ON s.id=b.session_id WHERE `+stateFilter+`) +
 (SELECT COALESCE(SUM(length(CAST(i.external_key AS BLOB))+length(i.id)+64),0) FROM backend_import_identities i JOIN backend_import_sessions s ON s.id=i.session_id WHERE `+stateFilter+`)`, pid, pid, pid, pid).Scan(&n)
	if err != nil {
		return n, err
	}
	var extra int64
	err = tx.QueryRowContext(ctx, `SELECT
      (SELECT COALESCE(SUM(length(CAST(d.document AS BLOB))),0) FROM backend_import_decisions d JOIN backend_import_sessions s ON s.id=d.session_id WHERE `+stateFilter+`) +
      (SELECT COALESCE(SUM(length(CAST(p.document AS BLOB))+length(CAST(p.details AS BLOB))),0) FROM backend_import_previews p JOIN backend_import_sessions s ON s.id=p.session_id WHERE `+stateFilter+`) +
      (SELECT COALESCE(SUM(length(a.external_key)+length(a.source_key)+128),0) FROM backend_import_aliases a JOIN backend_import_sessions s ON s.id=a.session_id WHERE `+stateFilter+`)`, pid, pid, pid).Scan(&extra)
	if err != nil {
		return 0, err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='backend_import_source_decisions'`).Scan(&exists); err != nil {
		return 0, err
	}
	var sourceBytes int64
	if exists != 0 {
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(CAST(d.document AS BLOB))+length(CAST(d.decision_key AS BLOB))+length(d.decision_id)+length(d.input_hash)+128),0) FROM backend_import_source_decisions d JOIN backend_import_sessions s ON s.id=d.session_id WHERE `+stateFilter, pid).Scan(&sourceBytes)
	} else {
		var version int
		if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
			return 0, err
		}
		if version >= 20 {
			return 0, errors.New("source decision storage is missing from a schema20 store")
		}
	}
	return n + extra + sourceBytes, err
}
func (r *Repo) checkStaging(ctx context.Context, tx *sql.Tx, pid string, reserved int64) error {
	n, err := stagingBytes(ctx, tx, pid)
	if err != nil {
		return err
	}
	if n+r.db.TransientBytes("backend:"+pid)+reserved > MaxProjectStagingBytes {
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
		if err := validateImportMode(in); err != nil {
			return err
		}
		mode := in.Mode
		if mode == "" {
			mode = "initial"
		}
		repositoryID := importRepositoryID(in)
		baseSession := &ImportSession{ProjectID: pid, BaseRevisionID: in.BaseRevisionID, RepositoryID: repositoryID, Manifest: in.Manifest, Mode: mode, GraphScope: in.GraphScope, Profile: selectedProfile(in.Profile), ProfileExtension: in.ProfileExtension}
		baseSession.SourceScope, baseSession.ScopeStatus, baseSession.SyncPolicy = in.SourceScope, in.ScopeStatus, in.SyncPolicy
		baseSession.ChangeManifest = in.ChangeManifest
		if err := requireImportBase(ctx, tx, baseSession); err != nil {
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
		*result = ImportSession{ID: uuid.NewV7().String(), ProjectID: pid, BaseRevisionID: in.BaseRevisionID, Mode: mode, GraphScope: in.GraphScope, Profile: selectedProfile(in.Profile), ProfileExtension: in.ProfileExtension, RepositoryID: repositoryID, SnapshotID: uuid.NewV7().String(), ManifestHash: mh, Manifest: in.Manifest, Inventory: in.Inventory, State: "collecting", Version: 1, CreatedAt: now, UpdatedAt: now}
		result.SourceScope, result.ScopeStatus, result.SyncPolicy = in.SourceScope, in.ScopeStatus, in.SyncPolicy
		result.ChangeManifest = in.ChangeManifest
		result.BaseVectorHash, result.SelectedPartition = baseSession.BaseVectorHash, baseSession.SelectedPartition
		if mode == "composed" {
			result.BasePartition = baseSession.BasePartition
			result.SelectedPartition = new(selectedSourcePartition(result))
		}
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_import_sessions(id,project_id,state,version,document) VALUES(?,?,?,?,?)`, result.ID, pid, result.State, result.Version, string(b))
		if err != nil {
			return err
		}
		// The row just inserted is already in the sum; reserving len(b) on top
		// counted it twice (review 2026-10-06, F83). Its begin receipt is not
		// staging (see stagingBytes).
		return r.checkStaging(ctx, tx, pid, 0)
	})
	if err != nil {
		return nil, err
	}
	if result.Mode == "" {
		result.Mode = "initial"
	}
	return result, nil
}

func importRepositoryID(in BeginImportInput) string {
	repositoryID := uuid.NewV7().String()
	if in.RepositoryID != nil {
		repositoryID = *in.RepositoryID
	}
	if in.SourceScope != nil && in.SourceScope.Kind != "add_repository" {
		repositoryID = in.SourceScope.RepositoryID
	}
	return repositoryID
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
		return r.putImportBatchTx(ctx, tx, pid, sid, bid, in, requestHash, result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repo) putImportBatchTx(ctx context.Context, tx *sql.Tx, pid, sid, bid string, in ImportBatchInput, requestHash string, result *BatchReceipt) error {
	started := time.Now()
	s, err := loadSession(ctx, tx, pid, sid)
	if err != nil {
		return err
	}
	var prior, response string
	if s.Mode == "composed" {
		for i, command := range in.Commands {
			if err := validateComposedWireMembers(command); err != nil {
				return importCommandError(err, i, command)
			}
		}
	}
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
	if err := validateImportBatchPayload(in); err != nil {
		return err
	}
	if err := requireSessionVersion(s, in.ExpectedImportVersion); err != nil {
		return err
	}
	if err := requireCollectible(s); err != nil {
		return err
	}
	for i, c := range in.Commands {
		if err := validateCommand(c, s); err != nil {
			return importCommandError(err, i, c)
		}
	}
	validated := time.Now()
	identities := newBatchIdentities(nil)
	if s.Mode == "reconcile" {
		identities.index, err = r.loadImportBaseIdentityIndex(ctx, tx, pid, s.BaseRevisionID)
		if err != nil {
			return err
		}
	}
	baseLoaded := time.Now()
	if s.Mode == "composed" {
		if err := putComposedCommands(ctx, tx, s, in.Commands, result); err != nil {
			return err
		}
	} else if err := stageImportCommands(ctx, tx, s, sid, in.Commands, identities, result); err != nil {
		return err
	}
	staged := time.Now()
	if err := recordImportBatch(ctx, tx, s, sid, bid, in.PayloadHash, requestHash, result); err != nil {
		return err
	}
	if err := r.checkImportBatchStorage(ctx, tx, pid, sid, s); err != nil {
		return err
	}
	// These are server phases inside the writer, not transport latency or a
	// publication receipt. No source text, evidence or request keys are logged.
	slog.DebugContext(ctx, "backend import batch staged", "mode", s.Mode, "commands", len(in.Commands), "validation_us", validated.Sub(started).Microseconds(), "base_identity_us", baseLoaded.Sub(validated).Microseconds(), "stage_us", staged.Sub(baseLoaded).Microseconds(), "accounting_us", time.Since(staged).Microseconds())
	return nil
}

func (r *Repo) checkImportBatchStorage(ctx context.Context, tx *sql.Tx, pid, sid string, s *ImportSession) error {
	if err := checkImportSessionLimits(ctx, tx, s, sid); err != nil {
		return err
	}
	return r.checkStaging(ctx, tx, pid, 0)
}

// validateImportBatchPayload bounds a new batch and binds it to the payload
// hash the client computed over the canonical commands.
func validateImportBatchPayload(in ImportBatchInput) error {
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
	return nil
}

// stageImportCommands reserves an identity for every addressed command of a
// non-composed batch and stages it as a record or a decision.
func stageImportCommands(ctx context.Context, tx *sql.Tx, s *ImportSession, sid string, commands []ImportCommand, identities *batchIdentities, result *BatchReceipt) error {
	seen := map[string]bool{}
	result.Identities = []RecordIdentity{}
	for i, c := range commands {
		typ, key, err := commandAddress(c)
		if err != nil {
			return importCommandError(err, i, c)
		}
		address := typ + "\x00" + key
		if seen[address] {
			return importCommandError(semantic("commands", "Duplicate addressed command in one batch"), i, c)
		}
		seen[address] = true
		if err := validateCommand(c, s); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		id, err := reserveIdentity(ctx, tx, s, c, typ, key, identities)
		if err != nil {
			return importCommandError(err, i, c)
		}
		if id != "" { // "" = remove of an unknown key (F87)
			result.Identities = append(result.Identities, RecordIdentity{RecordType: typ, ExternalKey: key, ID: id})
		}
		if err := writeImportCommand(ctx, tx, sid, c, typ, key, identities); err != nil {
			return err
		}
	}
	return nil
}

// writeImportCommand applies one command to the staging tables: a remove
// drops both the record and any decision, an identity or deletion decision
// is staged apart from records.
func writeImportCommand(ctx context.Context, tx *sql.Tx, sid string, c ImportCommand, typ, key string, identities *batchIdentities) error {
	switch {
	case c.Op == "remove":
		if _, err := tx.ExecContext(ctx, `DELETE FROM backend_import_decisions WHERE session_id=? AND record_type=? AND external_key=?`, sid, typ, key); err != nil {
			return err
		}
		identities.unstage(typ, key)
		_, err := tx.ExecContext(ctx, `DELETE FROM backend_import_records WHERE session_id=? AND record_type=? AND external_key=?`, sid, typ, key)
		return err
	case c.Identity != nil || c.Deletion != nil:
		b, err := json.Marshal(c)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_import_decisions(session_id,record_type,external_key,document) VALUES(?,?,?,?) ON CONFLICT(session_id,record_type,external_key) DO UPDATE SET document=excluded.document`, sid, typ, key, string(b))
		if err != nil {
			return err
		}
		identities.stage(c, typ, key)
		return nil
	default:
		b, err := json.Marshal(c)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_import_records(session_id,record_type,external_key,document) VALUES(?,?,?,?) ON CONFLICT(session_id,record_type,external_key) DO UPDATE SET document=excluded.document`, sid, typ, key, string(b))
		return err
	}
}

// recordImportBatch advances the session past the accepted batch, drops its
// stale previews and stores the receipt a replay of the batch returns.
func recordImportBatch(ctx context.Context, tx *sql.Tx, s *ImportSession, sid, bid, payloadHash, requestHash string, result *BatchReceipt) error {
	s.Version++
	s.AcceptedBatchCount++
	s.State = "collecting"
	s.CandidateHash = nil
	if _, err := tx.ExecContext(ctx, `DELETE FROM backend_import_previews WHERE session_id=?`, sid); err != nil {
		return err
	}
	s.UpdatedAt = time.Now().UTC()
	if err := saveSession(ctx, tx, s); err != nil {
		return err
	}
	result.BatchSummary = BatchSummary{BatchID: bid, PayloadHash: payloadHash, AcceptedVersion: s.Version}
	receipt, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_import_batches(session_id,batch_id,payload_hash,request_hash,accepted_version,receipt) VALUES(?,?,?,?,?,?)`, sid, bid, payloadHash, requestHash, s.Version, string(receipt))
	return err
}

// checkImportSessionLimits enforces the revision record and byte limits on
// everything the session has staged so far.
func checkImportSessionLimits(ctx context.Context, tx *sql.Tx, s *ImportSession, sid string) error {
	var nodes, edges, evidence int
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(record_type='node'),0),COALESCE(SUM(record_type='edge'),0),COALESCE(SUM(record_type='evidence'),0) FROM backend_import_records WHERE session_id=?`, sid).Scan(&nodes, &edges, &evidence)
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
	return nil
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
		if _, err := tx.ExecContext(ctx, `DELETE FROM backend_import_previews WHERE session_id=?`, sid); err != nil {
			return err
		}
		s.Version++
		s.UpdatedAt = time.Now().UTC()
		*result = *s
		// No staging check: Abort closes the session, so it only ever frees
		// staging, and it is the documented way out of the open-session limit.
		// It used to run checkStaging and was refused at the cap, leaving the
		// session stuck (review 2026-10-06, F83/F172).
		return saveSession(ctx, tx, s)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
