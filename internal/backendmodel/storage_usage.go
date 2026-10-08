package backendmodel

import (
	"context"
	"database/sql"
	"time"
)

type StorageUsage struct {
	Version                      string    `json:"version"`
	ProjectID                    string    `json:"projectId"`
	SampledAt                    time.Time `json:"sampledAt"`
	ActiveImports                int64     `json:"activeImports"`
	ClosedImports                int64     `json:"closedImports"`
	ActiveStagingBytes           int64     `json:"activeStagingBytes"`
	TransientReservedBytes       int64     `json:"transientReservedBytes"`
	StagingLimitBytes            int64     `json:"stagingLimitBytes"`
	RemainingStagingBytes        int64     `json:"remainingStagingBytes"`
	ClosedImportBytes            int64     `json:"closedImportBytes"`
	RetainedLogicalBytes         int64     `json:"retainedLogicalBytes"`
	RetainedDistinctPayloadBytes int64     `json:"retainedDistinctPayloadBytes"`
	DurableReceiptBytes          int64     `json:"durableReceiptBytes"`
	Accounting                   string    `json:"accounting"`
	Retention                    string    `json:"retention"`
}

// Usage is a read-time estimate, not a reservation or a sum of database-file
// bytes. Active batch receipts occur in both staging and receipt measurements;
// immutable payloads can be shared with another project. Writers still enforce
// their own quotas under the existing transaction/reservation protocol.
func (r *Repo) StorageUsage(ctx context.Context, pid string) (*StorageUsage, error) {
	if !ValidID(pid) {
		return nil, notFound()
	}
	out := &StorageUsage{Version: "backend-storage-usage-v1", ProjectID: pid, StagingLimitBytes: MaxProjectStagingBytes, Accounting: "independent_overlapping_measures", Retention: "explicit_offline_project_erasure"}
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		if _, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id=?`, pid)); err != nil {
			return err
		}
		var err error
		out.ActiveStagingBytes, err = stagingBytes(ctx, tx, pid)
		if err != nil {
			return err
		}
		out.ClosedImportBytes, err = importSessionBytes(ctx, tx, pid, true)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(CASE WHEN state IN ('committed','aborted') THEN 0 ELSE 1 END),0),coalesce(sum(CASE WHEN state IN ('committed','aborted') THEN 1 ELSE 0 END),0) FROM backend_import_sessions WHERE project_id=?`, pid).Scan(&out.ActiveImports, &out.ClosedImports); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT
 (SELECT coalesce(sum(b.byte_length),0) FROM backend_payload_members m JOIN backend_payload_blobs b ON b.key=m.payload_key WHERE m.project_id=?)+
 (SELECT coalesce(sum(b.byte_length),0) FROM backend_observation_members m JOIN backend_observation_blobs o ON o.hash=m.hash JOIN backend_payload_blobs b ON b.key=o.payload_key WHERE m.project_id=?)`, pid, pid).Scan(&out.RetainedLogicalBytes); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(b.byte_length),0) FROM backend_payload_blobs b WHERE b.key IN (SELECT payload_key FROM backend_payload_members WHERE project_id=? UNION SELECT o.payload_key FROM backend_observation_members m JOIN backend_observation_blobs o ON o.hash=m.hash WHERE m.project_id=?)`, pid, pid).Scan(&out.RetainedDistinctPayloadBytes); err != nil {
			return err
		}
		out.DurableReceiptBytes, err = projectReceiptBytes(ctx, tx, pid)
		return err
	})
	if err != nil {
		return nil, err
	}
	out.TransientReservedBytes = r.db.TransientBytes("backend:" + pid)
	out.RemainingStagingBytes = max(0, out.StagingLimitBytes-out.ActiveStagingBytes-out.TransientReservedBytes)
	out.SampledAt = time.Now().UTC()
	return out, nil
}

func projectReceiptBytes(ctx context.Context, tx *sql.Tx, pid string) (int64, error) {
	var total int64
	err := tx.QueryRowContext(ctx, `SELECT
 (SELECT coalesce(sum(length(CAST(response AS BLOB))),0) FROM backend_command_receipts WHERE (instr(scope,':')>0 AND (substr(scope,instr(scope,':')+1)=? OR substr(scope,instr(scope,':')+1,37)=?)) OR (scope='create' AND json_extract(response,'$.id')=?))+
 (SELECT coalesce(sum(length(CAST(b.receipt AS BLOB))),0) FROM backend_import_batches b JOIN backend_import_sessions s ON s.id=b.session_id WHERE s.project_id=?)+
 (SELECT coalesce(sum(length(CAST(receipt AS BLOB))),0) FROM backend_diagram_receipts WHERE project_id=?)+
 (SELECT coalesce(sum(length(CAST(response AS BLOB))),0) FROM backend_analysis_receipts WHERE project_id=?)+
 (SELECT coalesce(sum(length(CAST(response AS BLOB))),0) FROM backend_finding_receipts WHERE project_id=?)+
 (SELECT coalesce(sum(length(CAST(response AS BLOB))),0) FROM backend_materialization_receipts WHERE project_id=?)+
 (SELECT coalesce(sum(length(CAST(document AS BLOB))),0) FROM backend_observation_receipts WHERE project_id=?)+
 (SELECT coalesce(sum(length(CAST(result_json AS BLOB))),0) FROM backend_replay_receipts WHERE project_id=?)+
 (SELECT coalesce(sum(length(CAST(r.response AS BLOB))),0) FROM backend_portable_receipts r JOIN backend_portable_sessions s ON s.id=r.session_id WHERE s.project_id=?)`, pid, pid+":", pid, pid, pid, pid, pid, pid, pid, pid, pid).Scan(&total)
	return total, err
}
