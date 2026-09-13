package store

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

// Follow creates or upgrades a Stories follow. This never touches GitHub's
// follow graph. An explicit native follow deterministically upgrades an
// import-only relationship and clears an unfollow tombstone.
func (s *Store) Follow(ctx context.Context, tx pgx.Tx, follower uuid.UUID,
	followee domain.GitHubID, prov domain.Provenance) error {
	_, err := s.q(tx).Exec(ctx, `
		INSERT INTO relationships
			(follower_user_id, followee_github_user_id, state, provenance)
		VALUES ($1,$2,'active',$3)
		ON CONFLICT (follower_user_id, followee_github_user_id) DO UPDATE SET
			state         = 'active',
			unfollowed_at = NULL,
			updated_at    = now(),
			-- native always wins; an import never downgrades a native follow
			provenance    = CASE WHEN EXCLUDED.provenance = 'stories_native'
			                     THEN 'stories_native'
			                     ELSE relationships.provenance END`,
		follower, int64(followee), string(prov))
	if isForeignKeyViolation(err) {
		return ErrNotFound
	}
	return wrap("follow", err)
}

// Unfollow leaves a tombstone rather than deleting the row, so that a later
// GitHub re-import will not silently resurrect it.
func (s *Store) Unfollow(ctx context.Context, follower uuid.UUID, followee domain.GitHubID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE relationships
		SET state = 'unfollowed', unfollowed_at = now(), updated_at = now()
		WHERE follower_user_id = $1 AND followee_github_user_id = $2 AND state = 'active'`,
		follower, int64(followee))
	return wrap("unfollow", err)
}

// Follows reports whether follower currently follows followee on Stories.
func (s *Store) Follows(ctx context.Context, tx pgx.Tx, follower uuid.UUID, followee domain.GitHubID) (bool, error) {
	var ok bool
	err := s.q(tx).QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM relationships
			WHERE follower_user_id = $1 AND followee_github_user_id = $2 AND state = 'active')`,
		follower, int64(followee)).Scan(&ok)
	return ok, wrap("follows", err)
}

// RelationshipProvenance returns the provenance of an active follow.
func (s *Store) RelationshipProvenance(ctx context.Context, follower uuid.UUID,
	followee domain.GitHubID) (domain.Provenance, bool, error) {
	var p string
	err := s.pool.QueryRow(ctx, `
		SELECT provenance FROM relationships
		WHERE follower_user_id = $1 AND followee_github_user_id = $2 AND state = 'active'`,
		follower, int64(followee)).Scan(&p)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	return domain.Provenance(p), err == nil, wrap("relationship provenance", err)
}

// ImportChange is one row of a follow-import preview or result.
type ImportChange struct {
	Identity domain.Identity
	Action   string // added | already_following | skipped_unfollowed | skipped_blocked
}

