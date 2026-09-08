package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewRejectsInvalidJobs(t *testing.T) {
	valid := Job{Name: "valid", Interval: time.Hour, Run: func(context.Context) error { return nil }}
	for _, test := range []struct {
		name string
		jobs []Job
	}{
		{name: "empty"},
		{name: "missing name", jobs: []Job{{Interval: time.Hour, Run: valid.Run}}},
		{name: "invalid interval", jobs: []Job{{Name: "invalid", Run: valid.Run}}},
		{name: "missing callback", jobs: []Job{{Name: "invalid", Interval: time.Hour}}},
		{name: "duplicate name", jobs: []Job{valid, valid}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.jobs...); err == nil {
				t.Fatal("New accepted invalid jobs")
			}
		})
	}
}

func TestSchedulerWaitsForTickAndPreventsJobOverlap(t *testing.T) {
	ticks := newFakeTicker()
	started := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	var active atomic.Int32
	var maximum atomic.Int32
	scheduled, err := New(Job{
		Name: "archive", Interval: time.Hour,
		Run: func(context.Context) error {
			current := active.Add(1)
			defer active.Add(-1)
			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			started <- struct{}{}
			<-release
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduled.newTicker = func(time.Duration) ticker { return ticks }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduled.Run(ctx) }()
	awaitSignal(t, ticks.created, "ticker creation")

	select {
	case <-started:
		t.Fatal("job ran before its first tick")
	default:
	}
	ticks.tick()
	awaitSignal(t, started, "first job invocation")
	ticks.tick()
	select {
	case <-started:
		t.Fatal("job overlapped its previous invocation")
	case <-time.After(20 * time.Millisecond):
	}
	release <- struct{}{}
	awaitSignal(t, started, "second job invocation")
	if maximum.Load() != 1 {
		t.Fatalf("maximum concurrent invocations = %d, want 1", maximum.Load())
	}
	release <- struct{}{}
	cancel()
	if err := awaitResult(t, done, "scheduler shutdown"); err != nil {
		t.Fatal(err)
	}
	if !ticks.stopped.Load() {
		t.Fatal("ticker was not stopped")
	}
}

func TestSchedulerCancellationStopsRunningJobAndWaits(t *testing.T) {
	ticks := newFakeTicker()
	started := make(chan struct{})
	stopped := make(chan struct{})
	scheduled, err := New(Job{
		Name: "archive", Interval: time.Hour,
		Run: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(stopped)
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduled.newTicker = func(time.Duration) ticker { return ticks }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduled.Run(ctx) }()
	awaitSignal(t, ticks.created, "ticker creation")
	ticks.tick()
	awaitSignal(t, started, "job start")
	cancel()
	awaitSignal(t, stopped, "job cancellation")
	if err := awaitResult(t, done, "scheduler shutdown"); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulerContinuesAfterJobFailure(t *testing.T) {
	ticks := newFakeTicker()
	invoked := make(chan struct{}, 2)
	var calls atomic.Int32
	scheduled, err := New(Job{
		Name: "archive", Interval: time.Hour,
		Run: func(context.Context) error {
			current := calls.Add(1)
			invoked <- struct{}{}
			if current == 1 {
				return errors.New("temporary failure")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduled.newTicker = func(time.Duration) ticker { return ticks }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduled.Run(ctx) }()
	awaitSignal(t, ticks.created, "ticker creation")

	ticks.tick()
	awaitSignal(t, invoked, "failed job invocation")
	ticks.tick()
	awaitSignal(t, invoked, "retried job invocation")
	if calls.Load() != 2 {
		t.Fatalf("job calls = %d, want 2", calls.Load())
	}
	cancel()
	if err := awaitResult(t, done, "scheduler shutdown"); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulerTriggerRunsImmediatelyAndCoalescesPendingRequests(t *testing.T) {
	ticks := newFakeTicker()
	started := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	scheduled, err := New(Job{
		Name: "archive", Interval: time.Hour,
		Run: func(context.Context) error {
			started <- struct{}{}
			<-release
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduled.newTicker = func(time.Duration) ticker { return ticks }
	if queued, err := scheduled.Trigger("archive"); err != nil || !queued {
		t.Fatalf("first trigger = %v, %v", queued, err)
	}
	if queued, err := scheduled.Trigger("archive"); err != nil || queued {
		t.Fatalf("duplicate pending trigger = %v, %v", queued, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduled.Run(ctx) }()
	awaitSignal(t, started, "immediate job invocation")

	if queued, err := scheduled.Trigger("archive"); err != nil || !queued {
		t.Fatalf("trigger during active job = %v, %v", queued, err)
	}
	if queued, err := scheduled.Trigger("archive"); err != nil || queued {
		t.Fatalf("duplicate active trigger = %v, %v", queued, err)
	}
	release <- struct{}{}
	awaitSignal(t, started, "queued job invocation")
	release <- struct{}{}
	cancel()
	if err := awaitResult(t, done, "scheduler shutdown"); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduled.Trigger("missing"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("missing job error = %v, want ErrJobNotFound", err)
	}
}

func TestSchedulerRejectsConcurrentRun(t *testing.T) {
	ticks := newFakeTicker()
	scheduled, err := New(Job{Name: "archive", Interval: time.Hour, Run: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	scheduled.newTicker = func(time.Duration) ticker { return ticks }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduled.Run(ctx) }()
	awaitSignal(t, ticks.created, "ticker creation")
	if err := scheduled.Run(context.Background()); !errors.Is(err, ErrRunning) {
		t.Fatalf("concurrent Run error = %v, want ErrRunning", err)
	}
	cancel()
	if err := awaitResult(t, done, "scheduler shutdown"); err != nil {
		t.Fatal(err)
	}
}

type fakeTicker struct {
	channel chan time.Time
	created chan struct{}
	stopped atomic.Bool
}

func newFakeTicker() *fakeTicker {
	return &fakeTicker{channel: make(chan time.Time, 8), created: make(chan struct{})}
}

func (ticker *fakeTicker) C() <-chan time.Time {
	select {
	case <-ticker.created:
	default:
		close(ticker.created)
	}
	return ticker.channel
}

func (ticker *fakeTicker) Stop() {
	ticker.stopped.Store(true)
}

func (ticker *fakeTicker) tick() {
	ticker.channel <- time.Now()
}

func awaitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func awaitResult(t *testing.T, result <-chan error, description string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
		return nil
	}
}
