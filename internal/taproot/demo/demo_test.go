package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/store"
)

var testStart = time.Date(2026, 11, 3, 17, 42, 0, 0, time.UTC)

func indexedAccounts() []*demoAccount {
	accounts := demoAccounts()
	for i, a := range accounts {
		a.authIndex = string(rune('a' + i))
	}
	return accounts
}

func TestGenerateIsDeterministic(t *testing.T) {
	a := generate(testStart, indexedAccounts())
	b := generate(testStart, indexedAccounts())
	if !reflect.DeepEqual(a, b) {
		t.Fatal("two runs with the same start time generated different traffic")
	}
	// Another start gives the same history, shifted to end there.
	shift := 37*time.Hour + 13*time.Minute + 29*time.Second
	later := generate(testStart.Add(shift), indexedAccounts())
	if len(later) != len(a) {
		t.Fatalf("%d requests after shifting the start, want %d", len(later), len(a))
	}
	for i := range a {
		x, y := a[i], later[i]
		if y.RequestedAt.Sub(x.RequestedAt) != shift || x.RequestID != y.RequestID || x.Detail.TotalTokens != y.Detail.TotalTokens {
			t.Fatalf("request %d differs beyond the shift: %s %s vs %s %s", i, x.RequestID, x.RequestedAt, y.RequestID, y.RequestedAt)
		}
		for k := range x.ResponseHeaders {
			if strings.HasSuffix(strings.ToLower(k), "reset") || strings.HasSuffix(strings.ToLower(k), "reset-at") {
				continue
			}
			if x.ResponseHeaders.Get(k) != y.ResponseHeaders.Get(k) {
				t.Fatalf("request %d header %s: %q vs %q", i, k, x.ResponseHeaders.Get(k), y.ResponseHeaders.Get(k))
			}
		}
	}
	if end := a[len(a)-1].RequestedAt; end.After(testStart) || testStart.Sub(end) > time.Minute {
		t.Fatalf("history ends at %s, want just before the start %s", end, testStart)
	}
}

func TestGenerateShape(t *testing.T) {
	requests := generate(testStart, indexedAccounts())
	from := testStart.Add(-historyDays * 24 * time.Hour)
	var failures, rejected, burst, oneHour, fiveMin int
	models, devicesSeen := map[string]int{}, map[string]bool{}
	for i, r := range requests {
		if i > 0 && r.RequestedAt.Before(requests[i-1].RequestedAt) {
			t.Fatalf("request %d is out of order", i)
		}
		if r.RequestedAt.Before(from) || r.RequestedAt.After(testStart.Add(time.Minute)) {
			t.Fatalf("request at %s is outside the history", r.RequestedAt)
		}
		models[r.Model]++
		devicesSeen[r.device.Name] = true
		if r.Failed {
			failures++
			if r.Fail.StatusCode == 429 {
				rejected++
			}
		}
		if r.burst {
			burst++
		}
		if p := r.Detail.CacheCreation1hTokens; p != nil && *p > 0 {
			oneHour++
		}
		if p := r.Detail.CacheCreation5mTokens; p != nil && *p > 0 {
			fiveMin++
		}
	}
	if n := len(requests); n < 3000 || n > 30000 {
		t.Fatalf("generated %d requests; want a month of steady use", n)
	}
	if failures == 0 || failures*50 > len(requests) {
		t.Fatalf("%d failures of %d requests; want a few", failures, len(requests))
	}
	if rejected == 0 {
		t.Fatal("no request hit a limit")
	}
	if burst != burstSize {
		t.Fatalf("burst has %d requests, want %d", burst, burstSize)
	}
	if oneHour == 0 || fiveMin == 0 {
		t.Fatalf("cache writes: %d with 1h, %d with 5m; want both", oneHour, fiveMin)
	}
	for _, m := range []model{opus, sonnet, haiku, sol} {
		if models[m.name] == 0 {
			t.Fatalf("no requests for %s", m.name)
		}
	}
	for _, d := range append(devices, serverDevice) {
		if !devicesSeen[d.Name] {
			t.Fatalf("no requests from %s", d.Name)
		}
	}
}

