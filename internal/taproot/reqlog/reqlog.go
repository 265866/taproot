// Package reqlog stores request logs natively: compressed, indexed, and pruned
// to a configured budget.
package reqlog

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/265866/taproot/internal/config"
	"github.com/265866/taproot/internal/logging"
	"github.com/265866/taproot/internal/taproot/store"
)

func init() {
	store.Register(store.Schema{Module: "reqlog", SQL: `
CREATE TABLE IF NOT EXISTS reqlog_segments (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL,
	day TEXT NOT NULL,
	state TEXT NOT NULL,
	dict_id INTEGER NOT NULL DEFAULT 0,
	raw_bytes INTEGER NOT NULL DEFAULT 0,
	disk_bytes INTEGER NOT NULL DEFAULT 0,
	first_ts INTEGER,
	last_ts INTEGER,
	created_at INTEGER NOT NULL,
	sealed_at INTEGER
);
CREATE INDEX IF NOT EXISTS reqlog_segments_state ON reqlog_segments (state, last_ts);

CREATE TABLE IF NOT EXISTS reqlog_records (
	id INTEGER PRIMARY KEY,
	request_id TEXT NOT NULL,
	trace_id TEXT NOT NULL DEFAULT '',
	session_id TEXT NOT NULL DEFAULT '',
	parent_session_id TEXT NOT NULL DEFAULT '',
	ts INTEGER NOT NULL,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	method TEXT NOT NULL,
	path TEXT NOT NULL,
	url TEXT NOT NULL,
	status INTEGER NOT NULL,
	model TEXT NOT NULL DEFAULT '',
	auth_index TEXT NOT NULL DEFAULT '',
	streaming INTEGER NOT NULL DEFAULT 0,
	error_only INTEGER NOT NULL DEFAULT 0,
	dropped TEXT NOT NULL DEFAULT '',
	req_bytes INTEGER NOT NULL DEFAULT 0,
	resp_bytes INTEGER NOT NULL DEFAULT 0,
	text_bytes INTEGER NOT NULL DEFAULT 0,
	segment_id INTEGER NOT NULL REFERENCES reqlog_segments (id),
	offset INTEGER NOT NULL,
	length INTEGER NOT NULL,
	UNIQUE (segment_id, offset)
);
CREATE INDEX IF NOT EXISTS reqlog_records_ts ON reqlog_records (ts, id);
CREATE INDEX IF NOT EXISTS reqlog_records_request_id ON reqlog_records (request_id);
CREATE INDEX IF NOT EXISTS reqlog_records_session ON reqlog_records (session_id, ts);
-- List filters walk these newest first instead of scanning every record.
-- errors_ts serves every status filter at or above 400 (statusClause repeats
-- its term), and text_bytes covers the stats sum.
CREATE INDEX IF NOT EXISTS reqlog_records_model ON reqlog_records (model, ts);
CREATE INDEX IF NOT EXISTS reqlog_records_auth ON reqlog_records (auth_index, ts);
CREATE INDEX IF NOT EXISTS reqlog_records_errors_ts ON reqlog_records (ts) WHERE status >= 400;
CREATE INDEX IF NOT EXISTS reqlog_records_text_bytes ON reqlog_records (text_bytes);

CREATE TABLE IF NOT EXISTS reqlog_packs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	state TEXT NOT NULL,
	bytes INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS reqlog_blobs (
	hash BLOB PRIMARY KEY,
	pack_id INTEGER NOT NULL REFERENCES reqlog_packs (id),
	offset INTEGER NOT NULL,
	stored_bytes INTEGER NOT NULL,
	raw_bytes INTEGER NOT NULL,
	codec INTEGER NOT NULL,
	last_ts INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS reqlog_blobs_pack ON reqlog_blobs (pack_id);
CREATE INDEX IF NOT EXISTS reqlog_blobs_last_ts ON reqlog_blobs (last_ts);
`})
}

// Logs owns request log storage.
type Logs struct {
	engine *engine
	logger *requestLogger
	cancel context.CancelFunc
}

// New prepares the module and recovers segments a previous run left open.
// Recording starts when the server installs the logger from LoggerFactory;
// writing to disk starts with Start.
func New(cfg Config, db *store.DB, dataDir string) (*Logs, error) {
	return newLogs(context.Background(), cfg, db, dataDir, time.Now)
}

func newLogs(ctx context.Context, cfg Config, db *store.DB, dataDir string, now func() time.Time) (*Logs, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	eng, err := newEngine(ctx, db, filepath.Join(dataDir, "request-logs"), cfg, now)
	if err != nil {
		return nil, err
	}
	logger := &requestLogger{engine: eng, now: now}
	ex := newExcluder(cfg)
	logger.excluder.Store(&ex)
	return &Logs{engine: eng, logger: logger}, nil
}

// LoggerFactory replaces upstream's file request logger. The server calls it
// once; request-log in config.yaml stays the on/off switch.
func (l *Logs) LoggerFactory() func(*config.Config, string) logging.RequestLogger {
	return func(cfg *config.Config, _ string) logging.RequestLogger {
		l.logger.SetEnabled(cfg != nil && cfg.RequestLog)
		return l.logger
	}
}

// Start runs the writer, compactor, and retention until ctx ends. Records
// still queued when ctx ends are written before the writer exits.
func (l *Logs) Start(ctx context.Context) {
	ctx, l.cancel = context.WithCancel(ctx)
	l.engine.start(ctx)
}

// Close stops background work and waits for it.
func (l *Logs) Close() {
	if l.cancel != nil {
		l.cancel()
	}
	l.engine.wait()
}

// Retention is the editable subset of Config for the settings page.
type Retention struct {
	MaxDays         int      `json:"maxDays"`
	MaxBytes        ByteSize `json:"maxBytes"`
	ExcludePaths    []string `json:"excludePaths"`
	KeepUnrouted404 bool     `json:"keepUnrouted404"`
}

// Retention returns the active retention settings.
func (l *Logs) Retention() Retention {
	cfg := l.engine.retention.Load()
	return Retention{MaxDays: cfg.MaxDays, MaxBytes: cfg.MaxBytes, ExcludePaths: cfg.ExcludePaths, KeepUnrouted404: cfg.KeepUnrouted404}
}

// SetRetention applies new retention settings without a restart. Persisting
// them to taproot.yaml is the caller's job.
func (l *Logs) SetRetention(r Retention) error {
	cfg := *l.engine.retention.Load()
	cfg.MaxDays, cfg.MaxBytes, cfg.ExcludePaths, cfg.KeepUnrouted404 = r.MaxDays, r.MaxBytes, r.ExcludePaths, r.KeepUnrouted404
	if err := cfg.validate(); err != nil {
		return fmt.Errorf("request log retention: %w", err)
	}
	cfg = cfg.withDefaults()
	l.engine.retention.Store(&cfg)
	ex := newExcluder(cfg)
	l.logger.excluder.Store(&ex)
	return nil
}
