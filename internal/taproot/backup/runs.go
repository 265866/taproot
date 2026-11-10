package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/265866/taproot/internal/taproot/store"
)

func init() {
	store.Register(store.Schema{Module: "backup", SQL: `CREATE TABLE IF NOT EXISTS backup_scheduled_run (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		ran_at INTEGER NOT NULL,
		error TEXT NOT NULL DEFAULT ''
	)`})
}

// Run is the outcome of the last scheduled backup.
type Run struct {
	At time.Time
	// Error is why the run failed, or "" when it wrote a backup.
	Error string
}

// RecordScheduledRun stores the outcome of a scheduled run, replacing the
// previous one. The schedule loop calls it after each run.
func (b *Backups) RecordScheduledRun(ctx context.Context, at time.Time, runErr error) error {
	msg := ""
	if runErr != nil {
		msg = runErr.Error()
	}
	_, err := b.db.ExecContext(ctx, `INSERT INTO backup_scheduled_run (id, ran_at, error) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET ran_at = excluded.ran_at, error = excluded.error`, at.UnixMilli(), msg)
	if err != nil {
		return fmt.Errorf("record scheduled backup run: %w", err)
	}
	return nil
}

// LastScheduledRun returns the outcome of the last scheduled run, and false
// when none was recorded.
func (b *Backups) LastScheduledRun(ctx context.Context) (Run, bool, error) {
	var (
		at  int64
		msg string
	)
	err := b.db.QueryRowContext(ctx, `SELECT ran_at, error FROM backup_scheduled_run WHERE id = 1`).Scan(&at, &msg)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, fmt.Errorf("read last scheduled backup run: %w", err)
	}
	return Run{At: time.UnixMilli(at).UTC(), Error: msg}, true, nil
}
