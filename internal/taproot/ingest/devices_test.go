package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/access"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

type fakeDevices map[string]access.Device

func (f fakeDevices) RequestDevice(id string) (access.Device, bool) {
	d, ok := f[id]
	return d, ok
}

var (
	laptop = access.Device{ID: "node:nLaptop", Name: "laptop", Login: "alice@github", Via: access.ViaTailnet}
	phone  = access.Device{ID: "node:nPhone", Name: "phone", Login: "alice@github", Via: access.ViaTailnet}
)

type devRecord struct {
	trace, session, parent string
	at                     time.Duration
	input, read, w5, w1h   int64
	unsplit                int64
	provider               string
}

// seedDevices writes, from base (2026-11-02T10:00Z), using claude-opus-5-5
// (input 4, read 0.2, 5m write 5, 1h write 8 USD per Mtok):
//
//	laptop main session s1: +0m w5 1000 in 100; +2m w5 200 w1h 100 read 1000;
//	  +12m w5 12000 (10-minute pause: a re-write); +80m w5 1500 (68 minutes: not one)
//	laptop subagent of s1: +3m w1h 4000 (excluded from the main-conversation split)
//	phone main session s2: +5m w1h 2000
//	no device: +6m Claude main with 500 unsplit writes, +7m Codex
func seedDevices(t *testing.T, i *Ingest) time.Time {
	t.Helper()
	base := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	i.AttachDevices(fakeDevices{"t-laptop": laptop, "t-phone": phone})
	for n, r := range []devRecord{
		{trace: "t-laptop", session: "s1", at: 0, w5: 1000, input: 100},
		{trace: "t-laptop", session: "s1", at: 2 * time.Minute, w5: 200, w1h: 100, read: 1000},
		{trace: "t-laptop", session: "s1:agent:x", parent: "s1", at: 3 * time.Minute, w1h: 4000},
		{trace: "t-phone", session: "s2", at: 5 * time.Minute, w1h: 2000},
		{trace: "gone", session: "s3", at: 6 * time.Minute, unsplit: 500},
		{trace: "gone", session: "s4", at: 7 * time.Minute, provider: "codex", input: 900},
		{trace: "t-laptop", session: "s1", at: 12 * time.Minute, w5: 12000},
		{trace: "t-laptop", session: "s1", at: 80 * time.Minute, w5: 1500},
	} {
		rec := usage.Record{
			RequestID: "d" + string(rune('a'+n)), TraceID: r.trace, SessionID: r.session, ParentSessionID: r.parent,
			Provider: "claude", Model: "claude-opus-5-5", AuthIndex: "auth-a", RequestedAt: base.Add(r.at), Latency: time.Second,
			Detail: usage.Detail{InputTokens: r.input, CacheReadTokens: r.read, CacheCreationTokens: r.w5 + r.w1h + r.unsplit},
		}
		if r.w5+r.w1h > 0 {
			rec.Detail.CacheCreation5mTokens, rec.Detail.CacheCreation1hTokens = &r.w5, &r.w1h
		}
		if r.provider != "" {
			rec.Provider, rec.Model, rec.AuthIndex = r.provider, "gpt-6-sol", "auth-c"
		}
		i.HandleUsage(context.Background(), rec)
	}
	mustFlush(t, i)
	return base
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestUsageDevices(t *testing.T) {
	i, r, _ := newAPI(t)
	base := seedDevices(t, i)
	rangeQ := q("from", rfc(base), "to", rfc(base.Add(2*time.Hour)))

	var resp struct{ Devices []deviceJSON }
	if code := call(t, r, http.MethodGet, "/api/usage/devices"+rangeQ, nil, &resp); code != http.StatusOK {
		t.Fatalf("devices status %d", code)
	}
	byID := map[string]deviceJSON{}
	for _, d := range resp.Devices {
		byID[d.DeviceID] = d
	}
	if len(byID) != 3 {
		t.Fatalf("devices = %+v, want laptop, phone, and no device", resp.Devices)
	}

	l := byID[laptop.ID]
	if l.Name != "laptop" || l.Login != "alice@github" || l.Via != "tailnet" || l.Requests != 5 {
		t.Errorf("laptop = %+v", l)
	}
	if l.LastSeen != formatTime(base.Add(80*time.Minute)) {
		t.Errorf("laptop last seen %s", l.LastSeen)
	}
	// Writes: main 1000+300+12000+1500 = 14800, subagent 4000. Hit = 1000 / (1000 + 18800 + 100).
	if l.CacheWriteTokens != 18800 || l.CacheWrite5mTokens != 14700 || l.CacheWrite1hTokens != 4100 {
		t.Errorf("laptop writes %d/%d/%d", l.CacheWriteTokens, l.CacheWrite5mTokens, l.CacheWrite1hTokens)
	}
	if l.CacheHitRatio == nil || !near(*l.CacheHitRatio, 1000.0/19900) {
		t.Errorf("laptop cache hit %v", l.CacheHitRatio)
	}
	m := l.Main
	if m.Requests != 4 || m.SplitRequests != 4 || m.CacheWrite5mTokens != 14700 || m.CacheWrite1hTokens != 100 {
		t.Errorf("laptop main = %+v", m)
	}
	if m.FiveMinuteShare == nil || !near(*m.FiveMinuteShare, 14700.0/14800) {
		t.Errorf("laptop 5m share %v, want 14700/14800", m.FiveMinuteShare)
	}
	// One re-write of 12000 tokens: 12000 * (5 - 0.2) = 57600 µ$; the one-hour
	// premium covers the other 2700 5m tokens at 8 - 5: 8100 µ$.
	e := m.Estimate
	if e.RewriteRequests != 1 || e.RewriteTokens != 12000 || !near(e.RewriteCostUSD, 0.0576) ||
		!near(e.PremiumCostUSD, 0.0081) || !near(e.NetSavingsUSD, 0.0495) {
		t.Errorf("laptop estimate = %+v", e)
	}

	p := byID[phone.ID].Main
	if p.FiveMinuteShare == nil || *p.FiveMinuteShare != 0 || p.Estimate.NetSavingsUSD != 0 {
		t.Errorf("phone main = %+v", p)
	}
	none := byID[""]
	if none.Requests != 2 || none.Name != "" || none.Main.Requests != 1 || none.Main.SplitRequests != 0 || none.Main.FiveMinuteShare != nil {
		t.Errorf("no-device = %+v", none)
	}

	// A range off hour boundaries reads its edges from raw events and the
	// middle hour from the rollup; it holds the same events, so the same answer.
	var edges struct{ Devices []deviceJSON }
	call(t, r, http.MethodGet, "/api/usage/devices"+q("from", rfc(base.Add(-30*time.Minute)), "to", rfc(base.Add(90*time.Minute))), nil, &edges)
	if got, want := mustJSON(t, edges.Devices), mustJSON(t, resp.Devices); got != want {
		t.Errorf("devices over 09:30-11:30 = %s, want %s", got, want)
	}

	// Device totals add up to the summary, which groups by the same IDs.
	var sum summaryResp
	if code := call(t, r, http.MethodGet, "/api/usage/summary"+rangeQ+"&bucket=1h&groupBy=device", nil, &sum); code != http.StatusOK {
		t.Fatalf("summary status %d", code)
	}
	var requests int64
	var cost float64
	groups := map[string]Totals{}
	for _, b := range sum.Buckets {
		for k, g := range b.Groups {
			cur := groups[k]
			cur.Requests += g.Requests
			cur.CostUSD += g.CostUSD
			groups[k] = cur
		}
	}
	for _, d := range resp.Devices {
		key := d.DeviceID
		if key == "" {
			key = noneGroupKey
		}
		if groups[key].Requests != d.Requests || !near(groups[key].CostUSD, d.CostUSD) {
			t.Errorf("summary group %s = %+v, devices say %d requests $%f", key, groups[key], d.Requests, d.CostUSD)
		}
		requests += d.Requests
		cost += d.CostUSD
	}
	if requests != sum.Totals.Requests || requests != 8 || !near(cost, sum.Totals.CostUSD) {
		t.Errorf("device totals %d $%f, summary %d $%f", requests, cost, sum.Totals.Requests, sum.Totals.CostUSD)
	}

	// Events carry the device and filter by it; "(none)" selects events without one.
	var ev struct{ Events []UsageEvent }
	call(t, r, http.MethodGet, "/api/usage/events"+rangeQ+"&deviceId="+laptop.ID, nil, &ev)
	if len(ev.Events) != 5 || ev.Events[0].DeviceName != "laptop" || ev.Events[0].DeviceLogin != "alice@github" {
		t.Errorf("laptop events = %+v", ev.Events)
	}
	call(t, r, http.MethodGet, "/api/usage/events"+rangeQ+"&deviceId=(none)", nil, &ev)
	if len(ev.Events) != 2 || ev.Events[0].DeviceID != "" {
		t.Errorf("no-device events = %+v", ev.Events)
	}
}

type summaryResp struct {
	Totals  Totals
	Buckets []struct {
		T      string
		Groups map[string]Totals
	}
}

// TestDeviceMigrationKeepsExistingEvents opens a database written before the
// device columns existed and checks the rows survive with no device.
func TestDeviceMigrationKeepsExistingEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "taproot.db")
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_migrations (module TEXT NOT NULL, version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT '', PRIMARY KEY (module, version))`,
		schemaV1, seedPricesSQL(), `ALTER TABLE accounts ADD COLUMN removed_at INTEGER`,
		`INSERT INTO schema_migrations (module, version) VALUES ('ingest', 1), ('ingest', 2), ('ingest', 3)`,
		`INSERT INTO usage_events (event_key, ts, provider, model, session_id, cache_write_tokens) VALUES ('old', 1793613600000, 'claude', 'claude-opus-5-5', 's', 300)`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("build v3 database: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), store.Config{}, dir)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	devices, err := loadDevices(context.Background(), db, 0, math.MaxInt64, newPriceBook(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].DeviceID != "" || devices[0].Requests != 1 || devices[0].CacheWriteTokens != 300 {
		t.Fatalf("devices after migration = %+v", devices)
	}
}

// TestDeviceMigrationOnSeededDB migrates a copy of a real database when
// TAPROOT_SEEDED_DB names one, and checks device totals match the events.
func TestDeviceMigrationOnSeededDB(t *testing.T) {
	src := os.Getenv("TAPROOT_SEEDED_DB")
	if src == "" {
		t.Skip("TAPROOT_SEEDED_DB is not set")
	}
	dir := t.TempDir()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "taproot.db"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), store.Config{}, dir)
	if err != nil {
		t.Fatalf("migrate seeded copy: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var events, version int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations WHERE module = 'ingest'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	prices, err := loadPrices(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	devices, err := loadDevices(context.Background(), db, 0, math.MaxInt64, newPriceBook(prices))
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, d := range devices {
		total += d.Requests
	}
	t.Logf("migrated to ingest v%d; %d events; devices over all history in %s: %d", version, events, time.Since(started), len(devices))
	if version < 4 || total != events || events == 0 {
		t.Fatalf("version %d, device requests %d, events %d", version, total, events)
	}
}
