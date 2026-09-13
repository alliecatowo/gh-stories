package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/config"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/ghclient"
	"github.com/alliecatowo/gh-stories/internal/store"
)

// authorizeBaseURL is GitHub's own authorize endpoint. Unlike ghclient's
// Options.BaseURL, this is never overridden by tests: StartAuthorization only
// builds a URL string for the browser to be redirected to, it does not
// perform a network call, so there is nothing an httptest.Server needs to
// intercept here.
const authorizeBaseURL = "https://github.com/login/oauth/authorize"

// Purpose values for StartAuthorization / store.OAuthFlow.Purpose. These
// match the oauth_flows_purpose_ck check constraint in the schema exactly;
// there is no third value.
const (
	// PurposeWebLogin is an ordinary browser sign-in.
	PurposeWebLogin = "web_login"
	// PurposeCLIApproval is a browser authorization that exists to approve a
	// pending CLI login (pendingLoginID is set).
	PurposeCLIApproval = "cli_approval"
)

// ErrInvalidState is returned when a callback's state does not match a live,
// unconsumed, unexpired server-side flow record — whether because it was
// replayed, has expired, or was never issued. Deliberately the same error
// for all three: telling them apart would help an attacker calibrate a
// forgery attempt.
var ErrInvalidState = errors.New("auth: invalid or expired authorization state")

// ErrRedirectMismatch is returned when the redirect URI recorded for a flow
// no longer matches this service's configured OAuth callback.
var ErrRedirectMismatch = errors.New("auth: redirect uri mismatch")

// ErrAccountTypeNotAllowed is returned when the authenticated GitHub account
// is not a human account. Only human accounts can post in this release —
// Organizations and Bots are refused.
var ErrAccountTypeNotAllowed = errors.New("auth: only personal github accounts may sign in")

// ErrGitHubOAuthNotConfigured is returned when GHS_GITHUB_CLIENT_ID/SECRET are
// not set. Non-production environments may run without them (using the
// build-tag-isolated test identity provider instead); attempting the real
// flow without them is a configuration error, not a security refusal.
var ErrGitHubOAuthNotConfigured = errors.New("auth: github oauth is not configured")

// Service implements the GitHub OAuth flows, session issuance, and the
// pending-login (CLI) flow. It holds no state of its own beyond its
// dependencies: the store is the single source of truth for everything
// persisted.
type Service struct {
	store *store.Store
	gh    *ghclient.Client
	cfg   *config.Config
}

// NewService constructs a Service. cfg.SessionTTL and cfg.PendingLoginTTL
// govern issued session and pending-login lifetimes; st.Clock is the single
// time source consulted for every expiry decision made in the store.
func NewService(st *store.Store, gh *ghclient.Client, cfg *config.Config) *Service {
	return &Service{store: st, gh: gh, cfg: cfg}
}

// Result is the outcome of a completed authorization.
type Result struct {
	User         *domain.User
	Flow         *store.OAuthFlow
	IsNewAccount bool
	// UpstreamToken is the GitHub access token obtained by the code
	// exchange, returned ONLY so the immediate caller can perform a
	// follow-graph import in the same request. It is never persisted: there
	// is no column for it anywhere in this schema, and the caller must
	// discard it once the import step (if any) is done.
	UpstreamToken string
}

