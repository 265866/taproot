package reqlog

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// upstreamRecord is one log file written by upstream's FileRequestLogger,
// parsed back into what the middleware handed it.
type upstreamRecord struct {
	file        string
	raw         []byte
	url, method string
	ts          time.Time
	reqHeaders  map[string][]string
	reqBody     []byte
	apiRequest  []byte
	apiResponse []byte
	status      int
	respHeaders map[string][]string
	respBody    []byte
	streaming   bool
}

var sectionStart = regexp.MustCompile(`(?m)^=== (API REQUEST|API ERROR RESPONSE|API RESPONSE|RESPONSE|WEBSOCKET TIMELINE|API WEBSOCKET TIMELINE)\b[^\n]*===\n`)

func parseUpstreamLog(name string, raw []byte) (*upstreamRecord, error) {
	r := &upstreamRecord{file: name, raw: raw, reqHeaders: map[string][]string{}, respHeaders: map[string][]string{}}
	text := string(raw)
	headersAt := strings.Index(text, "=== HEADERS ===\n")
	bodyAt := strings.Index(text, "=== REQUEST BODY ===\n")
	if headersAt < 0 || bodyAt < 0 {
		return nil, fmt.Errorf("%s: missing request sections", name)
	}
	for _, line := range strings.Split(text[:headersAt], "\n") {
		key, value, _ := strings.Cut(line, ": ")
		switch key {
		case "URL":
			r.url = value
		case "Method":
			r.method = value
		case "Timestamp":
			ts, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return nil, fmt.Errorf("%s: timestamp: %w", name, err)
			}
			r.ts = ts
		}
	}
	for _, line := range strings.Split(strings.TrimRight(text[headersAt+len("=== HEADERS ===\n"):bodyAt], "\n"), "\n") {
		if key, value, ok := strings.Cut(line, ": "); ok {
			r.reqHeaders[key] = append(r.reqHeaders[key], value)
		}
	}
	rest := text[bodyAt+len("=== REQUEST BODY ===\n"):]
	marks := sectionStart.FindAllStringSubmatchIndex(rest, -1)
	if len(marks) == 0 {
		return nil, fmt.Errorf("%s: no response section", name)
	}
	r.reqBody = []byte(strings.TrimSuffix(rest[:marks[0][0]], "\n\n\n"))
	for i, m := range marks {
		end := len(rest)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		kind := rest[m[2]:m[3]]
		section := rest[m[0]:end]
		switch kind {
		case "API REQUEST":
			r.apiRequest = append(r.apiRequest, section...)
		case "API RESPONSE":
			r.apiResponse = append(r.apiResponse, section...)
		case "RESPONSE":
			body := section[len("=== RESPONSE ===\n"):]
			head, payload, _ := strings.Cut(body, "\n\n")
			for _, line := range strings.Split(head, "\n") {
				key, value, ok := strings.Cut(line, ": ")
				if !ok {
					continue
				}
				if key == "Status" {
					r.status, _ = strconv.Atoi(value)
					continue
				}
				r.respHeaders[key] = append(r.respHeaders[key], value)
			}
			r.streaming = strings.Contains(headerValue(r.respHeaders, "Content-Type"), "text/event-stream")
			if !r.streaming {
				payload = strings.TrimSuffix(payload, "\n")
			}
			r.respBody = []byte(payload)
		}
	}
	r.apiRequest = bytes.TrimRight(r.apiRequest, "\n")
	r.apiResponse = bytes.TrimRight(r.apiResponse, "\n")
	return r, nil
}

var (
	versionLine      = regexp.MustCompile(`(?m)^Version: .*$`)
	maskedCredential = regexp.MustCompile(`(?mi)^((?:[\w-]*(?:authorization|api-key|apikey|token|secret|password|cookie)[\w-]*)): .*$`)
	storedCredential = regexp.MustCompile(`(?mi)^(authorization|x-api-key|cookie|set-cookie): (?:bearer )?[^[]`)
)

// normalizeLog removes differences that are intended: build version,
// credential masking, and header order inside header blocks.
func normalizeLog(text string) string {
	text = versionLine.ReplaceAllString(text, "Version: X")
	text = maskedCredential.ReplaceAllString(text, "$1: X")
	lines := strings.Split(text, "\n")
	inHeaders := false
	start := 0
	for i := 0; i <= len(lines); i++ {
		isHeader := i < len(lines) && inHeaders && strings.Contains(lines[i], ": ") && lines[i] != ""
		if inHeaders && !isHeader {
			sort.Strings(lines[start:i])
			inHeaders = false
		}
		if i < len(lines) && (lines[i] == "=== HEADERS ===" || lines[i] == "Headers:" || strings.HasPrefix(lines[i], "Status: ")) {
			inHeaders, start = true, i+1
		}
	}
	return strings.Join(lines, "\n")
}

type memSampler struct {
	peakHeap atomic.Uint64
	stop     chan struct{}
	done     sync.WaitGroup
}

