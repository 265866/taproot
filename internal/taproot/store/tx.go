package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"

	log "github.com/sirupsen/logrus"
)

// WithImmediateTx runs fn inside BEGIN IMMEDIATE on one connection. The
// write lock is taken up front, so a read-modify-write is safe when several
// writers share the database. A deferred transaction that reads and then
// writes instead fails with SQLITE_BUSY when another writer commits first,
// and busy_timeout does not retry it.
//
// A connection that may still be inside a transaction is discarded rather
// than returned to the pool: the driver does not check for an open
// transaction when it reuses one, so it would keep the write lock and every
// other writer would fail with SQLITE_BUSY until a restart.
func WithImmediateTx(ctx context.Context, db *sql.DB, fn func(*sql.Conn) error) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	dirty := false
	defer func() {
		if dirty {
			// Raw closes the connection when f returns ErrBadConn.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			return
		}
		if errClose := conn.Close(); errClose != nil {
			log.WithError(errClose).Warn("taproot store: close connection")
		}
	}()
	rollback := func() error {
		_, errRollback := conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		dirty = errRollback != nil
		return errRollback
	}
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		dirty = true
		return fmt.Errorf("begin immediate: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			if errRollback := rollback(); errRollback != nil {
				log.WithError(errRollback).Warn("taproot store: rollback after panic")
			}
			panic(p)
		}
	}()
	if err = fn(conn); err != nil {
		if errRollback := rollback(); errRollback != nil {
			err = errors.Join(err, fmt.Errorf("rollback: %w", errRollback))
		}
		return err
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		if errRollback := rollback(); errRollback != nil {
			log.WithError(errRollback).Warn("taproot store: rollback after failed commit")
		}
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
