package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/clock"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/store"
	"github.com/alliecatowo/gh-stories/internal/testdb"
)

func trioWithClock(t *testing.T) (*store.Store, *clock.Controllable, *domain.User, *domain.User) {
	t.Helper()
	s, clk := testdb.New(t)
	return s, clk, testdb.Account(t, s, aliceID, "alice"), testdb.Account(t, s, bobID, "bob")
}

// TestExpiryBoundaryIsExclusive: the item works just before expiry and fails at
// and after the boundary. Crossing 24 hours takes microseconds, not a day.
func TestExpiryBoundaryIsExclusive(t *testing.T) {
	ctx := context.Background()
	s, clk, alice, bob := trioWithClock(t)
	story := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "cat")

	item, err := s.StoryForViewer(ctx, bob.ID, bob.GitHubID, story)
	require.NoError(t, err)
	require.NotNil(t, item.PublishedAt)
	require.NotNil(t, item.ExpiresAt)
	require.Equal(t, 24*time.Hour, item.ExpiresAt.Sub(*item.PublishedAt),
		"expires_at must be exactly published_at + 24h")

	// One microsecond before the boundary: still visible everywhere.
	clk.Advance(24*time.Hour - time.Microsecond)
	require.True(t, canView(t, s, bob, story), "visible immediately before expiry")

	statuses, err := s.RingStatuses(ctx, bob.ID, bob.GitHubID, []domain.GitHubID{alice.GitHubID})
	require.NoError(t, err)
	require.True(t, statuses[0].HasActive)

	groups, _, err := s.Feed(ctx, bob.ID, bob.GitHubID, false, 25)
	require.NoError(t, err)
	require.Len(t, groups, 1)

	// At exactly expires_at the item is expired: the boundary is exclusive.
	clk.Advance(time.Microsecond)
	require.False(t, canView(t, s, bob, story), "expired at exactly expires_at")

	statuses, err = s.RingStatuses(ctx, bob.ID, bob.GitHubID, []domain.GitHubID{alice.GitHubID})
	require.NoError(t, err)
	require.False(t, statuses[0].HasActive, "ring status must expire with the item")

	groups, _, err = s.Feed(ctx, bob.ID, bob.GitHubID, false, 25)
	require.NoError(t, err)
	require.Empty(t, groups, "feed must expire with the item")

	_, err = s.AuthorSequence(ctx, bob.ID, bob.GitHubID, "alice")
	require.ErrorIs(t, err, store.ErrNotFound)
}

// TestIndependentClocks: each uploaded item is its own expiring Story.
// Posting another item must never extend an older one.
func TestIndependentClocks(t *testing.T) {
	ctx := context.Background()
	s, clk, alice, bob := trioWithClock(t)

	first := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "first")
	firstItem, err := s.StoryForViewer(ctx, bob.ID, bob.GitHubID, first)
	require.NoError(t, err)
	firstExpiry := *firstItem.ExpiresAt

	// Six hours later Alice posts again.
	clk.Advance(6 * time.Hour)
	second := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "second")

	firstItem, err = s.StoryForViewer(ctx, bob.ID, bob.GitHubID, first)
	require.NoError(t, err)
	require.Equal(t, firstExpiry, *firstItem.ExpiresAt,
		"posting another item must not extend the first item's expiry")

	// After the first item's own 24 hours, only the second survives.
	clk.Advance(18 * time.Hour)
	require.False(t, canView(t, s, bob, first))
	require.True(t, canView(t, s, bob, second))

	group, err := s.AuthorSequence(ctx, bob.ID, bob.GitHubID, "alice")
	require.NoError(t, err)
	require.Len(t, group.Items, 1, "only the still-live item appears in the sequence")
	require.Equal(t, second, group.Items[0].ID)
}

// TestProcessingTimeDoesNotConsumeTheWindow: published_at is set when the
// normalized media becomes ready, not when the upload started.
func TestProcessingTimeDoesNotConsumeTheWindow(t *testing.T) {
	ctx := context.Background()
	s, clk, alice, _ := trioWithClock(t)

	ui, err := s.CreateUploadIntent(ctx, alice.ID, "u/k1", "image/jpeg", 10, 30*time.Minute)
	require.NoError(t, err)
	storyID, _, err := s.FinalizeUpload(ctx, alice.ID, ui.ID, 10, "ab", store.StoryDraft{
		Visibility: domain.VisibilityPublic, AllowReplies: true, AllowReactions: true,
	}, "x.jpg")
	require.NoError(t, err)

	// A slow transcode: ninety minutes pass before the worker publishes.
	clk.Advance(90 * time.Minute)
	job, err := s.ClaimMediaJob(ctx, "w1", time.Minute)
	require.NoError(t, err)
	published, expires, err := s.PublishStory(ctx, store.PublishResult{
		JobID: job.ID, StoryID: storyID,
		Variants: []domain.MediaVariant{{Kind: domain.VariantImage, ObjectKey: "m/1", MIME: "image/jpeg"}},
	}, 24*time.Hour)
	require.NoError(t, err)
	require.Equal(t, 24*time.Hour, expires.Sub(published),
		"the 24-hour window starts when the item becomes visible, not when it was uploaded")
}

