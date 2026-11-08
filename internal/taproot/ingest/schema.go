package ingest

import "github.com/265866/taproot/internal/taproot/store"

// All timestamps are stored as Unix milliseconds (UTC) so range scans and
// bucketing stay integer arithmetic.
//
// Events recorded before device attribution have an empty device_id; the API
// reports them as the "(none)" group so totals still add up.
const schema = `
CREATE TABLE IF NOT EXISTS usage_events (
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
	source TEXT NOT NULL DEFAULT 'live',
	device_id TEXT NOT NULL DEFAULT '',
	device_name TEXT NOT NULL DEFAULT '',
	device_login TEXT NOT NULL DEFAULT '',
	device_via TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS usage_events_ts ON usage_events(ts, id);
CREATE INDEX IF NOT EXISTS usage_events_session_ts ON usage_events(session_id, ts);
CREATE INDEX IF NOT EXISTS usage_events_auth_ts ON usage_events(auth_index, ts);
CREATE INDEX IF NOT EXISTS usage_events_model_ts ON usage_events(model, ts);
CREATE INDEX IF NOT EXISTS usage_events_device_ts ON usage_events(device_id, ts);
-- Covers the cache TTL estimate's walk over main conversations.
CREATE INDEX IF NOT EXISTS usage_events_main_ts ON usage_events(ts, session_id, auth_index, model, cache_write_5m_tokens, device_id)
	WHERE ` + mainConversationSQL + `;

CREATE TABLE IF NOT EXISTS usage_rollup_hourly (
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

-- These tables, like usage_rollup_hourly, derive from usage_events and are
-- rebuilt from it when empty or stale (see rebuildDerived). main marks
-- Claude main-conversation requests.
CREATE TABLE IF NOT EXISTS usage_device_hourly (
	hour INTEGER NOT NULL,
	device_id TEXT NOT NULL,
	model TEXT NOT NULL,
	main INTEGER NOT NULL,
	requests INTEGER NOT NULL DEFAULT 0,
	failures INTEGER NOT NULL DEFAULT 0,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_5m_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_1h_tokens INTEGER NOT NULL DEFAULT 0,
	split_requests INTEGER NOT NULL DEFAULT 0,
	latency_hist BLOB,
	ttft_hist BLOB,
	PRIMARY KEY (hour, device_id, model, main)
) WITHOUT ROWID;

-- Events without a session have the empty session_id, as in usage_events.
CREATE TABLE IF NOT EXISTS usage_session_hourly (
	hour INTEGER NOT NULL,
	session_id TEXT NOT NULL,
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
	PRIMARY KEY (hour, session_id, model)
) WITHOUT ROWID;

-- Each session's first and last request, so the sessions list finds a
-- range's newest sessions without grouping every event in the range.
CREATE TABLE IF NOT EXISTS usage_sessions (
	session_id TEXT PRIMARY KEY,
	first_ts INTEGER NOT NULL,
	last_ts INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS usage_sessions_last ON usage_sessions(last_ts, session_id);

CREATE TABLE IF NOT EXISTS quota_observations (
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
CREATE UNIQUE INDEX IF NOT EXISTS quota_observations_key ON quota_observations(auth_index, window_name, observed_at);
CREATE INDEX IF NOT EXISTS quota_observations_observed ON quota_observations(observed_at);

-- removed_at marks accounts whose credential left the auth manager. The row
-- stays so historical usage keeps its label.
CREATE TABLE IF NOT EXISTS accounts (
	auth_index TEXT PRIMARY KEY,
	provider TEXT NOT NULL DEFAULT '',
	label TEXT NOT NULL DEFAULT '',
	email TEXT NOT NULL DEFAULT '',
	plan TEXT NOT NULL DEFAULT '',
	first_seen INTEGER,
	last_seen INTEGER,
	disabled INTEGER NOT NULL DEFAULT 0,
	removed_at INTEGER
);

CREATE TABLE IF NOT EXISTS prices (
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
	store.Register(store.Schema{Module: "ingest", SQL: schema, Seed: seedPricesSQL()})
}