// StartAuthorization begins a GitHub OAuth authorization-code-with-PKCE
// flow. It creates a server-side flow record (state hash, PKCE verifier,
// redirect URI) and returns the URL to send the browser to. purpose and
// returnTo are opaque to this package and are simply carried through to
// Result.Flow for the HTTP layer to interpret; pendingLoginID links this
// browser authorization to a CLI device-style login when present.
func (s *Service) StartAuthorization(ctx context.Context, purpose, returnTo string, pendingLoginID *uuid.UUID) (string, error) {
	if !s.cfg.GitHubOAuthConfigured() {
		return "", ErrGitHubOAuthNotConfigured
	}

	verifier, err := NewVerifier()
	if err != nil {
		return "", err
	}
	state, err := NewState()
	if err != nil {
		return "", err
	}

	redirectURI := s.cfg.OAuthCallbackURL()
	// Config has no dedicated "how long may a GitHub consent screen take"
	// setting, so we reuse PendingLoginTTL (10 minutes): it is already the
	// product's answer to "how long is a human plausibly mid-login for",
	// and it is what bounds the CLI-linked case this flow can also serve.
	if err := s.store.CreateOAuthFlow(ctx, HashState(state), verifier, redirectURI,
		purpose, returnTo, pendingLoginID, s.cfg.PendingLoginTTL); err != nil {
		return "", err
	}

	q := url.Values{
		"client_id":             {s.cfg.GitHubClientID},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {Challenge(verifier)},
		"code_challenge_method": {"S256"},
	}
	// Scope is intentionally the empty string (see ghclient.Scope for why);
	// omit the parameter entirely rather than send scope= with no value.
	if ghclient.Scope != "" {
		q.Set("scope", ghclient.Scope)
	}
	return authorizeBaseURL + "?" + q.Encode(), nil
}

// CompleteAuthorization validates state, exchanges the code server side,
// re-reads the authenticated GitHub identity, and registers/refreshes the
// account. It returns the flow so the caller knows where to send the user
// (Result.Flow.ReturnTo) and, for a CLI-linked flow, how to find the pending
// login to approve (Result.Flow.PendingLoginID).
func (s *Service) CompleteAuthorization(ctx context.Context, code, state string) (*Result, error) {
	if !s.cfg.GitHubOAuthConfigured() {
		return nil, ErrGitHubOAuthNotConfigured
	}

	// ConsumeOAuthFlow atomically claims the state exactly once (it is a SQL
	// UPDATE ... WHERE consumed_at IS NULL AND expires_at > now() RETURNING).
	// A replayed state finds no matching unconsumed row the second time; an
	// expired or never-issued state likewise finds nothing. All three map to
	// the same ErrInvalidState so as not to help an attacker distinguish them.
	flow, err := s.store.ConsumeOAuthFlow(ctx, HashState(state))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrInvalidState
		}
		return nil, err
	}

	// Defense in depth: the redirect URI we are about to send GitHub must be
	// exactly the one currently configured, even though it was already fixed
	// at StartAuthorization time. This catches a service reconfigured
	// mid-flight (PublicURL changed) sending a code exchange to a redirect
	// GitHub no longer associates with this authorization.
	if flow.RedirectURI != s.cfg.OAuthCallbackURL() {
		return nil, ErrRedirectMismatch
	}

	token, err := s.gh.ExchangeCode(ctx, s.cfg.GitHubClientID, s.cfg.GitHubClientSecret,
		code, flow.RedirectURI, flow.CodeVerifier)
	if err != nil {
		return nil, fmt.Errorf("auth: exchange authorization code: %w", err)
	}

	// Revalidate on every single authorization by asking GitHub who this
	// token actually belongs to. We never trust a login/id supplied by the
	// client (the callback query string, a form field, anything) — only
	// what GitHub itself says about the token we just minted.
	identity, err := s.gh.CurrentUser(ctx, token.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("auth: read authenticated identity: %w", err)
	}

	// Only human accounts can post in this release. The numeric id is
	// authoritative, so this check happens on freshly-fetched data, not on
	// anything cached or client-supplied.
	if identity.AccountType != "" && identity.AccountType != "User" {
		return nil, ErrAccountTypeNotAllowed
	}

	// EnsureUser -> UpsertIdentity keys on the numeric GitHub id, so a
	// renamed login updates the same row in place, and a different account
	// that has since claimed the old username is a distinct numeric id and
	// therefore a distinct row — it can never inherit this account's
	// Stories. See store.UpsertIdentity's stale-login handling.
	user, isNew, err := s.store.EnsureUser(ctx, nil, identity)
	if err != nil {
		return nil, err
	}

	return &Result{
		User:          user,
		Flow:          flow,
		IsNewAccount:  isNew,
		UpstreamToken: token.AccessToken,
	}, nil
}
