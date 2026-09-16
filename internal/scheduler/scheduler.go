// Package scheduler runs periodic jobs with a cluster-wide leader lock
// (plan 00, F4).
//
// The only periodic work before this was the SLA processor, a bare time.Ticker
// started in every server process. With more than one replica every replica ran
// it, so warnings were sent several times over and auto-close raced itself. Any
// job added the same way would inherit the same problem, and there were already
// jobs missing entirely — nothing ever started a scheduled campaign.
//
// Each tick takes a short-lived Redis lock named after the job. Whichever
// replica gets it runs; the rest skip that tick. Jobs must therefore be
// idempotent and safe to miss a tick: the lock guarantees at most one runner,
// not exactly one run.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/zerodha/logf"
)

// Job is one periodic task.
type Job struct {
	// Name identifies the job and names its lock.
	Name string

	// Interval is how often the job is attempted. Every replica attempts it;
	// only the lock holder runs.
	Interval time.Duration

	// Timeout bounds one run, and is also the lock's lifetime, so a replica
	// that dies mid-run cannot hold the job hostage.
	Timeout time.Duration

	Run func(ctx context.Context) error
}

// Scheduler runs registered jobs.
type Scheduler struct {
	Redis *redis.Client
	Log   logf.Logger

	// InstanceID identifies this replica in lock values, so a replica only
	// ever releases a lock it still holds.
	InstanceID string

	mu   sync.Mutex
	jobs []Job
}

// New builds a Scheduler.
func New(rdb *redis.Client, log logf.Logger) *Scheduler {
	return &Scheduler{
		Redis:      rdb,
		Log:        log,
		InstanceID: uuid.NewString(),
	}
}

// Register adds a job. Registering the same name twice panics: two jobs sharing
// a lock would take turns rather than both running, which is almost never what
// the author meant.
func (s *Scheduler) Register(j Job) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if j.Name == "" || j.Run == nil {
		panic("scheduler: job needs a name and a Run function")
	}
	if j.Interval <= 0 {
		panic("scheduler: job " + j.Name + " needs a positive interval")
	}
	if j.Timeout <= 0 {
		j.Timeout = j.Interval
	}
	for _, existing := range s.jobs {
		if existing.Name == j.Name {
			panic("scheduler: duplicate job name " + j.Name)
		}
	}
	s.jobs = append(s.jobs, j)
}

// Jobs returns the registered jobs.
func (s *Scheduler) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, len(s.jobs))
	copy(out, s.jobs)
	return out
}

// Start runs every registered job on its own schedule until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	for _, job := range s.Jobs() {
		go s.runLoop(ctx, job)
	}
	s.Log.Info("Scheduler started", "jobs", len(s.Jobs()), "instance", s.InstanceID)
}

func (s *Scheduler) runLoop(ctx context.Context, job Job) {
	ticker := time.NewTicker(job.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RunOnce(ctx, job)
		}
	}
}

// RunOnce attempts one run of a job, taking the lock first. It reports whether
// this replica ran it.
func (s *Scheduler) RunOnce(ctx context.Context, job Job) bool {
	acquired, err := s.acquire(ctx, job)
	if err != nil {
		s.Log.Error("Scheduler lock failed", "job", job.Name, "error", err)
		return false
	}
	if !acquired {
		// Another replica is on it. Not an error worth logging at info.
		s.Log.Debug("Scheduler job skipped, lock held elsewhere", "job", job.Name)
		return false
	}
	defer s.release(context.WithoutCancel(ctx), job)

	runCtx, cancel := context.WithTimeout(ctx, job.Timeout)
	defer cancel()

	start := time.Now()
	runErr := job.Run(runCtx)
	s.recordOutcome(context.WithoutCancel(ctx), job, runErr)

	if runErr != nil {
		s.Log.Error("Scheduler job failed", "job", job.Name, "error", runErr,
			"took", time.Since(start).String())
		return true
	}
	s.Log.Debug("Scheduler job finished", "job", job.Name, "took", time.Since(start).String())
	return true
}

func lockKey(name string) string    { return "wacrm:lock:job:" + name }
func lastRunKey(name string) string { return "wacrm:job:" + name + ":last_run" }
func lastErrKey(name string) string { return "wacrm:job:" + name + ":last_error" }

// acquire takes the job's lock for the length of its timeout.
func (s *Scheduler) acquire(ctx context.Context, job Job) (bool, error) {
	return s.Redis.SetNX(ctx, lockKey(job.Name), s.InstanceID, job.Timeout).Result()
}

// releaseScript deletes the lock only if this replica still owns it. A plain
// DEL would let a replica whose run overran its timeout delete the lock a
// different replica had since taken, allowing two runners at once.
var releaseScript = redis.NewScript(`
	if redis.call("GET", KEYS[1]) == ARGV[1] then
		return redis.call("DEL", KEYS[1])
	end
	return 0
`)

func (s *Scheduler) release(ctx context.Context, job Job) {
	if err := releaseScript.Run(ctx, s.Redis, []string{lockKey(job.Name)}, s.InstanceID).Err(); err != nil && err != redis.Nil {
		s.Log.Error("Scheduler lock release failed", "job", job.Name, "error", err)
	}
}

// recordOutcome stores the last run time and error for operator visibility.
func (s *Scheduler) recordOutcome(ctx context.Context, job Job, runErr error) {
	const retain = 7 * 24 * time.Hour

	s.Redis.Set(ctx, lastRunKey(job.Name), time.Now().UTC().Format(time.RFC3339), retain)
	if runErr != nil {
		s.Redis.Set(ctx, lastErrKey(job.Name), runErr.Error(), retain)
		return
	}
	s.Redis.Del(ctx, lastErrKey(job.Name))
}

// LastRun returns when a job last completed on any replica.
func (s *Scheduler) LastRun(ctx context.Context, name string) (time.Time, error) {
	v, err := s.Redis.Get(ctx, lastRunKey(name)).Result()
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: bad last_run for %s: %w", name, err)
	}
	return t, nil
}

// LastError returns a job's last recorded failure, empty when the last run
// succeeded.
func (s *Scheduler) LastError(ctx context.Context, name string) string {
	v, err := s.Redis.Get(ctx, lastErrKey(name)).Result()
	if err != nil {
		return ""
	}
	return v
}
