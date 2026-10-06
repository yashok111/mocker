package backendblob

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

// Migrate runs entirely inside the caller's pinned migration transaction.
// Foreign-key mode is set and restored by store outside that transaction.
func Migrate(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, Schema); err != nil {
		return err
	}
	witnesses := make(map[string]string, len(owners))
	for _, o := range owners {
		witness, err := rowDigest(ctx, tx, o, o.Table)
		if err != nil {
			return err
		}
		witnesses[o.Table] = witness
		ddl := strings.Replace(o.DDL, "CREATE TABLE "+o.Table+" (", "CREATE TABLE "+o.Table+"_blob_new (", 1)
		ddl = strings.Replace(ddl, `CREATE TABLE "`+o.Table+`" (`, "CREATE TABLE "+o.Table+"_blob_new (", 1)
		if _, err = tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("create %s: %w", o.Table, err)
		}
		var last int64
		firstPage := true
		for {
			names := make([]string, len(o.Columns))
			for i, c := range o.Columns {
				names[i] = c.Name
			}
			where := " WHERE rowid>?"
			args := []any{last}
			if firstPage {
				where = ""
				args = nil
				firstPage = false
			}
			rows, err := tx.QueryContext(ctx, "SELECT rowid,"+strings.Join(names, ",")+" FROM "+o.Table+where+" ORDER BY rowid LIMIT 64", args...)
			if err != nil {
				return err
			}
			count := 0
			for rows.Next() {
				values := make([]any, len(o.Columns))
				ptrs := make([]any, len(values)+1)
				ptrs[0] = &last
				for i := range values {
					ptrs[i+1] = &values[i]
				}
				if err = rows.Scan(ptrs...); err != nil {
					rows.Close()
					return err
				}
				count++
				for i, c := range o.Columns {
					if !c.Payload {
						continue
					}
					var raw []byte
					switch x := values[i].(type) {
					case string:
						raw = []byte(x)
					case []byte:
						raw = x
					default:
						rows.Close()
						return fmt.Errorf("invalid historical payload %s.%s", o.Table, c.Name)
					}
					d, err := domainFor(ctx, tx, o, c, values, raw, true)
					if err != nil {
						rows.Close()
						return err
					}
					values[i], err = Put(ctx, tx, d, raw)
					if err != nil {
						rows.Close()
						return err
					}
				}
				if o.preservesOrdinal() {
					values = append(values, last)
				}
				placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
				if _, err = tx.ExecContext(ctx, "INSERT INTO "+o.Table+"_blob_new("+strings.Join(o.projectionColumns(), ",")+") VALUES("+placeholders+")", values...); err != nil {
					rows.Close()
					return err
				}
				if err = saveMembershipMode(ctx, tx, o, values, true); err != nil {
					rows.Close()
					return err
				}
			}
			err = rows.Err()
			closeErr := rows.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			if count < 64 {
				break
			}
		}
	}
	if err := Seal(ctx, tx); err != nil {
		return err
	}
	if err := verify(ctx, tx, false); err != nil {
		return err
	}
	// Remove guards before dropping referenced tables. Recreate their compiled
	// definitions only after all physical FK parents have their original names.
	for _, o := range owners {
		for _, obj := range o.Objects {
			if obj.Kind == "trigger" {
				if _, err := tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+obj.Name); err != nil {
					return err
				}
			}
		}
	}
	for _, o := range owners {
		if _, err := tx.ExecContext(ctx, "DROP TABLE "+o.Table); err != nil {
			return err
		}
	}
	for _, o := range owners {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE "+o.Table+"_blob_new RENAME TO "+o.Table); err != nil {
			return err
		}
	}
	for _, o := range owners {
		if _, err := tx.ExecContext(ctx, o.ViewSQL()); err != nil {
			return err
		}
	}
	for _, o := range owners {
		for _, obj := range o.Objects {
			if _, err := tx.ExecContext(ctx, obj.SQL); err != nil {
				return fmt.Errorf("restore %s: %w", obj.Name, err)
			}
		}
	}
	for _, o := range owners {
		witness, err := rowDigest(ctx, tx, o, o.Table+"_documents")
		if err != nil {
			return err
		}
		if witness != witnesses[o.Table] {
			return fmt.Errorf("%w: migration byte/count witness %s", ErrCanonical, o.Table)
		}
	}
	return Verify(ctx, tx)
}
func rowDigest(ctx context.Context, q Reader, o Owner, table string) (string, error) {
	cols := make([]string, len(o.Columns))
	for i, c := range o.Columns {
		cols[i] = c.Name
	}
	rows, err := q.QueryContext(ctx, "SELECT "+strings.Join(cols, ",")+" FROM "+table+" ORDER BY "+strings.Join(o.PK, ","))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	h := sha256.New()
	stringField(h, o.Table)
	var count int64
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			return "", err
		}
		raw, err := encodeValues(values)
		if err != nil {
			return "", err
		}
		field(h, raw)
		count++
	}
	stringField(h, fmt.Sprint(count))
	return hex.EncodeToString(h.Sum(nil)), rows.Err()
}
