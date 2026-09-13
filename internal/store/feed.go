package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

// FeedGroup is one author's active, authorized sequence.
type FeedGroup struct {
	Author     domain.Identity
	Items      []*domain.StoryItem
	Seen       []bool
	HasUnseen  bool
	Muted      bool
	Provenance string // stories_native | github_import | self | none
}

// Feed returns the caller's authorized feed.
//
// Ordering is deterministic and specified:
//  1. authors with an unseen item first,
//  2. then explicitly native-followed authors before import-only authors,
//  3. then most recent eligible publication,
//  4. tie-broken by the author's GitHub numeric id.
//
// Muted authors are omitted unless includeMuted is set; they remain openable
// explicitly, and eligible ambient surfaces can still show a muted ring.
//
// Fetching the feed never records a view.
func (s *Store) Feed(ctx context.Context, viewer uuid.UUID, viewerGitHub domain.GitHubID,
	includeMuted bool, limit int) ([]FeedGroup, *FeedGroup, error) {
	if limit <= 0 || limit > 50 {
		limit = 25
	}
	now := s.Clock.Now()

	rows, err := s.pool.Query(ctx, `
		WITH visible AS (
			SELECT `+storySelect+`,
				(sv.viewer_user_id IS NOT NULL) AS seen,
				au.github_user_id AS author_github_id
			FROM story_items si
			JOIN users au ON au.id = si.author_user_id
			JOIN github_identities gi ON gi.github_user_id = au.github_user_id
			LEFT JOIN story_views sv ON sv.story_item_id = si.id AND sv.viewer_user_id = $1::uuid
			WHERE `+visibleToViewer+`
		),
		annotated AS (
			SELECT v.*,
				COALESCE(r.provenance, CASE WHEN v.author_user_id = $1::uuid THEN 'self' ELSE 'none' END) AS provenance,
				EXISTS (SELECT 1 FROM mutes m
					WHERE m.muter_user_id = $1::uuid AND m.muted_github_user_id = v.author_github_id) AS muted
			FROM visible v
			LEFT JOIN relationships r
				ON r.follower_user_id = $1::uuid
				AND r.followee_github_user_id = v.author_github_id
				AND r.state = 'active'
		),
		ranked AS (
			SELECT author_user_id,
				bool_or(NOT seen) AS has_unseen,
				max(published_at)  AS latest,
				min(author_github_id) AS author_github_id,
				min(provenance)    AS provenance,
				bool_or(muted)     AS muted
			FROM annotated GROUP BY author_user_id
		)
		SELECT a.*, rk.has_unseen
		FROM annotated a
		JOIN ranked rk ON rk.author_user_id = a.author_user_id
		WHERE ($4::boolean OR NOT rk.muted OR a.author_user_id = $1::uuid)
		ORDER BY
			(a.author_user_id = $1::uuid) DESC,
			rk.has_unseen DESC,
			CASE rk.provenance WHEN 'self' THEN 0 WHEN 'stories_native' THEN 1
			                   WHEN 'github_import' THEN 2 ELSE 3 END,
			rk.latest DESC,
			rk.author_github_id ASC,
			a.published_at ASC`,
		viewer, int64(viewerGitHub), now, includeMuted)
	if err != nil {
		return nil, nil, wrap("feed", err)
	}
	defer rows.Close()

	var order []uuid.UUID
	groups := map[uuid.UUID]*FeedGroup{}
	for rows.Next() {
		item, seen, ghID, prov, muted, err := scanFeedRow(rows)
		if err != nil {
			return nil, nil, err
		}
		g, ok := groups[item.AuthorUserID]
		if !ok {
			g = &FeedGroup{
				Author: domain.Identity{
					GitHubID: ghID, Login: item.Author.Login,
					AvatarURL: item.Author.AvatarURL, ProfileURL: item.Author.ProfileURL,
					Registered: true,
				},
				Muted: muted, Provenance: prov,
			}
			groups[item.AuthorUserID] = g
			order = append(order, item.AuthorUserID)
		}
		g.Items = append(g.Items, item)
		g.Seen = append(g.Seen, seen)
		if !seen {
			g.HasUnseen = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	if err := s.loadVariantsForGroups(ctx, groups); err != nil {
		return nil, nil, err
	}

	var me *FeedGroup
	out := make([]FeedGroup, 0, len(order))
	for _, id := range order {
		g := groups[id]
		if id == viewer {
			me = g
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, *g)
	}
	return out, me, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanFeedRow(r rowScanner) (*domain.StoryItem, bool, domain.GitHubID, string, bool, error) {
	var s domain.StoryItem
	var author domain.Identity
	var ghID, authorGH int64
	var vis, state, prov string
	var seen, muted, hasUnseen bool
	err := r.Scan(&s.ID, &s.AuthorUserID, &s.MediaJobID, &state, &s.Caption, &s.AltText,
		&vis, &s.AudienceListID, &s.AllowReplies, &s.AllowReactions,
		&s.PublishedAt, &s.ExpiresAt, &s.CreatedAt, &s.DeletedAt,
		&s.FailureCode, &s.FailureMessage,
		&ghID, &author.Login, &author.AvatarURL, &author.ProfileURL,
		&seen, &authorGH, &prov, &muted, &hasUnseen)
	if err != nil {
		return nil, false, 0, "", false, wrap("scan feed row", err)
	}
	s.State = domain.StoryState(state)
	s.Visibility = domain.Visibility(vis)
	author.GitHubID = domain.GitHubID(ghID)
	author.Registered = true
	s.Author = &author
	return &s, seen, domain.GitHubID(authorGH), prov, muted, nil
}

func (s *Store) loadVariantsForGroups(ctx context.Context, groups map[uuid.UUID]*FeedGroup) error {
	var ids []uuid.UUID
	index := map[uuid.UUID]*domain.StoryItem{}
	for _, g := range groups {
		for _, it := range g.Items {
			ids = append(ids, it.ID)
			index[it.ID] = it
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, story_item_id, kind, object_key, mime, width, height,
		       duration_ms, byte_size, checksum_sha256, has_audio
		FROM media_variants WHERE story_item_id = ANY($1)`, ids)
	if err != nil {
		return wrap("load feed variants", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v domain.MediaVariant
		var kind string
		if err := rows.Scan(&v.ID, &v.StoryID, &kind, &v.ObjectKey, &v.MIME,
			&v.Width, &v.Height, &v.DurationMS, &v.ByteSize, &v.Checksum, &v.HasAudio); err != nil {
			return err
		}
		v.Kind = domain.VariantKind(kind)
		if it := index[v.StoryID]; it != nil {
			it.Variants = append(it.Variants, v)
		}
	}
	return rows.Err()
}

// AuthorSequence returns one author's authorized active items, oldest first.
func (s *Store) AuthorSequence(ctx context.Context, viewer uuid.UUID,
	viewerGitHub domain.GitHubID, login string) (*FeedGroup, error) {
	id, err := s.IdentityByLogin(ctx, nil, login)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+storySelect+`,
			(sv.viewer_user_id IS NOT NULL) AS seen,
			au.github_user_id AS author_github_id,
			COALESCE(r.provenance, CASE WHEN si.author_user_id = $1::uuid THEN 'self' ELSE 'none' END),
			EXISTS (SELECT 1 FROM mutes m WHERE m.muter_user_id=$1::uuid
			        AND m.muted_github_user_id = au.github_user_id),
			false
		FROM story_items si
		JOIN users au ON au.id = si.author_user_id
		JOIN github_identities gi ON gi.github_user_id = au.github_user_id
		LEFT JOIN story_views sv ON sv.story_item_id = si.id AND sv.viewer_user_id = $1::uuid
		LEFT JOIN relationships r ON r.follower_user_id = $1::uuid
			AND r.followee_github_user_id = au.github_user_id AND r.state='active'
		WHERE au.github_user_id = $4 AND `+visibleToViewer+`
		ORDER BY si.published_at ASC`,
		viewer, int64(viewerGitHub), s.Clock.Now(), int64(id.GitHubID))
	if err != nil {
		return nil, wrap("author sequence", err)
	}
	defer rows.Close()
	g := &FeedGroup{Author: id}
	for rows.Next() {
		item, seen, _, prov, muted, err := scanFeedRow(rows)
		if err != nil {
			return nil, err
		}
		g.Items = append(g.Items, item)
		g.Seen = append(g.Seen, seen)
		g.Muted = muted
		g.Provenance = prov
		if !seen {
			g.HasUnseen = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(g.Items) == 0 {
		// No account, or nothing this caller may see — indistinguishable.
		return nil, ErrNotFound
	}
	if err := s.loadVariantsForGroups(ctx, map[uuid.UUID]*FeedGroup{g.Items[0].AuthorUserID: g}); err != nil {
		return nil, err
	}
	return g, nil
}

// OwnSequence returns the caller's own items, including ones still processing.
func (s *Store) OwnSequence(ctx context.Context, owner uuid.UUID) ([]*domain.StoryItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+storySelect+`
		FROM story_items si
		JOIN users au ON au.id = si.author_user_id
		JOIN github_identities gi ON gi.github_user_id = au.github_user_id
		WHERE si.author_user_id = $1
		  AND si.state IN ('processing','published','failed')
		  AND (si.expires_at IS NULL OR si.expires_at > $2)
		ORDER BY si.created_at ASC`, owner, s.Clock.Now())
	if err != nil {
		return nil, wrap("own sequence", err)
	}
	defer rows.Close()
	var out []*domain.StoryItem
	for rows.Next() {
		item, err := scanStory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, it := range out {
		if err := s.loadVariants(ctx, it); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// RingStatus is one avatar's ring state.
type RingStatus struct {
	GitHubID  domain.GitHubID
	Login     string
	HasActive bool
	HasUnseen bool
	Muted     bool
	IsSelf    bool
}

// RingStatuses answers a batch avatar lookup.
//
// It reveals only Stories the caller may access. An inaccessible active Story
// and no Story at all both come back as HasActive=false, with no counts, no
// timestamps and no audience — the two must be indistinguishable, or this
// endpoint becomes a private-Story oracle.
func (s *Store) RingStatuses(ctx context.Context, viewer uuid.UUID, viewerGitHub domain.GitHubID,
	ids []domain.GitHubID) ([]RingStatus, error) {
	out := make([]RingStatus, 0, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	raw := make([]int64, len(ids))
	for i, v := range ids {
		raw[i] = int64(v)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT target.github_user_id, COALESCE(gi.login, ''),
			COALESCE(bool_or(vis.visible), false)            AS has_active,
			COALESCE(bool_or(vis.visible AND NOT vis.seen), false) AS has_unseen,
			EXISTS (SELECT 1 FROM mutes m WHERE m.muter_user_id = $1::uuid
			        AND m.muted_github_user_id = target.github_user_id) AS muted,
			(target.github_user_id = $2::bigint) AS is_self
		FROM unnest($4::bigint[]) AS target(github_user_id)
		LEFT JOIN github_identities gi ON gi.github_user_id = target.github_user_id
		LEFT JOIN LATERAL (
			SELECT `+visibleToViewer+` AS visible,
			       (sv.viewer_user_id IS NOT NULL) AS seen
			FROM story_items si
			JOIN users au ON au.id = si.author_user_id
			LEFT JOIN story_views sv ON sv.story_item_id = si.id AND sv.viewer_user_id = $1::uuid
			WHERE au.github_user_id = target.github_user_id
		) vis ON true
		GROUP BY target.github_user_id, gi.login`,
		viewer, int64(viewerGitHub), s.Clock.Now(), raw)
	if err != nil {
		return nil, wrap("ring statuses", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r RingStatus
		var gid int64
		if err := rows.Scan(&gid, &r.Login, &r.HasActive, &r.HasUnseen, &r.Muted, &r.IsSelf); err != nil {
			return nil, err
		}
		r.GitHubID = domain.GitHubID(gid)
		out = append(out, r)
	}
	return out, rows.Err()
}

// FeedVersion is a cheap change token clients poll instead of refetching.
func (s *Store) FeedVersion(ctx context.Context, viewer uuid.UUID, viewerGitHub domain.GitHubID) (string, error) {
	var v *time.Time
	var n int64
	err := s.pool.QueryRow(ctx, `
		SELECT max(greatest(si.published_at, coalesce(si.deleted_at, si.published_at))), count(*)
		FROM story_items si
		JOIN users au ON au.id = si.author_user_id
		WHERE `+visibleToViewer, viewer, int64(viewerGitHub), s.Clock.Now()).Scan(&v, &n)
	if err != nil {
		return "", wrap("feed version", err)
	}
	if v == nil {
		return "0-0", nil
	}
	return v.UTC().Format(time.RFC3339Nano) + "-" + itoa(n), nil
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
