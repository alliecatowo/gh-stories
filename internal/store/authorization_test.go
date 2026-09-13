package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/store"
	"github.com/alliecatowo/gh-stories/internal/testdb"
)

// Controlled identities used throughout, matching the acceptance matrix:
// A is the author, B is the eligible viewer, C is the ineligible third party.
const (
	aliceID = 1001
	bobID   = 1002
	carolID = 1003
)

func newTrio(t *testing.T) (*store.Store, *domain.User, *domain.User, *domain.User) {
	t.Helper()
	s, _ := testdb.New(t)
	return s,
		testdb.Account(t, s, aliceID, "alice"),
		testdb.Account(t, s, bobID, "bob"),
		testdb.Account(t, s, carolID, "carol")
}

func canView(t *testing.T, s *store.Store, viewer *domain.User, storyID interface{ String() string }) bool {
	t.Helper()
	id, err := store.ParseUUID(storyID.String())
	require.NoError(t, err)
	_, err = s.StoryForViewer(context.Background(), viewer.ID, viewer.GitHubID, id)
	if err == nil {
		return true
	}
	require.ErrorIs(t, err, store.ErrNotFound, "authorization failures must be indistinguishable from absence")
	return false
}

// TestDefaultAudienceDirection is the rule most easily got backwards:
// "People I follow" means the people the AUTHOR follows. B following A is not
// enough; A must follow B.
func TestDefaultAudienceDirection(t *testing.T) {
	s, alice, bob, _ := newTrio(t)
	ctx := context.Background()

	// Bob follows Alice. That alone must NOT grant Bob access.
	require.NoError(t, s.Follow(ctx, nil, bob.ID, alice.GitHubID, domain.ProvenanceNative))
	story := testdb.PublishStory(t, s, alice, domain.VisibilityFollowersOfAuthor, nil, "concert")
	require.False(t, canView(t, s, bob, story),
		"B following A must not grant access to A's 'People I follow' item")

	// Now Alice follows Bob. Access is granted, evaluated against the current graph.
	require.NoError(t, s.Follow(ctx, nil, alice.ID, bob.GitHubID, domain.ProvenanceNative))
	require.True(t, canView(t, s, bob, story))

	// Alice unfollows Bob: access is withdrawn immediately, same item.
	require.NoError(t, s.Unfollow(ctx, alice.ID, bob.GitHubID))
	require.False(t, canView(t, s, bob, story))
}

func TestEveryAudience(t *testing.T) {
	ctx := context.Background()
	t.Run("my followers", func(t *testing.T) {
		s, alice, bob, carol := newTrio(t)
		require.NoError(t, s.Follow(ctx, nil, bob.ID, alice.GitHubID, domain.ProvenanceNative))
		story := testdb.PublishStory(t, s, alice, domain.VisibilityAuthorFollows, nil, "")
		require.True(t, canView(t, s, bob, story), "a follower of the author may view")
		require.False(t, canView(t, s, carol, story))
	})

	t.Run("mutuals", func(t *testing.T) {
		s, alice, bob, carol := newTrio(t)
		require.NoError(t, s.Follow(ctx, nil, bob.ID, alice.GitHubID, domain.ProvenanceNative))
		story := testdb.PublishStory(t, s, alice, domain.VisibilityMutuals, nil, "")
		require.False(t, canView(t, s, bob, story), "one direction is not a mutual")
		require.NoError(t, s.Follow(ctx, nil, alice.ID, bob.GitHubID, domain.ProvenanceNative))
		require.True(t, canView(t, s, bob, story))
		require.False(t, canView(t, s, carol, story))
	})

	t.Run("custom list", func(t *testing.T) {
		s, alice, bob, carol := newTrio(t)
		list, err := s.CreateAudienceList(ctx, alice.ID, "close friends", []string{"bob"})
		require.NoError(t, err)
		require.Len(t, list.Members, 1)
		story := testdb.PublishStory(t, s, alice, domain.VisibilityCustomList, &list.ID, "")
		require.True(t, canView(t, s, bob, story))
		require.False(t, canView(t, s, carol, story))

		// Removing Bob from the list revokes him on the next request.
		_, err = s.UpdateAudienceList(ctx, alice.ID, list.ID, nil, []string{"carol"}, true)
		require.NoError(t, err)
		require.False(t, canView(t, s, bob, story))
		require.True(t, canView(t, s, carol, story))
	})

	t.Run("public still requires identity", func(t *testing.T) {
		s, alice, _, carol := newTrio(t)
		story := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "")
		require.True(t, canView(t, s, carol, story), "any signed-in user may view a public item")
	})

	t.Run("owner always sees their own live item", func(t *testing.T) {
		s, alice, _, _ := newTrio(t)
		// Nobody else is eligible for either of these, but the owner is.
		story := testdb.PublishStory(t, s, alice, domain.VisibilityMutuals, nil, "")
		require.True(t, canView(t, s, alice, story))
	})
}

