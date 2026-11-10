package backup

import (
	"context"
	"os"
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

// runSchedule runs the schedule loop from 23:00 past the 00:00 run and
// returns the run it recorded.
func runSchedule(t *testing.T, f *fixture) (Run, bool) {
	t.Helper()
	clock := newFakeClock(time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC))
	clock.until = time.Date(2026, 11, 3, 0, 30, 0, 0, time.UTC)
	clock.attach(f.b)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.b.Start(ctx)
	scheduledAfter(t, f, clock)
	run, ok, err := f.b.LastScheduledRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return run, ok
}

func TestScheduleRecordsASuccessfulRun(t *testing.T) {
	f := newFixture(t, Config{DailyAt: "00:00", Keep: 10})
	if _, ok, err := f.b.LastScheduledRun(context.Background()); ok || err != nil {
		t.Fatalf("before any run: ok %t, err %v; want no run", ok, err)
	}
	run, ok := runSchedule(t, f)
	if !ok || run.Error != "" || run.At.Before(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("recorded run = %+v (ok %t), want a success at or after 00:00", run, ok)
	}
}

// A scheduled run that fails is recorded with its error, so the Dashboard
// can say so after a restart.
func TestScheduleRecordsAFailedRun(t *testing.T) {
	f := newFixture(t, Config{DailyAt: "00:00", Keep: 10})
	// config.yaml as a directory makes the backup fail to read it.
	if err := os.Remove(f.paths.ConfigFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.paths.ConfigFile, 0o700); err != nil {
		t.Fatal(err)
	}
	run, ok := runSchedule(t, f)
	if !ok || run.Error == "" {
		t.Fatalf("recorded run = %+v (ok %t), want a failure with its error", run, ok)
	}
	if err := f.b.RecordScheduledRun(context.Background(), run.At.Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	if next, _, _ := f.b.LastScheduledRun(context.Background()); next.Error != "" || !next.At.Equal(run.At.Add(time.Hour)) {
		t.Fatalf("after a later success the record is %+v, want it replaced", next)
	}
}
