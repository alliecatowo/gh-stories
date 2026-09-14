// Command worker runs the GitHub Stories background job processes: the
// media pipeline (claim upload -> validate -> normalize -> publish) and the
// cleanup sweep (expiry, abandoned-upload GC, retention purges, physical
// object deletion). See internal/worker for the loop implementations.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alliecatowo/gh-stories/internal/config"
	"github.com/alliecatowo/gh-stories/internal/db"
	"github.com/alliecatowo/gh-stories/internal/media"
	"github.com/alliecatowo/gh-stories/internal/objstore"
	"github.com/alliecatowo/gh-stories/internal/store"
	"github.com/alliecatowo/gh-stories/internal/version"
	"github.com/alliecatowo/gh-stories/internal/worker"
)

func main() {
	// -version must work without any configuration at all, so that an
	// operator (or a release gate) can identify an image without first
	// supplying a database and object store.
	showVersion := flag.Bool("version", false, "print version and exit")
	once := flag.Bool("once", false, "run a bounded batch (Cloud Run Job) and exit")
	maxJobs := flag.Int("max-jobs", 0, "max media jobs per bounded run (0 = drain once / single pass)")
	maxDuration := flag.Duration("max-duration", 0, "max wall time per bounded run (0 = no limit)")
	flag.Parse()
	if *showVersion {
		fmt.Println("gh-stories worker", version.Short(), version.BuildDate)
		return
	}
	if os.Getenv("GHS_WORKER_ONCE") != "" && !*once {
		*once = strings.EqualFold(os.Getenv("GHS_WORKER_ONCE"), "true")
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		log.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Migrations are opt-in: running them from every worker replica on every
	// restart would race multiple processes against the same schema change.
	// A deployment runs them from exactly one place (or sets this on exactly
	// one replica during a rollout) by setting GHS_WORKER_MIGRATE=true.
	if strings.EqualFold(os.Getenv("GHS_WORKER_MIGRATE"), "true") {
		applied, err := db.MigrateEmbedded(ctx, pool)
		if err != nil {
			log.Error("failed to run migrations", "error", err)
			os.Exit(1)
		}
		log.Info("migrations applied", "versions", applied)
	}

	objects, err := objstore.New(objstore.Config{
		Endpoint:       cfg.S3Endpoint,
		Region:         cfg.S3Region,
		Bucket:         cfg.S3Bucket,
		AccessKeyID:    cfg.S3AccessKeyID,
		SecretKey:      cfg.S3SecretKey,
		ForcePathStyle: cfg.S3ForcePathStyle,
	})
	if err != nil {
		log.Error("failed to build object store client", "error", err)
		os.Exit(1)
	}
	if err := objects.Healthy(ctx); err != nil {
		log.Error("object store is not reachable at startup", "error", err)
		os.Exit(1)
	}

	role := worker.Role(strings.ToLower(strings.TrimSpace(os.Getenv("GHS_WORKER_ROLE"))))
	switch role {
	case worker.RoleMedia, worker.RoleCleanup, worker.RoleBoth:
	case "":
		role = worker.RoleBoth
	default:
		log.Error("invalid GHS_WORKER_ROLE, must be media, cleanup or both", "value", role)
		os.Exit(1)
	}

	limits := media.DefaultLimits()
	limits.MaxOriginalBytes = cfg.MaxUploadBytes
	limits.MaxVideoSeconds = cfg.MaxVideoSeconds
	limits.MaxImagePixels = cfg.MaxImagePixels

	if _, ok := media.FFmpegAvailable(); !ok {
		log.Warn("ffmpeg not found on PATH: video jobs will fail with ffmpeg_unavailable; image jobs are unaffected")
	}

	st := store.New(pool, nil) // nil clock -> clock.Real{}; production never uses a controllable clock
	cfg2 := worker.Config{
		Role:          role,
		StoryLifetime: cfg.StoryLifetime,
		ViewRetention: cfg.ViewRetention,
		MediaLimits:   limits,
	}
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("GHS_WORKER_MAX_JOBS"))); err == nil && n > 0 && *maxJobs == 0 {
		*maxJobs = n
	}
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv("GHS_WORKER_MAX_DURATION"))); err == nil && d > 0 && *maxDuration == 0 {
		*maxDuration = d
	}
	cfg2.MaxJobs = *maxJobs
	cfg2.MaxDuration = *maxDuration
	w := worker.New(st, objects, nil, log, cfg2)

	if *once {
		log.Info("worker bounded run starting", "role", role, "env", cfg.Env,
			"max_jobs", cfg2.MaxJobs, "max_duration", cfg2.MaxDuration.String())
		n, err := w.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error("worker bounded run failed", "error", err)
			os.Exit(1)
		}
		log.Info("worker bounded run finished", "processed", n)
		return
	}
	log.Info("worker starting", "role", role, "env", cfg.Env)
	if err := w.Run(ctx); err != nil {
		log.Error("worker exited with error", "error", err)
		os.Exit(1)
	}

	// Give any last log lines/flushes a moment; Run itself already waited
	// for in-flight work to finish before returning.
	time.Sleep(50 * time.Millisecond)
	log.Info("worker exited cleanly")
}
