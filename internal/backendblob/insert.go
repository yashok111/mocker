package backendblob

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

var insertPattern = regexp.MustCompile(`(?is)^\s*INSERT\s+(OR\s+IGNORE\s+)?INTO\s+(backend_[a-z_]+)\s*\(([^)]+)\)\s*(.+)$`)

// Exec is the explicit immutable-owner insertion adapter. Call sites provide
// literal INSERT VALUES or INSERT SELECT statements. Only compiled owner and
// column identifiers are admitted; values remain SQL parameters. Copy callers
// select payload_key columns, never reconstructed JSON.
func Exec(ctx context.Context, tx *sql.Tx, query string, args ...any) (sql.Result, error) {
	match := insertPattern.FindStringSubmatch(query)
	if match == nil {
		return nil, fmt.Errorf("unsupported immutable INSERT")
	}
	o, err := Lookup(match[2])
	if err != nil {
		return nil, err
	}
	var columns []Column
	var supplied []string
	for name := range strings.SplitSeq(match[3], ",") {
		name = strings.TrimSpace(name)
		c, ok := o.column(name)
		if !ok {
			return nil, fmt.Errorf("unregistered %s column %s", o.Table, name)
		}
		columns = append(columns, c)
		supplied = append(supplied, name)
	}
	body := strings.TrimSpace(match[4])
	suffix := ""
	if i := strings.Index(strings.ToUpper(body), " ON CONFLICT"); i >= 0 {
		suffix = body[i:]
		body = body[:i]
	}
	if !strings.HasPrefix(strings.ToUpper(body), "VALUES") && !strings.HasPrefix(strings.ToUpper(body), "SELECT") {
		return nil, fmt.Errorf("unsupported immutable row source")
	}
	rows, err := tx.QueryContext(ctx, body, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var affected int64
	for rows.Next() {
		v := make([]any, len(columns))
		ptrs := make([]any, len(v))
		for i := range v {
			ptrs[i] = &v[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		values := make([]any, len(o.Columns))
		for i, c := range o.Columns {
			found := false
			for j, sup := range columns {
				if sup.Name == c.Name {
					values[i] = v[j]
					found = true
					break
				}
			}
			if !found && c.Default != "" {
				if err = tx.QueryRowContext(ctx, "SELECT "+c.Default).Scan(&values[i]); err != nil {
					return nil, err
				}
			}
			if c.Payload && values[i] == nil {
				return nil, fmt.Errorf("missing immutable payload %s.%s", o.Table, c.Name)
			}
		}
		for i, c := range o.Columns {
			if !c.Payload {
				continue
			}
			isKey := false
			for j, sup := range columns {
				if sup.Name == c.Name {
					isKey = supplied[j] == c.StorageName()
					break
				}
			}
			var raw []byte
			if isKey {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("invalid payload key")
				}
				raw, err = Get(ctx, tx, key)
			} else {
				switch x := values[i].(type) {
				case string:
					raw = []byte(x)
				case []byte:
					raw = x
				default:
					return nil, fmt.Errorf("invalid payload %T", values[i])
				}
			}
			if err != nil {
				return nil, err
			}
			d, err := domainFor(ctx, tx, o, c, values, raw, false)
			if err != nil {
				return nil, err
			}
			if isKey {
				if Key(d, raw) != values[i] {
					return nil, fmt.Errorf("%w: copy domain mismatch", ErrCanonical)
				}
			} else {
				values[i], err = Put(ctx, tx, d, raw)
				if err != nil {
					return nil, err
				}
			}
		}
		names := o.storageColumns()
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
		command := "INSERT "
		if match[1] != "" {
			command += "OR IGNORE "
		}
		result, err := tx.ExecContext(ctx, command+"INTO "+o.Table+"("+strings.Join(names, ",")+") VALUES("+placeholders+")"+suffix, values...)
		if err != nil {
			return nil, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n != 0 {
			if o.preservesOrdinal() {
				ordinal, e := result.LastInsertId()
				if e != nil {
					return nil, e
				}
				values = append(values, ordinal)
			}
			if err = saveMembership(ctx, tx, o, values); err != nil {
				return nil, err
			}
			affected += n
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return insertResult(affected), nil
}

type insertResult int64

func (r insertResult) LastInsertId() (int64, error) {
	return 0, fmt.Errorf("immutable owners use explicit primary keys")
}
func (r insertResult) RowsAffected() (int64, error) { return int64(r), nil }

func domainFor(ctx context.Context, q Queryer, o Owner, c Column, values []any, raw []byte, legacy bool) (Domain, error) {
	if err := validateProjection(o, values, raw); err != nil {
		return Domain{}, err
	}
	schema := "legacy"
	typ := c.Name
	m := rowMap(o, values)
	if r, ok := m["record_type"].(string); ok {
		typ = r + "/" + c.Name
	}
	// SQLite only observes a small schema tag; the stored bytes never pass through
	// a JSON encoder. Untagged historical formats have a versioned registry domain.
	if err := q.QueryRowContext(ctx, `SELECT CASE WHEN json_valid(?) THEN COALESCE(CAST(json_extract(?,'$.schemaVersion') AS TEXT),CAST(json_extract(?,'$.viewSchemaVersion') AS TEXT),CAST(json_extract(?,'$.profileVersion') AS TEXT),'legacy') ELSE 'opaque' END`, raw, raw, raw, raw).Scan(&schema); err != nil {
		return Domain{}, err
	}
	if o.Table == "backend_graph_records" {
		query := "SELECT COALESCE(CAST(json_extract(document,'$.schemaVersion') AS TEXT),'legacy') FROM backend_revisions WHERE id=? AND project_id=?"
		args := []any{m["revision_id"], m["project_id"]}
		if !legacy {
			ownerID, err := identity(m["project_id"], m["revision_id"])
			if err != nil {
				return Domain{}, err
			}
			query = "SELECT COALESCE(CAST(json_extract(CAST(b.payload AS TEXT),'$.schemaVersion') AS TEXT),'legacy') FROM backend_payload_members m JOIN backend_payload_blobs b ON b.key=m.payload_key WHERE m.owner='backend_revisions' AND m.owner_id=? AND m.version='raw-owner-v1' AND m.member_type='document'"
			args = []any{ownerID}
		}
		if err := q.QueryRowContext(ctx, query, args...).Scan(&schema); err != nil {
			return Domain{}, err
		}
	}
	return Domain{Owner: o.Table + "/" + c.Name, Schema: schema, RecordType: typ}, nil
}
