package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
)

// applyMigration21 runs before traffic on a pinned connection. SQLite ignores
// foreign_keys changes inside a transaction, so this rebuild cannot use Write.
func (db *DB) applyMigration21(ctx context.Context, m migration) (err error) {
	conn, err := db.W.Conn(ctx)
	if err != nil {
		return fmt.Errorf("pin migration writer: %w", err)
	}
	defer func() {
		err = errors.Join(err, restoreMigration21Connection(ctx, conn))
		err = errors.Join(err, conn.Close())
	}()
	if err := migration21ForeignKeyMode(ctx, conn, false); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration rebuild: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback migration rebuild: %w", rollbackErr))
		}
	}()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if err := checkMigration21ForeignKeys(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version=21"); err != nil {
		return err
	}
	return tx.Commit()
}

func migration21ForeignKeyMode(ctx context.Context, conn *sql.Conn, enabled bool) error {
	value, want := "OFF", 0
	if enabled {
		value, want = "ON", 1
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys="+value); err != nil {
		return fmt.Errorf("set foreign_keys %s: %w", value, err)
	}
	var got int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&got); err != nil {
		return fmt.Errorf("read foreign_keys: %w", err)
	}
	if got != want {
		return fmt.Errorf("foreign_keys=%d after requesting %s", got, value)
	}
	return nil
}

func checkMigration21ForeignKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("migration foreign_key_check: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only pragma
	if rows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var fk int
		if err := rows.Scan(&table, &rowID, &parent, &fk); err != nil {
			return err
		}
		return fmt.Errorf("migration foreign key violation: table=%s row=%v parent=%s key=%d", table, rowID, parent, fk)
	}
	return rows.Err()
}

// Restoration uses a fresh bounded context even if startup was cancelled. A
// connection that cannot prove FK enforcement is never returned to the pool.
func restoreMigration21Connection(ctx context.Context, conn *sql.Conn) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := migration21ForeignKeyMode(ctx, conn, true); err != nil {
		discardErr := conn.Raw(func(any) error { return driver.ErrBadConn })
		return errors.Join(fmt.Errorf("restore migration foreign keys: %w", err), discardErr)
	}
	return nil
}