// TestExpiredItemStaysDeadAcrossRestart: expiry is enforced on every read,
// independently of the background cleanup job ever running.
func TestExpiredItemStaysDeadWithoutCleanup(t *testing.T) {
	ctx := context.Background()
	s, clk, alice, bob := trioWithClock(t)
	story := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "")

	clk.Advance(25 * time.Hour)
	require.False(t, canView(t, s, bob, story),
		"no cleanup job has run; the read path alone must refuse it")

	// Now run cleanup, as the worker eventually would.
	n, err := s.ExpireStories(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	// And it is still refused afterwards — cleanup cannot resurrect it either.
	require.False(t, canView(t, s, bob, story))

	job, err := s.ClaimCleanupJob(ctx, "w1", time.Minute)
	require.NoError(t, err)
	require.Equal(t, "delete_objects", job.Kind,
		"expiry must schedule physical object removal")
}

// TestDeletionSchedulesPhysicalCleanup covers the delete-then-revoke path.
func TestDeletionSchedulesPhysicalCleanup(t *testing.T) {
	ctx := context.Background()
	s, _, alice, bob := trioWithClock(t)
	story := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "")
	require.True(t, canView(t, s, bob, story))

	require.NoError(t, s.DeleteStory(ctx, alice.ID, story, false, ""))
	require.False(t, canView(t, s, bob, story), "deletion removes server access immediately")

	// Publication already scheduled removal of the original upload, so drain
	// the queue and assert the derivatives are in there too.
	var scheduled string
	for {
		job, err := s.ClaimCleanupJob(ctx, "w1", time.Minute)
		if err != nil {
			break
		}
		require.Equal(t, "delete_objects", job.Kind)
		scheduled += string(job.Payload)
	}
	require.Contains(t, scheduled, "thumb.jpg",
		"derivatives must be scheduled for removal, not just the canonical file")
	require.Contains(t, scheduled, "image.jpg")
}

// TestAbandonedUploadsAreCollected proves the pipeline's garbage collection.
func TestAbandonedUploadsAreCollected(t *testing.T) {
	ctx := context.Background()
	s, clk, alice, _ := trioWithClock(t)
	_, err := s.CreateUploadIntent(ctx, alice.ID, "u/abandoned", "image/jpeg", 10, 30*time.Minute)
	require.NoError(t, err)

	n, err := s.GCAbandonedUploads(ctx, 100)
	require.NoError(t, err)
	require.Zero(t, n, "a live upload authorization must not be collected")

	clk.Advance(31 * time.Minute)
	n, err = s.GCAbandonedUploads(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	job, err := s.ClaimCleanupJob(ctx, "w1", time.Minute)
	require.NoError(t, err)
	require.Contains(t, string(job.Payload), "u/abandoned")
}

// TestFinalizeIsIdempotent: an interrupted post retried must not duplicate.
func TestFinalizeIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s, _, alice, _ := trioWithClock(t)
	ui, err := s.CreateUploadIntent(ctx, alice.ID, "u/k2", "image/jpeg", 10, 30*time.Minute)
	require.NoError(t, err)
	draft := store.StoryDraft{Visibility: domain.VisibilityPublic}

	first, created, err := s.FinalizeUpload(ctx, alice.ID, ui.ID, 10, "ab", draft, "x.jpg")
	require.NoError(t, err)
	require.True(t, created)

	second, created, err := s.FinalizeUpload(ctx, alice.ID, ui.ID, 10, "ab", draft, "x.jpg")
	require.NoError(t, err)
	require.False(t, created, "a replayed finalize must not create a second Story")
	require.Equal(t, first, second)
}

// TestFinalizeRejectsOtherPeoplesUpload: ownership failure is indistinguishable
// from the upload not existing.
func TestFinalizeRejectsForeignUpload(t *testing.T) {
	ctx := context.Background()
	s, _, alice, bob := trioWithClock(t)
	ui, err := s.CreateUploadIntent(ctx, alice.ID, "u/k3", "image/jpeg", 10, 30*time.Minute)
	require.NoError(t, err)

	_, _, err = s.FinalizeUpload(ctx, bob.ID, ui.ID, 10, "ab", store.StoryDraft{}, "x.jpg")
	require.ErrorIs(t, err, store.ErrNotFound)
}

