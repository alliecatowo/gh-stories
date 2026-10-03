package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/ghclient"
	"github.com/alliecatowo/gh-stories/internal/store"
)

// DeviceLoginStart is returned to the CLI when it starts a GitHub device
// authorization. It holds no credential: the device code stays server side.
type DeviceLoginStart struct {
	ID              uuid.UUID
	UserCode        string
	VerificationURI string
	ExpiresAt       time.Time
	IntervalSeconds int
}

// StartDeviceLogin starts GitHub's device grant and records its credential
// server side, alongside the other short-lived OAuth credentials this
// service retains. Scope stays empty, exactly as in the web flow.
func (s *Service) StartDeviceLogin(ctx context.Context, kind domain.ClientKind, label string) (*DeviceLoginStart, error) {
	if !s.cfg.GitHubOAuthConfigured() {
		return nil, ErrGitHubOAuthNotConfigured
	}
	code, err := s.gh.RequestDeviceCode(ctx, s.cfg.GitHubClientID)
	if err != nil {
		return nil, fmt.Errorf("auth: request device code: %w", err)
	}
	d, err := s.store.CreateDeviceLogin(ctx, code.DeviceCode, kind, label,
		s.store.Clock.Now().Add(time.Duration(code.ExpiresIn)*time.Second))
	if err != nil {
		return nil, err
	}
	return &DeviceLoginStart{
		ID: d.ID, UserCode: code.UserCode, VerificationURI: code.VerificationURI,
		ExpiresAt: d.ExpiresAt, IntervalSeconds: code.Interval,
	}, nil
}

// PollDeviceLogin polls GitHub for one device authorization and hands the
// completed Stories token to exactly one caller.
//
// Flow per poll: read the row (consuming a previously approved token if
// this poll is the lucky one), then — only while still pending — ask
// GitHub. On approval the identity is established exactly as the web flow
// does (GET /user, EnsureUser, IssueSession) and the one-shot token is
// parked for a single poll to collect.
func (s *Service) PollDeviceLogin(ctx context.Context, id uuid.UUID) (*store.PollResult, error) {
	d, token, err := s.store.PollDeviceLogin(ctx, id)
	if err != nil {
		return nil, err
	}
	if token != "" {
		return &store.PollResult{Status: "approved", Token: token, UserID: d.UserID}, nil
	}
	switch d.State {
	case "approved", "consumed", "expired":
		return &store.PollResult{Status: "expired"}, nil
	}
	ex, err := s.gh.ExchangeDeviceToken(ctx, s.cfg.GitHubClientID, s.cfg.GitHubClientSecret, d.DeviceCode)
	if err != nil {
		return nil, fmt.Errorf("auth: exchange device code: %w", err)
	}
	switch ex.Status {
	case ghclient.DevicePending:
		return &store.PollResult{Status: "pending"}, nil
	case ghclient.DeviceSlowDown:
		// GitHub requires the polling client to add five seconds to its
		// interval after this response. Surface it rather than hiding it as
		// pending so the CLI can apply that backoff before its next poll.
		return &store.PollResult{Status: "slow_down"}, nil
	case ghclient.DeviceDenied, ghclient.DeviceExpired:
		if err := s.store.ExpireDeviceLogin(ctx, id); err != nil {
			return nil, err
		}
		if ex.Status == ghclient.DeviceDenied {
			return &store.PollResult{Status: "denied"}, nil
		}
		return &store.PollResult{Status: "expired"}, nil
	case ghclient.DeviceApproved:
		identity, err := s.gh.CurrentUser(ctx, ex.AccessToken)
		if err != nil {
			return nil, fmt.Errorf("auth: read device identity: %w", err)
		}
		if identity.AccountType != "" && identity.AccountType != "User" {
			return nil, ErrAccountTypeNotAllowed
		}
		user, _, err := s.store.EnsureUser(ctx, nil, identity)
		if err != nil {
			return nil, err
		}
		issued, _, err := s.IssueSession(ctx, user.ID, d.ClientKind, d.ClientLabel, "")
		if err != nil {
			return nil, err
		}
		ok, err := s.store.ApproveDeviceLogin(ctx, id, user.ID, issued)
		if err != nil {
			return nil, err
		}
		if !ok {
			// A racing poll expired or consumed the row first; the parked
			// session simply expires unused.
			return &store.PollResult{Status: "expired"}, nil
		}
		// Re-enter the store to consume the token atomically; exactly one
		// poll — this one or a concurrent one — collects it.
		return s.PollDeviceLogin(ctx, id)
	}
	return &store.PollResult{Status: "expired"}, nil
}
