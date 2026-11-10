package routing

import (
	"strings"
	"sync"
	"time"
)

// Window is one quota window reading from taproot's quota store.
type Window struct {
	AuthIndex     string
	Provider      string
	Name          string
	Utilization   float64 // 0..1
	Status        string
	ResetsAt      time.Time
	PeriodSeconds int64
	ObservedAt    time.Time
}

// Readings holds the newest weekly window reading per credential (by auth
// index) from taproot's quota store. Upstream keeps quota headers in memory
// only; these survive a restart and include the idle-account poller's reads.
type Readings struct {
	mu      sync.RWMutex
	byIndex map[string]Window
}

// Stored is the store that selectors from NewSoonestResetSelector read.
// taproot.Setup feeds it from ingest.
var Stored = &Readings{}

// Observe keeps the weekly windows among ws that are newer than the stored
// reading of their credential.
func (r *Readings) Observe(ws []Window) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range ws {
		if w.AuthIndex == "" || !isWeekly(w) {
			continue
		}
		if prev, ok := r.byIndex[w.AuthIndex]; ok && w.ObservedAt.Before(prev.ObservedAt) {
			continue
		}
		if r.byIndex == nil {
			r.byIndex = map[string]Window{}
		}
		r.byIndex[w.AuthIndex] = w
	}
}

// isWeekly matches the windows WeeklyWindowOf reads from headers, under the
// names ingest stores them by.
func isWeekly(w Window) bool {
	switch strings.ToLower(w.Provider) {
	case "claude":
		return w.Name == "seven_day"
	case "codex":
		weekSeconds := int64(week / time.Second)
		return w.PeriodSeconds == weekSeconds && (w.Name == "codex_primary" || w.Name == "codex_secondary") ||
			w.PeriodSeconds == 0 && w.Name == "codex_secondary"
	}
	return false
}

func (r *Readings) weekly(authIndex string, now time.Time) (WeeklyWindow, bool) {
	if r == nil || authIndex == "" {
		return WeeklyWindow{}, false
	}
	r.mu.RLock()
	w, ok := r.byIndex[authIndex]
	r.mu.RUnlock()
	if !ok || w.ResetsAt.IsZero() {
		return WeeklyWindow{}, false
	}
	exhausted := w.Utilization >= 1 || strings.EqualFold(w.Status, "rejected")
	return rollForward(w.ResetsAt, week, exhausted, now), true
}
