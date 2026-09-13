package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/store"
)

// ErrUnauthenticated is returned by Authenticate for any bearer value that
// does not resolve to a live, active session: missing, malformed, unknown,
// revoked, expired, or belonging to a suspended/deleted account. All of
// these collapse to the same error so a caller (and the HTTP 401 it
// produces) cannot be used to probe which case applies.
var ErrUnauthenticated = errors.New("auth: not authenticated")

// IssueSession mints a new opaque bearer token and records a matching
// revocable session row. The raw token is returned exactly once — only its
// hash (store.HashToken) is ever persisted.
func (s *Service) IssueSession(ctx context.Context, userID uuid.UUID, kind domain.ClientKind, label, userAgent string) (string, *domain.Session, error) {
	token, err := NewToken()
	if err != nil {
		return "", nil, err
	}
	sess, err := s.store.CreateSession(ctx, nil, userID, token, kind, label, userAgent, s.cfg.SessionTTL)
	if err != nil {
		return "", nil, err
	}
	return token, sess, nil
}

// Authenticate resolves a bearer token to its session and user.
//
// The equality check that matters here — "does this token match a stored
// session" — is performed by the store as a hashed-value lookup
// (store.SessionByToken hashes the presented token with SHA-256 and does an
// indexed equality match against token_hash in SQL). That is what avoids a
// timing side channel: this function never itself compares the raw token
// against a stored secret byte-by-byte in Go, which is the pattern that
// would leak information through response-time variance.
//
// store.SessionByToken already filters revoked sessions, expired sessions,
// and deleted accounts at the SQL layer. It does NOT filter suspended
// accounts (a suspension is a moderation action layered on top of an
// otherwise-valid session), so that check happens here via User.Active.
func (s *Service) Authenticate(ctx context.Context, bearer string) (*domain.Session, *domain.User, error) {
	bearer = strings.TrimSpace(bearer)
	if bearer == "" {
		return nil, nil, ErrUnauthenticated
	}
	sess, user, err := s.store.SessionByToken(ctx, bearer)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, ErrUnauthenticated
		}
		return nil, nil, err
	}
	if !user.Active() {
		return nil, nil, ErrUnauthenticated
	}
	s.store.TouchSession(ctx, sess.ID)
	return sess, user, nil
}

// Logout revokes one session. It is scoped to userID so a caller can only
// revoke their own sessions.
func (s *Service) Logout(ctx context.Context, userID, sessionID uuid.UUID) error {
	return s.store.RevokeSession(ctx, userID, sessionID)
}
