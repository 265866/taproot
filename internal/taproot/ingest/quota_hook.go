package ingest

import (
	"context"
	"database/sql"
	"fmt"
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
	// AccountWide is false for model- and feature-scoped windows.
	AccountWide bool
}

func exportObservation(o observation) QuotaObservation {
	return QuotaObservation{
		AuthIndex: o.AuthIndex, Provider: o.Provider, Window: o.Window, Utilization: o.Utilization,
		ResetsAt: o.ResetsAt, Status: o.Status, ObservedAt: o.ObservedAt, AccountWide: accountWideWindow(o.Window),
	}
}

type quotaHook struct {
	fn atomic.Pointer[func([]QuotaObservation)]
}

func (h *quotaHook) publish(obs []observation) {
	fn := h.fn.Load()
	if fn == nil || len(obs) == 0 {
		return
	}
	out := make([]QuotaObservation, 0, len(obs))
	for _, o := range obs {
		if o.AuthIndex != "" && o.Window != "" {
			out = append(out, exportObservation(o))
		}
	}
	(*fn)(out)
}

// OnQuota makes fn receive every quota observation once its flush commits.
// fn runs on the flush goroutine, so it must return quickly.
func (i *Ingest) OnQuota(fn func([]QuotaObservation)) {
	i.quotaHook.fn.Store(&fn)
}

// LatestQuota returns the newest stored observation of each window, leaving
// out accounts whose credential was removed.
func (i *Ingest) LatestQuota(ctx context.Context) ([]QuotaObservation, error) {
	rows, err := i.db.QueryContext(ctx, `SELECT auth_index, provider, window_name, utilization, resets_at, status, MAX(observed_at)
		FROM quota_observations WHERE auth_index NOT IN (SELECT auth_index FROM accounts WHERE removed_at IS NOT NULL)
		GROUP BY auth_index, window_name`)
	if err != nil {
		return nil, fmt.Errorf("latest quota: %w", err)
	}
	defer closeRows(rows)
	var out []QuotaObservation
	for rows.Next() {
		var (
			o        observation
			resetsAt sql.NullInt64
			observed int64
		)
		if err := rows.Scan(&o.AuthIndex, &o.Provider, &o.Window, &o.Utilization, &resetsAt, &o.Status, &observed); err != nil {
			return nil, fmt.Errorf("latest quota: %w", err)
		}
		if resetsAt.Valid {
			o.ResetsAt = time.UnixMilli(resetsAt.Int64).UTC()
		}
		o.ObservedAt = time.UnixMilli(observed).UTC()
		out = append(out, exportObservation(o))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("latest quota: %w", err)
	}
	return out, nil
}
