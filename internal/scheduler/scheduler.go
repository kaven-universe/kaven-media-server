package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

var (
	ErrRunning     = errors.New("scheduler is already running")
	ErrJobNotFound = errors.New("scheduled job not found")
)

type Job struct {
	Name     string
	Interval time.Duration
	Run      func(context.Context) error
}

type Scheduler struct {
	jobs       []*scheduledJob
	jobsByName map[string]*scheduledJob
	newTicker  func(time.Duration) ticker

	mutex   sync.Mutex
	running bool
}

type scheduledJob struct {
	Job
	trigger chan struct{}
}

type ticker interface {
	C() <-chan time.Time
	Stop()
}

type systemTicker struct {
	*time.Ticker
}

func (ticker systemTicker) C() <-chan time.Time {
	return ticker.Ticker.C
}

func New(jobs ...Job) (*Scheduler, error) {
	if len(jobs) == 0 {
		return nil, errors.New("create scheduler: at least one job is required")
	}
	seen := make(map[string]struct{}, len(jobs))
	owned := make([]*scheduledJob, 0, len(jobs))
	byName := make(map[string]*scheduledJob, len(jobs))
	for index, job := range jobs {
		job.Name = strings.TrimSpace(job.Name)
		if job.Name == "" {
			return nil, fmt.Errorf("create scheduler: job %d has no name", index)
		}
		if _, duplicate := seen[job.Name]; duplicate {
			return nil, fmt.Errorf("create scheduler: duplicate job name %q", job.Name)
		}
		seen[job.Name] = struct{}{}
		if job.Interval <= 0 {
			return nil, fmt.Errorf("create scheduler: job %q interval must be positive", job.Name)
		}
		if job.Run == nil {
			return nil, fmt.Errorf("create scheduler: job %q callback is required", job.Name)
		}
		scheduled := &scheduledJob{Job: job, trigger: make(chan struct{}, 1)}
		owned = append(owned, scheduled)
		byName[job.Name] = scheduled
	}
	return &Scheduler{
		jobs: owned, jobsByName: byName,
		newTicker: func(interval time.Duration) ticker {
			return systemTicker{Ticker: time.NewTicker(interval)}
		},
	}, nil
}

// Trigger requests an immediate run of a named job. One request may remain
// queued while the job is active; additional requests are safely coalesced.
func (scheduler *Scheduler) Trigger(name string) (bool, error) {
	job, exists := scheduler.jobsByName[name]
	if !exists {
		return false, fmt.Errorf("trigger job %q: %w", name, ErrJobNotFound)
	}
	select {
	case job.trigger <- struct{}{}:
		return true, nil
	default:
		return false, nil
	}
}

// Run blocks until ctx is canceled and all running jobs have returned. A job
// receives the same context and must stop promptly when it is canceled.
func (scheduler *Scheduler) Run(ctx context.Context) error {
	scheduler.mutex.Lock()
	if scheduler.running {
		scheduler.mutex.Unlock()
		return ErrRunning
	}
	scheduler.running = true
	scheduler.mutex.Unlock()
	defer func() {
		scheduler.mutex.Lock()
		scheduler.running = false
		scheduler.mutex.Unlock()
	}()

	var workers sync.WaitGroup
	workers.Add(len(scheduler.jobs))
	for _, job := range scheduler.jobs {
		go func() {
			defer workers.Done()
			scheduler.runJob(ctx, job)
		}()
	}
	workers.Wait()
	return nil
}

func (scheduler *Scheduler) runJob(ctx context.Context, job *scheduledJob) {
	ticker := scheduler.newTicker(job.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			scheduler.execute(ctx, job.Job)
		case <-job.trigger:
			scheduler.execute(ctx, job.Job)
		}
	}
}

func (scheduler *Scheduler) execute(ctx context.Context, job Job) {
	started := time.Now()
	err := job.Run(ctx)
	if err != nil && ctx.Err() == nil {
		slog.Error("scheduled job failed", "job", job.Name, "duration", time.Since(started), "error", err)
		return
	}
	if ctx.Err() == nil {
		slog.Info("scheduled job completed", "job", job.Name, "duration", time.Since(started))
	}
}
