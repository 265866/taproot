package ingest

import (
	"context"
	"database/sql"

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
