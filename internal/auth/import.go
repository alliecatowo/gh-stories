package auth

import (
	"context"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

// maxImportFollows bounds how many followed accounts a single import will
// ever fetch and hand to the store, independent of whatever bound ghclient
// itself applies to a single page or the pagination loop.
const maxImportFollows = 5000

// ImportSummary tallies what a follow import did, mirroring the
// store.ImportChange actions.
type ImportSummary struct {
	GitHubFollowingCount int
	Added                int
	AlreadyFollowing     int
	SkippedUnfollowed    int
	SkippedBlocked       int
	// Sample is a handful of the accounts newly followed, so the summary can
	// say "you now follow maya, jules and 12 others" rather than a bare count.
	Sample []domain.Identity
}

// sampleSize bounds how many names an import summary names out loud.
const sampleSize = 5

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

	summary := &ImportSummary{GitHubFollowingCount: len(identities)}
	for _, c := range changes {
		if c.Action == "added" && len(summary.Sample) < sampleSize {
			summary.Sample = append(summary.Sample, c.Identity)
		}
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
