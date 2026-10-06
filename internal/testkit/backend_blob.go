package testkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/yashok111/mocker/internal/store"
	"regexp"
	"strings"

	"github.com/yashok111/mocker/internal/backendblob"
)

// ExecBackendOwner adapts fixture INSERTs to Store27 while preserving old-schema
// migration fixtures. It does not bypass immutable guards or validate test data.
func ExecBackendOwner(ctx context.Context, q any, query string, args ...any) (sql.Result, error) {
	switch db := q.(type) {
	case *sql.DB:
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		result, err := ExecBackendOwner(ctx, tx, query, args...)
		if err != nil {
			return nil, err
		}
		if err = backendblob.Seal(ctx, tx); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return result, nil
	case *sql.Tx:
		var version int
		if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			return nil, err
		}
		if version < 27 {
			return db.ExecContext(ctx, query, args...)
		}
		for _, o := range backendblob.Registry() {
			pattern := regexp.MustCompile(`(?i)(INSERT\s+(?:OR\s+IGNORE\s+)?INTO\s+` + o.Table + `)\s+(VALUES|SELECT)\b`)
			cols := make([]string, len(o.Columns))
			for i, c := range o.Columns {
				cols[i] = c.Name
			}
			query = pattern.ReplaceAllString(query, "${1}("+strings.Join(cols, ",")+") ${2}")
			for _, keyword := range []string{"FROM ", "JOIN "} {
				query = strings.ReplaceAll(query, keyword+o.Table+" ", keyword+o.Table+"_documents ")
			}
		}
		return backendblob.Exec(ctx, db, query, args...)
	default:
		return nil, fmt.Errorf("unsupported fixture writer %T", q)
	}
}

// EditLegacyBackendFixture is a test-only schema fixture builder. Historical
// tests used to edit Store26 TEXT directly to seed whitespace/legacy shapes.
// Reconstruct that isolated shape, apply the supplied fixture edit, then use the
// production Store27 migration. This is never an operator downgrade facility.
func EditLegacyBackendFixture(ctx context.Context, db *store.DB, edit func(*sql.Tx) error) (err error) {
	conn, err := db.W.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_, e := conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys=ON")
			err = errors.Join(err, e)
		}
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	owners := backendblob.Registry()
	for _, o := range owners {
		ddl := strings.Replace(o.OldDDL, "CREATE TABLE "+o.Table+" (", "CREATE TABLE "+o.Table+"_fixture26 (", 1)
		ddl = strings.Replace(ddl, `CREATE TABLE "`+o.Table+`" (`, "CREATE TABLE "+o.Table+"_fixture26 (", 1)
		if _, err = tx.ExecContext(ctx, ddl); err != nil {
			return err
		}
		cols := make([]string, len(o.Columns))
		for i, c := range o.Columns {
			cols[i] = c.Name
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO "+o.Table+"_fixture26("+strings.Join(cols, ",")+") SELECT "+strings.Join(cols, ",")+" FROM "+o.Table+"_documents"); err != nil {
			return err
		}
	}
	for _, o := range owners {
		for _, obj := range o.Objects {
			if obj.Kind == "trigger" {
				if _, err = tx.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+obj.Name); err != nil {
					return err
				}
			}
		}
		if _, err = tx.ExecContext(ctx, "DROP VIEW "+o.Table+"_documents"); err != nil {
			return err
		}
	}
	for _, o := range owners {
		if _, err = tx.ExecContext(ctx, "DROP TABLE "+o.Table); err != nil {
			return err
		}
	}
	for _, o := range owners {
		if _, err = tx.ExecContext(ctx, "ALTER TABLE "+o.Table+"_fixture26 RENAME TO "+o.Table); err != nil {
			return err
		}
	}
	for _, o := range owners {
		for _, obj := range o.OldObjects {
			if _, err = tx.ExecContext(ctx, obj.SQL); err != nil {
				return err
			}
		}
	}
	for _, table := range []string{"backend_payload_members", "backend_payload_manifests", "backend_payload_pending", "backend_payload_blobs"} {
		if _, err = tx.ExecContext(ctx, "DROP TABLE "+table); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=26"); err != nil {
		return err
	}
	if err = edit(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		return err
	}
	if err = conn.Close(); err != nil {
		return err
	}
	closed = true
	// The deferred restoration handles failures above; after returning the pinned
	// connection, Migrate owns its own settings restoration.
	return db.Migrate(ctx, nil)
}

// EditLegacyBackendPayload keeps a valid legacy fixture edit inside the upgrade
// transaction fixture; production immutable tables remain guarded.
func EditLegacyBackendPayload(ctx context.Context, db *store.DB, query string, args ...any) (result sql.Result, err error) {
	err = EditLegacyBackendFixture(ctx, db, func(tx *sql.Tx) error { var e error; result, e = tx.ExecContext(ctx, query, args...); return e })
	return
}
