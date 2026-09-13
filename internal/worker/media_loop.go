package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/media"
	"github.com/alliecatowo/gh-stories/internal/objstore"
	"github.com/alliecatowo/gh-stories/internal/store"
)

// runMediaLoop claims and processes media jobs until ctx is done. It never
// claims a new job after ctx is cancelled, but always finishes an
// already-claimed job (see Worker.Run's doc comment for why).
func (w *Worker) runMediaLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := w.store.ClaimMediaJob(ctx, w.cfg.WorkerID, w.cfg.MediaLeaseDuration)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				w.log.Error("claim media job failed", "error", err)
			}
			sleep(ctx, w.cfg.MediaPollInterval)
			continue
		}

		// Handle with a context independent of the loop's shutdown signal:
		// once a job is claimed, finishing it cleanly (success or a proper
		// failure record) is strictly better than aborting mid-transcode,
		// and the job's own MaxProcessingTime already bounds how long that
		// can take.
		w.handleMediaJob(context.Background(), job)
	}
}

// errDownloadTooLarge is a sentinel the media loop maps to the too_large
// error code: the object storage layer served more bytes than the
// configured original-size limit allows. This is a defense-in-depth check —
// the upload-intent path already enforces the same limit before accepting
// the upload — but the worker never trusts that an object it is about to
// feed to a decoder/ffmpeg is within bounds without checking again itself.
var errDownloadTooLarge = errors.New("worker: downloaded original exceeds the configured size limit")

func (w *Worker) handleMediaJob(ctx context.Context, job *domain.MediaJob) {
	start := w.clock.Now()
	log := w.log.With("job_id", job.ID, "attempt", job.Attempts)
	log.Info("media job claimed")

	storyID, storyIDErr := uuid.Parse(job.Input.StoryItemID)

	tempDir, err := os.MkdirTemp(w.cfg.MediaTempDir, "ghs-worker-*")
	if err != nil {
		w.failMediaJob(ctx, job, storyID, media.CodeInternal, "create temp dir: "+err.Error(), true, log)
		return
	}
	// Always clean up temp files, including on panic: a hostile input that
	// somehow panics a decoder must not also leak worker disk space forever.
	defer func() {
		_ = os.RemoveAll(tempDir)
		if r := recover(); r != nil {
			log.Error("panic handling media job", "panic", r)
			w.failMediaJob(context.Background(), job, storyID, media.CodeInternal,
				fmt.Sprintf("panic: %v", r), true, log)
		}
	}()

	if storyIDErr != nil {
		// This should be unreachable — the story id is written by
		// FinalizeUpload, never by anything this worker trusts less — but a
		// job this worker cannot even address a story for must not be
		// retried into a tight loop.
		w.failMediaJob(ctx, job, uuid.Nil, media.CodeInternal,
			"invalid story_item_id in job input: "+storyIDErr.Error(), false, log)
		return
	}

	originalPath := filepath.Join(tempDir, "original")
	if err := w.downloadOriginal(ctx, job.Input.ObjectKey, originalPath); err != nil {
		if errors.Is(err, errDownloadTooLarge) {
			w.failMediaJob(ctx, job, storyID, media.CodeTooLarge, err.Error(), false, log)
			return
		}
		if errors.Is(err, objstore.ErrNotExist) {
			// The original is gone (e.g. a previous attempt's cleanup raced
			// with this retry). Not the upload's fault, and not something a
			// further retry can fix either.
			w.failMediaJob(ctx, job, storyID, media.CodeInternal, "original object not found: "+err.Error(), false, log)
			return
		}
		w.failMediaJob(ctx, job, storyID, media.CodeInternal, "download original: "+err.Error(), true, log)
		return
	}

	out, err := media.Process(ctx, media.Input{
		Path:         originalPath,
		DeclaredMIME: job.Input.DeclaredMIME,
		Filename:     job.Input.Filename,
	}, media.Options{Limits: w.cfg.MediaLimits, TempDir: tempDir})
	if err != nil {
		out.Cleanup()
		code, retryable := media.CodeInternal, true
		var me *media.Error
		if errors.As(err, &me) {
			code, retryable = me.Code, me.Retryable
		}
		w.failMediaJob(ctx, job, storyID, code, err.Error(), retryable, log)
		return
	}
	defer out.Cleanup()

	variants := make([]domain.MediaVariant, 0, len(out.Variants))
	for _, v := range out.Variants {
		key := objstore.VariantKey(storyID.String(), string(v.Kind), extForMIME(v.MIME))
		if err := w.uploadVariant(ctx, key, v); err != nil {
			// A partially-uploaded variant set is safe to retry: VariantKey
			// is deterministic per (storyID, kind), so a retried attempt
			// overwrites the same keys rather than leaking new ones, and
			// PublishStory never ran, so nothing has been made visible yet.
			w.failMediaJob(ctx, job, storyID, media.CodeInternal, "upload variant "+string(v.Kind)+": "+err.Error(), true, log)
			return
		}
		variants = append(variants, domain.MediaVariant{
			StoryID: storyID, Kind: v.Kind, ObjectKey: key, MIME: v.MIME,
			Width: v.Width, Height: v.Height, DurationMS: v.DurationMS,
			ByteSize: v.ByteSize, Checksum: v.Checksum, HasAudio: v.HasAudio,
		})
	}

	if _, _, err := w.store.PublishStory(ctx, store.PublishResult{
		JobID: job.ID, StoryID: storyID, Variants: variants,
	}, w.cfg.StoryLifetime); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// The story was deleted (or somehow already published) while
			// this job ran. Not a failure of the job itself: PublishStory
			// deliberately refuses to resurrect it, and there is nothing
			// further to do — the uploaded variants are orphaned but
			// harmless, and will be swept up by ordinary story-expiry
			// cleanup for the story if it still exists in any form.
			log.Info("story no longer publishable, dropping job", "story_id", storyID)
			return
		}
		// The variants are durably uploaded but the publish transaction
		// itself failed (a transient DB problem). Leave the lease to expire
		// so ReapExpiredLeases hands this back to a retry, which will
		// re-upload to the same deterministic keys and try to publish again.
		log.Error("publish story failed", "story_id", storyID, "error", err)
		return
	}

	log.Info("media job published", "story_id", storyID, "duration", w.clock.Now().Sub(start), "variants", len(variants))
}

