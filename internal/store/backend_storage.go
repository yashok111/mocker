package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/yashok111/mocker/internal/backendblob"
)

// BackendStorage is strictly local offline maintenance. It never calls Open or
// Migrate and never creates a database. SQLite EXCLUSIVE locking mode in WAL
// acquires exclusive ownership against other connections, including readers.
func BackendStorage(ctx context.Context, path, project string, rebuild bool) (err error) {
	return backendMaintenance(ctx, path, rebuild, false, func(tx *sql.Tx) error {
		if rebuild {
			return backendblob.Rebuild(ctx, tx, project)
		}
		return backendblob.Verify(ctx, tx)
	})
}

// BackendRemediation previews or erases one project only while the application
// is stopped. Confirmation is bound to every planned stored row, not its label.
func BackendRemediation(ctx context.Context, path, project, confirmation string) (out any, err error) {
	err = backendMaintenance(ctx, path, confirmation != "", true, func(tx *sql.Tx) error {
		if confirmation == "" {
			out, err = backendblob.PreviewProjectPurge(ctx, tx, project)
		} else {
			out, err = backendblob.PurgeProject(ctx, tx, project, confirmation)
		}
		return err
	})
	return out, err
}

func backendMaintenance(ctx context.Context, path string, write, erasure bool, operation func(*sql.Tx) error) (err error) {
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("database must be a regular file")
	}
	u := url.URL{Path: filepath.ToSlash(path)}
	params := url.Values{"mode": {"rw"}, "_pragma": {"busy_timeout(0)", "locking_mode(EXCLUSIVE)"}, "_txlock": {"exclusive"}}
	db, err := sql.Open("sqlite", "file:"+u.EscapedPath()+"?"+params.Encode())
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer func() { err = errors.Join(err, db.Close()) }()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	// The pool is private and closed right after, so a failed return of the
	// pinned connection is reported with the result instead of dropped.
	defer func() { err = errors.Join(err, conn.Close()) }()
	// This is a private maintenance connection; FK mode never escapes a pool.
	if _, err = conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	if erasure {
		if _, err = conn.ExecContext(ctx, "PRAGMA secure_delete=ON"); err != nil {
			return err
		}
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("exclusive maintenance ownership unavailable (stop application): %w", err)
	}
	// The verify path ends in an explicit Rollback and the rebuild path in a
	// Commit, so only an early return reaches here with a live transaction.
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback backend storage maintenance: %w", rollbackErr))
		}
	}()
	if err = backendMaintenanceVersion(ctx, tx, erasure); err != nil {
		return err
	}
	err = operation(tx)
	if err != nil {
		return err
	}
	if err = verifyBackendMaintenance(ctx, tx); err != nil {
		return err
	}
	if !write {
		return tx.Rollback()
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if erasure {
		return finishBackendErasure(ctx, conn)
	}

	return nil
}

func backendMaintenanceVersion(ctx context.Context, tx *sql.Tx, erasure bool) error {
	var err error
	var version int
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	latest, err := LatestSchemaVersion()
	if err != nil {
		return err
	}
	if version < 27 || version > latest || erasure && version < 29 {
		return fmt.Errorf("unsupported Store%d for this maintenance operation; erasure requires Store29 or newer and maintenance never migrates", version)
	}
	return nil
}
func verifyBackendMaintenance(ctx context.Context, tx *sql.Tx) error {
	var err error
	if err = checkMigration21ForeignKeys(ctx, tx); err != nil {
		return err
	}
	var integrity string
	if err = tx.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("integrity: %s", integrity)
	}
	return nil
}

func finishBackendErasure(ctx context.Context, conn *sql.Conn) error {
	var err error
	// secure_delete only clears pages freed by this operation. VACUUM also
	// removes old freelist contents left by earlier updates/deletions. The
	// receipt was committed already, so repeating its exact confirmation can
	// finish this phase after cancellation, disk-full or a lost response.
	if _, err = conn.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("erasure committed; database compaction failed, replay the original confirmation: %w", err)
	}
	var busy, log, checkpointed int
	if err = conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed); err != nil {
		return fmt.Errorf("erasure committed; WAL checkpoint failed, replay the original confirmation: %w", err)
	}
	if busy != 0 {
		return fmt.Errorf("erasure committed; WAL checkpoint busy, replay the original confirmation")
	}
	return nil
}
