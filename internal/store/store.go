// Package store owns the SQLite file: connection pools, pragmas, migrations
// and the write-transaction helper. Nothing else in mocker opens the database.
//
// Two pools on one file (DESIGN §13). SQLite allows exactly one writer, so the
// writer pool is pinned to a single connection: serialising in the pool turns
// SQLITE_BUSY from a race into a queue. Readers get their own pool because WAL
// lets them run concurrently with the writer.
//
// The driver is modernc.org/sqlite — pure Go, so the binary builds with
// CGO_ENABLED=0 and ships on a distroless image with nothing beside it.
package store

import (
	"cmp"
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// pragmas applied to every connection of both pools.
//
// busy_timeout is a full 5s and that is safe here: a blocked query parks its
// goroutine, not the process. auto_vacuum only takes effect on a database that
// is still empty, which is why it is set at open time rather than later.
var pragmas = [][2]string{
	{"journal_mode", "WAL"},
	{"synchronous", "NORMAL"},
	{"foreign_keys", "ON"},
	{"busy_timeout", "5000"},
	{"auto_vacuum", "INCREMENTAL"},
}

// DB holds the writer and reader pools for one SQLite file.
type DB struct {
	// W is the single-connection writer pool. Use [DB.Write] instead of
	// touching it directly unless the statement is a one-shot INSERT/UPDATE.
	W *sql.DB
	// R is the reader pool. Read-only queries only — writing through it
	// defeats the serialisation the writer pool provides.
	R *sql.DB

	path string
}

// Open prepares the data directory, opens both pools and verifies the file is
// reachable. It does not migrate; call [DB.Migrate].
func Open(ctx context.Context, path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create data dir %s: %w", dir, err)
		}
	}

	writer, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		return nil, fmt.Errorf("open writer: %w", err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	writer.SetConnMaxIdleTime(0)

	reader, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("open reader: %w", err)
	}
	reader.SetMaxOpenConns(max(4, runtime.NumCPU()))
	reader.SetMaxIdleConns(4)

	db := &DB{W: writer, R: reader, path: path}
	if err := writer.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping %s: %w", path, err)
	}
	return db, nil
}

// dsn builds the driver DSN. The writer asks for BEGIN IMMEDIATE so a
// read-then-write transaction takes the write lock up front instead of
// discovering a conflict at COMMIT and failing with SQLITE_BUSY_SNAPSHOT.
func dsn(path string, writer bool) string {
	q := make(url.Values)
	for _, p := range pragmas {
		q.Add("_pragma", p[0]+"("+p[1]+")")
	}
	if writer {
		q.Set("_txlock", "immediate")
	}
	return "file:" + path + "?" + q.Encode()
}

// Close shuts both pools down.
func (db *DB) Close() error {
	var errs []error
	if db.R != nil {
		errs = append(errs, db.R.Close())
	}
	if db.W != nil {
		errs = append(errs, db.W.Close())
	}
	return errors.Join(errs...)
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// Write runs fn inside a write transaction, rolling back on error or panic.
//
// Every mutation goes through here: the single-connection writer pool plus one
// transaction per call is what keeps concurrent admin edits from interleaving
// halfway through a multi-table change (a workspace and its first override,
// say) and leaving a workspace nobody can render.
func (db *DB) Write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Migrate applies every embedded migration whose number exceeds the file's
// PRAGMA user_version, each in its own transaction.
//
// user_version is SQLite's own integer slot, so the schema version travels with
// the file: a copied database cannot disagree with a separate bookkeeping table
// about how far it has been migrated.
func (db *DB) Migrate(ctx context.Context, log *slog.Logger) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	var current int
	if err := db.W.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := db.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("migration %04d (%s): %w", m.version, m.name, err)
		}
		if log != nil {
			log.Info("migration applied", "version", m.version, "name", m.name)
		}
		current = m.version
	}
	return nil
}

// SchemaVersion reports the applied migration number.
func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := db.R.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

type migration struct {
	version int
	name    string
	sql     string
}

func (db *DB) applyMigration(ctx context.Context, m migration) error {
	// The statements run inside the transaction; the version bump cannot,
	// because PRAGMA user_version takes a literal only. Setting it right after
	// the commit leaves a crash window of one pragma: on restart the migration
	// would re-run, which is why every migration must be written so that a
	// second run over its own output fails loudly rather than corrupting
	// (CREATE TABLE without IF NOT EXISTS does exactly that).
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, m.sql)
		return err
	}); err != nil {
		return err
	}
	_, err := db.W.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(m.version))
	return err
}

// loadMigrations reads and orders the embedded files, whose names are
// NNNN_description.sql.
func loadMigrations() ([]migration, error) {
	entries, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	out := make([]migration, 0, len(entries))
	seen := make(map[int]string, len(entries))
	for _, name := range entries {
		base := filepath.Base(name)
		numPart, rest, ok := strings.Cut(strings.TrimSuffix(base, ".sql"), "_")
		if !ok {
			return nil, fmt.Errorf("migration %s: want NNNN_description.sql", base)
		}
		version, err := strconv.Atoi(numPart)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("migration %s: bad version prefix", base)
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations %s and %s share version %d", other, base, version)
		}
		seen[version] = base

		body, err := migrationFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: version, name: rest, sql: string(body)})
	}
	slices.SortFunc(out, func(a, b migration) int { return cmp.Compare(a.version, b.version) })
	return out, nil
}
