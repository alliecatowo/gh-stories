package store

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

// HashToken is the one-way transform applied before a bearer token touches the
// database. A database dump therefore contains no usable session tokens.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// CreateSession issues a revocable Stories session.
func (s *Store) CreateSession(ctx context.Context, tx pgx.Tx, userID uuid.UUID, token string,
	kind domain.ClientKind, label, userAgent string, ttl time.Duration) (*domain.Session, error) {
	var sess domain.Session
	var kindStr string
	err := s.q(tx).QueryRow(ctx, `
		INSERT INTO sessions (user_id, token_hash, client_kind, client_label, user_agent, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, user_id, client_kind, client_label, user_agent, created_at, last_used_at, expires_at, revoked_at`,
		userID, HashToken(token), string(kind), label, userAgent, s.Clock.Now().Add(ttl)).
		Scan(&sess.ID, &sess.UserID, &kindStr, &sess.ClientLabel, &sess.UserAgent,
			&sess.CreatedAt, &sess.LastUsedAt, &sess.ExpiresAt, &sess.RevokedAt)
	sess.ClientKind = domain.ClientKind(kindStr)
	return &sess, wrap("create session", err)
}

// SessionByToken resolves a bearer token to a live session and its user.
// A revoked, expired, suspended or deleted account resolves to nothing.
func (s *Store) SessionByToken(ctx context.Context, token string) (*domain.Session, *domain.User, error) {
	var sess domain.Session
	var kindStr string
	var u domain.User
	var ghID int64
	var vis string
	err := s.pool.QueryRow(ctx, `
		SELECT se.id, se.user_id, se.client_kind, se.client_label, se.user_agent,
		       se.created_at, se.last_used_at, se.expires_at, se.revoked_at,
		       `+userSelect+`
		FROM sessions se
		JOIN users u ON u.id = se.user_id
		JOIN github_identities gi ON gi.github_user_id = u.github_user_id
		WHERE se.token_hash = $1 AND se.revoked_at IS NULL AND se.expires_at > $2
		  AND u.deleted_at IS NULL`,
		HashToken(token), s.Clock.Now()).
		Scan(&sess.ID, &sess.UserID, &kindStr, &sess.ClientLabel, &sess.UserAgent,
			&sess.CreatedAt, &sess.LastUsedAt, &sess.ExpiresAt, &sess.RevokedAt,
			&u.ID, &ghID, &u.Login, &u.AvatarURL, &u.ProfileURL, &u.CreatedAt,
			&u.IsModerator, &u.SuspendedAt, &u.DeletedAt, &u.OnboardedAt,
			&vis, &u.DefaultAudienceListID, &u.DefaultAllowReplies, &u.DefaultAllowReactions)
	if err != nil {
		return nil, nil, norm(err)
	}
	sess.ClientKind = domain.ClientKind(kindStr)
	u.GitHubID = domain.GitHubID(ghID)
	u.DefaultVisibility = domain.Visibility(vis)
	return &sess, &u, nil
}

func (s *Store) TouchSession(ctx context.Context, id uuid.UUID) {
	_, _ = s.pool.Exec(ctx, `UPDATE sessions SET last_used_at = now() WHERE id = $1`, id)
}

func (s *Store) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`,
		sessionID, userID)
	if err != nil {
		return wrap("revoke session", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListSessions(ctx context.Context, userID uuid.UUID) ([]domain.Session, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, client_kind, client_label, user_agent,
		       created_at, last_used_at, expires_at, revoked_at
		FROM sessions WHERE user_id=$1 AND revoked_at IS NULL AND expires_at > $2
		ORDER BY last_used_at DESC`, userID, s.Clock.Now())
	if err != nil {
		return nil, wrap("list sessions", err)
	}
	defer rows.Close()
	var out []domain.Session
	for rows.Next() {
		var sess domain.Session
		var kind string
		if err := rows.Scan(&sess.ID, &sess.UserID, &kind, &sess.ClientLabel, &sess.UserAgent,
			&sess.CreatedAt, &sess.LastUsedAt, &sess.ExpiresAt, &sess.RevokedAt); err != nil {
			return nil, err
		}
		sess.ClientKind = domain.ClientKind(kind)
		out = append(out, sess)
	}
	return out, rows.Err()
}

// --------------------------------------------------------- OAuth flows

// CreateOAuthFlow stores the state and PKCE verifier server side. Neither ever
// reaches a frontend bundle.
func (s *Store) CreateOAuthFlow(ctx context.Context, stateHash []byte, verifier, redirectURI,
	purpose, returnTo string, pendingLoginID *uuid.UUID, ttl time.Duration) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oauth_flows (state_hash, code_verifier, redirect_uri, purpose, return_to,
		                         pending_login_id, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		stateHash, verifier, redirectURI, purpose, returnTo, pendingLoginID, s.Clock.Now().Add(ttl))
	return wrap("create oauth flow", err)
}

// OAuthFlow struct carries a consumed flow.
type OAuthFlow struct {
	ID             uuid.UUID
	CodeVerifier   string
	RedirectURI    string
	Purpose        string
	ReturnTo       string
	PendingLoginID *uuid.UUID
}

