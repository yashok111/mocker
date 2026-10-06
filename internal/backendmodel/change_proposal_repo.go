package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"math"
	"slices"
	"time"
	"uuid"
)

const changeProposalColumns = `id,project_id,name,version,status,current_draft_revision_id,current_draft_hash,created_at,updated_at,ready_reference`

func scanChangeProposal(row interface{ Scan(...any) error }) (*ChangeProposal, error) {
	p := new(ChangeProposal)
	var created, updated string
	var ready *string
	err := row.Scan(&p.ID, &p.ProjectID, &p.Name, &p.Version, &p.Status, &p.CurrentDraftRevisionID, &p.CurrentDraftHash, &created, &updated, &ready)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	if ready != nil {
		if err = decodeLifecycleAssociation([]byte(*ready), p); err != nil {
			return nil, err
		}
	}
	p.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, err
	}
	p.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	return p, err
}
func loadChangeProposal(ctx context.Context, q importReader, pid, id string) (*ChangeProposal, error) {
	if !ValidID(pid) || !ValidID(id) {
		return nil, notFound()
	}
	return scanChangeProposal(q.QueryRowContext(ctx, `SELECT `+changeProposalColumns+` FROM backend_change_proposals WHERE project_id=? AND id=?`, pid, id))
}
func loadChangeProposalRevision(ctx context.Context, q importReader, pid, id, rid string) (*ChangeProposalRevision, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT document FROM backend_change_proposal_revisions WHERE project_id=? AND proposal_id=? AND id=?`, pid, id, rid).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	var out ChangeProposalRevision
	if err = json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if out.DocumentVersion != ChangeProposalDocumentVersion {
		return nil, invalid("documentVersion", "Unsupported proposal version")
	}
	return &out, nil
}
func readChangeReceipt(ctx context.Context, q importReader, scope, key, digest string, out any) (bool, error) {
	var hash, raw string
	err := q.QueryRowContext(ctx, `SELECT request_hash,response FROM backend_command_receipts WHERE scope=? AND key=?`, scope, key).Scan(&hash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if hash != digest {
		return true, &FaultError{Status: 409, Code: "backend_idempotency_conflict", Message: "Idempotency key was used for a different request"}
	}
	if err = json.Unmarshal([]byte(raw), out); err != nil {
		return true, err
	}
	switch v := out.(type) {
	case *ChangeProposalDetail:
		v.receiptJSON = raw
	case *ChangeProposalApplyResult:
		v.receiptJSON = raw
	}
	return true, nil
}
func requireChangeCAS(p *ChangeProposal, version int64, rid string) error {
	if p.Version != version || p.Version == math.MaxInt64 || p.CurrentDraftRevisionID != rid {
		return &FaultError{Status: 409, Code: "backend_change_version_conflict", Message: "Proposal version or selected draft changed", CurrentVersion: p.Version, Details: map[string]any{"proposalRevisionId": p.CurrentDraftRevisionID, "semanticHash": p.CurrentDraftHash}}
	}
	return nil
}
func requireChangeDraft(p *ChangeProposal, version int64, rid string) error {
	if err := requireChangeCAS(p, version, rid); err != nil {
		return err
	}
	if p.Status != "draft" && p.Status != "ready" {
		return &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Only draft or ready proposals may be edited"}
	}
	return nil
}

func emptyChangeDelta() ChangeDelta {
	return ChangeDelta{Created: []ChangeCreatedRecord{}, Removed: []ChangeRemoval{}, Properties: []ChangeProperty{}, IdentityIntents: []ChangeIdentityIntent{}, ArtifactIntents: []ChangeArtifactIntent{}, EdgeNames: []ChangeEdgeName{}}
}
func (r *Repo) CreateChangeProposal(ctx context.Context, pid string, in CreateChangeProposalInput) (*ChangeProposalDetail, error) {
	if !ValidID(pid) {
		return nil, notFound()
	}
	if err := validateKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	digest, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	scope := "change-proposal-create:" + pid
	out := new(ChangeProposalDetail)
	if found, err := readChangeReceipt(ctx, r.db.R, scope, in.IdempotencyKey, digest, out); err != nil || found {
		return out, err
	}
	prepared, prepareErr := r.prepareChangeCreate(ctx, pid, in)
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		if found, err := readChangeReceipt(ctx, tx, scope, in.IdempotencyKey, digest, out); err != nil || found {
			return err
		}
		if prepareErr != nil {
			return prepareErr
		}
		now := time.Now().UTC()
		p := ChangeProposal{ID: uuid.NewV7().String(), ProjectID: pid, Name: in.Name, Version: 1, Status: "draft", CurrentDraftRevisionID: uuid.NewV7().String(), CurrentDraftHash: prepared.SemanticHash, CreatedAt: now, UpdatedAt: now}
		rev := *prepared
		rev.ID, rev.ProposalID, rev.AcceptedBatchRevisionID, rev.CreatedAt = p.CurrentDraftRevisionID, p.ID, p.CurrentDraftRevisionID, now
		stamp := now.Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_change_proposals (`+changeProposalColumns+`) VALUES(?,?,?,?,?,?,?,?,?,NULL)`, p.ID, pid, p.Name, p.Version, p.Status, p.CurrentDraftRevisionID, p.CurrentDraftHash, stamp, stamp); err != nil {
			return err
		}
		if err := persistChangeRevision(ctx, tx, p, rev, "create", "", []ChangeProposalCommand{}, nil); err != nil {
			return err
		}
		detail, err := changeProposalDetail(ctx, tx, p, rev, GetChangeProposalInput{})
		if err != nil {
			return err
		}
		*out = *detail
		response, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if err = writeChangeReceipt(ctx, tx, scope, in.IdempotencyKey, digest, string(response)); err != nil {
			return err
		}
		if err = checkChangeProposalQuota(ctx, tx, pid, p.ID); err != nil {
			return err
		}
		out.receiptJSON = string(response)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (r *Repo) prepareChangeCreate(ctx context.Context, pid string, in CreateChangeProposalInput) (*ChangeProposalRevision, error) {
	if _, err := normalizeName(in.Name); err != nil {
		return nil, err
	}
	if !ValidID(in.BaseRevisionID) {
		return nil, notFound()
	}
	bytes, err := changeSourceInputBytes(ctx, r.db.R, pid, in.BaseRevisionID)
	if err != nil {
		return nil, err
	}
	reservation, err := r.reserveChangeInput(ctx, pid, bytes)
	if err != nil {
		return nil, err
	}
	defer reservation.Release()
	source, err := loadComposedBase(ctx, r.db.R, pid, in.BaseRevisionID)
	if err != nil {
		return nil, err
	}
	schema := source.State.Revision.SchemaVersion
	if schema != EventsSchemaVersion && schema != ComposedSchemaVersion {
		return nil, &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Full proposals require an explicitly selected source5 or source6 baseline"}
	}
	context := revisionArtifactContext(&source.State)
	if context == nil {
		content, anchor, err := artifactSourceAnchors(ctx, r.db.R, &source.State)
		if err != nil {
			return nil, err
		}
		context = &ArtifactContext{SourceContentHash: content, SourceSemanticHash: anchor, APIBindings: []APIArtifactBinding{}, EditorBindings: []EditorBinding{}}
	}
	rev := &ChangeProposalRevision{DocumentVersion: ChangeProposalDocumentVersion, BaseRevisionID: in.BaseRevisionID, BaseSemanticHash: source.State.Revision.SemanticHash, BaseSchemaVersion: schema, SourceSnapshotIDs: slices.Clone(source.State.Revision.SourceSnapshotIDs), SourceVector: *source.SourceVector, ArtifactPins: slices.Clone(source.State.Revision.ArtifactPins), ArtifactContext: *context, Delta: emptyChangeDelta(), Criteria: []ChangeCriterion{}, Author: "user", Summary: "Empty full graph proposal"}
	if source.State.ArtifactContextV3 != nil {
		rev.ArtifactContextV3 = source.State.ArtifactContextV3
		rev.ArtifactContext = ArtifactContext{}
	}
	evaluation, err := newChangeEvaluation(source, *rev, map[string]ChangeObjectIdentity{})
	if err != nil {
		return nil, err
	}
	snapshot, diagnostics, err := evaluation.validate(ctx)
	if err != nil {
		return nil, err
	}
	if len(diagnostics) > 0 {
		return nil, changeInvalid(diagnostics)
	}
	rev.SemanticHash, err = changeSemanticHash(snapshot)
	return rev, err
}
func writeChangeReceipt(ctx context.Context, tx *sql.Tx, scope, key, digest, response string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO backend_command_receipts(scope,key,request_hash,response) VALUES(?,?,?,?)`, scope, key, digest, response)
	return err
}
func changeInvalid(d []ImportDiagnostic) error {
	return &FaultError{Status: 422, Code: "backend_change_invalid", Message: "Desired graph has unresolved validation diagnostics", Details: map[string]any{"diagnostics": d}}
}

func persistChangeRevision(ctx context.Context, tx *sql.Tx, p ChangeProposal, rev ChangeProposalRevision, action, restore string, commands []ChangeProposalCommand, identities []ChangeObjectIdentity) error {
	doc, err := json.Marshal(rev)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO backend_change_proposal_revisions(id,project_id,proposal_id,parent_revision_id,base_revision_id,document) VALUES(?,?,?,?,?,?)`, rev.ID, p.ProjectID, p.ID, rev.ParentRevisionID, rev.BaseRevisionID, string(doc)); err != nil {
		return err
	}
	commandJSON, err := canonicalJSON(commands)
	if err != nil {
		return err
	}
	batch := ChangeAppliedBatch{ProposalID: p.ID, RevisionID: rev.ID, Action: action, RestoreRevisionID: restore, Commands: commands, CommandsHash: hashBytes(commandJSON)}
	batchJSON, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	var restoreValue *string
	if restore != "" {
		restoreValue = new(restore)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO backend_change_proposal_batches(project_id,proposal_id,revision_id,action,restore_revision_id,commands,commands_hash,document) VALUES(?,?,?,?,?,?,?,?)`, p.ProjectID, p.ID, rev.ID, action, restoreValue, string(commandJSON), batch.CommandsHash, string(batchJSON)); err != nil {
		return err
	}
	for position, c := range commands {
		raw, err := canonicalJSON(c)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO backend_change_proposal_commands(proposal_id,command_id,revision_id,position,document) VALUES(?,?,?,?,?)`, p.ID, c.CommandID, rev.ID, position, string(raw)); err != nil {
			return err
		}
	}
	for _, identity := range identities {
		identity.FirstRevisionID = rev.ID
		raw, err := json.Marshal(identity)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO backend_change_proposal_identities(project_id,proposal_id,id,record_type,kind,first_revision_id,document) VALUES(?,?,?,?,?,?,?)`, p.ProjectID, p.ID, identity.ID, identity.RecordType, identity.Kind, rev.ID, string(raw)); err != nil {
			return err
		}
	}
	event, err := json.Marshal(struct {
		Action            string    `json:"action"`
		RevisionID        string    `json:"revisionId"`
		RestoreRevisionID string    `json:"restoreRevisionId,omitempty"`
		CreatedAt         time.Time `json:"createdAt"`
	}{Action: action, RevisionID: rev.ID, RestoreRevisionID: restore, CreatedAt: rev.CreatedAt})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_change_proposal_events(project_id,proposal_id,revision_id,version,document) VALUES(?,?,?,?,?)`, p.ProjectID, p.ID, rev.ID, p.Version, string(event))
	return err
}
func checkChangeProposalQuota(ctx context.Context, q importReader, pid, id string) error {
	var proposals, revisions int
	var retained int64
	err := q.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM backend_change_proposals WHERE project_id=?),(SELECT count(*) FROM backend_change_proposal_revisions WHERE proposal_id=?),
 (SELECT COALESCE(sum(length(CAST(document AS BLOB))),0) FROM backend_change_proposal_revisions WHERE project_id=?)+
 (SELECT COALESCE(sum(length(CAST(document AS BLOB))),0) FROM backend_change_proposal_events WHERE project_id=?)+
 (SELECT COALESCE(sum(length(CAST(document AS BLOB))),0) FROM backend_change_proposal_identities WHERE project_id=?)+
 (SELECT COALESCE(sum(length(CAST(document AS BLOB))+length(CAST(commands AS BLOB))),0) FROM backend_change_proposal_batches WHERE project_id=?)+
 (SELECT COALESCE(sum(length(CAST(c.document AS BLOB))),0) FROM backend_change_proposal_commands c JOIN backend_change_proposals p ON p.id=c.proposal_id WHERE p.project_id=?)+
 (SELECT COALESCE(sum(length(CAST(ready_reference AS BLOB))),0) FROM backend_change_proposals WHERE project_id=? AND json_extract(ready_reference,'$.documentVersion')='backend-change-lifecycle/v1')+
 (SELECT COALESCE(sum(length(CAST(response AS BLOB))),0) FROM backend_command_receipts WHERE scope=? OR scope LIKE ?)`, pid, id, pid, pid, pid, pid, pid, pid, "change-proposal-create:"+pid, "change-proposal-%:"+pid+":%").Scan(&proposals, &revisions, &retained)
	if err != nil {
		return err
	}
	if proposals > MaxChangeProposals || revisions > MaxChangeProposalRevisions || retained > MaxChangeProposalBytes {
		return &FaultError{Status: 409, Code: "backend_change_quota_exceeded", Message: "Full proposal retention quota exceeded"}
	}
	return nil
}

func (r *Repo) GetChangeProposal(ctx context.Context, pid, id string, in GetChangeProposalInput) (*ChangeProposalDetail, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := loadChangeProposal(ctx, tx, pid, id)
	if err != nil {
		return nil, err
	}
	rid := in.ProposalRevisionID
	if rid == "" {
		rid = p.CurrentDraftRevisionID
	}
	rev, err := loadChangeProposalRevision(ctx, tx, pid, id, rid)
	if err != nil {
		return nil, err
	}
	return changeProposalDetail(ctx, tx, *p, *rev, in)
}
func changeProposalDetail(ctx context.Context, q importReader, p ChangeProposal, rev ChangeProposalRevision, in GetChangeProposalInput) (*ChangeProposalDetail, error) {
	scope, err := requestDigest(struct {
		ID       string
		Version  int64
		Selected string
		Hash     string
	}{ID: p.ID, Version: p.Version, Selected: rev.ID, Hash: rev.SemanticHash})
	if err != nil {
		return nil, err
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "change-history", p.ProjectID, scope, true)
	if err != nil {
		return nil, err
	}
	out := &ChangeProposalDetail{Proposal: p, Revision: rev, History: []ProposalRevisionSummary{}}
	err = q.QueryRowContext(ctx, `SELECT p.current_revision_id,json_extract(r.document,'$.semanticHash') FROM backend_projects p JOIN backend_revisions r ON r.id=p.current_revision_id AND r.project_id=p.id WHERE p.id=?`, p.ProjectID).Scan(&out.CurrentSourceRevisionID, &out.CurrentSourceSemanticHash)
	if err != nil {
		return nil, err
	}
	out.BaseOutdated = out.CurrentSourceRevisionID != rev.BaseRevisionID
	rows, err := q.QueryContext(ctx, `SELECT document FROM backend_change_proposal_revisions WHERE proposal_id=? AND id>? ORDER BY id LIMIT ?`, p.ID, after, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if len(out.History) == limit {
			out.NextCursor = encodeGraphPage("change-history", p.ProjectID, scope, after)
			break
		}
		var summary ProposalRevisionSummary
		if err = json.Unmarshal([]byte(raw), &summary); err != nil {
			return nil, err
		}
		out.History = append(out.History, summary)
		after = summary.ID
	}
	return out, rows.Err()
}
func (r *Repo) ListChangeProposals(ctx context.Context, pid string, in ChangeProposalListInput) (*ChangeProposalPage, error) {
	if _, err := r.Get(ctx, pid); err != nil {
		return nil, err
	}
	if in.Status != "" && !slices.Contains([]string{"draft", "ready", "implemented", "archived"}, in.Status) {
		return nil, &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Only draft and ready proposals are supported"}
	}
	if in.BaseRevisionID != "" && !ValidID(in.BaseRevisionID) {
		return nil, invalid("baseRevisionId", "Expected canonical UUID")
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	version, err := changeProposalListVersion(ctx, tx, pid)
	if err != nil {
		return nil, err
	}
	scope, err := requestDigest(struct {
		Base, Status string
		Version      string
	}{Base: in.BaseRevisionID, Status: in.Status, Version: version})
	if err != nil {
		return nil, err
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "change-proposals", pid, scope, true)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+changeProposalColumns+` FROM backend_change_proposals WHERE project_id=? AND id>? AND (?='' OR status=?) AND (?='' OR current_draft_revision_id IN (SELECT id FROM backend_change_proposal_revisions WHERE base_revision_id=?)) ORDER BY id LIMIT ?`, pid, after, in.Status, in.Status, in.BaseRevisionID, in.BaseRevisionID, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := &ChangeProposalPage{Items: []ChangeProposal{}}
	for rows.Next() {
		p, err := scanChangeProposal(rows)
		if err != nil {
			return nil, err
		}
		if len(out.Items) == limit {
			out.NextCursor = encodeGraphPage("change-proposals", pid, scope, after)
			break
		}
		out.Items = append(out.Items, *p)
		after = p.ID
	}
	return out, rows.Err()
}

func changeProposalListVersion(ctx context.Context, q importReader, pid string) (string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,version FROM backend_change_proposals WHERE project_id=? ORDER BY id`, pid)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	type entry struct {
		ID      string
		Version int64
	}
	versions := []entry{}
	for rows.Next() {
		var value entry
		if err = rows.Scan(&value.ID, &value.Version); err != nil {
			return "", err
		}
		versions = append(versions, value)
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return requestDigest(versions)
}
