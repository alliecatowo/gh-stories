// Package worker runs the two background job loops that turn an authorized
// upload into a published Story (the media loop) and that keep object
// storage and the database converging on what has logically expired (the
// cleanup loop). Both loops are designed to survive a crash mid-job: work is
// claimed via a time-limited lease (see internal/store's ClaimMediaJob /
// ClaimCleanupJob), so a worker that dies mid-job simply lets the lease
// expire for another worker (or its own restart) to pick back up.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/alliecatowo/gh-stories/internal/clock"
	"github.com/alliecatowo/gh-stories/internal/media"
	"github.com/alliecatowo/gh-stories/internal/objstore"
	"github.com/alliecatowo/gh-stories/internal/store"
)

// Role selects which loop(s) a Worker process runs. Splitting media
// (CPU/ffmpeg heavy) from cleanup (I/O/DB heavy) across separate deployed
// processes is a capacity-planning choice operators can make later without
// any code change — RoleBoth is what a single small deployment needs.
type Role string

const (
	RoleMedia   Role = "media"
	RoleCleanup Role = "cleanup"
	RoleBoth    Role = "both"
)

// Config bundles every tunable the worker needs beyond the store/objstore/
// clock/logger it is constructed with. Callers wired to internal/config
// should map config.Config's fields onto this explicitly (see
// apps/worker/main.go) rather than this package importing config directly,
// keeping internal/worker testable without the rest of the service.
type Config struct {
	Role Role
	// WorkerID identifies this process as a lease owner. Defaults to
	// hostname-pid if empty, which is enough to tell leases apart in logs
	// and in the database without requiring operators to assign one.
	WorkerID string

	MediaLeaseDuration time.Duration
	MediaPollInterval  time.Duration
	MediaLimits        media.Limits
	// MediaTempDir overrides where per-job scratch directories are created.
	// Empty uses the OS default temp directory.
	MediaTempDir string

	CleanupInterval      time.Duration
	CleanupLeaseDuration time.Duration
	CleanupBatchSize     int

	StoryLifetime time.Duration
	ViewRetention time.Duration

	// Bounded execution for run-to-completion hosts (Cloud Run Jobs, cron
	// containers). MaxJobs caps media jobs per RunOnce (<= 0 means drain
	// until the queue is empty); MaxDuration caps wall time (0 = no cap).
	// Both are ignored by Run.
	MaxJobs     int
	MaxDuration time.Duration
}

func (c *Config) setDefaults() {
	if c.Role == "" {
		c.Role = RoleBoth
	}
	if c.WorkerID == "" {
		host, _ := os.Hostname()
		if host == "" {
			host = "worker"
		}
		c.WorkerID = host + "-" + strconv.Itoa(os.Getpid())
	}
	if c.MediaLeaseDuration <= 0 {
		c.MediaLeaseDuration = 5 * time.Minute
	}
	if c.MediaPollInterval <= 0 {
		c.MediaPollInterval = 2 * time.Second
	}
	if c.MediaLimits == (media.Limits{}) {
		c.MediaLimits = media.DefaultLimits()
	}
	if c.CleanupInterval <= 0 {
		c.CleanupInterval = 30 * time.Second
	}
	if c.CleanupLeaseDuration <= 0 {
		c.CleanupLeaseDuration = 2 * time.Minute
	}
	if c.CleanupBatchSize <= 0 {
		c.CleanupBatchSize = 200
	}
	if c.StoryLifetime <= 0 {
		c.StoryLifetime = 24 * time.Hour
	}
	if c.ViewRetention <= 0 {
		c.ViewRetention = 7 * 24 * time.Hour
	}
}

// Worker owns the media and cleanup loops. Construct with New and run with
// Run, which blocks until ctx is cancelled.
type Worker struct {
	store   *store.Store
	objects objstore.Store
	clock   clock.Clock
	log     *slog.Logger
	cfg     Config
}

// New builds a Worker. clk and log may be nil, defaulting to real time and
// slog.Default() respectively.
func New(st *store.Store, obj objstore.Store, clk clock.Clock, log *slog.Logger, cfg Config) *Worker {
	if clk == nil {
		clk = clock.Real{}
	}
	if log == nil {
		log = slog.Default()
	}
	cfg.setDefaults()
	return &Worker{store: st, objects: obj, clock: clk, log: log, cfg: cfg}
}

// Run starts the configured loop(s) and blocks until ctx is done, then waits
// for whichever loop(s) are running to finish their current unit of work
// (one media job, or one cleanup pass) before returning. It never claims new
// work once ctx is done, which is what makes shutdown graceful: an in-flight
// lease either completes normally or is simply left for ReapExpiredLeases to
// recover, but a newly-started SIGTERM never abandons work mid-claim.
func (w *Worker) Run(ctx context.Context) error {
	var wg sync.WaitGroup

	if w.cfg.Role == RoleMedia || w.cfg.Role == RoleBoth {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.runMediaLoop(ctx)
		}()
	}
	if w.cfg.Role == RoleCleanup || w.cfg.Role == RoleBoth {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.runCleanupLoop(ctx)
		}()
	}

	w.log.Info("worker started", "role", w.cfg.Role, "worker_id", w.cfg.WorkerID)
	wg.Wait()
	w.log.Info("worker stopped", "worker_id", w.cfg.WorkerID)
	return nil
}

// ProcessOneMediaJob claims and fully handles at most one due media job,
// synchronously. It reports false if no job was due. This is what Run's
// media loop calls in a cycle; it is exported separately so tests and
// operational tooling can drive the pipeline deterministically one job at a
// time instead of racing a background poll loop.
func (w *Worker) ProcessOneMediaJob(ctx context.Context) (bool, error) {
	job, err := w.store.ClaimMediaJob(ctx, w.cfg.WorkerID, w.cfg.MediaLeaseDuration)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	w.handleMediaJob(ctx, job)
	return true, nil
}

// RunCleanupOnce runs a single synchronous cleanup pass: the expiry/GC/purge
// sweeps plus draining the delete_objects backlog. Exported for the same
// reason as ProcessOneMediaJob.
func (w *Worker) RunCleanupOnce(ctx context.Context) {
	w.runCleanupPass(ctx)
}

// RunOnce is the run-to-completion mode for scale-to-zero hosts: it does ONE
// cleanup pass (when the role includes cleanup), then claims media jobs
// until the queue is empty, MaxJobs is reached, MaxDuration elapses or ctx is
// cancelled, and returns. An idle run therefore costs one cleanup pass and
// exits immediately. Idempotent and lease-based, so overlapping schedules are
// safe. It never starts new work after the deadline and always finishes a
// claimed job. Returns the number of media jobs processed; a claim error is
// returned so the host records a failed execution.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	var deadline time.Time
	if w.cfg.MaxDuration > 0 {
		deadline = time.Now().Add(w.cfg.MaxDuration)
	}
	if w.cfg.Role == RoleCleanup || w.cfg.Role == RoleBoth {
		w.runCleanupPass(ctx)
	}
	if w.cfg.Role != RoleMedia && w.cfg.Role != RoleBoth {
		return 0, ctx.Err()
	}
	processed := 0
	for w.cfg.MaxJobs <= 0 || processed < w.cfg.MaxJobs {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return processed, nil
		}
		found, err := w.ProcessOneMediaJob(ctx)
		if err != nil {
			return processed, err
		}
		if !found {
			return processed, nil // queue empty: exit instead of polling
		}
		processed++
	}
	return processed, nil
}

// sleep waits for d or until ctx is done, whichever comes first, so a poll
// interval never delays shutdown.
func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
