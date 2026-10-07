package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"maps"
	"slices"
	"sync"

	"github.com/yashok111/mocker/internal/store"
)

type analysisLeaseKey struct{}

// AnalysisInputReservation holds pair memory until all preparation is finished.
// Release is idempotent; its context becomes invalid after release.
type AnalysisInputReservation struct {
	mu                       sync.Mutex
	repo                     *Repo
	pid                      string
	released                 bool
	documents                map[string]int64
	reservation              *store.TransientReservation
	inputBytes, chargedBytes int64
}

func (l *AnalysisInputReservation) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.released {
		l.released = true
		l.reservation.Release()
	}
}
func analysisLease(ctx context.Context) *AnalysisInputReservation {
	l, _ := ctx.Value(analysisLeaseKey{}).(*AnalysisInputReservation)
	return l
}
func (l *AnalysisInputReservation) valid(r *Repo, pid string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released || l.repo != r || l.pid != pid {
		return invalid("analysisInput", "Invalid or released preparation reservation")
	}
	return nil
}
func (l *AnalysisInputReservation) admit(key string, size int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return invalid("analysisInput", "Preparation reservation is released")
	}
	if allowed, ok := l.documents[key]; !ok || size > allowed {
		return limitFault("Document was not admitted in analysis pair footprint")
	}
	return nil
}
func (r *Repo) ReserveAnalysisInput(ctx context.Context, pid string, f AnalysisInputFootprint) (context.Context, *AnalysisInputReservation, error) {
	if analysisLease(ctx) != nil {
		return ctx, nil, invalid("analysisInput", "Preparation reservation cannot be nested")
	}
	seal, err := requestDigest(f.Documents)
	if err != nil {
		return ctx, nil, err
	}
	var total int64
	docs := map[string]int64{}
	for _, d := range f.Documents {
		if d.Bytes < 0 || d.Bytes > MaxRevisionBytes || docs[d.Key] != 0 {
			return ctx, nil, invalid("footprint", "Invalid footprint")
		}
		total += d.Bytes
		docs[d.Key] = d.Bytes
	}
	if f.repo != r || f.projectID != pid || seal != f.seal || total != f.TotalBytes || total > MaxRevisionBytes {
		return ctx, nil, invalid("footprint", "Footprint must be an unchanged server admission")
	}
	reservation, err := r.reserveChangeInput(ctx, pid, total)
	if err != nil {
		return ctx, nil, err
	}
	l := &AnalysisInputReservation{repo: r, pid: pid, documents: docs, reservation: reservation, inputBytes: total, chargedBytes: 4*total + MaxChangeProposalCommandBytes}
	return context.WithValue(ctx, analysisLeaseKey{}, l), l, nil
}
func (r *Repo) validateAnalysisLeaseTarget(ctx context.Context, pid string, target BackendReadTarget) error {
	l := analysisLease(ctx)
	if l == nil {
		return nil
	}
	if err := l.valid(r, pid); err != nil {
		return err
	}
	f, err := r.EffectiveGraphInputFootprint(ctx, pid, target)
	if err != nil {
		return err
	}
	for _, d := range f.Documents {
		if err = l.admit(d.Key, d.Bytes); err != nil {
			return err
		}
	}
	return nil
}

type analysisFootprintBuilder struct {
	ctx       context.Context
	q         importReader
	pid       string
	documents map[string]int64
	total     int64
}

func (b *analysisFootprintBuilder) add(key string, size int64) error {
	if _, ok := b.documents[key]; ok {
		return nil
	}
	if size < 0 || size > MaxRevisionBytes || b.total > MaxRevisionBytes-size {
		return &FaultError{Status: 413, Code: "backend_analysis_input_limit", Message: "Combined immutable analysis inputs exceed 256 MiB", Details: map[string]any{"actualBytes": b.total + size, "allowedBytes": MaxRevisionBytes}}
	}
	b.documents[key] = size
	b.total += size
	return nil
}

// footprintRow answers a single-row lookup that found nothing with the given
// 404. Review 2026-10-06, F117: these scans run first on caller-supplied ids
// (fromRevisionId, a change-proposal or legacy proposal revision, a pinned
// owner) with no prior existence check, and the bare sql.ErrNoRows reached
// backendError as a logged 500 backend_internal.
func footprintRow(err, missing error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return missing
	}
	return err
}

