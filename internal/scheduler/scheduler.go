// Package scheduler runs the daily rate refresh and the nightly backup.
//
// It is the only network caller in the process: nothing in a request path ever
// waits on a provider. A failed run is logged and retried on the next tick; it
// never takes the server down and never blocks a render.
package scheduler

import (
	"context"
	"log/slog"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antlko/moneyapp/internal/platform/clock"
)

// Job is one scheduled piece of work.
type Job struct {
	Name     string
	Interval time.Duration
	// Jitter spreads the run over a window, so every deployment of this app does
	// not hit the same free endpoint at the same second.
	Jitter time.Duration
	// RunOnBoot catches up work missed while the process was down. A restart must
	// not cost a day's rates.
	RunOnBoot bool
	// FirstDelay overrides the wait before the first scheduled run, so a job with
	// a wall-clock time (the nightly backup) lands on it.
	FirstDelay time.Duration
	Run        func(ctx context.Context) error
}

// Scheduler runs jobs on their intervals until its context is cancelled.
type Scheduler struct {
	log   *slog.Logger
	clock clock.Clock
	jobs  []Job

	mu      sync.Mutex
	lastRun map[string]time.Time
}

// New builds a scheduler.
func New(log *slog.Logger, clk clock.Clock) *Scheduler {
	return &Scheduler{log: log, clock: clk, lastRun: map[string]time.Time{}}
}

// Add registers a job. It must be called before Start.
func (s *Scheduler) Add(job Job) { s.jobs = append(s.jobs, job) }

// Start runs every job in its own goroutine and returns immediately.
//
// Each goroutine recovers from a panic in its job: a bad response from a free
// API must not be able to take down the web server with it.
func (s *Scheduler) Start(ctx context.Context) {
	for _, job := range s.jobs {
		go s.loop(ctx, job)
	}
}

func (s *Scheduler) loop(ctx context.Context, job Job) {
	if job.Interval <= 0 {
		s.log.Warn("scheduler: job has no interval, not scheduling", "job", job.Name)
		return
	}
	if job.RunOnBoot {
		s.runOnce(ctx, job)
	}

	first := true
	for {
		wait := job.Interval
		if first && job.FirstDelay > 0 {
			wait = job.FirstDelay
		}
		first = false
		if job.Jitter > 0 {
			//nolint:gosec // jitter spreads load; it is not a security decision
			wait += time.Duration(rand.Int63n(int64(job.Jitter)))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
			s.runOnce(ctx, job)
		}
	}
}

func (s *Scheduler) runOnce(ctx context.Context, job Job) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("scheduler: job panicked", "job", job.Name, "panic", r)
		}
	}()

	started := s.clock.Now()
	if err := job.Run(ctx); err != nil {
		// A provider outage is an ordinary event, not a crash: the last value
		// stands and the next tick tries again.
		s.log.Warn("scheduler: job failed", "job", job.Name, "error", err.Error())
		return
	}
	s.mu.Lock()
	s.lastRun[job.Name] = started
	s.mu.Unlock()
	s.log.Info("scheduler: job ran",
		"job", job.Name, "duration_ms", s.clock.Now().Sub(started).Milliseconds())
}

// LastRun reports when a job last completed, for diagnostics.
func (s *Scheduler) LastRun(name string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.lastRun[name]
	return at, ok
}

// DailyAt parses the daily part of a five-field cron expression and returns the
// duration until its next occurrence.
//
// Only `M H * * *` is supported, which is the only shape the backup schedule has
// ever had. Anything else falls back to the interval, rather than pretending to
// be a cron implementation — a half-supported cron expression that silently runs
// at the wrong time is worse than one that is honestly ignored.
func DailyAt(schedule string, now time.Time, fallback time.Duration) time.Duration {
	fields := strings.Fields(schedule)
	if len(fields) != 5 || fields[2] != "*" || fields[3] != "*" || fields[4] != "*" {
		return fallback
	}
	minute, err := strconv.Atoi(fields[0])
	if err != nil || minute < 0 || minute > 59 {
		return fallback
	}
	hour, err := strconv.Atoi(fields[1])
	if err != nil || hour < 0 || hour > 23 {
		return fallback
	}

	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next.Sub(now)
}
