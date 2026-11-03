package ingest

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// keeperDDL is copied from a real CPA Usage Keeper app.db (tables taproot reads).
const keeperDDL = "CREATE TABLE `usage_events` (`id` integer PRIMARY KEY AUTOINCREMENT,`event_key` text,`api_group_key` text,`provider` text,`endpoint` text,`auth_type` text,`request_id` text,`client_ip` text,`x_forwarded_for` text,`user_agent` text,`model` text,`model_alias` text,`reasoning_effort` text NOT NULL DEFAULT \"\",`service_tier` text NOT NULL DEFAULT \"\",`response_service_tier` text NOT NULL DEFAULT \"\",`executor_type` text NOT NULL DEFAULT \"\",`timestamp` datetime,`source` text,`auth_index` text,`failed` numeric,`generate` numeric NOT NULL DEFAULT true,`latency_ms` integer,`ttft_ms` integer,`input_tokens` integer,`output_tokens` integer,`reasoning_tokens` integer,`cached_tokens` integer,`cache_read_tokens` integer NOT NULL DEFAULT 0,`cache_creation_tokens` integer NOT NULL DEFAULT 0,`total_tokens` integer,`created_at` datetime, `session_id` text, `parent_session_id` text, `response_model` text NOT NULL DEFAULT \"\", `status_code` integer, `stream` numeric, `execution_id` text, `trace_id` text, `node_kind` text, `is_fork` numeric, `is_compaction` numeric, `cost_usd` real, `cost_available` boolean, `effective_service_tier` text NOT NULL DEFAULT \"\", `is_native_key` numeric, `access_provider` text);\n" +
	"CREATE TABLE `quota_cycles` (`id` integer PRIMARY KEY AUTOINCREMENT,`provider` text NOT NULL,`auth_index` text NOT NULL,`quota_key` text NOT NULL,`window_seconds` integer NOT NULL,`reset_at_source` text NOT NULL,`window_started_at` datetime NOT NULL,`reset_at` datetime NOT NULL,`first_observed_at` datetime NOT NULL,`last_observed_at` datetime NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL);\n" +
	"CREATE TABLE `quota_percent_segments` (`id` integer PRIMARY KEY AUTOINCREMENT,`cycle_id` integer NOT NULL,`remaining_percent` integer NOT NULL,`first_observed_at` datetime NOT NULL,`last_observed_at` datetime NOT NULL,`observation_count` integer NOT NULL DEFAULT 1,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL);\n" +
	"CREATE TABLE `model_price_settings` (`id` integer PRIMARY KEY AUTOINCREMENT,`model` text,`pricing_style` text NOT NULL DEFAULT \"openai\",`prompt_price_per1_m` real,`completion_price_per1_m` real,`cache_read_price_per1_m` real,`cache_creation_price_per1_m` real NOT NULL DEFAULT 0,`price_multiplier` real NOT NULL DEFAULT 1,`created_at` datetime,`updated_at` datetime, `branches_json` text NOT NULL DEFAULT \"[]\");\n" +
	"CREATE TABLE `usage_identities` (`id` integer PRIMARY KEY AUTOINCREMENT,`name` text,`alias` text,`auth_type` integer,`auth_type_name` text,`identity` text,`type` text,`provider` text,`plan_type` text,`disabled` numeric,`is_deleted` numeric);\n"

func writeKeeperFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	stmts := []string{keeperDDL,
		// Claude row: keeper input includes cache read and write.
		"INSERT INTO usage_events (event_key, provider, request_id, model, model_alias, timestamp, auth_index, failed, latency_ms, ttft_ms, input_tokens, output_tokens, reasoning_tokens, cached_tokens, cache_read_tokens, cache_creation_tokens, total_tokens, session_id, status_code, stream, trace_id, service_tier, response_service_tier) VALUES " +
			"('ev-1','claude','ev-1','claude-opus-5-5','opus','2026-10-20T10:00:00.5-07:00','aaaa',0,3000,500,10310,100,20,10000,10000,300,10410,'sess-1',200,1,'tr-1','auto','standard')",
		// Codex rows reusing one short event key across restarts.
		"INSERT INTO usage_events (event_key, provider, request_id, model, timestamp, auth_index, failed, latency_ms, ttft_ms, input_tokens, output_tokens, reasoning_tokens, cached_tokens, cache_read_tokens, cache_creation_tokens, service_tier) VALUES " +
			"('00000002','codex','00000002','gpt-6-astra','2026-09-24T08:39:47.264517506-07:00','cccc',0,5998,0,10612,99,0,8000,8000,0,'auto')," +
			"('00000002','codex','00000002','gpt-6-astra','2026-09-25T17:19:02.818837842-07:00','cccc',1,3928,0,10515,70,0,0,0,0,'auto')",
		"INSERT INTO quota_cycles (provider, auth_index, quota_key, window_seconds, reset_at_source, window_started_at, reset_at, first_observed_at, last_observed_at, created_at, updated_at) VALUES " +
			"('claude','aaaa','five_hour',18000,'absolute','2026-10-20T08:00:00Z','2026-10-20T13:00:00.000000000Z','2026-10-20T09:00:00Z','2026-10-20T12:00:00Z','x','x')," +
			"('codex','cccc','rate_limit.primary_window',604800,'absolute','2026-09-20T00:00:00Z','2026-09-27T00:00:00Z','2026-09-24T00:00:00Z','2026-09-25T00:00:00Z','x','x')",
		"INSERT INTO quota_percent_segments (cycle_id, remaining_percent, first_observed_at, last_observed_at, observation_count, created_at, updated_at) VALUES " +
			"(1, 99, '2026-10-20T09:00:00.000000001Z', '2026-10-20T09:30:00Z', 5, 'x', 'x')," +
			"(1, 0, '2026-10-20T12:00:00Z', '2026-10-20T12:00:00Z', 1, 'x', 'x')," +
			"(2, 53, '2026-09-24T00:00:00Z', '2026-09-25T00:00:00Z', 9, 'x', 'x')",
		"INSERT INTO model_price_settings (model, prompt_price_per1_m, completion_price_per1_m, cache_read_price_per1_m, cache_creation_price_per1_m, price_multiplier, updated_at) VALUES " +
			"('gpt-5.3-codex-spark', 1.75, 14, 0.175, 0, 1, '2026-09-09T17:33:07.053613857-07:00')," +
			"('gpt-6-astra', 99, 99, 99, 99, 1, '2026-09-09T17:33:07Z')",
		"INSERT INTO usage_identities (name, identity, provider, plan_type, disabled, is_deleted) VALUES " +
			"('a@example.test','aaaa','claude',NULL,0,0),('c@example.test','cccc','codex','free',0,1)",
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("fixture: %v\n%s", err, s)
		}
	}
	return path
}

func TestImportUsageKeeperIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	path := writeKeeperFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO accounts (auth_index, provider, label, email, disabled) VALUES ('aaaa', 'claude', 'Work', 'live@example.test', 0)`); err != nil {
		t.Fatal(err)
	}

	first, err := ImportUsageKeeper(ctx, db, path)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	want := KeeperImportStats{EventsRead: 3, EventsInserted: 3, ObservationsRead: 5, ObservationsInserted: 5, PricesInserted: 1, AccountsUpdated: 1}
	if first != want {
		t.Fatalf("first import stats = %+v, want %+v", first, want)
	}
	second, err := ImportUsageKeeper(ctx, db, path)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.EventsInserted != 0 || second.ObservationsInserted != 0 || second.PricesInserted != 0 || second.AccountsUpdated != 0 {
		t.Fatalf("second import wrote rows: %+v", second)
	}

	i := &Ingest{db: db}
	if n := queryInt(t, i, `SELECT COUNT(*) FROM usage_events`); n != 3 {
		t.Fatalf("events = %d, want 3", n)
	}
	if n := queryInt(t, i, `SELECT SUM(requests) FROM usage_rollup_hourly`); n != 3 {
		t.Fatalf("rollup requests = %d, want 3 (second import must not double count)", n)
	}

	var (
		ts                                     int64
		input, cacheRead, cacheWrite, ttft     int64
		alias, tier, source, session, provider string
	)
	if err := db.QueryRow(`SELECT ts, input_tokens, cache_read_tokens, cache_write_tokens, ttft_ms, alias, service_tier, source, session_id, provider
		FROM usage_events WHERE request_id='ev-1'`).Scan(&ts, &input, &cacheRead, &cacheWrite, &ttft, &alias, &tier, &source, &session, &provider); err != nil {
		t.Fatal(err)
	}
	wantTS := time.Date(2026, 10, 20, 17, 0, 0, 500e6, time.UTC).UnixMilli()
	if ts != wantTS || input != 10 || cacheRead != 10000 || cacheWrite != 300 || ttft != 500 ||
		alias != "opus" || tier != "standard" || source != "usage-keeper" || session != "sess-1" || provider != "claude" {
		t.Fatalf("claude row = ts %d input %d cr %d cw %d ttft %d alias %q tier %q source %q session %q", ts, input, cacheRead, cacheWrite, ttft, alias, tier, source, session)
	}
	if n := queryInt(t, i, `SELECT COUNT(*) FROM usage_events WHERE request_id='00000002' AND ttft_ms IS NULL`); n != 2 {
		t.Fatalf("codex rows with reused event key = %d, want 2", n)
	}
	if n := queryInt(t, i, `SELECT input_tokens FROM usage_events WHERE request_id='00000002' AND failed=0`); n != 2612 {
		t.Fatalf("codex uncached input = %d, want 2612", n)
	}

	rows, err := db.Query(`SELECT window_name, utilization, status, period_seconds, resets_at FROM quota_observations ORDER BY auth_index, observed_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRows(rows)
	type obsRow struct {
		window string
		util   float64
		status string
		period int64
		resets int64
	}
	var got []obsRow
	for rows.Next() {
		var r obsRow
		if err := rows.Scan(&r.window, &r.util, &r.status, &r.period, &r.resets); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	reset5h := time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC).UnixMilli()
	reset7d := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC).UnixMilli()
	wantObs := []obsRow{
		{"five_hour", 0.01, "", 18000, reset5h},
		{"five_hour", 0.01, "", 18000, reset5h},
		{"five_hour", 1, "rejected", 18000, reset5h},
		{"codex_primary", 0.47, "", 604800, reset7d},
		{"codex_primary", 0.47, "", 604800, reset7d},
	}
	// A segment yields points at its first and last observation; a
	// single-instant segment yields one.
	if len(got) != len(wantObs) {
		t.Fatalf("observations = %+v", got)
	}
	for n := range wantObs {
		if got[n] != wantObs[n] {
			t.Errorf("observation %d = %+v, want %+v", n, got[n], wantObs[n])
		}
	}

	var p Price
	if err := db.QueryRow(`SELECT input, output, cache_read, source FROM prices WHERE model_pattern='gpt-5.3-codex-spark'`).Scan(&p.Input, &p.Output, &p.CacheRead, &p.Source); err != nil {
		t.Fatal(err)
	}
	if p.Input != 1.75 || p.Output != 14 || p.CacheRead != 0.175 || p.Source != keeperPriceLabel {
		t.Fatalf("imported price = %+v", p)
	}
	if n := queryInt(t, i, `SELECT CAST(input AS INTEGER) FROM prices WHERE model_pattern='gpt-6-astra'`); n != 10 {
		t.Fatalf("seeded gpt-6-astra input = %d, keeper value must not override the seed", n)
	}
	var label, email, plan string
	var disabled int
	if err := db.QueryRow(`SELECT label, email, plan, disabled FROM accounts WHERE auth_index='cccc'`).Scan(&label, &email, &plan, &disabled); err != nil {
		t.Fatal(err)
	}
	if label != "c@example.test" || email != "c@example.test" || plan != "free" || disabled != 1 {
		t.Fatalf("account cccc = %q %q %q %d", label, email, plan, disabled)
	}
	if err := db.QueryRow(`SELECT label, email FROM accounts WHERE auth_index='aaaa'`).Scan(&label, &email); err != nil || label != "Work" || email != "live@example.test" {
		t.Fatalf("manager-labelled account overwritten: %q %q %v", label, email, err)
	}
	if n := queryInt(t, i, `SELECT first_seen FROM accounts WHERE auth_index='cccc'`); n != time.Date(2026, 9, 24, 15, 39, 47, 264e6, time.UTC).UnixMilli() {
		t.Fatalf("cccc first_seen = %d", n)
	}
}

func TestImportUsageKeeperRejectsOtherDatabases(t *testing.T) {
	db := openTestDB(t)
	if _, err := ImportUsageKeeper(context.Background(), db, filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Fatal("missing file accepted")
	}
	path := filepath.Join(t.TempDir(), "other.db")
	other, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Exec(`CREATE TABLE unrelated (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	_ = other.Close()
	if _, err := ImportUsageKeeper(context.Background(), db, path); err == nil {
		t.Fatal("database without usage_events accepted")
	}
}
