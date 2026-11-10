package ingest

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// QuotaObservation is one quota window reading as other taproot modules see it.
type QuotaObservation struct {
	AuthIndex   string
	Provider    string
	Window      string
	Utilization float64 // 0..1
	ResetsAt    time.Time
	Status      string
	ObservedAt  time.Time
	// PeriodSeconds is the window's length, or 0 when unknown.
	PeriodSeconds int64
	// AccountWide is false for model- and feature-scoped windows.
	AccountWide bool
}

func exportObservation(o observation) QuotaObservation {
	return QuotaObservation{
		AuthIndex: o.AuthIndex, Provider: o.Provider, Window: o.Window, Utilization: o.Utilization,
		ResetsAt: o.ResetsAt, Status: o.Status, ObservedAt: o.ObservedAt, PeriodSeconds: o.PeriodSeconds,
		AccountWide: accountWideWindow(o.Window),
	}
}

type quotaHook struct {
	mu  sync.Mutex
	fns atomic.Pointer[[]func([]QuotaObservation)]
}

func (h *quotaHook) publish(obs []observation) {
	fns := h.fns.Load()
	if fns == nil || len(obs) == 0 {
		return
	}
	out := make([]QuotaObservation, 0, len(obs))
	for _, o := range obs {
		if o.AuthIndex != "" && o.Window != "" {
			out = append(out, exportObservation(o))
		}
	}
	for _, fn := range *fns {
		fn(out)
	}
}

// OnQuota makes fn receive every quota observation once its flush commits.
// fn runs on the flush goroutine, so it must return quickly, and it shares
// the slice with other subscribers, so it must not modify it.
func (i *Ingest) OnQuota(fn func([]QuotaObservation)) {
	h := &i.quotaHook
	h.mu.Lock()
	defer h.mu.Unlock()
	var fns []func([]QuotaObservation)
	if cur := h.fns.Load(); cur != nil {
		fns = append(fns, *cur...)
	}
	fns = append(fns, fn)
	h.fns.Store(&fns)
}

// LatestQuota returns the newest stored observation of each window, leaving
// out accounts whose credential was removed.
func (i *Ingest) LatestQuota(ctx context.Context) ([]QuotaObservation, error) {
	latest, err := i.latestObservations(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]QuotaObservation, 0, len(latest))
	for _, o := range latest {
		out = append(out, exportObservation(o))
	}
	return out, nil
}
