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
	defer conn.Close()
	// This is a private maintenance connection; FK mode never escapes a pool.
	if _, err = conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("exclusive maintenance ownership unavailable (stop application): %w", err)
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != 27 {
		return fmt.Errorf("backend storage requires Store27, got %d; maintenance never migrates", version)
	}
	if rebuild {
		err = backendblob.Rebuild(ctx, tx, project)
	} else {
		err = backendblob.Verify(ctx, tx)
	}
	if err != nil {
		return err
	}
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
	if !rebuild {
		return tx.Rollback()
	}
	return tx.Commit()
}