// ImportFollows applies (or previews) an import of GitHub follows. Callers
// pass the full deduplicated identity set fetched from GitHub.
func (s *Store) ImportFollows(ctx context.Context, userID uuid.UUID,
	identities []domain.Identity, preview bool) ([]ImportChange, error) {
	changes := make([]ImportChange, 0, len(identities))
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		for _, id := range identities {
			if err := s.UpsertIdentity(ctx, tx, id); err != nil {
				return err
			}
			var state *string
			err := tx.QueryRow(ctx, `
				SELECT state FROM relationships
				WHERE follower_user_id = $1 AND followee_github_user_id = $2`,
				userID, int64(id.GitHubID)).Scan(&state)
			if err != nil && err != pgx.ErrNoRows {
				return wrap("import lookup", err)
			}
			var blocked bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM blocks b
					WHERE (b.blocker_user_id = $1 AND b.blocked_github_user_id = $2)
					   OR (b.blocked_github_user_id = (SELECT github_user_id FROM users WHERE id = $1)
					       AND b.blocker_user_id = (SELECT id FROM users WHERE github_user_id = $2)))`,
				userID, int64(id.GitHubID)).Scan(&blocked); err != nil {
				return wrap("import block check", err)
			}
			switch {
			case blocked:
				changes = append(changes, ImportChange{id, "skipped_blocked"})
				continue
			case state != nil && *state == string(domain.RelationshipUnfollowed):
				// Deliberately not resurrected.
				changes = append(changes, ImportChange{id, "skipped_unfollowed"})
				continue
			case state != nil && *state == string(domain.RelationshipActive):
				changes = append(changes, ImportChange{id, "already_following"})
				continue
			}
			if !preview {
				if err := s.Follow(ctx, tx, userID, id.GitHubID, domain.ProvenanceGitHubImport); err != nil {
					return err
				}
			}
			changes = append(changes, ImportChange{id, "added"})
		}
		if preview {
			// Roll the transaction back so a preview leaves nothing behind,
			// including the identity upserts we needed to evaluate it.
			return errPreviewRollback
		}
		return s.MarkOnboarded(ctx, tx, userID)
	})
	if err == errPreviewRollback {
		err = nil
	}
	return changes, err
}

var errPreviewRollback = &previewRollback{}

type previewRollback struct{}

func (*previewRollback) Error() string { return "preview rollback" }

// --------------------------------------------------------- mute/block/hide

func (s *Store) SetMute(ctx context.Context, userID uuid.UUID, target domain.GitHubID, on bool) error {
	return s.toggle(ctx, `mutes`, `muter_user_id`, `muted_github_user_id`, userID, target, on)
}

func (s *Store) SetBlock(ctx context.Context, userID uuid.UUID, target domain.GitHubID, on bool) error {
	if !on {
		return s.toggle(ctx, `blocks`, `blocker_user_id`, `blocked_github_user_id`, userID, target, false)
	}
	// Blocking also removes the follow relationships in both directions and
	// prevents new interactions.
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO blocks (blocker_user_id, blocked_github_user_id) VALUES ($1,$2)
			 ON CONFLICT DO NOTHING`, userID, int64(target)); err != nil {
			if isForeignKeyViolation(err) {
				return ErrNotFound
			}
			return wrap("block", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE relationships SET state='unfollowed', unfollowed_at=now(), updated_at=now()
			WHERE state='active' AND (
				(follower_user_id = $1 AND followee_github_user_id = $2)
				OR (followee_github_user_id = (SELECT github_user_id FROM users WHERE id = $1)
				    AND follower_user_id = (SELECT id FROM users WHERE github_user_id = $2)))`,
			userID, int64(target)); err != nil {
			return wrap("block unfollow", err)
		}
		return nil
	})
}

func (s *Store) SetHide(ctx context.Context, userID uuid.UUID, target domain.GitHubID, on bool) error {
	return s.toggle(ctx, `hide_rules`, `author_user_id`, `hidden_github_user_id`, userID, target, on)
}

func (s *Store) toggle(ctx context.Context, table, ownerCol, targetCol string,
	userID uuid.UUID, target domain.GitHubID, on bool) error {
	var err error
	if on {
		_, err = s.pool.Exec(ctx,
			`INSERT INTO `+table+` (`+ownerCol+`, `+targetCol+`) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			userID, int64(target))
		if isForeignKeyViolation(err) {
			return ErrNotFound
		}
	} else {
		_, err = s.pool.Exec(ctx,
			`DELETE FROM `+table+` WHERE `+ownerCol+` = $1 AND `+targetCol+` = $2`,
			userID, int64(target))
	}
	return wrap("toggle "+table, err)
}

// BlockedEitherWay reports whether a block exists in either direction. A block
// overrides audience access regardless of who set it.
func (s *Store) BlockedEitherWay(ctx context.Context, tx pgx.Tx,
	a uuid.UUID, aGitHub domain.GitHubID, b uuid.UUID, bGitHub domain.GitHubID) (bool, error) {
	var ok bool
	err := s.q(tx).QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM blocks WHERE blocker_user_id = $1 AND blocked_github_user_id = $4
			UNION ALL
			SELECT 1 FROM blocks WHERE blocker_user_id = $3 AND blocked_github_user_id = $2)`,
		a, int64(aGitHub), b, int64(bGitHub)).Scan(&ok)
	return ok, wrap("blocked either way", err)
}

func (s *Store) Muted(ctx context.Context, userID uuid.UUID, target domain.GitHubID) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM mutes WHERE muter_user_id=$1 AND muted_github_user_id=$2)`,
		userID, int64(target)).Scan(&ok)
	return ok, wrap("muted", err)
}

// ------------------------------------------------------------ listings

type RelationshipRow struct {
	Identity   domain.Identity
	Provenance domain.Provenance
	FollowsMe  bool
	Muted      bool
	Blocked    bool
	Hidden     bool
}

