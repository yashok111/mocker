package backendblob

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Rebuild replaces only the selected project's projections, plus globally
// content-addressed observation blobs. Canonical membership is never rewritten.
// The caller owns the exclusive offline transaction with FKs disabled BEFORE
// BEGIN. Any error (including cancellation or disk full) must roll it back.
func Rebuild(ctx context.Context, tx *sql.Tx, project string) error {
	if project == "" {
		return fmt.Errorf("project is required")
	}
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM backend_projects WHERE id=?", project).Scan(&exists); err != nil {
		return err
	}
	if exists != 1 {
		return fmt.Errorf("unknown project")
	}
	// These are derived lookup indexes over immutable memberships, not canonical
	// history. Recreate them transactionally, including when corruption forces
	// the subsequent canonical verification to roll the transaction back.
	for _, obj := range canonicalIndexes {
		if _, err := tx.ExecContext(ctx, "DROP INDEX IF EXISTS "+obj.Name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, obj.SQL); err != nil {
			return err
		}
	}
	if err := verify(ctx, tx, false, ""); err != nil {
		return err
	}
	for _, o := range owners {
		if err := rebuildOwner(ctx, tx, o, project); err != nil {
			return err
		}
	}
	if err := checkShadowForeignKeys(ctx, tx); err != nil {
		return err
	}
	if err := dropOwnerGuards(ctx, tx, true); err != nil {
		return err
	}
	if err := swapInShadows(ctx, tx, "_blob_rebuild"); err != nil {
		return err
	}
	for _, o := range owners {
		for _, obj := range o.Objects {
			if _, err := tx.ExecContext(ctx, obj.SQL); err != nil {
				return err
			}
		}
	}
	return verify(ctx, tx, true, project)
}

// rebuildOwner fills o's _blob_rebuild shadow: other projects' rows copied
// through, this project's regenerated from canonical members, then both
// checked against the canonical record before anything is swapped.
func rebuildOwner(ctx context.Context, tx *sql.Tx, o Owner, project string) error {
	if _, err := tx.ExecContext(ctx, shadowDDL(o, "_blob_rebuild")); err != nil {
		return err
	}
	//nolint:gosec // G202: table and column names come from the compiled owner registry, never from input
	insert := "INSERT INTO " + o.Table + "_blob_rebuild(" + strings.Join(o.projectionColumns(), ",") + ") VALUES(" + strings.TrimSuffix(strings.Repeat("?,", o.metadataCount()), ",") + ")"
	copied, err := copyForeignRows(ctx, tx, o, insert, project)
	if err != nil {
		return err
	}
	last := ""
	if err = walkMembers(ctx, tx, o, func(id, version, typ, mid, key, pid string, meta []byte, v []any) error {
		if pid != project && pid != "" {
			return nil
		}
		unique := id + "/" + version + "/" + mid
		if unique == last {
			return nil
		}
		last = unique
		_, err := tx.ExecContext(ctx, insert, v...)
		return err
	}); err != nil {
		return err
	}
	// Compare shadows with immutable canonical rows before touching old
	// indexes — only the rows this rebuild regenerated. Other projects'
	// rows were copied through as they are, damaged or missing, and are
	// their own rebuild's to repair: comparing them too (review
	// 2026-10-06, F163) made damage in project B abort A's repair and the
	// reverse, so neither could ever be fixed.
	if err = walkMembers(ctx, tx, o, func(id, version, typ, mid, key, pid string, meta []byte, v []any) error {
		if pid != project && pid != "" {
			return nil
		}
		shadow := o
		shadow.Table = o.Table + "_blob_rebuild"
		got, err := physicalRow(ctx, tx, shadow, v)
		if err != nil {
			return err
		}
		encoded, err := encodeValues(got)
		if err != nil {
			return err
		}
		if string(encoded) != string(meta) {
			return fmt.Errorf("%w: shadow mismatch %s", ErrDerived, o.Table)
		}
		return nil
	}); err != nil {
		return err
	}
	// Every shadow row is either regenerated from this scope's canonical
	// members or copied through, so the count is checked against exactly
	// that sum rather than the global member count a missing row in
	// another project would break.
	var actual, canonical int64
	//nolint:gosec // G202: the table name comes from the compiled owner registry, never from input
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM "+o.Table+"_blob_rebuild").Scan(&actual); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(DISTINCT member_id) FROM backend_payload_members WHERE owner=? AND (project_id=? OR project_id='')", o.Table, project).Scan(&canonical); err != nil {
		return err
	}
	if actual != canonical+copied {
		return fmt.Errorf("%w: shadow count %s", ErrDerived, o.Table)
	}
	return nil
}

