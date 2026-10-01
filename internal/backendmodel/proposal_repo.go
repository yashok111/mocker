package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"slices"
	"time"
	"uuid"
)

const proposalColumns = `id,project_id,version,name,status,base_revision_id,base_semantic_hash,repository_id,datastore_id,facet_key,draft_revision_id,draft_hash,created_at,updated_at`

func scanProposal(row interface{ Scan(...any) error }) (*Proposal, error) {
	p := new(Proposal)
	var created, updated string
	err := row.Scan(&p.ID, &p.ProjectID, &p.Version, &p.Name, &p.Status, &p.BaseRevisionID, &p.BaseSemanticHash, &p.RepositoryID, &p.DatastoreID, &p.FacetKey, &p.DraftRevisionID, &p.DraftHash, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	p.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return nil, err
	}
	p.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	return p, err
}

func loadProposal(ctx context.Context, q importReader, pid, proposalID string) (*Proposal, error) {
	if !ValidID(pid) || !ValidID(proposalID) {
		return nil, notFound()
	}
	return scanProposal(q.QueryRowContext(ctx, `SELECT `+proposalColumns+` FROM backend_proposals WHERE project_id=? AND id=?`, pid, proposalID))
}

func loadProposalRevision(ctx context.Context, q importReader, proposalID, revisionID string) (*ProposalRevision, error) {
	if !ValidID(revisionID) {
		return nil, notFound()
	}
	var doc string
	err := q.QueryRowContext(ctx, `SELECT document FROM backend_proposal_revisions WHERE proposal_id=? AND id=?`, proposalID, revisionID).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	rev := new(ProposalRevision)
	if err := json.Unmarshal([]byte(doc), rev); err != nil {
		return nil, err
	}
	if rev.DocumentVersion != ProposalDocumentVersion {
		return nil, &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Unsupported proposal document version"}
	}
	return rev, nil
}

func relationalUnavailable() error {
	return &FaultError{Status: 422, Code: "backend_relational_unavailable", Message: "Select a schema2 source revision, relational datastore and existing source facet"}
}

