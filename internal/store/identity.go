package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

const identitySelect = `
	gi.github_user_id, gi.login, gi.avatar_url, gi.profile_url, gi.account_type,
	gi.refreshed_at, (u.id IS NOT NULL AND u.deleted_at IS NULL AND u.suspended_at IS NULL) AS registered`

// UpsertIdentity records or refreshes a GitHub identity. The numeric id is the
// key, so a renamed login updates in place instead of creating a new account
// and never transfers an old account's Stories to the new holder of a username.
func (s *Store) UpsertIdentity(ctx context.Context, tx pgx.Tx, id domain.Identity) error {
	accountType := id.AccountType
	if accountType == "" {
		accountType = "User"
	}
	_, err := s.q(tx).Exec(ctx, `
		INSERT INTO github_identities
			(github_user_id, login, login_lower, avatar_url, profile_url, account_type, refreshed_at)
		VALUES ($1,$2,lower($2),$3,$4,$5,now())
		ON CONFLICT (github_user_id) DO UPDATE SET
			login        = EXCLUDED.login,
			login_lower  = EXCLUDED.login_lower,
			avatar_url   = EXCLUDED.avatar_url,
			profile_url  = EXCLUDED.profile_url,
			account_type = EXCLUDED.account_type,
			refreshed_at = now()`,
		int64(id.GitHubID), id.Login, id.AvatarURL, id.ProfileURL, accountType)
	if err != nil && isUniqueViolation(err) {
		// Another identity currently holds this login (a rename we have not
		// observed yet). Release the stale login, then retry once.
		if _, e := s.q(tx).Exec(ctx,
			`UPDATE github_identities SET login = login || '#stale-' || github_user_id,
			        login_lower = lower(login || '#stale-' || github_user_id)
			 WHERE login_lower = lower($1) AND github_user_id <> $2`,
			id.Login, int64(id.GitHubID)); e != nil {
			return wrap("release stale login", e)
		}
		return s.UpsertIdentity(ctx, tx, id)
	}
	return wrap("upsert identity", err)
}

func scanIdentity(row pgx.Row) (domain.Identity, error) {
	var id domain.Identity
	var ghID int64
	err := row.Scan(&ghID, &id.Login, &id.AvatarURL, &id.ProfileURL,
		&id.AccountType, &id.RefreshedAt, &id.Registered)
	id.GitHubID = domain.GitHubID(ghID)
	return id, norm(err)
}

func (s *Store) IdentityByGitHubID(ctx context.Context, tx pgx.Tx, gid domain.GitHubID) (domain.Identity, error) {
	return scanIdentity(s.q(tx).QueryRow(ctx, `
		SELECT `+identitySelect+`
		FROM github_identities gi LEFT JOIN users u ON u.github_user_id = gi.github_user_id
		WHERE gi.github_user_id = $1`, int64(gid)))
}

func (s *Store) IdentityByLogin(ctx context.Context, tx pgx.Tx, login string) (domain.Identity, error) {
	return scanIdentity(s.q(tx).QueryRow(ctx, `
		SELECT `+identitySelect+`
		FROM github_identities gi LEFT JOIN users u ON u.github_user_id = gi.github_user_id
		WHERE gi.login_lower = lower($1)`, strings.TrimPrefix(login, "@")))
}