func (w *Worker) failMediaJob(ctx context.Context, job *domain.MediaJob, storyID uuid.UUID,
	code, message string, retryable bool, log *slog.Logger) {
	log.Warn("media job failed", "code", code, "retryable", retryable, "error", message)
	if err := w.store.FailMediaJob(ctx, job.ID, storyID, code, message, retryable); err != nil {
		log.Error("failed to record media job failure", "error", err)
	}
}

// downloadOriginal streams the uploaded original from object storage to a
// worker-generated local path — never the client's own filename — bounded to
// the configured size limit so a mismatched or corrupted object metadata
// record can never make the worker write an unbounded amount of data to
// disk.
func (w *Worker) downloadOriginal(ctx context.Context, key, path string) error {
	obj, err := w.objects.Get(ctx, key, "")
	if err != nil {
		return err
	}
	defer obj.Body.Close()

	limit := w.cfg.MediaLimits.MaxOriginalBytes
	if limit <= 0 {
		limit = media.DefaultLimits().MaxOriginalBytes
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	n, err := io.Copy(f, io.LimitReader(obj.Body, limit+1))
	if err != nil {
		return fmt.Errorf("write original to temp file: %w", err)
	}
	if n > limit {
		return errDownloadTooLarge
	}
	return nil
}

// uploadVariant writes one produced variant to object storage, reading from
// disk for a large (video) variant instead of holding it in memory again.
func (w *Worker) uploadVariant(ctx context.Context, key string, v media.Variant) error {
	if v.TempPath != "" {
		f, err := os.Open(v.TempPath)
		if err != nil {
			return err
		}
		defer f.Close()
		return w.objects.Put(ctx, key, v.MIME, f, v.ByteSize)
	}
	return w.objects.Put(ctx, key, v.MIME, bytes.NewReader(v.Data), int64(len(v.Data)))
}

var mimeExtensions = map[string]string{
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/webp": "webp",
	"video/mp4":  "mp4",
}

// extForMIME picks the file extension used in an output object key. It is
// purely cosmetic (object keys are opaque to every reader in this system,
// which always trusts the stored MIME, not the key) but keeps the bucket
// browsable by a human during operations.
func extForMIME(mime string) string {
	if ext, ok := mimeExtensions[mime]; ok {
		return ext
	}
	return "bin"
}