// TestImportedFollowGrantsAccess proves imported relationships behave exactly
// like native ones for authorization, differing only in provenance.
func TestImportedFollowGrantsAccess(t *testing.T) {
	ctx := context.Background()
	s, alice, bob, _ := newTrio(t)
	_, err := s.ImportFollows(ctx, alice.ID, []domain.Identity{{
		GitHubID: bob.GitHubID, Login: "bob", AvatarURL: "a", ProfileURL: "p",
	}}, false)
	require.NoError(t, err)

	story := testdb.PublishStory(t, s, alice, domain.VisibilityFollowersOfAuthor, nil, "")
	require.True(t, canView(t, s, bob, story))

	prov, ok, err := s.RelationshipProvenance(ctx, alice.ID, bob.GitHubID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, domain.ProvenanceGitHubImport, prov)
}

// TestUnfollowTombstoneSurvivesReimport is the rule that stops an import from
// silently resurrecting a relationship the user deliberately removed.
func TestUnfollowTombstoneSurvivesReimport(t *testing.T) {
	ctx := context.Background()
	s, alice, bob, _ := newTrio(t)
	bobIdentity := domain.Identity{GitHubID: bob.GitHubID, Login: "bob"}

	_, err := s.ImportFollows(ctx, alice.ID, []domain.Identity{bobIdentity}, false)
	require.NoError(t, err)
	require.NoError(t, s.Unfollow(ctx, alice.ID, bob.GitHubID))

	changes, err := s.ImportFollows(ctx, alice.ID, []domain.Identity{bobIdentity}, false)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, "skipped_unfollowed", changes[0].Action)

	follows, err := s.Follows(ctx, nil, alice.ID, bob.GitHubID)
	require.NoError(t, err)
	require.False(t, follows, "a re-import must not resurrect an explicit unfollow")
}

// TestNativeFollowUpgradesProvenance checks the deterministic upgrade.
func TestNativeFollowUpgradesProvenance(t *testing.T) {
	ctx := context.Background()
	s, alice, bob, _ := newTrio(t)
	require.NoError(t, s.Follow(ctx, nil, alice.ID, bob.GitHubID, domain.ProvenanceGitHubImport))
	require.NoError(t, s.Follow(ctx, nil, alice.ID, bob.GitHubID, domain.ProvenanceNative))
	prov, _, err := s.RelationshipProvenance(ctx, alice.ID, bob.GitHubID)
	require.NoError(t, err)
	require.Equal(t, domain.ProvenanceNative, prov)

	// An import must never downgrade a native follow.
	require.NoError(t, s.Follow(ctx, nil, alice.ID, bob.GitHubID, domain.ProvenanceGitHubImport))
	prov, _, err = s.RelationshipProvenance(ctx, alice.ID, bob.GitHubID)
	require.NoError(t, err)
	require.Equal(t, domain.ProvenanceNative, prov)
}

// TestPreviewLeavesNothingBehind covers the "later manual re-import previews
// its changes" requirement.
func TestImportPreviewAppliesNothing(t *testing.T) {
	ctx := context.Background()
	s, alice, bob, _ := newTrio(t)
	changes, err := s.ImportFollows(ctx, alice.ID,
		[]domain.Identity{{GitHubID: bob.GitHubID, Login: "bob"}}, true)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, "added", changes[0].Action)

	follows, err := s.Follows(ctx, nil, alice.ID, bob.GitHubID)
	require.NoError(t, err)
	require.False(t, follows, "a preview must not apply the change")
}