// IdentitiesByGitHubIDs returns a map for batch ring-status lookups.
func (s *Store) IdentitiesByGitHubIDs(ctx context.Context, ids []domain.GitHubID) (map[domain.GitHubID]domain.Identity, error) {
	out := make(map[domain.GitHubID]domain.Identity, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	raw := make([]int64, len(ids))
	for i, v := range ids {
		raw[i] = int64(v)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+identitySelect+`
		FROM github_identities gi LEFT JOIN users u ON u.github_user_id = gi.github_user_id
		WHERE gi.github_user_id = ANY($1)`, raw)
	if err != nil {
		return nil, wrap("identities by ids", err)
	}
	defer rows.Close()
	for rows.Next() {
		id, err := scanIdentity(rows)
		if err != nil {
			return nil, err
		}
		out[id.GitHubID] = id
	}
	return out, rows.Err()
}

// IdentitiesByLogins is the login-keyed form of the batch lookup.
func (s *Store) IdentitiesByLogins(ctx context.Context, logins []string) (map[string]domain.Identity, error) {
	out := make(map[string]domain.Identity, len(logins))
	if len(logins) == 0 {
		return out, nil
	}
	lower := make([]string, 0, len(logins))
	for _, l := range logins {
		lower = append(lower, strings.ToLower(strings.TrimPrefix(l, "@")))
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+identitySelect+`
		FROM github_identities gi LEFT JOIN users u ON u.github_user_id = gi.github_user_id
		WHERE gi.login_lower = ANY($1)`, lower)
	if err != nil {
		return nil, wrap("identities by logins", err)
	}
	defer rows.Close()
	for rows.Next() {
		id, err := scanIdentity(rows)
		if err != nil {
			return nil, err
		}
		out[strings.ToLower(id.Login)] = id
	}
	return out, rows.Err()
}

// SearchIdentities backs username lookup. There is no Explore feed; discovery
// is imports, this search, and authorized rings on GitHub.
func (s *Store) SearchIdentities(ctx context.Context, q string, limit int) ([]domain.Identity, error) {
	q = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(q), "@"))
	if q == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 25 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+identitySelect+`
		FROM github_identities gi JOIN users u ON u.github_user_id = gi.github_user_id
		WHERE u.deleted_at IS NULL AND u.suspended_at IS NULL
		  AND gi.login_lower LIKE $1 || '%'
		ORDER BY length(gi.login), gi.login_lower
		LIMIT $2`, q, limit)
	if err != nil {
		return nil, wrap("search identities", err)
	}
	defer rows.Close()
	var out []domain.Identity
	for rows.Next() {
		id, err := scanIdentity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- users

const userSelect = `
	u.id, u.github_user_id, gi.login, gi.avatar_url, gi.profile_url, u.created_at,
	u.is_moderator, u.suspended_at, u.deleted_at, u.follow_import_completed_at,
	u.default_visibility, u.default_audience_list_id,
	u.default_allow_replies, u.default_allow_reactions`

func scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	var ghID int64
	var vis string
	err := row.Scan(&u.ID, &ghID, &u.Login, &u.AvatarURL, &u.ProfileURL, &u.CreatedAt,
		&u.IsModerator, &u.SuspendedAt, &u.DeletedAt, &u.OnboardedAt,
		&vis, &u.DefaultAudienceListID, &u.DefaultAllowReplies, &u.DefaultAllowReactions)
	if err != nil {
		return nil, norm(err)
	}
	u.GitHubID = domain.GitHubID(ghID)
	u.DefaultVisibility = domain.Visibility(vis)
	return &u, nil
}

func (s *Store) UserByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.User, error) {
	return scanUser(s.q(tx).QueryRow(ctx, `
		SELECT `+userSelect+` FROM users u
		JOIN github_identities gi ON gi.github_user_id = u.github_user_id
		WHERE u.id = $1`, id))
}

func (s *Store) UserByGitHubID(ctx context.Context, tx pgx.Tx, gid domain.GitHubID) (*domain.User, error) {
	return scanUser(s.q(tx).QueryRow(ctx, `
		SELECT `+userSelect+` FROM users u
		JOIN github_identities gi ON gi.github_user_id = u.github_user_id
		WHERE u.github_user_id = $1`, int64(gid)))
}

func (s *Store) UserByLogin(ctx context.Context, tx pgx.Tx, login string) (*domain.User, error) {
	return scanUser(s.q(tx).QueryRow(ctx, `
		SELECT `+userSelect+` FROM users u
		JOIN github_identities gi ON gi.github_user_id = u.github_user_id
		WHERE gi.login_lower = lower($1)`, strings.TrimPrefix(login, "@")))
}

