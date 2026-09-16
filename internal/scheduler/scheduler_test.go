package scheduler_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shridarpatil/whatomate/internal/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zerodha/logf"
)

func testRedis(t *testing.T) *redis.Client {
	t.Helper()

	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set, skipping scheduler test")
	}
	opt, err := redis.ParseURL(url)
	require.NoError(t, err)

	client := redis.NewClient(opt)
	require.NoError(t, client.Ping(context.Background()).Err())
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func newScheduler(t *testing.T, rdb *redis.Client) *scheduler.Scheduler {
	t.Helper()
	return scheduler.New(rdb, logf.New(logf.Opts{Level: logf.ErrorLevel}))
}

// clearLocks removes any lock left by an earlier test run.
func clearLocks(t *testing.T, rdb *redis.Client, name string) {
	t.Helper()
	ctx := context.Background()
	rdb.Del(ctx, "wacrm:lock:job:"+name)
	rdb.Del(ctx, "wacrm:job:"+name+":last_run")
	rdb.Del(ctx, "wacrm:job:"+name+":last_error")
}

func TestRunOnce_RunsTheJob(t *testing.T) {
	rdb := testRedis(t)
	clearLocks(t, rdb, "test_runs")

	var runs atomic.Int32
	s := newScheduler(t, rdb)
	job := scheduler.Job{
		Name: "test_runs", Interval: time.Minute, Timeout: 5 * time.Second,
		Run: func(context.Context) error { runs.Add(1); return nil },
	}

	assert.True(t, s.RunOnce(context.Background(), job))
	assert.EqualValues(t, 1, runs.Load())
}

// The whole reason this package exists: with several replicas, only one may run
// a job per tick. Without the lock the SLA processor sent every warning once
// per replica.
func TestRunOnce_OnlyOneReplicaRunsATick(t *testing.T) {
	rdb := testRedis(t)
	clearLocks(t, rdb, "test_single_runner")

	var runs atomic.Int32
	makeJob := func() scheduler.Job {
		return scheduler.Job{
			Name: "test_single_runner", Interval: time.Minute, Timeout: 10 * time.Second,
			Run: func(context.Context) error {
				runs.Add(1)
				// Hold the lock long enough that the others genuinely collide.
				time.Sleep(150 * time.Millisecond)
				return nil
			},
		}
	}

	// Five separate replicas, each with its own instance id.
	var wg sync.WaitGroup
	ran := make([]bool, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ran[idx] = newScheduler(t, rdb).RunOnce(context.Background(), makeJob())
		}(i)
	}
	wg.Wait()

	assert.EqualValues(t, 1, runs.Load(), "exactly one replica may run a tick")

	winners := 0
	for _, r := range ran {
		if r {
			winners++
		}
	}
	assert.Equal(t, 1, winners, "only the lock holder reports having run")
}

// The lock must be released, or the job would run once and never again.
func TestRunOnce_ReleasesTheLockForTheNextTick(t *testing.T) {
	rdb := testRedis(t)
	clearLocks(t, rdb, "test_release")

	var runs atomic.Int32
	s := newScheduler(t, rdb)
	job := scheduler.Job{
		Name: "test_release", Interval: time.Minute, Timeout: 10 * time.Second,
		Run: func(context.Context) error { runs.Add(1); return nil },
	}

	require.True(t, s.RunOnce(context.Background(), job))
	require.True(t, s.RunOnce(context.Background(), job), "the next tick can take the lock again")
	assert.EqualValues(t, 2, runs.Load())
}

// A replica must not delete a lock another replica has since acquired, or two
// runners could overlap.
func TestRelease_OnlyRemovesItsOwnLock(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	clearLocks(t, rdb, "test_ownership")

	// A different replica holds the lock.
	require.NoError(t, rdb.Set(ctx, "wacrm:lock:job:test_ownership", "someone-else", time.Minute).Err())

	s := newScheduler(t, rdb)
	ran := s.RunOnce(ctx, scheduler.Job{
		Name: "test_ownership", Interval: time.Minute, Timeout: 5 * time.Second,
		Run: func(context.Context) error { return nil },
	})
	assert.False(t, ran, "the lock is held elsewhere")

	held, err := rdb.Get(ctx, "wacrm:lock:job:test_ownership").Result()
	require.NoError(t, err)
	assert.Equal(t, "someone-else", held, "skipping must not steal or clear another replica's lock")
}

func TestRunOnce_RecordsSuccessAndFailure(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	clearLocks(t, rdb, "test_outcome")

	s := newScheduler(t, rdb)
	failing := scheduler.Job{
		Name: "test_outcome", Interval: time.Minute, Timeout: 5 * time.Second,
		Run: func(context.Context) error { return errors.New("job blew up") },
	}
	require.True(t, s.RunOnce(ctx, failing))

	_, err := s.LastRun(ctx, "test_outcome")
	require.NoError(t, err, "a failed run still records when it ran")
	assert.Equal(t, "job blew up", s.LastError(ctx, "test_outcome"))

	// A later success must clear the stale error.
	succeeding := failing
	succeeding.Run = func(context.Context) error { return nil }
	require.True(t, s.RunOnce(ctx, succeeding))
	assert.Empty(t, s.LastError(ctx, "test_outcome"))
}

// A job that overruns must be cut off rather than holding its lock past expiry
// and overlapping with the next replica's run.
func TestRunOnce_CancelsAJobThatOverrunsItsTimeout(t *testing.T) {
	rdb := testRedis(t)
	clearLocks(t, rdb, "test_timeout")

	var observed error
	s := newScheduler(t, rdb)
	require.True(t, s.RunOnce(context.Background(), scheduler.Job{
		Name: "test_timeout", Interval: time.Minute, Timeout: 50 * time.Millisecond,
		Run: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				observed = ctx.Err()
				return ctx.Err()
			case <-time.After(2 * time.Second):
				return nil
			}
		},
	}))

	assert.ErrorIs(t, observed, context.DeadlineExceeded)
}

func TestRegister_RejectsDuplicateNames(t *testing.T) {
	s := newScheduler(t, testRedis(t))
	job := scheduler.Job{
		Name: "dupe", Interval: time.Minute,
		Run: func(context.Context) error { return nil },
	}
	s.Register(job)

	assert.Panics(t, func() { s.Register(job) },
		"two jobs sharing a lock would alternate instead of both running")
}

func TestRegister_RequiresAnInterval(t *testing.T) {
	s := newScheduler(t, testRedis(t))
	assert.Panics(t, func() {
		s.Register(scheduler.Job{Name: "no-interval", Run: func(context.Context) error { return nil }})
	})
}

func TestJobs_ReturnsRegisteredJobs(t *testing.T) {
	s := newScheduler(t, testRedis(t))
	s.Register(scheduler.Job{Name: "a", Interval: time.Minute, Run: func(context.Context) error { return nil }})
	s.Register(scheduler.Job{Name: "b", Interval: time.Minute, Run: func(context.Context) error { return nil }})

	assert.Len(t, s.Jobs(), 2)
}