// changeRevisionNotFound is the answer AnalysisProposalBaseRevision already
// gives for the same lookup.
func changeRevisionNotFound() *FaultError {
	return &FaultError{Status: 404, Code: "backend_change_not_found", Message: "Change proposal revision not found"}
}

func (b *analysisFootprintBuilder) source(rid string) error {
	key := "source:" + rid
	if _, ok := b.documents[key]; ok {
		return nil
	}
	size, err := changeSourceInputBytes(b.ctx, b.q, b.pid, rid)
	if err != nil {
		return err
	}
	var metadata int64
	if err = b.q.QueryRowContext(b.ctx, `SELECT length(CAST(document AS BLOB)) FROM backend_revisions_documents WHERE project_id=? AND id=?`, b.pid, rid).Scan(&metadata); err != nil {
		return footprintRow(err, notFound())
	}
	if err = b.add(key, size); err != nil {
		return err
	}
	if err = b.add("revision:"+rid, metadata); err != nil {
		return err
	}
	if err = b.pins(`SELECT value FROM backend_revisions_documents r,json_each(r.document,'$.artifactPins') WHERE r.project_id=? AND r.id=?`, b.pid, rid); err != nil {
		return err
	}
	rows, err := b.q.QueryContext(b.ctx, `SELECT DISTINCT value FROM backend_graph_records_documents,json_tree(document) WHERE project_id=? AND revision_id=? AND key='historicalRevisionId' AND type='text' UNION SELECT source_revision_id FROM backend_revision_legacy_proof_bases_documents WHERE project_id=? AND revision_id=?`, b.pid, rid, b.pid, rid)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var refs []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return err
		}
		refs = append(refs, id)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range refs {
		if err = b.source(id); err != nil {
			return err
		}
	}
	return nil
}
func (b *analysisFootprintBuilder) pins(query string, args ...any) error {
	rows, err := b.q.QueryContext(b.ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var pins []ArtifactPin
	for rows.Next() {
		var raw string
		var pin ArtifactPin
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(raw), &pin); err != nil {
			return err
		}
		pins = append(pins, pin)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, pin := range pins {
		if err = b.owner(pin.Kind, pin.ID, pin.RevisionID); err != nil {
			return err
		}
	}
	return nil
}
func (b *analysisFootprintBuilder) owner(kind, id, rid string) error {
	key := kind + ":" + id + ":" + rid
	if _, ok := b.documents[key]; ok {
		return nil
	}
	if !ValidAPIArtifactID(id) || !ValidAPIArtifactID(rid) {
		return invalid("artifact", "Expected exact owner IDs")
	}
	var size int64
	var err error
	switch kind {
	case "api_design":
		err = b.q.QueryRowContext(b.ctx, `SELECT length(CAST(document AS BLOB)) FROM api_design_revisions WHERE design_id=? AND id=?`, id, rid).Scan(&size)
	case "design_scenario":
		err = b.q.QueryRowContext(b.ctx, `SELECT length(CAST(document AS BLOB))+length(CAST(form_drafts AS BLOB)) FROM design_scenario_revisions WHERE scenario_id=? AND id=?`, id, rid).Scan(&size)
	default:
		return invalid("artifact", "Unsupported owner kind")
	}
	if err != nil {
		return footprintRow(err, notFound())
	}
	if err = b.add(key, size); err != nil {
		return err
	}
	if kind != "design_scenario" {
		return nil
	}
	rows, err := b.q.QueryContext(b.ctx, `SELECT CAST(json_extract(value,'$.source.designId') AS TEXT),CAST(json_extract(value,'$.source.revisionId') AS TEXT) FROM design_scenario_revisions r,json_each(r.document,'$.contracts') WHERE r.scenario_id=? AND r.id=? AND json_type(value,'$.source.designId')='integer' AND json_type(value,'$.source.revisionId')='integer'`, id, rid)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var origins [][2]string
	for rows.Next() {
		var pair [2]string
		if err = rows.Scan(&pair[0], &pair[1]); err != nil {
			return err
		}
		origins = append(origins, pair)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, o := range origins {
		if err = b.owner("api_design", o[0], o[1]); err != nil {
			return err
		}
	}
	return nil
}
func (b *analysisFootprintBuilder) target(target BackendReadTarget) error {
	if err := target.Validate(); err != nil {
		return err
	}
	if target.ImportCandidate != nil {
		return invalid("target", "Analysis requires immutable saved targets")
	}
	if target.RevisionID != "" {
		return b.source(target.RevisionID)
	}
	var raw, base string
	var size int64
	var err error
	if p := target.ChangeProposal; p != nil {
		err = b.q.QueryRowContext(b.ctx, `SELECT base_revision_id,length(CAST(document AS BLOB)) FROM backend_change_proposal_revisions_documents WHERE project_id=? AND proposal_id=? AND id=?`, b.pid, p.ProposalID, p.ProposalRevisionID).Scan(&base, &size)
		if err != nil {
			return footprintRow(err, changeRevisionNotFound())
		}
		if err = b.add("change:"+p.ProposalRevisionID, size); err != nil {
			return err
		}
		// Permanent memberships are immutable first-revision facts; count the bounded
		// pre-draft roster before loading it. Later ledger growth is never replay input.
		var ledger int64
		if err = b.q.QueryRowContext(b.ctx, analysisIdentityBytesSQL, b.pid, p.ProposalID, p.ProposalRevisionID, b.pid, p.ProposalID).Scan(&ledger); err != nil {
			return err
		}
		var historical int64
		historical, err = changeHistoricalIdentityBytes(b.ctx, b.q, b.pid, p.ProposalID, p.ProposalRevisionID)
		if err != nil {
			return err
		}
		if err = b.add("historical-identities:"+p.ProposalRevisionID, historical); err != nil {
			return err
		}
		if err = b.add("identities:"+p.ProposalRevisionID, ledger); err != nil {
			return err
		}
		err = b.q.QueryRowContext(b.ctx, `SELECT document FROM backend_change_proposal_revisions_documents WHERE project_id=? AND proposal_id=? AND id=?`, b.pid, p.ProposalID, p.ProposalRevisionID).Scan(&raw)
	} else {
		p := target.Proposal
		err = b.q.QueryRowContext(b.ctx, `SELECT json_extract(r.document,'$.baseRevisionId'),length(CAST(r.document AS BLOB)) FROM backend_proposal_revisions_documents r JOIN backend_proposals p ON p.id=r.proposal_id WHERE p.project_id=? AND r.proposal_id=? AND r.id=?`, b.pid, p.ProposalID, p.ProposalRevisionID).Scan(&base, &size)
		if err != nil {
			return footprintRow(err, notFound())
		}
		if err = b.add("legacy:"+p.ProposalRevisionID, size); err != nil {
			return err
		}
		err = b.q.QueryRowContext(b.ctx, `SELECT document FROM backend_proposal_revisions_documents WHERE proposal_id=? AND id=?`, p.ProposalID, p.ProposalRevisionID).Scan(&raw)
	}
	if err != nil {
		return err
	}
	if err = b.source(base); err != nil {
		return err
	}
	return b.references([]byte(raw))
}

const analysisIdentityCTE = `WITH RECURSIVE ancestors(id,parent_revision_id) AS (SELECT id,parent_revision_id FROM backend_change_proposal_revisions_documents WHERE project_id=? AND proposal_id=? AND id=? UNION SELECT r.id,r.parent_revision_id FROM backend_change_proposal_revisions_documents r JOIN ancestors a ON r.id=a.parent_revision_id WHERE r.project_id=? AND r.proposal_id=?) `
const analysisIdentityBytesSQL = analysisIdentityCTE + `SELECT COALESCE(sum(length(CAST(i.document AS BLOB))),0) FROM backend_change_proposal_identities_documents i JOIN ancestors a ON a.id=i.first_revision_id`

func (b *analysisFootprintBuilder) references(raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var walk func(any) error
	walk = func(v any) error {
		switch x := v.(type) {
		case map[string]any:
			if id, ok := x["historicalRevisionId"].(string); ok && id != "" {
				if err := b.source(id); err != nil {
					return err
				}
			}
			if kind, _ := x["kind"].(string); kind == "source" {
				if id, ok := x["revisionId"].(string); ok && id != "" {
					if err := b.source(id); err != nil {
						return err
					}
				}
			}
			if kind, _ := x["kind"].(string); kind == "api_design" || kind == "design_scenario" {
				id, _ := x["id"].(string)
				rid, _ := x["revisionId"].(string)
				if rid != "" {
					if err := b.owner(kind, id, rid); err != nil {
						return err
					}
				}
			}
			for _, child := range x {
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range x {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(value)
}
func (r *Repo) analysisFootprint(ctx context.Context, pid string, from string, target BackendReadTarget, preview *AnalysisCommandPreviewInput) (AnalysisInputFootprint, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AnalysisInputFootprint{}, err
	}
	defer func() { _ = tx.Rollback() }()
	b := analysisFootprintBuilder{ctx: ctx, q: tx, pid: pid, documents: map[string]int64{}}
	if from != "" {
		if err = b.source(from); err != nil {
			return AnalysisInputFootprint{}, err
		}
	}
	if preview != nil {
		target = BackendReadTarget{ChangeProposal: new(preview.ChangeProposal)}
	}
	if err = b.target(target); err != nil {
		return AnalysisInputFootprint{}, err
	}
	if preview != nil {
		if preview.Preview.ProposalRevisionID != preview.ChangeProposal.ProposalRevisionID {
			return AnalysisInputFootprint{}, invalid("preview", "Exact draft differs from preview")
		}
		if err = validateChangeCommands(preview.Preview.Commands); err != nil {
			return AnalysisInputFootprint{}, err
		}
		raw, encodeErr := json.Marshal(preview.Preview.Commands)
		if encodeErr != nil {
			return AnalysisInputFootprint{}, encodeErr
		}
		hash, _ := requestDigest(preview.Preview.Commands)
		if err = b.add("commands:"+hash, int64(len(raw))); err != nil {
			return AnalysisInputFootprint{}, err
		}
		for _, c := range preview.Preview.Commands {
			if c.Type == "set_artifact_pin" {
				if err = b.owner(c.Artifact.Kind, c.Artifact.ID, c.RevisionID); err != nil {
					return AnalysisInputFootprint{}, err
				}
			}
		}
		if err = b.references(raw); err != nil {
			return AnalysisInputFootprint{}, err
		}
	}
	f := AnalysisInputFootprint{TotalBytes: b.total, repo: r, projectID: pid}
	for _, key := range slices.Sorted(maps.Keys(b.documents)) {
		f.Documents = append(f.Documents, AnalysisDocumentBytes{key, b.documents[key]})
	}
	f.seal, err = requestDigest(f.Documents)
	return f, err
}
func (r *Repo) EffectiveGraphInputFootprint(ctx context.Context, pid string, target BackendReadTarget) (AnalysisInputFootprint, error) {
	return r.analysisFootprint(ctx, pid, "", target, nil)
}
func (r *Repo) AnalysisPairInputFootprint(ctx context.Context, pid, from string, target BackendReadTarget, preview *AnalysisCommandPreviewInput) (AnalysisInputFootprint, error) {
	if !ValidID(from) {
		return AnalysisInputFootprint{}, invalid("fromRevisionId", "Expected exact source revision")
	}
	if preview != nil && (target.RevisionID != "" || target.ChangeProposal != nil || target.Proposal != nil || target.ImportCandidate != nil) {
		return AnalysisInputFootprint{}, invalid("target", "Preview and saved target are exclusive")
	}
	return r.analysisFootprint(ctx, pid, from, target, preview)
}

func (l *AnalysisInputReservation) reconcilePrepared(ctx context.Context, rawBytes int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return invalid("analysisInput", "Preparation reservation is released")
	}
	required := 4*l.inputBytes + 2*rawBytes + MaxChangeProposalCommandBytes
	if required <= l.chargedBytes {
		return nil
	}
	err := l.repo.db.Write(ctx, func(tx *sql.Tx) error {
		staged, err := stagingBytes(ctx, tx, l.pid)
		if err != nil {
			return err
		}
		if !l.reservation.Resize(required, MaxProjectStagingBytes-staged) {
			return limitFault("Prepared analysis pair exceeds transient memory budget")
		}
		return nil
	})
	if err == nil {
		l.chargedBytes = required
	}
	return err
}