// Use the strict source decoder on the pinned source record; intent is never
// passed off as an imported facet or a new source observation.
func proposalBase(ctx context.Context, q importReader, pid string, in CreateProposalInput) (*RevisionState, error) {
	state, err := loadSourceState(ctx, q, pid, in.BaseRevisionID)
	if err != nil {
		return nil, err
	}
	if state.Revision.SchemaVersion != "2" || len(state.Sources) == 0 {
		return nil, relationalUnavailable()
	}
	p, err := scanProject(q.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id=?`, pid))
	if err != nil {
		return nil, err
	}
	if len(p.Repositories) != 1 || p.Repositories[0].ID != in.RepositoryID {
		return nil, notFound()
	}
	if !slices.ContainsFunc(state.Sources, func(s SourceSnapshot) bool { return s.RepositoryID == in.RepositoryID }) {
		return nil, notFound()
	}
	var raw string
	err = q.QueryRowContext(ctx, `SELECT document FROM backend_graph_records WHERE project_id=? AND revision_id=? AND record_type='node' AND id=?`, pid, in.BaseRevisionID, in.DatastoreID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	var ds Node
	if err := json.Unmarshal([]byte(raw), &ds); err != nil {
		return nil, err
	}
	if ds.Kind != "datastore" || !relationalSubject(ds.Kind, ds.Attributes, false) {
		return nil, relationalUnavailable()
	}
	if ds.Ownership != nil && ds.Ownership.RepositoryID != in.RepositoryID {
		return nil, notFound()
	}
	fs, _, err := relationalFacetObject(ds.Kind, ds.Attributes)
	if err != nil {
		return nil, err
	}
	if fs[in.FacetKey] == nil {
		return nil, relationalUnavailable()
	}
	if _, err := decodeRelationalFacet(ds.Kind, fs[in.FacetKey], true); err != nil {
		return nil, err
	}
	return state, nil
}

func readProposalReceipt(ctx context.Context, q importReader, scope, key, digest string, result any) (bool, error) {
	var hash, raw string
	err := q.QueryRowContext(ctx, `SELECT request_hash,response FROM backend_command_receipts WHERE scope=? AND key=?`, scope, key).Scan(&hash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if hash != digest {
		return true, &FaultError{Status: 409, Code: "backend_idempotency_conflict", Message: "Idempotency key was already used with a different request"}
	}
	if err := json.Unmarshal([]byte(raw), result); err != nil {
		return true, err
	}
	switch out := result.(type) {
	case *ProposalDetail:
		out.receiptJSON = raw
	case *ProposalApplyResult:
		out.receiptJSON = raw
	}
	return true, nil
}

func (r *Repo) CreateProposal(ctx context.Context, pid string, in CreateProposalInput) (*ProposalDetail, error) {
	if !ValidID(pid) {
		return nil, notFound()
	}
	name, err := normalizeName(in.Name)
	if err != nil {
		return nil, err
	}
	in.Name = name
	if err := validateKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	if !externalKey(in.FacetKey) {
		return nil, invalid("facetKey", "Use a valid relational facet key")
	}
	if !ValidID(in.BaseRevisionID) || !ValidID(in.RepositoryID) || !ValidID(in.DatastoreID) {
		return nil, notFound()
	}
	digest, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	result := new(ProposalDetail)
	scope := "proposal-create:" + pid
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		found, err := readProposalReceipt(ctx, tx, scope, in.IdempotencyKey, digest, result)
		if err != nil || found {
			return err
		}
		state, err := proposalBase(ctx, tx, pid, in)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		p := Proposal{ID: uuid.NewV7().String(), ProjectID: pid, Version: 1, Name: name, Status: "draft", BaseRevisionID: in.BaseRevisionID, BaseSemanticHash: state.Revision.SemanticHash, RepositoryID: in.RepositoryID, DatastoreID: in.DatastoreID, FacetKey: in.FacetKey, DraftRevisionID: uuid.NewV7().String(), CreatedAt: now, UpdatedAt: now}
		rev := ProposalRevision{ID: p.DraftRevisionID, ProposalID: p.ID, DocumentVersion: ProposalDocumentVersion, BaseRevisionID: p.BaseRevisionID, BaseSemanticHash: p.BaseSemanticHash, SourceSnapshotIDs: slices.Clone(state.Revision.SourceSnapshotIDs), ArtifactPins: slices.Clone(state.Revision.ArtifactPins), Commands: []ProposalCommand{}, Overlays: []ProposalOverlay{}, Criteria: []ProposalCriterion{}, Author: "user", Summary: "Empty database proposal", CreatedAt: now}
		// Identity and time of the result revision are outside the semantic domain.
		semantic := struct {
			DocumentVersion   string              `json:"documentVersion"`
			ProposalID        string              `json:"proposalId"`
			BaseRevisionID    string              `json:"baseRevisionId"`
			BaseSemanticHash  string              `json:"baseSemanticHash"`
			RepositoryID      string              `json:"repositoryId"`
			DatastoreID       string              `json:"datastoreId"`
			FacetKey          string              `json:"facetKey"`
			SourceSnapshotIDs []string            `json:"sourceSnapshotIds"`
			ArtifactPins      []ArtifactPin       `json:"artifactPins"`
			Commands          []ProposalCommand   `json:"commands"`
			Overlays          []ProposalOverlay   `json:"overlays"`
			Criteria          []ProposalCriterion `json:"criteria"`
		}{ProposalDocumentVersion, p.ID, p.BaseRevisionID, p.BaseSemanticHash, p.RepositoryID, p.DatastoreID, p.FacetKey, rev.SourceSnapshotIDs, rev.ArtifactPins, rev.Commands, rev.Overlays, rev.Criteria}
		rev.SemanticHash, err = requestDigest(semantic)
		if err != nil {
			return err
		}
		p.DraftHash = rev.SemanticHash
		doc, err := json.Marshal(rev)
		if err != nil {
			return err
		}
		if err := r.checkProposalStaging(ctx, tx, pid, int64(len(doc))); err != nil {
			return err
		}
		stamp := now.Format(time.RFC3339Nano)
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_proposals (`+proposalColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.ID, p.ProjectID, p.Version, p.Name, p.Status, p.BaseRevisionID, p.BaseSemanticHash, p.RepositoryID, p.DatastoreID, p.FacetKey, p.DraftRevisionID, p.DraftHash, stamp, stamp)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_proposal_revisions(id,proposal_id,parent_revision_id,document) VALUES(?,?,NULL,?)`, rev.ID, p.ID, string(doc))
		if err != nil {
			return err
		}
		detail, err := proposalDetail(ctx, tx, pid, p, rev, GetProposalInput{})
		if err != nil {
			return err
		}
		*result = *detail
		response, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_command_receipts(scope,key,request_hash,response) VALUES(?,?,?,?)`, scope, in.IdempotencyKey, digest, string(response))
		if err == nil {
			result.receiptJSON = string(response)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repo) ListProposals(ctx context.Context, pid string, in ProposalListInput) (*ProposalPage, error) {
	if _, err := r.Get(ctx, pid); err != nil {
		return nil, err
	}
	if in.Status != "" && in.Status != "draft" {
		return nil, &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Only draft proposals are supported"}
	}
	if in.BaseRevisionID != "" && !ValidID(in.BaseRevisionID) {
		return nil, invalid("baseRevisionId", "Use a canonical revision UUID")
	}
	scope, err := requestDigest(struct{ BaseRevisionID, Status string }{in.BaseRevisionID, in.Status})
	if err != nil {
		return nil, err
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "proposals", pid, scope, true)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.R.QueryContext(ctx, `SELECT `+proposalColumns+` FROM backend_proposals WHERE project_id=? AND id>? AND (?='' OR base_revision_id=?) AND (?='' OR status=?) ORDER BY id LIMIT ?`, pid, after, in.BaseRevisionID, in.BaseRevisionID, in.Status, in.Status, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &ProposalPage{Items: []Proposal{}}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		if len(out.Items) == limit {
			out.NextCursor = encodeGraphPage("proposals", pid, scope, after)
			break
		}
		out.Items = append(out.Items, *p)
		after = p.ID
	}
	return out, rows.Err()
}

