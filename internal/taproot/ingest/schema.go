package ingest

import "github.com/265866/taproot/internal/taproot/store"

// All timestamps are stored as Unix milliseconds (UTC) so range scans and
// bucketing stay integer arithmetic.
const schemaV1 = `
CREATE TABLE usage_events (
	id INTEGER PRIMARY KEY,
	event_key TEXT NOT NULL UNIQUE,
	ts INTEGER NOT NULL,
	request_id TEXT NOT NULL DEFAULT '',
	trace_id TEXT NOT NULL DEFAULT '',
	session_id TEXT NOT NULL DEFAULT '',
	parent_session_id TEXT NOT NULL DEFAULT '',
	provider TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	alias TEXT NOT NULL DEFAULT '',
	auth_index TEXT NOT NULL DEFAULT '',
	auth_id TEXT NOT NULL DEFAULT '',
	stream INTEGER NOT NULL DEFAULT 0,
	failed INTEGER NOT NULL DEFAULT 0,
	status_code INTEGER NOT NULL DEFAULT 0,
	latency_ms INTEGER NOT NULL DEFAULT 0,
	ttft_ms INTEGER,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_5m_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_1h_tokens INTEGER NOT NULL DEFAULT 0,
	service_tier TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL DEFAULT 'live'
);
CREATE INDEX usage_events_ts ON usage_events(ts, id);
CREATE INDEX usage_events_session_ts ON usage_events(session_id, ts);
CREATE INDEX usage_events_auth_ts ON usage_events(auth_index, ts);
CREATE INDEX usage_events_model_ts ON usage_events(model, ts);

CREATE TABLE usage_rollup_hourly (
	hour INTEGER NOT NULL,
	provider TEXT NOT NULL,
	auth_index TEXT NOT NULL,
	model TEXT NOT NULL,
	requests INTEGER NOT NULL DEFAULT 0,
	failures INTEGER NOT NULL DEFAULT 0,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_5m_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_1h_tokens INTEGER NOT NULL DEFAULT 0,
	latency_hist BLOB,
	ttft_hist BLOB,
	PRIMARY KEY (hour, provider, auth_index, model)
) WITHOUT ROWID;

CREATE TABLE quota_observations (
	id INTEGER PRIMARY KEY,
	auth_index TEXT NOT NULL,
	provider TEXT NOT NULL,
	window_name TEXT NOT NULL,
	utilization REAL NOT NULL,
	resets_at INTEGER,
	status TEXT NOT NULL DEFAULT '',
	period_seconds INTEGER NOT NULL DEFAULT 0,
	observed_at INTEGER NOT NULL,
	source TEXT NOT NULL DEFAULT 'header'
);
CREATE UNIQUE INDEX quota_observations_key ON quota_observations(auth_index, window_name, observed_at);
CREATE INDEX quota_observations_observed ON quota_observations(observed_at);

CREATE TABLE accounts (
	auth_index TEXT PRIMARY KEY,
	provider TEXT NOT NULL DEFAULT '',
	label TEXT NOT NULL DEFAULT '',
	email TEXT NOT NULL DEFAULT '',
	plan TEXT NOT NULL DEFAULT '',
	first_seen INTEGER,
	last_seen INTEGER,
	disabled INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE prices (
	model_pattern TEXT PRIMARY KEY,
	input REAL NOT NULL DEFAULT 0,
	output REAL NOT NULL DEFAULT 0,
	cache_read REAL NOT NULL DEFAULT 0,
	cache_write_5m REAL NOT NULL DEFAULT 0,
	cache_write_1h REAL NOT NULL DEFAULT 0,
	source TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT ''
);
`

func init() {
	store.Register(
		store.Migration{Module: "ingest", Version: 1, SQL: schemaV1},
		store.Migration{Module: "ingest", Version: 2, SQL: seedPricesSQL()},
		// removed_at marks accounts whose credential left the auth manager.
		// The row stays so historical usage keeps its label.
		store.Migration{Module: "ingest", Version: 3, SQL: `ALTER TABLE accounts ADD COLUMN removed_at INTEGER`},
	)
}