func startMemSampler() *memSampler {
	s := &memSampler{stop: make(chan struct{})}
	s.done.Add(1)
	go func() {
		defer s.done.Done()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
		for {
			metrics.Read(sample)
			if live := sample[0].Value.Uint64(); live > s.peakHeap.Load() {
				s.peakHeap.Store(live)
			}
			select {
			case <-s.stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return s
}

func (s *memSampler) finish() uint64 {
	close(s.stop)
	s.done.Wait()
	return s.peakHeap.Load()
}

func heapNow() uint64 {
	runtime.GC()
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

type replayed struct {
	id   string
	path string
	pass int
}

type replayResult struct {
	all                   []replayed
	frameBytes, diskBytes int64
	segs                  []segmentSummary
	submit, write, seal   time.Duration
	submitMax             time.Duration
	worst                 time.Duration
	worstID               string
	latencies             []time.Duration
	exact, normalized     int
	peakWrite, peakSeal   uint64
	peakRead              uint64
}

type segmentSummary struct {
	id        int64
	session   string
	raw, disk int64
	records   int
}

// replayFixture feeds every record through the logger passes times, seals,
// and reads every record back from the sealed segments.
func replayFixture(t *testing.T, paths []string, passes int, sample bool) replayResult {
	t.Helper()
	// The timing run queues everything first and then times the writer alone,
	// so fixture loading does not count toward write throughput.
	var h *harness
	if sample {
		h = newHarness(t, Config{})
	} else {
		h = newIdleHarness(t, Config{QueueBytes: 4 << 30})
	}
	lg := h.logs.logger
	var res replayResult
	sampler := startMemSampler()
	for pass := 0; pass < passes; pass++ {
		for i, path := range paths {
			rec := loadFixture(t, path)
			id := fmt.Sprintf("fixture-p%02d-%04d", pass, i)
			res.all = append(res.all, replayed{id: id, path: path, pass: pass})
			ts := rec.ts.Add(time.Duration(pass) * time.Second)
			h.clock.set(ts)
			callStart := time.Now()
			if rec.streaming {
				w, err := lg.LogStreamingRequest(rec.url, rec.method, rec.reqHeaders, rec.reqBody, id)
				if err != nil {
					t.Fatal(err)
				}
				_ = w.WriteStatus(rec.status, rec.respHeaders)
				for _, event := range bytes.SplitAfter(rec.respBody, []byte("\n\n")) {
					w.WriteChunkAsync(event)
				}
				_ = w.WriteAPIRequest(rec.apiRequest)
				_ = w.WriteAPIResponse(rec.apiResponse)
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err := lg.LogRequest(rec.url, rec.method, rec.reqHeaders, rec.reqBody, rec.status, withoutEncoding(rec.respHeaders), rec.respBody,
				nil, rec.apiRequest, rec.apiResponse, nil, nil, id, ts, time.Time{}); err != nil {
				t.Fatal(err)
			}
			call := time.Since(callStart)
			res.submit += call
			res.submitMax = max(res.submitMax, call)
		}
	}
	start := time.Now()
	if !sample {
		h.startWriter()
	}
	h.flush()
	res.write = time.Since(start)
	res.peakWrite = sampler.finish()

	stats, err := h.logs.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Count != int64(len(res.all)) || stats.DroppedBodies+stats.DroppedRecords+stats.Truncated != 0 {
		t.Fatalf("stored %d of %d, drops %+v", stats.Count, len(res.all), stats)
	}
	res.frameBytes = stats.RawBytes

	sampler = startMemSampler()
	start = time.Now()
	h.sealAll()
	res.seal = time.Since(start)
	res.peakSeal = sampler.finish()
	if stats, err = h.logs.Stats(context.Background()); err != nil {
		t.Fatal(err)
	}
	res.diskBytes = stats.BytesOnDisk

	rows, err := h.db.Query(`SELECT s.id, s.session_id, s.raw_bytes, s.disk_bytes, COUNT(l.id)
		FROM reqlog_segments s JOIN reqlog_records l ON l.segment_id = s.id GROUP BY s.id ORDER BY s.raw_bytes DESC`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s segmentSummary
		if err := rows.Scan(&s.id, &s.session, &s.raw, &s.disk, &s.records); err != nil {
			t.Fatal(err)
		}
		res.segs = append(res.segs, s)
	}
	_ = rows.Close()

	if sample {
		sampler = startMemSampler()
	}
	runtime.GC()
	for _, r := range res.all {
		start := time.Now()
		_, raw, err := h.logs.Raw(context.Background(), r.id)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("%s: %v", r.path, err)
		}
		res.latencies = append(res.latencies, elapsed)
		if elapsed > res.worst {
			res.worst, res.worstID = elapsed, r.id
		}
		if storedCredential.Match(raw) {
			t.Fatalf("%s: credential header value stored", r.path)
		}
		if r.pass > 0 {
			// Later passes are shifted in time; the first pass checks fidelity.
			continue
		}
		original, err := os.ReadFile(r.path)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case bytes.Equal(raw, original):
			res.exact++
		case normalizeLog(string(raw)) == normalizeLog(string(original)):
			res.normalized++
		default:
			got, want := normalizeLog(string(raw)), normalizeLog(string(original))
			at := 0
			for at < len(got) && at < len(want) && got[at] == want[at] {
				at++
			}
			t.Fatalf("%s differs from the original at byte %d (lengths %d vs %d)", r.path, at, len(got), len(want))
		}
	}
	if sample {
		res.peakRead = sampler.finish()
	}
	return res
}

// TestFixtureReplay replays a directory of upstream request log files through
// the logger and reports compression, throughput, lookup latency, and memory.
// Set TAPROOT_REQLOG_FIXTURE_DIR to an extracted fixture; the files are never
// copied into the repository. TAPROOT_REQLOG_FIXTURE_REPEAT replays it more
// times to fill segments to the size cap for lookup timing; repeated content
// inflates the compression ratio, so take ratios from a single pass.
func TestFixtureReplay(t *testing.T) {
	dir := os.Getenv("TAPROOT_REQLOG_FIXTURE_DIR")
	if dir == "" {
		t.Skip("TAPROOT_REQLOG_FIXTURE_DIR not set")
	}
	passes := 1
	if raw := os.Getenv("TAPROOT_REQLOG_FIXTURE_REPEAT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			t.Fatalf("TAPROOT_REQLOG_FIXTURE_REPEAT=%q", raw)
		}
		passes = n
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixture logs in %s: %v", dir, err)
	}
	// Files load one at a time during replay so the fixture does not sit in
	// the heap the measurements watch.
	stamps := map[string]time.Time{}
	var fixtureBytes int64
	for _, p := range paths {
		rec := loadFixture(t, p)
		stamps[p] = rec.ts
		fixtureBytes += int64(len(rec.raw))
	}
	sort.SliceStable(paths, func(i, j int) bool { return stamps[paths[i]].Before(stamps[paths[j]]) })
	records := paths
	mb := func(n int64) float64 { return float64(n) / (1 << 20) }

	timing := replayFixture(t, paths, passes, false)
	t.Logf("records: %d files x %d passes, %.1f MiB upstream text, %.1f MiB stored frames, %d segments",
		len(records), passes, mb(fixtureBytes)*float64(passes), mb(timing.frameBytes), len(timing.segs))
	for _, s := range timing.segs {
		t.Logf("  segment %d: %d records, raw %.1f MiB, disk %.1f KiB, ratio %.1fx, session %q",
			s.id, s.records, mb(s.raw), float64(s.disk)/1024, float64(s.raw)/float64(max(s.disk, 1)), s.session)
	}
	t.Logf("compression: %.2f MiB on disk, ratio %.1fx vs upstream files, %.1fx vs stored frames",
		mb(timing.diskBytes), float64(fixtureBytes*int64(passes))/float64(timing.diskBytes), float64(timing.frameBytes)/float64(timing.diskBytes))
	t.Logf("write: logger calls on the request path took %v total, slowest %v; writer drained the queue (render, write, index) in %v = %.0f MiB/s",
		timing.submit.Round(time.Millisecond), timing.submitMax, timing.write.Round(time.Millisecond), mb(timing.frameBytes)/timing.write.Seconds())
	t.Logf("seal: %v = %.0f MiB/s, compression plus verification", timing.seal.Round(time.Millisecond), mb(timing.frameBytes)/timing.seal.Seconds())
	sort.Slice(timing.latencies, func(i, j int) bool { return timing.latencies[i] < timing.latencies[j] })
	pct := func(p float64) time.Duration {
		return timing.latencies[int(p*float64(len(timing.latencies)-1))].Round(time.Millisecond)
	}
	t.Logf("lookup over %d sealed records: p50 %v, p95 %v, max %v (%s); largest segment %.0f MiB raw",
		len(timing.all), pct(0.5), pct(0.95), timing.worst.Round(time.Millisecond), timing.worstID, mb(timing.segs[0].raw))
	t.Logf("fidelity: %d byte-identical, %d identical after normalizing version, credential masking, and header order",
		timing.exact, timing.normalized)

	// A low GC target keeps live-heap samples current; it slows the run, so
	// the timings above come from the first replay.
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	base := heapNow()
	if p := os.Getenv("HEAPPROF"); p != "" {
		f, _ := os.Create(p)
		_ = pprof.WriteHeapProfile(f)
		_ = f.Close()
	}
	memory := replayFixture(t, paths, passes, true)
	t.Logf("memory, live heap above the %.0f MiB baseline: write +%.0f MiB, seal +%.0f MiB, lookups +%.0f MiB",
		mb(int64(base)), mb(int64(memory.peakWrite)-int64(base)), mb(int64(memory.peakSeal)-int64(base)), mb(int64(memory.peakRead)-int64(base)))
}

func withoutEncoding(h map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k, v := range h {
		if !strings.EqualFold(k, "Content-Encoding") {
			out[k] = v
		}
	}
	return out
}

func loadFixture(t *testing.T, path string) *upstreamRecord {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := parseUpstreamLog(filepath.Base(path), raw)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}
