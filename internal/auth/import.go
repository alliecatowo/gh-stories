package auth

import (
	"context"

	"github.com/google/uuid"
)

// maxImportFollows bounds how many followed accounts a single import will
// ever fetch and hand to the store, independent of whatever bound ghclient
// itself applies to a single page or the pagination loop.
const maxImportFollows = 5000

// ImportSummary tallies what a follow import did, mirroring the
// store.ImportChange actions.
type ImportSummary struct {
	Added             int
	AlreadyFollowing  int
	SkippedUnfollowed int
	SkippedBlocked    int
}

// ImportFollows fetches who the user follows on GitHub and hands the
// deduplicated set to the store. Opt-out: the caller decides `enabled` —
// when false, nothing is fetched or imported and an empty summary is
// returned, so a user who declines the import during onboarding never has
// their GitHub token used for it at all.
//
// This never mutates GitHub: ghclient.Following is read-only, and
// store.ImportFollows only ever creates or upgrades Stories-side follow
// rows, never anything on GitHub's graph.
func (s *Service) ImportFollows(ctx context.Context, userID uuid.UUID, upstreamToken string, enabled, preview bool) (*ImportSummary, error) {
	if !enabled {
		return &ImportSummary{}, nil
	}

	identities, err := s.gh.Following(ctx, upstreamToken, maxImportFollows)
	if err != nil {
		return nil, err
	}

	changes, err := s.store.ImportFollows(ctx, userID, identities, preview)
	if err != nil {
		return nil, err
	}

	summary := &ImportSummary{}
	for _, c := range changes {
		switch c.Action {
		case "added":
			summary.Added++
		case "already_following":
			summary.AlreadyFollowing++
		case "skipped_unfollowed":
			summary.SkippedUnfollowed++
		case "skipped_blocked":
			summary.SkippedBlocked++
		}
	}
	return summary, nil
}
