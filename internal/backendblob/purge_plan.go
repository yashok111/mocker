package backendblob

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
)

const PurgePolicy = "offline-project-erasure-v1"

type PurgeTableCount struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
}
type PurgePreview struct {
	Policy             string            `json:"policy"`
	ProjectID          string            `json:"projectId"`
	ProjectVersion     int64             `json:"projectVersion"`
	RevisionID         string            `json:"revisionId"`
	ConfirmationHash   string            `json:"confirmationHash"`
	Tables             []PurgeTableCount `json:"tables"`
	PayloadBytes       int64             `json:"payloadBytes"`
	SharedPayloadBytes int64             `json:"sharedPayloadBytes"`
	OutsideScope       []string          `json:"outsideScope"`
}

type purgeTable struct {
	name    string
	columns []string
}
type purgeFK struct {
	table    string
	from, to []string
}

// Identifiers originate only in the trusted local schema; quoting also makes
// unusual identifiers inert. Project IDs and all stored values stay parameters.
func purgeIdentifier(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func purgeTables(ctx context.Context, tx *sql.Tx) ([]purgeTable, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND substr(name,1,8)='backend_' AND name NOT IN ('backend_project_purge_receipts','backend_installation_identity') ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tables []purgeTable
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, purgeTable{name: name})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range tables {
		columns, err := purgeColumns(ctx, tx, tables[i].name)
		if err != nil {
			return nil, err
		}
		tables[i].columns = columns
	}
	return tables, nil
}

func seedPurgeRows(ctx context.Context, tx *sql.Tx, pid string, tables []purgeTable) error {
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE purge_rows(table_name TEXT NOT NULL,row_id INTEGER NOT NULL,PRIMARY KEY(table_name,row_id))`); err != nil {
		return err
	}
	for _, table := range tables {
		field := "project_id"
		if table.name == "backend_projects" {
			field = "id"
		} else if !slices.Contains(table.columns, field) {
			continue
		}
		//nolint:gosec // G202: schema-derived identifiers are quoted; project identity is a bound parameter.
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO purge_rows SELECT ?,rowid FROM `+purgeIdentifier(table.name)+` WHERE `+purgeIdentifier(field)+`=?`, table.name, pid); err != nil {
			return err
		}
	}
	// All non-create receipts use operation:projectUUID[:resource]. Match that
	// exact component, including future operation families, never an arbitrary
	// occurrence of the UUID in a foreign project's resource suffix.
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO purge_rows SELECT 'backend_command_receipts',rowid FROM backend_command_receipts WHERE (instr(scope,':')>0 AND (substr(scope,instr(scope,':')+1)=? OR substr(scope,instr(scope,':')+1,37)=?)) OR (scope='create' AND json_extract(response,'$.id')=?)`, pid, pid+":", pid)
	return err
}

func purgeForeignKeys(ctx context.Context, tx *sql.Tx, table string) ([]purgeFK, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,"table","from","to" FROM pragma_foreign_key_list(?) ORDER BY id,seq`, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []purgeFK
	last := -1
	for rows.Next() {
		var id int
		var parent, from, to string
		if err := rows.Scan(&id, &parent, &from, &to); err != nil {
			return nil, err
		}
		if id != last {
			out = append(out, purgeFK{table: parent})
			last = id
		}
		fk := &out[len(out)-1]
		fk.from = append(fk.from, from)
		fk.to = append(fk.to, to)
	}
	return out, rows.Err()
}

// Traverse children to include staging, revisions, views, export chunks and
// receipts that have no project_id of their own. Snapshot the complete row set
// before deleting anything, so cycles and deletion order cannot lose ownership.
func expandPurgeRows(ctx context.Context, tx *sql.Tx, tables []purgeTable) error {
	queries := []string{}
	args := [][]any{}
	for _, table := range tables {
		fks, err := purgeForeignKeys(ctx, tx, table.name)
		if err != nil {
			return err
		}
		for _, fk := range fks {
			joins := make([]string, len(fk.from))
			for i := range joins {
				joins[i] = "c." + purgeIdentifier(fk.from[i]) + "=p." + purgeIdentifier(fk.to[i])
			}
			queries = append(queries, `INSERT OR IGNORE INTO purge_rows SELECT ?,c.rowid FROM `+purgeIdentifier(table.name)+` c JOIN `+purgeIdentifier(fk.table)+` p ON `+strings.Join(joins, " AND ")+` JOIN purge_rows selected ON selected.table_name=? AND selected.row_id=p.rowid`)
			args = append(args, []any{table.name, fk.table})
		}
	}
	for {
		var added int64
		for i, q := range queries {
			result, err := tx.ExecContext(ctx, q, args[i]...)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			added += n
		}
		if added == 0 {
			return nil
		}
	}
}