// TestWorkerCannotResurrectDeletedStory: a Story deleted while processing must
// not be published when the worker finishes.
func TestWorkerCannotPublishDeletedStory(t *testing.T) {
	ctx := context.Background()
	s, _, alice, _ := trioWithClock(t)
	ui, err := s.CreateUploadIntent(ctx, alice.ID, "u/k4", "image/jpeg", 10, 30*time.Minute)
	require.NoError(t, err)
	storyID, _, err := s.FinalizeUpload(ctx, alice.ID, ui.ID, 10, "ab",
		store.StoryDraft{Visibility: domain.VisibilityPublic}, "x.jpg")
	require.NoError(t, err)

	require.NoError(t, s.DeleteStory(ctx, alice.ID, storyID, false, ""))

	job, err := s.ClaimMediaJob(ctx, "w1", time.Minute)
	require.NoError(t, err)
	_, _, err = s.PublishStory(ctx, store.PublishResult{
		JobID: job.ID, StoryID: storyID,
		Variants: []domain.MediaVariant{{Kind: domain.VariantImage, ObjectKey: "m/x", MIME: "image/jpeg"}},
	}, 24*time.Hour)
	require.ErrorIs(t, err, store.ErrNotFound,
		"a worker finishing after a deletion must not publish the item")
}

// TestLeaseRecoveryAfterWorkerDeath proves a crashed worker's job is reclaimed.
func TestLeaseRecoveryAfterWorkerDeath(t *testing.T) {
	ctx := context.Background()
	s, clk, alice, _ := trioWithClock(t)
	ui, err := s.CreateUploadIntent(ctx, alice.ID, "u/k5", "image/jpeg", 10, 30*time.Minute)
	require.NoError(t, err)
	_, _, err = s.FinalizeUpload(ctx, alice.ID, ui.ID, 10, "ab", store.StoryDraft{
		Visibility: domain.VisibilityPublic}, "x.jpg")
	require.NoError(t, err)

	first, err := s.ClaimMediaJob(ctx, "worker-that-dies", 30*time.Second)
	require.NoError(t, err)

	// Another worker must not steal a live lease.
	_, err = s.ClaimMediaJob(ctx, "worker-2", 30*time.Second)
	require.ErrorIs(t, err, store.ErrNotFound)

	// After the lease expires it becomes claimable again.
	clk.Advance(31 * time.Second)
	second, err := s.ClaimMediaJob(ctx, "worker-2", 30*time.Second)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, 2, second.Attempts)
}

// TestFeedOrdering pins the specified deterministic order.
func TestFeedOrdering(t *testing.T) {
	ctx := context.Background()
	s, clk, alice, bob := trioWithClock(t)
	carol := testdb.Account(t, s, carolID, "carol")
	dave := testdb.Account(t, s, 1004, "dave")

	// Bob natively follows carol and dave; alice is import-only.
	require.NoError(t, s.Follow(ctx, nil, bob.ID, carol.GitHubID, domain.ProvenanceNative))
	require.NoError(t, s.Follow(ctx, nil, bob.ID, dave.GitHubID, domain.ProvenanceNative))
	require.NoError(t, s.Follow(ctx, nil, bob.ID, alice.GitHubID, domain.ProvenanceGitHubImport))

	aliceStory := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "alice")
	clk.Advance(time.Minute)
	testdb.PublishStory(t, s, dave, domain.VisibilityPublic, nil, "dave")
	clk.Advance(time.Minute)
	testdb.PublishStory(t, s, carol, domain.VisibilityPublic, nil, "carol")

	groups, _, err := s.Feed(ctx, bob.ID, bob.GitHubID, false, 25)
	require.NoError(t, err)
	require.Len(t, groups, 3)
	// All unseen, so native-followed authors come before the import-only one,
	// most recent first among equals.
	require.Equal(t, "carol", groups[0].Author.Login)
	require.Equal(t, "dave", groups[1].Author.Login)
	require.Equal(t, "alice", groups[2].Author.Login)

	// Once alice's item is seen, alice drops below the unseen authors anyway,
	// and carol/dave keep their native-before-import precedence.
	require.NoError(t, s.RecordView(ctx, aliceStory, bob.ID))
	groups, _, err = s.Feed(ctx, bob.ID, bob.GitHubID, false, 25)
	require.NoError(t, err)
	require.Equal(t, "alice", groups[2].Author.Login)
	require.False(t, groups[2].HasUnseen)
	require.True(t, groups[0].HasUnseen)
}

// TestFeedDoesNotMarkViews: feed and status reads never record a view.
func TestFeedDoesNotRecordViews(t *testing.T) {
	ctx := context.Background()
	s, _, alice, bob := trioWithClock(t)
	story := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "")

	_, _, err := s.Feed(ctx, bob.ID, bob.GitHubID, false, 25)
	require.NoError(t, err)
	_, err = s.RingStatuses(ctx, bob.ID, bob.GitHubID, []domain.GitHubID{alice.GitHubID})
	require.NoError(t, err)
	_, err = s.StoryForViewer(ctx, bob.ID, bob.GitHubID, story)
	require.NoError(t, err)

	views, _, err := s.Counts(ctx, story)
	require.NoError(t, err)
	require.Zero(t, views, "fetching the feed or metadata must not mark a view")

	// Delivering the media does.
	require.NoError(t, s.RecordView(ctx, story, bob.ID))
	require.NoError(t, s.RecordView(ctx, story, bob.ID))
	views, _, err = s.Counts(ctx, story)
	require.NoError(t, err)
	require.Equal(t, 1, views, "repeated delivery to the same viewer is one view")
}