// copyForeignRows copies into the shadow, as they are, the rows of o that
// belong to another project, and counts them.
func copyForeignRows(ctx context.Context, tx *sql.Tx, o Owner, insert, project string) (int64, error) {
	//nolint:gosec // G202: table and column names come from the compiled owner registry, never from input
	rows, err := tx.QueryContext(ctx, "SELECT "+strings.Join(o.projectionColumns(), ",")+" FROM "+o.Table+" ORDER BY "+strings.Join(o.PK, ","))
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var copied int64
	for rows.Next() {
		v := make([]any, o.metadataCount())
		ptrs := make([]any, len(v))
		for i := range v {
			ptrs[i] = &v[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			return 0, err
		}
		foreign, err := isForeignRow(ctx, tx, o, v, project)
		if err != nil {
			return 0, err
		}
		if !foreign {
			continue
		}
		if _, err = tx.ExecContext(ctx, insert, v...); err != nil {
			return 0, err
		}
		copied++
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	// The old code dropped this close error; a failed close is now reported.
	return copied, rows.Close()
}

// isForeignRow reports whether a physical row belongs to a project other than
// the one being rebuilt, by its projection or by its canonical membership.
func isForeignRow(ctx context.Context, tx *sql.Tx, o Owner, v []any, project string) (bool, error) {
	pid, _, mid, err := ownerIdentity(ctx, tx, o, v)
	if err != nil {
		return false, err
	}
	var canonicalProject string
	err = tx.QueryRowContext(ctx, "SELECT project_id FROM backend_payload_members WHERE owner=? AND member_id=? LIMIT 1", o.Table, mid).Scan(&canonicalProject)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if pid == project || pid == "" || err == nil && canonicalProject == project {
		return false, nil
	}
	return true, nil
}

// Check relationships against the complete shadow set, including when a damaged
// original parent is precisely what rebuild is repairing. SQLite's ordinary FK
// check runs again after switching the original physical names.
func checkShadowForeignKeys(ctx context.Context, tx *sql.Tx) error {
	for _, o := range owners {
		links, err := shadowRelations(ctx, tx, o)
		if err != nil {
			return err
		}
		for _, r := range links {
			parent := r.parent
			if _, e := Lookup(parent); e == nil {
				parent += "_blob_rebuild"
			}
			var nonnull, equal []string
			for i, col := range r.from {
				nonnull = append(nonnull, `c."`+col+`" IS NOT NULL`)
				equal = append(equal, `p."`+r.to[i]+`"=c."`+col+`"`)
			}
			var count int
			query := "SELECT count(*) FROM " + o.Table + "_blob_rebuild c WHERE " + strings.Join(nonnull, " AND ") + " AND NOT EXISTS(SELECT 1 FROM " + parent + " p WHERE " + strings.Join(equal, " AND ") + ")"
			if err = tx.QueryRowContext(ctx, query).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("%w: shadow foreign key %s to %s", ErrCanonical, o.Table, r.parent)
			}
		}
	}
	return nil
}

type shadowRelation struct {
	parent   string
	from, to []string
}

// shadowRelations reads the foreign keys SQLite compiled for o's shadow,
// grouping a composite key's columns under its id.
func shadowRelations(ctx context.Context, tx *sql.Tx, o Owner) (map[int]*shadowRelation, error) {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_list("+o.Table+"_blob_rebuild)")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	links := map[int]*shadowRelation{}
	for rows.Next() {
		var id, seq int
		var parent, from, to, onUpdate, onDelete, match string
		if err = rows.Scan(&id, &seq, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return nil, err
		}
		r := links[id]
		if r == nil {
			r = &shadowRelation{parent: parent}
			links[id] = r
		}
		r.from = append(r.from, from)
		r.to = append(r.to, to)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return links, rows.Close()
}