// EnsureUser registers an account for a GitHub identity, or returns the
// existing one. Only human accounts can post in this release.
func (s *Store) EnsureUser(ctx context.Context, tx pgx.Tx, id domain.Identity) (*domain.User, bool, error) {
	if err := s.UpsertIdentity(ctx, tx, id); err != nil {
		return nil, false, err
	}
	var userID uuid.UUID
	var inserted bool
	err := s.q(tx).QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO users (github_user_id) VALUES ($1)
			ON CONFLICT (github_user_id) DO NOTHING
			RETURNING id
		)
		SELECT id, true FROM ins
		UNION ALL
		SELECT id, false FROM users WHERE github_user_id = $1
		LIMIT 1`, int64(id.GitHubID)).Scan(&userID, &inserted)
	if err != nil {
		return nil, false, wrap("ensure user", err)
	}
	// Signing in again clears a soft deletion of the account record itself.
	if _, err := s.q(tx).Exec(ctx,
		`UPDATE users SET last_seen_at = now() WHERE id = $1`, userID); err != nil {
		return nil, false, wrap("touch user", err)
	}
	u, err := s.UserByID(ctx, tx, userID)
	return u, inserted, err
}

func (s *Store) MarkOnboarded(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := s.q(tx).Exec(ctx,
		`UPDATE users SET follow_import_completed_at = now() WHERE id = $1`, userID)
	return wrap("mark onboarded", err)
}

// SuspendUser is a moderation action; a suspension overrides audience access.
func (s *Store) SuspendUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID, reason string) error {
	_, err := s.q(tx).Exec(ctx,
		`UPDATE users SET suspended_at = now(), suspended_reason = $2 WHERE id = $1`, userID, reason)
	return wrap("suspend user", err)
}

func (s *Store) UnsuspendUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := s.q(tx).Exec(ctx,
		`UPDATE users SET suspended_at = NULL, suspended_reason = NULL WHERE id = $1`, userID)
	return wrap("unsuspend user", err)
}

// UpdateSettings applies the account preference subset.
func (s *Store) UpdateSettings(ctx context.Context, userID uuid.UUID,
	vis *domain.Visibility, listID *uuid.UUID, clearList bool, replies, reactions *bool) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users SET
			default_visibility        = COALESCE($2, default_visibility),
			default_audience_list_id  = CASE WHEN $4 THEN NULL ELSE COALESCE($3, default_audience_list_id) END,
			default_allow_replies     = COALESCE($5, default_allow_replies),
			default_allow_reactions   = COALESCE($6, default_allow_reactions)
		WHERE id = $1`,
		userID, visPtr(vis), listID, clearList, replies, reactions)
	return wrap("update settings", err)
}

func visPtr(v *domain.Visibility) *string {
	if v == nil {
		return nil
	}
	s := string(*v)
	return &s
}

// DeleteAccount removes the account and everything that cascades from it. The
// github_identities row survives so other people's relationships to that
// identity stay coherent, but it is no longer registered.
func (s *Store) DeleteAccount(ctx context.Context, userID uuid.UUID) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		// Schedule physical object cleanup before the rows disappear.
		if _, err := tx.Exec(ctx, `
			INSERT INTO cleanup_jobs (kind, payload, run_after)
			SELECT 'delete_objects', jsonb_build_object('object_keys',
				coalesce(jsonb_agg(k), '[]'::jsonb)), $2
			FROM (
				SELECT mv.object_key AS k FROM media_variants mv
				JOIN story_items si ON si.id = mv.story_item_id
				WHERE si.author_user_id = $1
				UNION ALL
				SELECT ui.object_key FROM upload_intents ui WHERE ui.user_id = $1
			) keys`, userID, s.Clock.Now()); err != nil {
			return wrap("schedule account object cleanup", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
			return wrap("delete account", err)
		}
		return nil
	})
}

// TouchSeen updates the last-seen marker used only for operational visibility.
func (s *Store) TouchSeen(ctx context.Context, userID uuid.UUID, at time.Time) {
	_, _ = s.pool.Exec(ctx, `UPDATE users SET last_seen_at = $2 WHERE id = $1`, userID, at)
}