// TestSeed writes a demo through the real modules and checks what the
// dashboard reads back: consistent rollups, devices, quota with resets,
// request logs, and backups.
func TestSeed(t *testing.T) {
	dir := t.TempDir()
	if _, err := seed(context.Background(), dir, 8399, testStart, scenarioNormal); err != nil {
		t.Fatalf("seed: %v", err)
	}
	ctx := context.Background()
	db, err := store.Open(ctx, store.Config{}, filepath.Join(dir, "taproot"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	count := func(q string, args ...any) int64 {
		t.Helper()
		var n int64
		if err := db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	if got := count(`SELECT COUNT(*) FROM access_requests WHERE dismissed_at IS NULL AND node_id = ? AND name_hint = ? AND client = ? AND count = 7`,
		danaPC.id, "Dana's PC", "Claude Code"); got != 1 {
		t.Fatalf("open access requests from dana's PC = %d, want 1", got)
	}
	events := count(`SELECT COUNT(*) FROM usage_events`)
	if got := count(`SELECT SUM(requests) FROM usage_rollup_hourly`); got != events {
		t.Fatalf("hourly rollup counts %d requests, events table has %d", got, events)
	}
	if got := count(`SELECT SUM(requests) FROM usage_device_hourly`); got != events {
		t.Fatalf("device rollup counts %d requests, events table has %d", got, events)
	}
	if got := count(`SELECT COUNT(DISTINCT device_id) FROM usage_events WHERE device_id <> ''`); got != int64(len(devices)+1) {
		t.Fatalf("%d devices, want %d", got, len(devices)+1)
	}
	if got := count(`SELECT COUNT(*) FROM usage_events WHERE device_id = ''`); got != 0 {
		t.Fatalf("%d events without a device", got)
	}
	if count(`SELECT SUM(cache_write_5m_tokens) FROM usage_events`) == 0 || count(`SELECT SUM(cache_write_1h_tokens) FROM usage_events`) == 0 {
		t.Fatal("want both 5m and 1h cache writes")
	}
	for _, a := range demoAccountsFrom(t, dir) {
		for _, w := range a.windows {
			if n := count(`SELECT COUNT(*) FROM quota_observations WHERE auth_index = ? AND window_name = ?`, a.index, w); n < 20 {
				t.Fatalf("%s %s: %d quota observations", a.file, w, n)
			}
			// A reset shows as a later observation below an earlier one.
			drops := count(`SELECT COUNT(*) FROM quota_observations o WHERE auth_index = ? AND window_name = ? AND observed_at > ?
				AND utilization < (SELECT p.utilization FROM quota_observations p WHERE p.auth_index = o.auth_index
					AND p.window_name = o.window_name AND p.observed_at < o.observed_at ORDER BY p.observed_at DESC LIMIT 1) - 0.2`,
				a.index, w, testStart.Add(-14*24*time.Hour).UnixMilli())
			if drops == 0 {
				t.Fatalf("%s %s: no reset in the last two weeks", a.file, w)
			}
		}
		if peak := count(`SELECT CAST(MAX(utilization) * 100 AS INTEGER) FROM quota_observations WHERE auth_index = ? AND window_name IN ('seven_day', 'codex_secondary')`, a.index); peak < 30 {
			t.Fatalf("%s: weekly peak %d%%; want a believable pool chart", a.file, peak)
		}
	}
	if got := count(`SELECT COUNT(*) FROM reqlog_records`); got != burstSize {
		t.Fatalf("%d request logs, want %d", got, burstSize)
	}
	if got := count(`SELECT COUNT(*) FROM reqlog_records r JOIN usage_events e ON e.request_id = r.request_id AND e.session_id = r.session_id`); got != burstSize {
		t.Fatalf("%d request logs match a usage event by request and session, want %d", got, burstSize)
	}
	// Every usage event is an attempt of one client request.
	if got := count(`SELECT SUM(attempts) FROM client_requests`); got != events {
		t.Fatalf("client requests count %d attempts, usage has %d events", got, events)
	}
	if got, want := count(`SELECT SUM(requests) FROM client_request_slots`), count(`SELECT COUNT(*) FROM client_requests`); got != want {
		t.Fatalf("slots count %d client requests, table has %d", got, want)
	}
	if count(`SELECT COUNT(*) FROM client_requests WHERE attempts > 1 AND failed = 0`) == 0 {
		t.Fatal("want retried requests that succeeded")
	}
	if got := count(`SELECT COUNT(*) FROM client_requests WHERE request_id IN (SELECT request_id FROM reqlog_records)`); got != burstSize {
		t.Fatalf("%d client requests open a request log, want %d", got, burstSize)
	}
	recent := testStart.Add(-2 * time.Hour).UnixMilli()
	for _, c := range []struct {
		code   string
		failed int
	}{{"bad_gateway", 1}, {"bad_request", 1}, {"client_closed", 0}, {"overloaded", 1}, {"rate_limited", 1}, {"unknown_provider", 1}} {
		if count(`SELECT COUNT(*) FROM client_requests WHERE error_code = ? AND failed = ? AND ts >= ? AND (error_message <> '' OR failed = 0)`, c.code, c.failed, recent) == 0 {
			t.Errorf("no %s request in the last two hours", c.code)
		}
	}
	if count(`SELECT COUNT(*) FROM client_requests WHERE error_code = 'unknown_provider' AND attempts = 0`) != 1 {
		t.Error("want one request no account was tried for")
	}
	backups, err := os.ReadDir(filepath.Join(dir, "taproot", "backups"))
	if err != nil || len(backups) != 2 {
		t.Fatalf("backups: %v, %d entries", err, len(backups))
	}
	// The Dashboard's Needs-you list: a failed scheduled backup, and a
	// signed-out Codex account that served until three hours ago.
	if count(`SELECT COUNT(*) FROM backup_scheduled_run WHERE error <> ''`) != 1 {
		t.Error("want a failed scheduled backup run")
	}
	dana := signedOutAccount(t)
	if count(`SELECT COUNT(*) FROM usage_events WHERE auth_id LIKE ? AND ts >= ?`, "%"+dana.email+"%", testStart.Add(-dana.signedOut).UnixMilli()) != 0 {
		t.Error("the signed-out account served after it lost its login")
	}
	if count(`SELECT COUNT(*) FROM usage_events WHERE auth_id LIKE ?`, "%"+dana.email+"%") == 0 {
		t.Error("the signed-out account has no history")
	}
	// Without -degraded the Dashboard's verdict is normal: under 5% of the
	// last quarter hour's requests failed.
	quarter := testStart.Add(-15 * time.Minute).UnixMilli()
	if failed, all := count(`SELECT COUNT(*) FROM client_requests WHERE failed = 1 AND ts >= ?`, quarter), count(`SELECT COUNT(*) FROM client_requests WHERE ts >= ?`, quarter); failed*20 >= all && all >= 20 {
		t.Errorf("last 15 min: %d of %d failed; the default demo would read degraded", failed, all)
	}
	journal, err := os.ReadDir(filepath.Join(dir, "auths", ".codex-refresh"))
	if err != nil || len(journal) != 1 {
		t.Fatalf("refresh journal: %v, %d claims; want one for the signed-out account", err, len(journal))
	}
}

func signedOutAccount(t *testing.T) *demoAccount {
	t.Helper()
	for _, a := range demoAccounts() {
		if a.signedOut > 0 {
			return a
		}
	}
	t.Fatal("no signed-out demo account")
	return nil
}

// -degraded adds a quarter hour in which at least 5% of client requests
// failed, still with every usage event an attempt of one client request.
func TestSeedDegraded(t *testing.T) {
	dir := t.TempDir()
	if _, err := seed(context.Background(), dir, 8399, testStart, scenarioDegraded); err != nil {
		t.Fatalf("seed: %v", err)
	}
	db, err := store.Open(context.Background(), store.Config{}, filepath.Join(dir, "taproot"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var requests, failed, attempts, events int64
	if err := db.QueryRow(`SELECT COUNT(*), SUM(failed) FROM client_requests WHERE ts >= ?`, testStart.Add(-15*time.Minute).UnixMilli()).Scan(&requests, &failed); err != nil {
		t.Fatal(err)
	}
	if requests < 20 || failed*20 < requests || failed*2 >= requests {
		t.Errorf("last 15 min: %d of %d failed, want at least 5%% of at least 20 and under half", failed, requests)
	}
	if err := db.QueryRow(`SELECT (SELECT SUM(attempts) FROM client_requests), (SELECT COUNT(*) FROM usage_events)`).Scan(&attempts, &events); err != nil || attempts != events {
		t.Errorf("attempts %d, usage events %d (%v)", attempts, events, err)
	}
}

// -all-clear seeds nothing that needs the user: no signed-out account, no
// access request, no failed backup run, and no alerts webhook.
func TestSeedAllClear(t *testing.T) {
	dir := t.TempDir()
	if _, err := seed(context.Background(), dir, 8399, testStart, scenarioAllClear); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "auths", ".codex-refresh")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("refresh journal: %v, want none (no account is signed out)", err)
	}
	tp, err := os.ReadFile(filepath.Join(dir, "taproot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(tp), "discord-webhook") {
		t.Error("taproot.yaml sets an alerts webhook")
	}
	db, err := store.Open(context.Background(), store.Config{}, filepath.Join(dir, "taproot"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var requests, runs int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM access_requests), (SELECT COUNT(*) FROM client_requests)`).Scan(&runs, &requests); err != nil {
		t.Fatal(err)
	}
	if runs != 0 || requests == 0 {
		t.Errorf("%d access requests and %d client requests, want none and some", runs, requests)
	}
	var failedRuns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM backup_scheduled_run WHERE error != ''`).Scan(&failedRuns); err != nil {
		t.Fatal(err)
	}
	if failedRuns != 0 {
		t.Errorf("%d failed scheduled backup runs, want none", failedRuns)
	}
}

// -first-run writes the config and nothing else, as a new server has it.
func TestSeedFirstRun(t *testing.T) {
	dir := t.TempDir()
	if _, err := seed(context.Background(), dir, 8399, testStart, scenarioFirstRun); err != nil {
		t.Fatalf("seed: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "auths"))
	if err != nil || len(entries) != 0 {
		t.Errorf("auth dir: %d entries (%v), want an empty directory", len(entries), err)
	}
	if _, err := os.Stat(filepath.Join(dir, "taproot")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("data dir: %v, want none until the server creates it", err)
	}
	for _, f := range []string{"config.yaml", "taproot.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func TestPickScenario(t *testing.T) {
	if _, err := pickScenario(true, true, false); err == nil {
		t.Error("two scenarios at once were accepted")
	}
	for _, c := range []struct {
		degraded, allClear, firstRun bool
		want                         scenario
	}{{false, false, false, scenarioNormal}, {true, false, false, scenarioDegraded}, {false, true, false, scenarioAllClear}, {false, false, true, scenarioFirstRun}} {
		if got, err := pickScenario(c.degraded, c.allClear, c.firstRun); err != nil || got != c.want {
			t.Errorf("pickScenario(%v, %v, %v) = %v, %v; want %v", c.degraded, c.allClear, c.firstRun, got, err, c.want)
		}
	}
}

type seededAccount struct {
	file, index string
	windows     []string
}

// demoAccountsFrom reads the written credential files and derives their
// indexes the way the server does.
func demoAccountsFrom(t *testing.T, dir string) []seededAccount {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "auths"))
	if err != nil {
		t.Fatal(err)
	}
	var out []seededAccount
	for _, e := range entries {
		if e.IsDir() {
			continue // the Codex refresh journal
		}
		provider, _, _ := strings.Cut(e.Name(), "-")
		windows := []string{"five_hour", "seven_day"}
		if provider == "codex" {
			windows = []string{"codex_primary", "codex_secondary"}
		}
		path := filepath.Join(dir, "auths", e.Name())
		out = append(out, seededAccount{file: e.Name(), index: authIndex(path, provider, map[string]any{"type": provider}), windows: windows})
	}
	if len(out) != len(demoAccounts()) {
		t.Fatalf("%d credential files", len(out))
	}
	return out
}

// TestDemoDataIsSynthetic checks every file the demo writes for personal
// data: emails only at example.com or example.org, no real-looking tokens.
func TestDemoDataIsSynthetic(t *testing.T) {
	dir := t.TempDir()
	start := testStart
	if err := writeAuthFiles(filepath.Join(dir, "auths"), demoAccounts(), start); err != nil {
		t.Fatal(err)
	}
	if err := writeConfig(dir, filepath.Join(dir, "auths"), 8399, true); err != nil {
		t.Fatal(err)
	}
	domain := regexp.MustCompile(`@[A-Za-z0-9.-]+`)
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range domain.FindAllString(string(raw)+" "+d.Name(), -1) {
			if m != "@github" && !strings.HasPrefix(m, "@example.com") && !strings.HasPrefix(m, "@example.org") {
				t.Errorf("%s: unexpected address domain %q", d.Name(), m)
			}
		}
		if strings.Contains(string(raw), "sk-ant-") {
			t.Errorf("%s: holds a token in a real format", d.Name())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSeedRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := seed(context.Background(), dir, 8399, testStart, scenarioNormal); err == nil {
		t.Fatal("seed wrote into a non-empty directory")
	}
}
