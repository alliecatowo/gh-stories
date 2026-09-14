package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/testdb"
)

// TestModeratorsComeOnlyFromConfiguration: moderator access is an operator
// decision expressed in the deployment, never something the product grants.
func TestModeratorsComeOnlyFromConfiguration(t *testing.T) {
	ctx := context.Background()
	s, _ := testdb.New(t)
	alice := testdb.Account(t, s, 5001, "alice")
	bob := testdb.Account(t, s, 5002, "bob")

	// Nobody is a moderator by default.
	require.False(t, alice.IsModerator)
	require.False(t, bob.IsModerator)

	granted, revoked, err := s.SyncModerators(ctx, []domain.GitHubID{alice.GitHubID})
	require.NoError(t, err)
	require.Equal(t, 1, granted)
	require.Zero(t, revoked)

	reloaded, err := s.UserByID(ctx, nil, alice.ID)
	require.NoError(t, err)
	require.True(t, reloaded.IsModerator)

	other, err := s.UserByID(ctx, nil, bob.ID)
	require.NoError(t, err)
	require.False(t, other.IsModerator, "only listed ids get access")

	// Re-applying the same list changes nothing.
	granted, revoked, err = s.SyncModerators(ctx, []domain.GitHubID{alice.GitHubID})
	require.NoError(t, err)
	require.Zero(t, granted)
	require.Zero(t, revoked)

	// Removing an id from the list actually demotes them.
	granted, revoked, err = s.SyncModerators(ctx, []domain.GitHubID{bob.GitHubID})
	require.NoError(t, err)
	require.Equal(t, 1, granted)
	require.Equal(t, 1, revoked)

	reloaded, err = s.UserByID(ctx, nil, alice.ID)
	require.NoError(t, err)
	require.False(t, reloaded.IsModerator, "removal from the list must take effect")

	// An empty list demotes everyone.
	_, revoked, err = s.SyncModerators(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, 1, revoked)
}
