package worker_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/media"
	"github.com/alliecatowo/gh-stories/internal/objstore"
	"github.com/alliecatowo/gh-stories/internal/store"
	"github.com/alliecatowo/gh-stories/internal/testdb"
	"github.com/alliecatowo/gh-stories/internal/worker"
)

const authorGitHubID = 9001

// realMinIO returns an objstore.Store for the local MinIO instance started by
// `mise run infra:up`, skipping (not failing) the whole test file when it is
// unreachable — this is an integration suite against real infrastructure,
// matching how internal/objstore's own tests behave.
func realMinIO(t *testing.T) objstore.Store {
	t.Helper()
	st, err := objstore.New(objstore.Config{
		Endpoint: "http://localhost:55900", Region: "us-east-1", Bucket: "stories-media",
		AccessKeyID: "storiesminio", SecretKey: "storiesminio_local_dev", ForcePathStyle: true,
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := st.Healthy(ctx); err != nil {
		t.Skipf("MinIO unreachable (%v); start it with `mise run infra:up`", err)
	}
	return st
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", name))
	require.NoError(t, err, "run `go run tests/fixtures/generate.go` first")
	return data
}

func newWorker(t *testing.T, st *store.Store, objects objstore.Store) *worker.Worker {
	t.Helper()
	limits := media.DefaultLimits()
	limits.MaxProcessingTime = 60 * time.Second
	quietLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	return worker.New(st, objects, nil, quietLog, worker.Config{
		Role:          worker.RoleBoth,
		WorkerID:      "test-worker",
		StoryLifetime: 24 * time.Hour,
		ViewRetention: 7 * 24 * time.Hour,
		MediaLimits:   limits,
	})
}

// uploadAndFinalize simulates the client side of the upload flow: it
// authorizes an upload, writes real bytes to object storage at the
// authorized key (standing in for a presigned PUT), and finalizes — leaving
// exactly one queued media job for the worker to claim, the same state the
// real API handlers would leave it in.
func uploadAndFinalize(t *testing.T, s *store.Store, objects objstore.Store,
	author *domain.User, data []byte, mime, filename string) (storyID, objectKey string) {
	t.Helper()
	ctx := context.Background()

	key := objstore.NewObjectKey("upload", author.ID.String())
	require.NoError(t, objects.Put(ctx, key, mime, bytes.NewReader(data), int64(len(data))))

	ui, err := s.CreateUploadIntent(ctx, author.ID, key, mime, int64(len(data)), 30*time.Minute)
	require.NoError(t, err)

	sum := sha256.Sum256(data)
	id, _, err := s.FinalizeUpload(ctx, author.ID, ui.ID, int64(len(data)), hex.EncodeToString(sum[:]),
		store.StoryDraft{
			Caption: "worker e2e test", Visibility: domain.VisibilityPublic,
			AllowReplies: true, AllowReactions: true,
		}, filename)
	require.NoError(t, err)
	return id.String(), key
}

func TestMediaLoopPublishesStoryWithVariants(t *testing.T) {
	s, _ := testdb.New(t)
	objects := realMinIO(t)
	author := testdb.Account(t, s, authorGitHubID, "worker-e2e-author")
	w := newWorker(t, s, objects)

	data := fixture(t, "landscape.png")
	storyIDStr, originalKey := uploadAndFinalize(t, s, objects, author, data, "image/png", "landscape.png")
	storyID, err := store.ParseUUID(storyIDStr)
	require.NoError(t, err)

	ctx := context.Background()
	found, err := w.ProcessOneMediaJob(ctx)
	require.NoError(t, err)
	require.True(t, found, "expected exactly one queued media job")

	item, err := s.OwnStory(ctx, author.ID, storyID)
	require.NoError(t, err)
	require.Equal(t, domain.StoryPublished, item.State)
	require.NotNil(t, item.PublishedAt)
	require.NotNil(t, item.ExpiresAt)
	require.Equal(t, 24*time.Hour, item.ExpiresAt.Sub(*item.PublishedAt))

	kinds := map[domain.VariantKind]domain.MediaVariant{}
	for _, v := range item.Variants {
		kinds[v.Kind] = v
	}
	require.Contains(t, kinds, domain.VariantImage)
	require.Contains(t, kinds, domain.VariantThumb)
	require.Contains(t, kinds, domain.VariantTerminal)
	require.NotContains(t, kinds, domain.VariantVideo, "a static PNG must not produce a video variant")

	// Every recorded variant object must actually exist in object storage,
	// with the byte size the worker recorded.
	for kind, v := range kinds {
		head, err := objects.Head(ctx, v.ObjectKey)
		require.NoErrorf(t, err, "variant %s object missing from storage", kind)
		require.Equal(t, v.ByteSize, head.ContentLength)
	}

	// PublishStory schedules the original for deletion; running one cleanup
	// pass should physically remove it.
	w.RunCleanupOnce(ctx)
	_, err = objects.Head(ctx, originalKey)
	require.ErrorIs(t, err, objstore.ErrNotExist, "cleanup must have deleted the original after publish")
}

func TestMediaLoopFailsStoryOnCorruptUpload(t *testing.T) {
	s, _ := testdb.New(t)
	objects := realMinIO(t)
	author := testdb.Account(t, s, authorGitHubID+1, "worker-e2e-corrupt")
	w := newWorker(t, s, objects)

	data := fixture(t, "truncated.jpg")
	storyIDStr, _ := uploadAndFinalize(t, s, objects, author, data, "image/jpeg", "truncated.jpg")
	storyID, err := store.ParseUUID(storyIDStr)
	require.NoError(t, err)

	ctx := context.Background()
	found, err := w.ProcessOneMediaJob(ctx)
	require.NoError(t, err)
	require.True(t, found)

	item, err := s.OwnStory(ctx, author.ID, storyID)
	require.NoError(t, err)
	require.Equal(t, domain.StoryFailed, item.State)
	require.Equal(t, media.CodeCorruptMedia, item.FailureCode)
	require.Empty(t, item.Variants)
}

func TestMediaLoopFailsStoryOnMIMEMismatch(t *testing.T) {
	s, _ := testdb.New(t)
	objects := realMinIO(t)
	author := testdb.Account(t, s, authorGitHubID+2, "worker-e2e-mismatch")
	w := newWorker(t, s, objects)

	// fake.png is real JPEG bytes; declaring it as image/png must be
	// rejected as mime_mismatch (a non-retryable, hostile-input failure).
	data := fixture(t, "fake.png")
	storyIDStr, _ := uploadAndFinalize(t, s, objects, author, data, "image/png", "fake.png")
	storyID, err := store.ParseUUID(storyIDStr)
	require.NoError(t, err)

	ctx := context.Background()
	found, err := w.ProcessOneMediaJob(ctx)
	require.NoError(t, err)
	require.True(t, found)

	item, err := s.OwnStory(ctx, author.ID, storyID)
	require.NoError(t, err)
	require.Equal(t, domain.StoryFailed, item.State)
	require.Equal(t, media.CodeMIMEMismatch, item.FailureCode)
}

func TestCleanupDeleteObjectsJobRemovesKeys(t *testing.T) {
	s, clk := testdb.New(t)
	objects := realMinIO(t)
	w := newWorker(t, s, objects)
	ctx := context.Background()

	key1 := "test/cleanup/" + t.Name() + "/a"
	key2 := "test/cleanup/" + t.Name() + "/b"
	require.NoError(t, objects.Put(ctx, key1, "text/plain", bytes.NewReader([]byte("a")), 1))
	require.NoError(t, objects.Put(ctx, key2, "text/plain", bytes.NewReader([]byte("b")), 1))

	require.NoError(t, s.EnqueueCleanup(ctx, "delete_objects",
		// run_after must come from the injected clock, not wall-clock time:
		// the worker claims jobs using the store's clock, so a wall-clock
		// timestamp would schedule this job months away from "now".
		map[string]any{"object_keys": []string{key1, key2, "already/gone/key"}},
		clk.Now().Add(-time.Second)))

	w.RunCleanupOnce(ctx)

	_, err := objects.Head(ctx, key1)
	require.ErrorIs(t, err, objstore.ErrNotExist)
	_, err = objects.Head(ctx, key2)
	require.ErrorIs(t, err, objstore.ErrNotExist)
}

func TestMediaLoopIsIdleWithNoQueuedJobs(t *testing.T) {
	s, _ := testdb.New(t)
	objects := realMinIO(t)
	w := newWorker(t, s, objects)

	found, err := w.ProcessOneMediaJob(context.Background())
	require.NoError(t, err)
	require.False(t, found)
}