// ListFollowing returns accounts the user follows, most recent first.
func (s *Store) ListFollowing(ctx context.Context, userID uuid.UUID, gid domain.GitHubID,
	limit int, after string) ([]RelationshipRow, string, error) {
	return s.listRelationships(ctx, userID, gid, limit, after, `
		SELECT `+identitySelect+`, r.provenance, r.created_at,
			EXISTS (SELECT 1 FROM relationships rb
				JOIN users ub ON ub.id = rb.follower_user_id
				WHERE ub.github_user_id = gi.github_user_id
				  AND rb.followee_github_user_id = $2 AND rb.state='active') AS follows_me,
			EXISTS (SELECT 1 FROM mutes m WHERE m.muter_user_id=$1 AND m.muted_github_user_id=gi.github_user_id) AS muted,
			EXISTS (SELECT 1 FROM blocks b WHERE b.blocker_user_id=$1 AND b.blocked_github_user_id=gi.github_user_id) AS blocked,
			EXISTS (SELECT 1 FROM hide_rules h WHERE h.author_user_id=$1 AND h.hidden_github_user_id=gi.github_user_id) AS hidden
		FROM relationships r
		JOIN github_identities gi ON gi.github_user_id = r.followee_github_user_id
		LEFT JOIN users u ON u.github_user_id = gi.github_user_id
		WHERE r.follower_user_id = $1 AND r.state='active' AND ($3 = '' OR r.created_at < $3::timestamptz)
		ORDER BY r.created_at DESC LIMIT $4`)
}

// ListFollowers returns accounts following the user.
func (s *Store) ListFollowers(ctx context.Context, userID uuid.UUID, gid domain.GitHubID,
	limit int, after string) ([]RelationshipRow, string, error) {
	return s.listRelationships(ctx, userID, gid, limit, after, `
		SELECT `+identitySelect+`, r.provenance, r.created_at,
			EXISTS (SELECT 1 FROM relationships rf WHERE rf.follower_user_id=$1
				AND rf.followee_github_user_id = gi.github_user_id AND rf.state='active') AS follows_me,
			EXISTS (SELECT 1 FROM mutes m WHERE m.muter_user_id=$1 AND m.muted_github_user_id=gi.github_user_id) AS muted,
			EXISTS (SELECT 1 FROM blocks b WHERE b.blocker_user_id=$1 AND b.blocked_github_user_id=gi.github_user_id) AS blocked,
			EXISTS (SELECT 1 FROM hide_rules h WHERE h.author_user_id=$1 AND h.hidden_github_user_id=gi.github_user_id) AS hidden
		FROM relationships r
		JOIN users fu ON fu.id = r.follower_user_id
		JOIN github_identities gi ON gi.github_user_id = fu.github_user_id
		LEFT JOIN users u ON u.github_user_id = gi.github_user_id
		WHERE r.followee_github_user_id = $2 AND r.state='active'
		  AND fu.deleted_at IS NULL AND fu.suspended_at IS NULL
		  AND ($3 = '' OR r.created_at < $3::timestamptz)
		ORDER BY r.created_at DESC LIMIT $4`)
}

func (s *Store) listRelationships(ctx context.Context, userID uuid.UUID, gid domain.GitHubID,
	limit int, after, query string) ([]RelationshipRow, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, query, userID, int64(gid), after, limit)
	if err != nil {
		return nil, "", wrap("list relationships", err)
	}
	defer rows.Close()
	var out []RelationshipRow
	var last string
	for rows.Next() {
		var r RelationshipRow
		var ghID int64
		var prov string
		var createdAt any
		if err := rows.Scan(&ghID, &r.Identity.Login, &r.Identity.AvatarURL, &r.Identity.ProfileURL,
			&r.Identity.AccountType, &r.Identity.RefreshedAt, &r.Identity.Registered,
			&prov, &createdAt, &r.FollowsMe, &r.Muted, &r.Blocked, &r.Hidden); err != nil {
			return nil, "", err
		}
		r.Identity.GitHubID = domain.GitHubID(ghID)
		r.Provenance = domain.Provenance(prov)
		out = append(out, r)
		if t, ok := createdAt.(interface{ Format(string) string }); ok {
			last = t.Format("2006-01-02T15:04:05.000000Z07:00")
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) == limit {
		next = last
	}
	return out, next, nil
}

