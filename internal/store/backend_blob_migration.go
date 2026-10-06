package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"github.com/yashok111/mocker/internal/backendblob"
)

func (db *DB) applyBlobMigration(ctx context.Context, m migration) (err error) {
	conn, err := db.W.Conn(ctx)
	if err != nil {
		return err
	}
	var foreign int
	if err = conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreign); err != nil {
		_ = conn.Close()
		return err
	}
	defer func() {
		restore, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if restoreErr := migration21ForeignKeyMode(restore, conn, foreign != 0); restoreErr != nil {
			err = errors.Join(err, restoreErr, conn.Raw(func(any) error { return driver.ErrBadConn }))
		}
		err = errors.Join(err, conn.Close())
	}()
	if err = migration21ForeignKeyMode(ctx, conn, false); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
			err = errors.Join(err, e)
		}
	}()
	if _, err = tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if err = backendblob.Migrate(ctx, tx); err != nil {
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
		return fmt.Errorf("migration integrity: %s", integrity)
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=27"); err != nil {
		return err
	}
	return tx.Commit()
}
