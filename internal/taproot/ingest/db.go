package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	log "github.com/sirupsen/logrus"
)

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.WithError(err).Warn("taproot ingest: close rows")
	}
}

// withImmediateTx runs fn inside BEGIN IMMEDIATE on one connection. The
// write lock is taken up front, so read-modify-write of rollup rows is safe
// even when the importer and the server write the same database.
func withImmediateTx(ctx context.Context, db *sql.DB, fn func(*sql.Conn) error) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer func() {
		if errClose := conn.Close(); errClose != nil {
			log.WithError(errClose).Warn("taproot ingest: close connection")
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
			log.WithError(errRollback).Warn("taproot ingest: rollback after failed commit")
		}
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
