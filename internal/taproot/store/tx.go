package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	log "github.com/sirupsen/logrus"
)

// WithImmediateTx runs fn inside BEGIN IMMEDIATE on one connection. The
// write lock is taken up front, so a read-modify-write is safe when several
// writers share the database. A deferred transaction that reads and then
// writes instead fails with SQLITE_BUSY when another writer commits first,
// and busy_timeout does not retry it.
func WithImmediateTx(ctx context.Context, db *sql.DB, fn func(*sql.Conn) error) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer func() {
		if errClose := conn.Close(); errClose != nil {
			log.WithError(errClose).Warn("taproot store: close connection")
		}
	}()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate: %w", err)
	}
	if err = fn(conn); err != nil {
		if _, errRollback := conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK"); errRollback != nil {
			err = errors.Join(err, fmt.Errorf("rollback: %w", errRollback))
		}
		return err
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		if _, errRollback := conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK"); errRollback != nil {
			log.WithError(errRollback).Warn("taproot store: rollback after failed commit")
		}
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
