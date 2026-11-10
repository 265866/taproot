package backup

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeClock drives the schedule loop without waiting: each timer moves the
// clock by its duration, plus skew for the timer of that index, and fires at
// once. A timer that would end past until never fires, so the loop idles.
// Stopping a timer that has not been read moves the clock back.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	until  time.Time
	skew   map[int]time.Duration
	timers int
	// longest is the longest wait the loop asked for.
	longest time.Duration
	// idle is closed once the loop waits past until, which it does only
	// after any backup it started has finished.
	idle     chan struct{}
	idleOnce sync.Once
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start, skew: map[int]time.Duration{}, idle: make(chan struct{})}
}

func (c *fakeClock) attach(b *Backups) {
	b.now = func() time.Time {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.now
	}
	b.newTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.longest = max(c.longest, d)
		next := c.now.Add(d + c.skew[c.timers])
		c.timers++
		if next.After(c.until) {
			c.idleOnce.Do(func() { close(c.idle) })
			return nil, func() bool { return true }
		}
		prev := c.now
		c.now = next
		ch := make(chan time.Time, 1)
		ch <- next
		// A loop woken by a schedule change stops the timer instead, and
		// the clock never got there.
		return ch, func() bool {
			select {
			case <-ch:
				c.mu.Lock()
				c.now = prev
				c.mu.Unlock()
			default:
			}
			return true
		}
	}
}

// scheduledAfter waits until the loop idles past the fake clock's end, then
// returns the scheduled backups.
func scheduledAfter(t *testing.T, f *fixture, clock *fakeClock) []Entry {
	t.Helper()
	select {
	case <-clock.idle:
	case <-time.After(5 * time.Second):
		t.Fatal("the schedule loop did not reach the end of the fake clock")
	}
	var out []Entry
	for _, e := range f.list(t) {
		if e.Reason == ReasonScheduled {
			out = append(out, e)
		}
	}
	return out
}

// A wall clock that steps back while the loop waits must not run the
// backup before its time, nor run the same day's backup twice.
func TestScheduleRunsOnceWhenTheClockStepsBack(t *testing.T) {
	f := newFixture(t, Config{DailyAt: "00:00", Keep: 10})
	start := time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC)
	clock := newFakeClock(start)
	clock.until = time.Date(2026, 11, 3, 0, 30, 0, 0, time.UTC)
	// The first wait ends five minutes short on the wall clock, as after an
	// NTP step back.
	clock.skew[0] = -5 * time.Minute
	clock.attach(f.b)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.b.Start(ctx)

	got := scheduledAfter(t, f, clock)
	if len(got) != 1 {
		t.Fatalf("scheduled backups = %+v, want one", got)
	}
	if want := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC); got[0].CreatedAt.Before(want) {
		t.Fatalf("backup ran at %s, before its time %s", got[0].CreatedAt, want)
	}
	if clock.longest > maxScheduleWait {
		t.Fatalf("the loop waited %s at once; a suspended host would run the backup that late", clock.longest)
	}
}
