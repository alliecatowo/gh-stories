package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/alliecatowo/gh-stories/internal/store"
)

// runCleanupLoop periodically sweeps expired/abandoned state and drains the
// delete_objects backlog until ctx is done. Every step here is independently
// idempotent, so a crash between any two steps just means the next tick
// redoes (harmlessly) whatever didn't finish.
func (w *Worker) runCleanupLoop(ctx context.Context) {
	w.runCleanupPass(ctx)
	ticker := time.NewTicker(w.cfg.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runCleanupPass(ctx)
		}
	}
}

func (w *Worker) runCleanupPass(ctx context.Context) {
	log := w.log

	if err := w.store.ReapExpiredLeases(ctx); err != nil {
		log.Error("reap expired leases failed", "error", err)
	}
	if n, err := w.store.ExpireStories(ctx, w.cfg.CleanupBatchSize); err != nil {
		log.Error("expire stories failed", "error", err)
	} else if n > 0 {
		log.Info("expired stories", "count", n)
	}
	if n, err := w.store.GCAbandonedUploads(ctx, w.cfg.CleanupBatchSize); err != nil {
		log.Error("gc abandoned uploads failed", "error", err)
	} else if n > 0 {
		log.Info("gc'd abandoned uploads", "count", n)
	}
	if n, err := w.store.PurgeReplies(ctx); err != nil {
		log.Error("purge replies failed", "error", err)
	} else if n > 0 {
		log.Info("purged replies", "count", n)
	}
	if n, err := w.store.PurgeViews(ctx, w.cfg.ViewRetention); err != nil {
		log.Error("purge views failed", "error", err)
	} else if n > 0 {
		log.Info("purged views", "count", n)
	}
	if err := w.store.PurgeExpiredAuth(ctx); err != nil {
		log.Error("purge expired auth failed", "error", err)
	}

	w.drainCleanupJobs(ctx, log)
}

// drainCleanupJobs claims and completes cleanup_jobs (currently just
// "delete_objects") until none are left due or ctx is done. Draining the
// whole backlog in one pass, rather than one job per tick, is what keeps
// physical object deletion within the product's 15-minute target under
// healthy conditions even if the backlog briefly grows past one tick's worth
// of work.
func (w *Worker) drainCleanupJobs(ctx context.Context, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		job, err := w.store.ClaimCleanupJob(ctx, w.cfg.WorkerID, w.cfg.CleanupLeaseDuration)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				log.Error("claim cleanup job failed", "error", err)
			}
			return
		}
		w.handleCleanupJob(ctx, job, log)
	}
}

type deleteObjectsPayload struct {
	ObjectKeys []string `json:"object_keys"`
}

func (w *Worker) handleCleanupJob(ctx context.Context, job *store.CleanupJob, log *slog.Logger) {
	log = log.With("cleanup_job_id", job.ID, "kind", job.Kind, "attempt", job.Attempts)

	switch job.Kind {
	case "delete_objects":
		var payload deleteObjectsPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			log.Error("unparseable delete_objects payload", "error", err)
			w.failCleanupJob(ctx, job, "unparseable payload: "+err.Error(), log)
			return
		}
		// Delete is idempotent (a key that is already gone is a success),
		// which is exactly what a leased-and-retried job needs: a retry
		// after a partial prior failure never fails on the keys that were
		// already removed.
		if err := w.objects.Delete(ctx, payload.ObjectKeys...); err != nil {
			log.Error("delete objects failed", "error", err, "key_count", len(payload.ObjectKeys))
			w.failCleanupJob(ctx, job, err.Error(), log)
			return
		}
		if err := w.store.CompleteCleanupJob(ctx, job.ID); err != nil {
			log.Error("complete cleanup job failed", "error", err)
			return
		}
		log.Info("deleted objects", "key_count", len(payload.ObjectKeys))
	default:
		log.Warn("unknown cleanup job kind")
		w.failCleanupJob(ctx, job, "unknown cleanup job kind: "+job.Kind, log)
	}
}

func (w *Worker) failCleanupJob(ctx context.Context, job *store.CleanupJob, msg string, log *slog.Logger) {
	if err := w.store.FailCleanupJob(ctx, job.ID, job.Attempts, job.Max, msg); err != nil {
		log.Error("failed to record cleanup job failure", "error", err)
	}
}