// TestBlockOverridesAudience: a block in EITHER direction wins over any
// audience, including public.
func TestBlockOverridesAudience(t *testing.T) {
	ctx := context.Background()
	t.Run("author blocks viewer", func(t *testing.T) {
		s, alice, bob, _ := newTrio(t)
		story := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "")
		require.True(t, canView(t, s, bob, story))
		require.NoError(t, s.SetBlock(ctx, alice.ID, bob.GitHubID, true))
		require.False(t, canView(t, s, bob, story))
	})
	t.Run("viewer blocks author", func(t *testing.T) {
		s, alice, bob, _ := newTrio(t)
		story := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "")
		require.NoError(t, s.SetBlock(ctx, bob.ID, alice.GitHubID, true))
		require.False(t, canView(t, s, bob, story))
	})
}

func TestHideRuleOverridesAudience(t *testing.T) {
	ctx := context.Background()
	s, alice, bob, _ := newTrio(t)
	story := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "")
	require.True(t, canView(t, s, bob, story))
	require.NoError(t, s.SetHide(ctx, alice.ID, bob.GitHubID, true))
	require.False(t, canView(t, s, bob, story))
	require.NoError(t, s.SetHide(ctx, alice.ID, bob.GitHubID, false))
	require.True(t, canView(t, s, bob, story))
}

// TestMuteDoesNotChangeAccess: mute is presentation, not permission.
func TestMuteDoesNotChangeAccess(t *testing.T) {
	ctx := context.Background()
	s, alice, bob, _ := newTrio(t)
	story := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "")
	require.NoError(t, s.SetMute(ctx, bob.ID, alice.GitHubID, true))

	require.True(t, canView(t, s, bob, story), "mute must not revoke access")

	groups, _, err := s.Feed(ctx, bob.ID, bob.GitHubID, false, 25)
	require.NoError(t, err)
	require.Empty(t, groups, "a muted author is omitted from the default feed")

	groups, _, err = s.Feed(ctx, bob.ID, bob.GitHubID, true, 25)
	require.NoError(t, err)
	require.Len(t, groups, 1, "a muted author can still be opened explicitly")
	require.True(t, groups[0].Muted)
}

func TestSuspensionAndDeletionOverrideAudience(t *testing.T) {
	ctx := context.Background()
	t.Run("suspended author", func(t *testing.T) {
		s, alice, bob, _ := newTrio(t)
		story := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "")
		require.NoError(t, s.SuspendUser(ctx, nil, alice.ID, "spam"))
		require.False(t, canView(t, s, bob, story))
	})
	t.Run("deleted item", func(t *testing.T) {
		s, alice, bob, _ := newTrio(t)
		story := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "")
		require.NoError(t, s.DeleteStory(ctx, alice.ID, story, false, ""))
		require.False(t, canView(t, s, bob, story))
		require.False(t, canView(t, s, alice, story), "even the owner loses a deleted item")
	})
	t.Run("moderator removal", func(t *testing.T) {
		s, alice, bob, _ := newTrio(t)
		story := testdb.PublishStory(t, s, alice, domain.VisibilityPublic, nil, "")
		require.NoError(t, s.DeleteStory(ctx, bob.ID, story, true, "policy"))
		require.False(t, canView(t, s, alice, story))
	})
}

// TestRingStatusIsNotAnOracle: the batch lookup must not distinguish
// "no Story" from "a Story you may not see".
func TestRingStatusIsNotAnOracle(t *testing.T) {
	ctx := context.Background()
	s, alice, bob, carol := newTrio(t)
	dave := testdb.Account(t, s, 1004, "dave") // has no Stories at all

	require.NoError(t, s.Follow(ctx, nil, alice.ID, bob.GitHubID, domain.ProvenanceNative))
	testdb.PublishStory(t, s, alice, domain.VisibilityFollowersOfAuthor, nil, "private")

	statuses, err := s.RingStatuses(ctx, carol.ID, carol.GitHubID,
		[]domain.GitHubID{alice.GitHubID, dave.GitHubID})
	require.NoError(t, err)
	require.Len(t, statuses, 2)
	for _, st := range statuses {
		require.False(t, st.HasActive,
			"an inaccessible active Story must look exactly like no Story")
		require.False(t, st.HasUnseen)
	}

	// Bob, who IS eligible, sees the ring.
	statuses, err = s.RingStatuses(ctx, bob.ID, bob.GitHubID, []domain.GitHubID{alice.GitHubID})
	require.NoError(t, err)
	require.True(t, statuses[0].HasActive)
	require.True(t, statuses[0].HasUnseen)
}
