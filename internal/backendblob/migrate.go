package backendblob

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"slices"
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
		if err = migrateOwner(ctx, tx, o); err != nil {
			return err
		}
	}
	if err := Seal(ctx, tx); err != nil {
		return err
	}
	if err := verify(ctx, tx, false, ""); err != nil {
		return err
	}
	// Remove guards before dropping referenced tables. Recreate their compiled
	// definitions only after all physical FK parents have their original names.
	if err := dropOwnerGuards(ctx, tx, false); err != nil {
		return err
	}
	if err := swapInShadows(ctx, tx, "_blob_new"); err != nil {
		return err
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

// shadowDDL renames the compiled CREATE TABLE of o to its shadow table.
func shadowDDL(o Owner, suffix string) string {
	ddl := strings.Replace(o.DDL, "CREATE TABLE "+o.Table+" (", "CREATE TABLE "+o.Table+suffix+" (", 1)
	return strings.Replace(ddl, `CREATE TABLE "`+o.Table+`" (`, "CREATE TABLE "+o.Table+suffix+" (", 1)
}

// migrateOwner copies every historical row of o into its _blob_new shadow,
// page by page in rowid order, moving each payload into the blob store.
func migrateOwner(ctx context.Context, tx *sql.Tx, o Owner) error {
	if _, err := tx.ExecContext(ctx, shadowDDL(o, "_blob_new")); err != nil {
		return fmt.Errorf("create %s: %w", o.Table, err)
	}
	var last int64
	for firstPage := true; ; firstPage = false {
		count, err := migratePageRows(ctx, tx, o, &last, firstPage)
		if err != nil {
			return err
		}
		if count < 64 {
			return nil
		}
	}
}

// migratePageRows copies one page after rowid *last and advances *last; the
// first page has no lower bound.
func migratePageRows(ctx context.Context, tx *sql.Tx, o Owner, last *int64, firstPage bool) (int, error) {
	names := make([]string, len(o.Columns))
	for i, c := range o.Columns {
		names[i] = c.Name
	}
	where := " WHERE rowid>?"
	args := []any{*last}
	if firstPage {
		where = ""
		args = nil
	}
	//nolint:gosec // G202: table and column names come from the compiled owner registry, never from input
	rows, err := tx.QueryContext(ctx, "SELECT rowid,"+strings.Join(names, ",")+" FROM "+o.Table+where+" ORDER BY rowid LIMIT 64", args...)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		values := make([]any, len(o.Columns))
		ptrs := make([]any, len(values)+1)
		ptrs[0] = last
		for i := range values {
			ptrs[i+1] = &values[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			return 0, err
		}
		count++
		if err = migrateRow(ctx, tx, o, values, *last); err != nil {
			return 0, err
		}
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	// A failed close is reported, not swallowed by the deferred one.
	if err = rows.Close(); err != nil {
		return 0, err
	}
	return count, nil
}

// migrateRow stores one historical row's payloads as blobs and writes the row
// with their keys into the shadow, together with its legacy membership.
func migrateRow(ctx context.Context, tx *sql.Tx, o Owner, values []any, rowid int64) error {
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
			return fmt.Errorf("invalid historical payload %s.%s", o.Table, c.Name)
		}
		d, err := domainFor(ctx, tx, o, c, values, raw, true)
		if err != nil {
			return err
		}
		values[i], err = Put(ctx, tx, d, raw)
		if err != nil {
			return err
		}
	}
	if o.preservesOrdinal() {
		// The ordinal rides after the columns, as the projection lists it.
		values = slices.Concat(values, []any{rowid})
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
	//nolint:gosec // G202: table and column names come from the compiled owner registry, never from input
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+o.Table+"_blob_new("+strings.Join(o.projectionColumns(), ",")+") VALUES("+placeholders+")", values...); err != nil {
		return err
	}
	return saveMembershipMode(ctx, tx, o, values, true)
}

// dropOwnerGuards drops every owner's triggers (and, for a rebuild, its
// documents view) so the physical tables beneath them can be swapped.
func dropOwnerGuards(ctx context.Context, tx *sql.Tx, views bool) error {
	for _, o := range owners {
		for _, obj := range o.Objects {
			if obj.Kind == "trigger" {
				if _, err := tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+obj.Name); err != nil {
					return err
				}
			}
		}
		if !views {
			continue
		}
		if _, err := tx.ExecContext(ctx, "DROP VIEW IF EXISTS "+o.Table+"_documents"); err != nil {
			return err
		}
	}
	return nil
}

// swapInShadows replaces every owner table by its shadow and recreates the
// documents views. All drops precede all renames: a shadow's FK parents must
// not be renamed while an original still holds the name.
func swapInShadows(ctx context.Context, tx *sql.Tx, suffix string) error {
	for _, o := range owners {
		if _, err := tx.ExecContext(ctx, "DROP TABLE "+o.Table); err != nil {
			return err
		}
	}
	for _, o := range owners {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE "+o.Table+suffix+" RENAME TO "+o.Table); err != nil {
			return err
		}
	}
	for _, o := range owners {
		if _, err := tx.ExecContext(ctx, o.ViewSQL()); err != nil {
			return err
		}
	}
	return nil
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
	defer func() { _ = rows.Close() }()
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