func selectPurgePayloads(ctx context.Context, tx *sql.Tx) error {
	// Observation content is shared globally. Erase only blobs reached by this
	// project's selected members and not by any retained member; retain the rest.
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO purge_rows SELECT 'backend_observation_blobs',b.rowid FROM backend_observation_blobs b WHERE EXISTS(SELECT 1 FROM backend_observation_members m JOIN purge_rows x ON x.table_name='backend_observation_members' AND x.row_id=m.rowid WHERE m.hash=b.hash) AND NOT EXISTS(SELECT 1 FROM backend_observation_members m WHERE m.hash=b.hash AND NOT EXISTS(SELECT 1 FROM purge_rows x WHERE x.table_name='backend_observation_members' AND x.row_id=m.rowid))`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO purge_rows SELECT 'backend_payload_members',m.rowid FROM backend_payload_members m JOIN backend_observation_blobs b ON m.owner='backend_observation_blobs' AND m.payload_key=b.payload_key JOIN purge_rows x ON x.table_name='backend_observation_blobs' AND x.row_id=b.rowid`); err != nil {
		return err
	}
	// A canonical group spanning another project cannot be partially erased:
	// its immutable digest would cease to describe its surviving members.
	var shared int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_payload_members m JOIN purge_rows x ON x.table_name='backend_payload_members' AND x.row_id=m.rowid WHERE EXISTS(SELECT 1 FROM backend_payload_members other WHERE other.owner=m.owner AND other.owner_id=m.owner_id AND other.version=m.version AND NOT EXISTS(SELECT 1 FROM purge_rows y WHERE y.table_name='backend_payload_members' AND y.row_id=other.rowid))`).Scan(&shared); err != nil {
		return err
	}
	if shared != 0 {
		return fmt.Errorf("erasure closure shares canonical groups with retained data; refusing partial history erasure")
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO purge_rows SELECT 'backend_payload_manifests',f.rowid FROM backend_payload_manifests f JOIN backend_payload_members m USING(owner,owner_id,version) JOIN purge_rows x ON x.table_name='backend_payload_members' AND x.row_id=m.rowid`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO purge_rows SELECT 'backend_payload_blobs',b.rowid FROM backend_payload_blobs b WHERE EXISTS(SELECT 1 FROM backend_payload_members m JOIN purge_rows x ON x.table_name='backend_payload_members' AND x.row_id=m.rowid WHERE m.payload_key=b.key) AND NOT EXISTS(SELECT 1 FROM backend_payload_members m WHERE m.payload_key=b.key AND NOT EXISTS(SELECT 1 FROM purge_rows x WHERE x.table_name='backend_payload_members' AND x.row_id=m.rowid))`)
	return err
}

func PreviewProjectPurge(ctx context.Context, tx *sql.Tx, pid string) (*PurgePreview, error) {
	out := &PurgePreview{Policy: PurgePolicy, ProjectID: pid, Tables: []PurgeTableCount{}, OutsideScope: []string{"external backups and filesystem snapshots", "downloaded exports and client request/receipt files", "independent portable copies and materialized API/scenario artifacts", "shared payloads still owned by another project"}}
	if err := tx.QueryRowContext(ctx, `SELECT version,current_revision_id FROM backend_projects WHERE id=?`, pid).Scan(&out.ProjectVersion, &out.RevisionID); err != nil {
		return nil, err
	}
	tables, err := purgeTables(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := seedPurgeRows(ctx, tx, pid, tables); err != nil {
		return nil, err
	}
	if err := expandPurgeRows(ctx, tx, tables); err != nil {
		return nil, err
	}
	if err := selectPurgePayloads(ctx, tx); err != nil {
		return nil, err
	}
	if err := rejectForeignPurgeRows(ctx, tx, pid, tables); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(byte_length),0) FROM backend_payload_blobs b JOIN purge_rows x ON x.table_name='backend_payload_blobs' AND x.row_id=b.rowid`).Scan(&out.PayloadBytes); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(b.byte_length),0) FROM backend_payload_blobs b WHERE EXISTS(SELECT 1 FROM backend_payload_members m JOIN purge_rows x ON x.table_name='backend_payload_members' AND x.row_id=m.rowid WHERE m.payload_key=b.key) AND NOT EXISTS(SELECT 1 FROM purge_rows x WHERE x.table_name='backend_payload_blobs' AND x.row_id=b.rowid)`).Scan(&out.SharedPayloadBytes); err != nil {
		return nil, err
	}
	h := sha256.New()
	if err := json.MarshalWrite(h, []any{out.Policy, out.ProjectID, out.ProjectVersion, out.RevisionID, out.OutsideScope}); err != nil {
		return nil, err
	}
	for _, table := range tables {
		n, err := hashPurgeTable(ctx, tx, table, h)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			out.Tables = append(out.Tables, PurgeTableCount{Table: table.name, Rows: n})
		}
	}
	out.ConfirmationHash = hex.EncodeToString(h.Sum(nil))
	return out, nil
}

func rejectForeignPurgeRows(ctx context.Context, tx *sql.Tx, pid string, tables []purgeTable) error {
	for _, table := range tables {
		field := "project_id"
		if table.name == "backend_projects" {
			field = "id"
		} else if !slices.Contains(table.columns, field) {
			continue
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+purgeIdentifier(table.name)+` t JOIN purge_rows x ON x.table_name=? AND x.row_id=t.rowid WHERE t.`+purgeIdentifier(field)+`<>? AND t.`+purgeIdentifier(field)+`<>''`, table.name, pid).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("erasure would affect another project through %s", table.name)
		}
	}
	return nil
}

func purgeColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var columns []string
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, err
		}
		columns = append(columns, col)
	}
	return columns, rows.Err()
}