// ListSimple returns the identities in a one-sided table (mutes/blocks/hides).
func (s *Store) ListSimple(ctx context.Context, table, ownerCol, targetCol string,
	userID uuid.UUID) ([]domain.Identity, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+identitySelect+`
		FROM `+table+` t
		JOIN github_identities gi ON gi.github_user_id = t.`+targetCol+`
		LEFT JOIN users u ON u.github_user_id = gi.github_user_id
		WHERE t.`+ownerCol+` = $1
		ORDER BY gi.login_lower`, userID)
	if err != nil {
		return nil, wrap("list "+table, err)
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

// ------------------------------------------------------- audience lists

type AudienceList struct {
	ID      uuid.UUID
	Name    string
	Members []domain.Identity
}

func (s *Store) CreateAudienceList(ctx context.Context, userID uuid.UUID,
	name string, logins []string) (*AudienceList, error) {
	var list AudienceList
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO audience_lists (owner_user_id, name) VALUES ($1,$2) RETURNING id, name`,
			userID, strings.TrimSpace(name)).Scan(&list.ID, &list.Name); err != nil {
			if isUniqueViolation(err) {
				return ErrNotFound
			}
			return wrap("create audience list", err)
		}
		return s.replaceMembers(ctx, tx, list.ID, logins)
	})
	if err != nil {
		return nil, err
	}
	return s.AudienceList(ctx, userID, list.ID)
}

func (s *Store) UpdateAudienceList(ctx context.Context, userID, listID uuid.UUID,
	name *string, logins []string, replaceMembers bool) (*AudienceList, error) {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		var owner uuid.UUID
		if err := tx.QueryRow(ctx,
			`SELECT owner_user_id FROM audience_lists WHERE id=$1 AND deleted_at IS NULL`,
			listID).Scan(&owner); err != nil {
			return norm(err)
		}
		if owner != userID {
			return ErrNotFound
		}
		if name != nil {
			if _, err := tx.Exec(ctx,
				`UPDATE audience_lists SET name=$2, updated_at=now() WHERE id=$1`,
				listID, strings.TrimSpace(*name)); err != nil {
				return wrap("rename audience list", err)
			}
		}
		if replaceMembers {
			return s.replaceMembers(ctx, tx, listID, logins)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.AudienceList(ctx, userID, listID)
}

func (s *Store) replaceMembers(ctx context.Context, tx pgx.Tx, listID uuid.UUID, logins []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM audience_list_members WHERE list_id=$1`, listID); err != nil {
		return wrap("clear audience list", err)
	}
	for _, l := range logins {
		var gid int64
		err := tx.QueryRow(ctx,
			`SELECT github_user_id FROM github_identities WHERE login_lower = lower($1)`,
			strings.TrimPrefix(l, "@")).Scan(&gid)
		if err == pgx.ErrNoRows {
			continue // unknown account: silently skipped, reported by member count
		}
		if err != nil {
			return wrap("resolve audience member", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO audience_list_members (list_id, github_user_id) VALUES ($1,$2)
			 ON CONFLICT DO NOTHING`, listID, gid); err != nil {
			return wrap("add audience member", err)
		}
	}
	return nil
}

func (s *Store) AudienceList(ctx context.Context, userID, listID uuid.UUID) (*AudienceList, error) {
	var list AudienceList
	if err := s.pool.QueryRow(ctx,
		`SELECT id, name FROM audience_lists WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL`,
		listID, userID).Scan(&list.ID, &list.Name); err != nil {
		return nil, norm(err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+identitySelect+`
		FROM audience_list_members m
		JOIN github_identities gi ON gi.github_user_id = m.github_user_id
		LEFT JOIN users u ON u.github_user_id = gi.github_user_id
		WHERE m.list_id = $1 ORDER BY gi.login_lower`, listID)
	if err != nil {
		return nil, wrap("audience members", err)
	}
	defer rows.Close()
	for rows.Next() {
		id, err := scanIdentity(rows)
		if err != nil {
			return nil, err
		}
		list.Members = append(list.Members, id)
	}
	return &list, rows.Err()
}

func (s *Store) ListAudienceLists(ctx context.Context, userID uuid.UUID) ([]AudienceList, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id FROM audience_lists WHERE owner_user_id=$1 AND deleted_at IS NULL ORDER BY name`, userID)
	if err != nil {
		return nil, wrap("list audience lists", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := make([]AudienceList, 0, len(ids))
	for _, id := range ids {
		l, err := s.AudienceList(ctx, userID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, nil
}

func (s *Store) DeleteAudienceList(ctx context.Context, userID, listID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE audience_lists SET deleted_at=now() WHERE id=$1 AND owner_user_id=$2`, listID, userID)
	return wrap("delete audience list", err)
}
