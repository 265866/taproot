package reqlog

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestStatusFiltersUseErrorIndex checks every status filter shape returns
// exactly its records, and that the ones at or above 400 can walk the partial
// errors index instead of every record.
func TestStatusFiltersUseErrorIndex(t *testing.T) {
	h := newHarness(t, Config{})
	ts := h.clock.now()
	statuses := []int{200, 201, 404, 429, 429, 500, 503}
	for i, status := range statuses {
		logSimple(t, h.logs.logger, fmt.Sprintf("s%d-%d", i, status), "/v1/messages", "sess", status, ts.Add(time.Duration(i)*time.Second), []byte(`{"n":1}`))
	}
	h.flush()
	want := func(keep func(int) bool) []string {
		var ids []string
		for i := len(statuses) - 1; i >= 0; i-- {
			if keep(statuses[i]) {
				ids = append(ids, fmt.Sprintf("s%d-%d", i, statuses[i]))
			}
		}
		return ids
	}
	cases := map[string]func(int) bool{
		"error": func(s int) bool { return s >= 400 },
		"4xx":   func(s int) bool { return s >= 400 && s < 500 },
		"5xx":   func(s int) bool { return s >= 500 },
		"2xx":   func(s int) bool { return s < 300 },
		"429":   func(s int) bool { return s == 429 },
		"200":   func(s int) bool { return s == 200 },
	}
	for filter, keep := range cases {
		rows, _, err := h.logs.List(context.Background(), ListFilter{Status: filter})
		if err != nil {
			t.Fatalf("%s: %v", filter, err)
		}
		var got []string
		for _, r := range rows {
			got = append(got, r.RequestID)
		}
		if !slices.Equal(got, want(keep)) {
			t.Errorf("status=%s returned %v, want %v", filter, got, want(keep))
		}
	}
	for _, c := range []struct {
		filter ListFilter
		index  string
	}{
		{ListFilter{Status: "error"}, "reqlog_records_errors_ts"},
		{ListFilter{Status: "4xx"}, "reqlog_records_errors_ts"},
		{ListFilter{Status: "5xx"}, "reqlog_records_errors_ts"},
		{ListFilter{Status: "429"}, "reqlog_records_errors_ts"},
		{ListFilter{Status: "error", Query: "abc"}, "reqlog_records_errors_ts"},
		{ListFilter{Model: "claude-x"}, "reqlog_records_model"},
		{ListFilter{AuthIndex: "a1"}, "reqlog_records_auth"},
	} {
		if plan := listPlan(t, h, c.filter); !strings.Contains(plan, c.index) {
			t.Errorf("%+v: plan %q does not use %s", c.filter, plan, c.index)
		}
	}
}

// listPlan is SQLite's plan for List's query under f.
func listPlan(t *testing.T, h *harness, f ListFilter) string {
	t.Helper()
	query, args, _, err := listQuery(f)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := h.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail + "; ")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan.String()
}