func (r *Repo) GetProposal(ctx context.Context, pid, proposalID string, in GetProposalInput) (*ProposalDetail, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	p, err := loadProposal(ctx, tx, pid, proposalID)
	if err != nil {
		return nil, err
	}
	rid := in.ProposalRevisionID
	if rid == "" {
		rid = p.DraftRevisionID
	}
	rev, err := loadProposalRevision(ctx, tx, p.ID, rid)
	if err != nil {
		return nil, err
	}
	return proposalDetail(ctx, tx, pid, *p, *rev, in)
}

func proposalDetail(ctx context.Context, q importReader, pid string, p Proposal, rev ProposalRevision, in GetProposalInput) (*ProposalDetail, error) {
	scope, err := requestDigest(struct{ ProposalID, DraftRevisionID, DraftHash, SelectedRevisionID, SelectedHash string }{p.ID, p.DraftRevisionID, p.DraftHash, rev.ID, rev.SemanticHash})
	if err != nil {
		return nil, err
	}
	limit, after, err := decodeGraphPage(in.Limit, in.Cursor, "proposal-history", pid, scope, true)
	if err != nil {
		return nil, err
	}
	out := &ProposalDetail{Proposal: p, Revision: rev, History: []ProposalRevisionSummary{}}
	out.EffectiveGraphHash, err = proposalEffectiveGraphHash(p, rev.Overlays)
	if err != nil {
		return nil, err
	}
	err = q.QueryRowContext(ctx, `SELECT p.current_revision_id,json_extract(r.document,'$.semanticHash') FROM backend_projects p JOIN backend_revisions r ON r.id=p.current_revision_id AND r.project_id=p.id WHERE p.id=?`, pid).Scan(&out.CurrentSourceRevisionID, &out.CurrentSourceSemanticHash)
	if err != nil {
		return nil, err
	}
	out.BaseOutdated = out.CurrentSourceRevisionID != p.BaseRevisionID
	// Bind pagination to current and selected draft pins within the same read
	// snapshot, so a later save cannot silently splice two histories together.
	rows, err := q.QueryContext(ctx, `SELECT document FROM backend_proposal_revisions WHERE proposal_id=? AND id>? ORDER BY id LIMIT ?`, p.ID, after, limit+1)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		var summary ProposalRevisionSummary
		if err := json.Unmarshal([]byte(raw), &summary); err != nil {
			rows.Close()
			return nil, err
		}
		if len(out.History) == limit {
			out.NextCursor = encodeGraphPage("proposal-history", pid, scope, after)
			break
		}
		out.History = append(out.History, summary)
		after = summary.ID
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var receipt string
	err = q.QueryRowContext(ctx, `SELECT response FROM backend_command_receipts WHERE scope=? AND json_extract(response,'$.revision.id')=? ORDER BY key LIMIT 1`, "proposal-apply:"+pid+":"+p.ID, p.DraftRevisionID).Scan(&receipt)
	if err == nil {
		out.LastApplyReceipt = []byte(receipt)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return out, nil
}
