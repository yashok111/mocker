package backendmodel

import (
	"context"
	"database/sql"
	"strconv"
	"sync"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/store"
)

// One request budget includes baseline, draft, permanent identity ledger,
// historical locators and every
// exact owner snapshot, including nested artifacts read by the owner resolver.
type changeReadBudget struct {
	mu          sync.Mutex
	repo        *Repo
	pid         string
	reservation *store.TransientReservation
	bytes       int64
	seen        map[string]bool
	lease       *AnalysisInputReservation
	// tx, when set, is the read snapshot the caller holds across evaluation:
	// owner sizes and snapshots are read on it, never on a second reader-pool
	// connection (review 2026-10-06, F3, the frozen-preview replay).
	tx *sql.Tx
}

func (b *changeReadBudget) admit(ctx context.Context, key string, size int64) error {
	if b == nil {
		return nil
	}
	if b.lease != nil {
		return b.lease.admit(key, size)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.seen[key] {
		return nil
	}
	total := b.bytes + size
	if total > MaxRevisionBytes {
		return limitFault("Combined immutable proposal inputs exceed materialization bound")
	}
	err := b.repo.db.Write(ctx, func(tx *sql.Tx) error {
		staged, err := stagingBytes(ctx, tx, b.pid)
		if err != nil {
			return err
		}
		if !b.reservation.Resize(4*total+MaxChangeProposalCommandBytes, MaxProjectStagingBytes-staged) {
			return limitFault("Combined proposal readers exceed project transient budget")
		}
		return nil
	})
	if err != nil {
		return err
	}
	b.bytes = total
	b.seen[key] = true
	return nil
}
func (b *changeReadBudget) artifact(ctx context.Context, kind string, id, rid int64) error {
	if b == nil {
		return nil
	}
	key := kind + ":" + strconv.FormatInt(id, 10) + ":" + strconv.FormatInt(rid, 10)
	var size int64
	var err error
	var q importReader = b.repo.db.R
	if b.tx != nil {
		q = b.tx
	}
	switch kind {
	case "api_design":
		err = q.QueryRowContext(ctx, `SELECT length(CAST(document AS BLOB)) FROM api_design_revisions WHERE design_id=? AND id=?`, id, rid).Scan(&size)
	case "design_scenario":
		err = q.QueryRowContext(ctx, `SELECT length(CAST(document AS BLOB))+length(CAST(form_drafts AS BLOB)) FROM design_scenario_revisions WHERE scenario_id=? AND id=?`, id, rid).Scan(&size)
	}
	if err != nil {
		return err
	}
	return b.admit(ctx, key, size)
}

type changeAPIArtifactReader struct {
	APIArtifactReader
	budget *changeReadBudget
}

func (r changeAPIArtifactReader) ArtifactSnapshot(ctx context.Context, id, rid int64) (*apidesign.ArtifactSnapshot, error) {
	if err := r.budget.artifact(ctx, "api_design", id, rid); err != nil {
		return nil, err
	}
	return r.APIArtifactReader.ArtifactSnapshot(ctx, id, rid)
}

type changeScenarioArtifactReader struct {
	ScenarioArtifactReader
	budget *changeReadBudget
}

func (r changeScenarioArtifactReader) ArtifactSnapshot(ctx context.Context, id, rid int64) (*designscenario.ArtifactSnapshot, error) {
	if err := r.budget.artifact(ctx, "design_scenario", id, rid); err != nil {
		return nil, err
	}
	return r.ScenarioArtifactReader.ArtifactSnapshot(ctx, id, rid)
}
func (r changeScenarioArtifactReader) ArtifactInspectionSnapshot(ctx context.Context, id, rid int64) (*designscenario.ArtifactInspectionSnapshot, error) {
	if err := r.budget.artifact(ctx, "design_scenario", id, rid); err != nil {
		return nil, err
	}
	return r.ScenarioArtifactReader.ArtifactInspectionSnapshot(ctx, id, rid)
}
func (e *changeEvaluation) referencedSource(ctx context.Context, q importReader, pid, rid string) (*SourceGraphSnapshot, error) {
	if rid == e.revision.BaseRevisionID {
		return e.source, nil
	}
	if graph := e.referenceGraphs[rid]; graph != nil {
		return graph, nil
	}
	size, err := changeSourceInputBytes(ctx, q, pid, rid)
	if err != nil {
		return nil, err
	}
	if err = e.readBudget.admit(ctx, "source:"+rid, size); err != nil {
		return nil, err
	}
	graph, err := loadSourceGraph(ctx, q, pid, rid)
	if err != nil {
		return nil, err
	}
	if e.referenceGraphs == nil {
		e.referenceGraphs = map[string]*SourceGraphSnapshot{}
	}
	e.referenceGraphs[rid] = graph
	return graph, nil
}

func changeSourceInputBytes(ctx context.Context, q importReader, pid, rid string) (int64, error) {
	var bytes int64
	err := q.QueryRowContext(ctx, `SELECT
 (SELECT COALESCE(sum(length(CAST(document AS BLOB))),0) FROM backend_graph_records_documents WHERE project_id=? AND revision_id=?)+
 (SELECT COALESCE(sum(length(CAST(document AS BLOB))),0) FROM backend_revision_sources_documents WHERE revision_id=?)+
 (SELECT COALESCE(sum(length(CAST(document AS BLOB))),0) FROM backend_revision_assertions_documents WHERE project_id=? AND revision_id=?)+
 (SELECT COALESCE(sum(length(CAST(document AS BLOB))),0) FROM backend_revision_assertion_resolutions_documents WHERE project_id=? AND revision_id=?)+
 (SELECT COALESCE(sum(length(CAST(document AS BLOB))),0) FROM backend_revision_legacy_proof_bases_documents WHERE project_id=? AND revision_id=?)+
 (SELECT COALESCE(sum(length(CAST(document AS BLOB))),0) FROM backend_revision_api_artifacts_documents WHERE revision_id=?)`, pid, rid, rid, pid, rid, pid, rid, pid, rid, rid).Scan(&bytes)
	return bytes, err
}

func changeIdentityInputBytes(ctx context.Context, q importReader, pid, proposalID string) (int64, error) {
	var bytes int64
	err := q.QueryRowContext(
		ctx,
		`SELECT COALESCE(sum(length(CAST(document AS BLOB))),0) FROM backend_change_proposal_identities_documents WHERE project_id=? AND proposal_id=?`,
		pid,
		proposalID,
	).Scan(&bytes)
	return bytes, err
}
func (r *Repo) reserveChangeInput(ctx context.Context, pid string, bytes int64) (*store.TransientReservation, error) {
	if bytes < 0 || bytes > MaxRevisionBytes {
		return nil, limitFault("Pinned proposal inputs exceed materialization bounds")
	}
	if lease := analysisLease(ctx); lease != nil {
		if err := lease.valid(r, pid); err != nil {
			return nil, err
		}
		admitted, _ := ctx.Value(analysisChangePreparationKey{}).(*analysisChangePreparation)
		if admitted == nil || admitted.lease != lease || admitted.projectID != pid {
			return nil, invalid("analysisInput", "Preparation reservation borrowing requires exact internal admission")
		}
		if bytes > lease.inputBytes {
			return nil, limitFault("Prepared inputs exceed the admitted analysis inventory")
		}
		return lease.reservation, nil
	}
	var reservation *store.TransientReservation
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		staged, err := stagingBytes(ctx, tx, pid)
		if err != nil {
			return err
		}
		var ok bool
		reservation, ok = r.db.ReserveTransient("backend:"+pid, 4*bytes+MaxChangeProposalCommandBytes, MaxProjectStagingBytes-staged)
		if !ok {
			return limitFault("Project transient proposal budget exhausted")
		}
		return nil
	})
	return reservation, err
}
func (r *Repo) reserveChangeRestore(ctx context.Context, pid, id string, in RestoreChangeProposalInput) (*store.TransientReservation, error) {
	var bytes int64
	err := r.db.R.QueryRowContext(ctx, `SELECT COALESCE(sum(length(CAST(document AS BLOB))),0) FROM backend_change_proposal_revisions_documents WHERE project_id=? AND proposal_id=? AND (id=? OR id=?)`, pid, id, in.ProposalRevisionID, in.RestoreRevisionID).Scan(&bytes)
	if err != nil {
		return nil, err
	}
	return r.reserveChangeInput(ctx, pid, bytes)
}