// ConsumeOAuthFlow atomically claims a state exactly once. A replayed or
// expired authorization finds nothing and is rejected.
func (s *Store) ConsumeOAuthFlow(ctx context.Context, stateHash []byte) (*OAuthFlow, error) {
	var f OAuthFlow
	err := s.pool.QueryRow(ctx, `
		UPDATE oauth_flows SET consumed_at = now()
		WHERE state_hash = $1 AND consumed_at IS NULL AND expires_at > $2
		RETURNING id, code_verifier, redirect_uri, purpose, return_to, pending_login_id`,
		stateHash, s.Clock.Now()).
		Scan(&f.ID, &f.CodeVerifier, &f.RedirectURI, &f.Purpose, &f.ReturnTo, &f.PendingLoginID)
	return &f, norm(err)
}

// ------------------------------------------------- pending CLI authorization

type PendingLogin struct {
	ID          uuid.UUID
	UserCode    string
	ClientKind  domain.ClientKind
	ClientLabel string
	State       string
	ExpiresAt   time.Time
	ApprovedBy  *uuid.UUID
}

func (s *Store) CreatePendingLogin(ctx context.Context, secretHash []byte, userCode string,
	kind domain.ClientKind, label string, ttl time.Duration) (*PendingLogin, error) {
	var p PendingLogin
	var kindStr string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO pending_logins (polling_secret_hash, user_code, client_kind, client_label, expires_at)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, user_code, client_kind, client_label, state, expires_at`,
		secretHash, userCode, string(kind), label, s.Clock.Now().Add(ttl)).
		Scan(&p.ID, &p.UserCode, &kindStr, &p.ClientLabel, &p.State, &p.ExpiresAt)
	p.ClientKind = domain.ClientKind(kindStr)
	return &p, wrap("create pending login", err)
}

func (s *Store) PendingLogin(ctx context.Context, id uuid.UUID) (*PendingLogin, error) {
	var p PendingLogin
	var kindStr string
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_code, client_kind, client_label, state, expires_at, approved_user_id
		FROM pending_logins WHERE id=$1`, id).
		Scan(&p.ID, &p.UserCode, &kindStr, &p.ClientLabel, &p.State, &p.ExpiresAt, &p.ApprovedBy)
	p.ClientKind = domain.ClientKind(kindStr)
	return &p, norm(err)
}

// ApprovePendingLogin records the user's explicit approval and stores the
// one-shot token the CLI will collect on its next poll.
func (s *Store) ApprovePendingLogin(ctx context.Context, id, userID, sessionID uuid.UUID, token string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE pending_logins
		SET state='approved', approved_user_id=$2, issued_session_id=$3,
		    issued_token=$4, approved_at=now()
		WHERE id=$1 AND state='pending' AND expires_at > $5`,
		id, userID, sessionID, token, s.Clock.Now())
	if err != nil {
		return wrap("approve pending login", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DenyPendingLogin(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE pending_logins SET state='denied' WHERE id=$1 AND state='pending'`, id)
	return wrap("deny pending login", err)
}

// PollResult is what a bounded poll returns.
type PollResult struct {
	Status string // pending | approved | denied | expired
	Token  string // present exactly once
	UserID *uuid.UUID
}

// PollPendingLogin is bounded, rate limited and single use: the token is
// cleared as it is handed over, so a second poll cannot retrieve it.
func (s *Store) PollPendingLogin(ctx context.Context, id uuid.UUID, secretHash []byte, maxPolls int) (*PollResult, error) {
	now := s.Clock.Now()
	var res PollResult
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var state string
		var expires time.Time
		var polls int
		var token *string
		var userID *uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT state, expires_at, poll_count, issued_token, approved_user_id
			FROM pending_logins
			WHERE id=$1 AND polling_secret_hash=$2
			FOR UPDATE`, id, secretHash).Scan(&state, &expires, &polls, &token, &userID)
		if err != nil {
			return norm(err)
		}
		if polls >= maxPolls {
			res.Status = "expired"
			_, err := tx.Exec(ctx, `UPDATE pending_logins SET state='expired' WHERE id=$1`, id)
			return wrap("poll budget", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE pending_logins SET poll_count = poll_count + 1, last_poll_at = $2 WHERE id=$1`,
			id, now); err != nil {
			return wrap("count poll", err)
		}
		if !now.Before(expires) && state == "pending" {
			res.Status = "expired"
			_, err := tx.Exec(ctx, `UPDATE pending_logins SET state='expired' WHERE id=$1`, id)
			return wrap("expire pending login", err)
		}
		switch state {
		case "approved":
			if token == nil {
				res.Status = "expired" // already collected; single use
				return nil
			}
			res.Status = "approved"
			res.Token = *token
			res.UserID = userID
			_, err := tx.Exec(ctx,
				`UPDATE pending_logins SET state='consumed', issued_token=NULL, consumed_at=now()
				 WHERE id=$1`, id)
			return wrap("consume pending login", err)
		case "pending":
			res.Status = "pending"
		case "denied":
			res.Status = "denied"
		default:
			res.Status = "expired"
		}
		return nil
	})
	return &res, err
}
