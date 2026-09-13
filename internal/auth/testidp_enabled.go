//go:build ghs_testidp

package auth

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/alliecatowo/gh-stories/internal/config"
	"github.com/alliecatowo/gh-stories/internal/domain"
)

// TestIdentityProviderCompiled reports whether the test identity provider is
// present in this binary. True here, because this file only compiles when
// the ghs_testidp build tag is set — which a production build must never
// set. See testidp.go for the normal-build (false) counterpart.
func TestIdentityProviderCompiled() bool { return true }

// ErrTestIdentityProviderDisabled is returned when the test identity
// provider is compiled in (ghs_testidp) but not permitted to run: either the
// process environment is production, or the operator has not opted in via
// GHS_TEST_IDENTITY_PROVIDER. Two independent checks, because a build tag
// controls what code exists and this flag controls whether that code may
// act — belt and braces.
var ErrTestIdentityProviderDisabled = errors.New("auth: test identity provider is disabled")

// testIdentityGitHubIDBase pushes synthetic GitHub ids well clear of any
// plausible real GitHub user id, so a test identity can never collide with,
// and therefore can never be confused for, a real account.
const testIdentityGitHubIDBase = 900_000_000_000

// CompleteTestAuthorization signs in as a sample account by login WITHOUT
// contacting GitHub, for the local demo only. It exists so the demo can run
// the real API, database, storage, worker and privacy rules end to end,
// replacing only the external identity boundary.
//
// This is unreachable in any binary built without ghs_testidp, and refuses
// at runtime a second time in case that tag is ever mistakenly set on a
// production build: config.Load already refuses to start a production
// process with GHS_TEST_IDENTITY_PROVIDER set, so both checks below would
// have to be independently defeated for this to run in production.
func (s *Service) CompleteTestAuthorization(ctx context.Context, login string) (*Result, error) {
	if s.cfg.Env == config.EnvProduction {
		return nil, ErrTestIdentityProviderDisabled
	}
	if !s.cfg.TestIdentityProvider {
		return nil, ErrTestIdentityProviderDisabled
	}
	if login == "" {
		return nil, fmt.Errorf("auth: test identity provider: login is required")
	}

	identity := syntheticTestIdentity(login)
	user, isNew, err := s.store.EnsureUser(ctx, nil, identity)
	if err != nil {
		return nil, err
	}
	return &Result{User: user, IsNewAccount: isNew}, nil
}

// syntheticTestIdentity deterministically derives a fake-but-stable GitHub
// identity from a login string, so signing in as "octocat" twice in the same
// local database resolves to the same account both times.
func syntheticTestIdentity(login string) domain.Identity {
	h := fnv.New64a()
	_, _ = h.Write([]byte(login))
	gid := domain.GitHubID(testIdentityGitHubIDBase + int64(h.Sum64()%1_000_000_000))
	return domain.Identity{
		GitHubID:    gid,
		Login:       login,
		AvatarURL:   "https://avatars.githubusercontent.com/u/0?v=4",
		ProfileURL:  "https://github.com/" + login,
		AccountType: "User",
	}
}
