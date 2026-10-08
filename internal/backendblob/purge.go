package backendblob

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"time"
)

type PurgeReceipt struct {
	Policy           string `json:"policy"`
	ProjectID        string `json:"projectId"`
	ConfirmationHash string `json:"confirmationHash"`
	ErasedAt         string `json:"erasedAt"`
	RowsDeleted      int64  `json:"rowsDeleted"`
	PayloadBytes     int64  `json:"payloadBytes"`
}

func hashPurgeTable(ctx context.Context, tx *sql.Tx, table purgeTable, w io.Writer) (int64, error) {
	if err := json.MarshalWrite(w, table.name); err != nil {
		return 0, err
	}
	//nolint:gosec // G202: quoted table identifier comes from the exclusive connection's schema; stored values remain bound parameters.
	rows, err := tx.QueryContext(ctx, `SELECT t.rowid,t.* FROM `+purgeIdentifier(table.name)+` t JOIN purge_rows selected ON selected.table_name=? AND selected.row_id=t.rowid ORDER BY t.rowid`, table.name)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	var count int64
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return 0, err
		}
		if err := json.MarshalWrite(w, values); err != nil {
			return 0, err
		}
		count++
	}
	return count, rows.Err()
}

// PurgeProject is only called by the exclusive offline maintenance connection,
// with FKs disabled before BEGIN. Every deletion and guard change is transactional;
// no ordinary HTTP/MCP operation can enter this lifecycle path.
func PurgeProject(ctx context.Context, tx *sql.Tx, pid, confirmation string) (*PurgeReceipt, error) {
	if len(confirmation) != 64 {
		return nil, fmt.Errorf("exact preview confirmation hash required")
	}
	receipt := &PurgeReceipt{Policy: PurgePolicy, ProjectID: pid}
	err := tx.QueryRowContext(ctx, `SELECT policy,confirmation_hash,erased_at,rows_deleted,payload_bytes FROM backend_project_purge_receipts WHERE project_id=?`, pid).Scan(&receipt.Policy, &receipt.ConfirmationHash, &receipt.ErasedAt, &receipt.RowsDeleted, &receipt.PayloadBytes)
	if err == nil {
		if confirmation != receipt.ConfirmationHash {
			return nil, fmt.Errorf("project was erased by a different confirmed preview")
		}
		return receipt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err := Verify(ctx, tx); err != nil {
		return nil, err
	}
	preview, err := PreviewProjectPurge(ctx, tx, pid)
	if err != nil {
		return nil, err
	}
	if preview.ConfirmationHash != confirmation {
		return nil, fmt.Errorf("purge preview changed or confirmation does not match; obtain a new preview")
	}
	guards, err := purgeGuards(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, guard := range guards {
		if _, err := tx.ExecContext(ctx, `DROP TRIGGER `+purgeIdentifier(guard.Name)); err != nil {
			return nil, err
		}
	}
	for _, table := range preview.Tables {
		//nolint:gosec // G202: the preview table roster is built from trusted SQLite metadata in this same exclusive transaction.
		result, err := tx.ExecContext(ctx, `DELETE FROM `+purgeIdentifier(table.Table)+` WHERE rowid IN (SELECT row_id FROM purge_rows WHERE table_name=?)`, table.Table)
		if err != nil {
			return nil, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n != table.Rows {
			return nil, fmt.Errorf("erasure row count changed for %s", table.Table)
		}
		receipt.RowsDeleted += n
	}
	for _, guard := range guards {
		if _, err := tx.ExecContext(ctx, guard.SQL); err != nil {
			return nil, err
		}
	}
	if err := Verify(ctx, tx); err != nil {
		return nil, err
	}
	receipt.ConfirmationHash, receipt.ErasedAt, receipt.PayloadBytes = confirmation, time.Now().UTC().Format(time.RFC3339Nano), preview.PayloadBytes
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_project_purge_receipts(project_id,policy,confirmation_hash,erased_at,rows_deleted,payload_bytes) VALUES(?,?,?,?,?,?)`, pid, receipt.Policy, confirmation, receipt.ErasedAt, receipt.RowsDeleted, receipt.PayloadBytes)
	return receipt, err
}

func purgeGuards(ctx context.Context, tx *sql.Tx) ([]Object, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name,sql FROM sqlite_schema WHERE type='trigger' AND tbl_name IN (SELECT DISTINCT table_name FROM purge_rows) ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Object{}
	for rows.Next() {
		var guard Object
		if err := rows.Scan(&guard.Name, &guard.SQL); err != nil {
			return nil, err
		}
		out = append(out, guard)
	}
	return out, rows.Err()
}
