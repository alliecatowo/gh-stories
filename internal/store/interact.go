package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

// RecordView records that authorized media was delivered to a non-owner. It is
// idempotent: repeated delivery of the same item to the same viewer is one
// view. A view reflects media delivery, not proof that a human looked at every
// pixel; the client's separate render acknowledgement fills in acknowledged_at.
func (s *Store) RecordView(ctx context.Context, storyID, viewer uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO story_views (story_item_id, viewer_user_id, delivered_at)
		VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, storyID, viewer, s.Clock.Now())
	return wrap("record view", err)
}

// AcknowledgeView records that the client actually rendered the media.
func (s *Store) AcknowledgeView(ctx context.Context, storyID, viewer uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO story_views (story_item_id, viewer_user_id, delivered_at, acknowledged_at)
		VALUES ($1,$2,$3,$3)
		ON CONFLICT (story_item_id, viewer_user_id)
		DO UPDATE SET acknowledged_at = COALESCE(story_views.acknowledged_at, EXCLUDED.acknowledged_at)`,
		storyID, viewer, s.Clock.Now())
	return wrap("acknowledge view", err)
}

// Seen reports whether the viewer has already viewed the item.
func (s *Store) Seen(ctx context.Context, storyID, viewer uuid.UUID) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM story_views WHERE story_item_id=$1 AND viewer_user_id=$2)`,
		storyID, viewer).Scan(&ok)
	return ok, wrap("seen", err)
}

// Viewer is one entry of the author-only named viewer list.
type Viewer struct {
	Identity domain.Identity
	ViewedAt time.Time
	Reaction string
}

// Viewers returns the named viewer list. Only the author may call this; the
// caller must have already established ownership.
func (s *Store) Viewers(ctx context.Context, owner, storyID uuid.UUID) ([]Viewer, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM story_items WHERE id=$1 AND author_user_id=$2
		        AND state IN ('published','processing'))`, storyID, owner).Scan(&exists); err != nil {
		return nil, wrap("viewers ownership", err)
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+identitySelect+`, sv.delivered_at, COALESCE(rx.emoji,'')
		FROM story_views sv
		JOIN users vu ON vu.id = sv.viewer_user_id
		JOIN github_identities gi ON gi.github_user_id = vu.github_user_id
		LEFT JOIN users u ON u.github_user_id = gi.github_user_id
		LEFT JOIN reactions rx ON rx.story_item_id = sv.story_item_id AND rx.user_id = sv.viewer_user_id
		WHERE sv.story_item_id = $1
		ORDER BY sv.delivered_at DESC`, storyID)
	if err != nil {
		return nil, wrap("viewers", err)
	}
	defer rows.Close()
	var out []Viewer
	for rows.Next() {
		var v Viewer
		var ghID int64
		if err := rows.Scan(&ghID, &v.Identity.Login, &v.Identity.AvatarURL, &v.Identity.ProfileURL,
			&v.Identity.AccountType, &v.Identity.RefreshedAt, &v.Identity.Registered,
			&v.ViewedAt, &v.Reaction); err != nil {
			return nil, err
		}
		v.Identity.GitHubID = domain.GitHubID(ghID)
		out = append(out, v)
	}
	return out, rows.Err()
}

// ViewerCount and ReactionCount are shown to the author only.
func (s *Store) Counts(ctx context.Context, storyID uuid.UUID) (views, reactions int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM story_views WHERE story_item_id=$1),
		       (SELECT count(*) FROM reactions   WHERE story_item_id=$1)`, storyID).
		Scan(&views, &reactions)
	return views, reactions, wrap("counts", err)
}

// ------------------------------------------------------------- reactions

// SetReaction stores or replaces the caller's reaction. One per user per item.
func (s *Store) SetReaction(ctx context.Context, storyID, userID, authorID uuid.UUID, emoji string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO reactions (story_item_id, user_id, emoji)
			VALUES ($1,$2,$3)
			ON CONFLICT (story_item_id, user_id)
			DO UPDATE SET emoji = EXCLUDED.emoji, updated_at = now()`,
			storyID, userID, emoji); err != nil {
			return wrap("set reaction", err)
		}
		if userID == authorID {
			return nil // do not notify yourself
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO inbox_events (user_id, kind, actor_user_id, story_item_id, emoji)
			VALUES ($1,'reaction',$2,$3,$4)
			ON CONFLICT (user_id, story_item_id, actor_user_id) WHERE kind = 'reaction'
			DO UPDATE SET emoji = EXCLUDED.emoji, created_at = now(), read_at = NULL`,
			authorID, userID, storyID, emoji)
		return wrap("reaction inbox", err)
	})
}

func (s *Store) ClearReaction(ctx context.Context, storyID, userID uuid.UUID) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM reactions WHERE story_item_id=$1 AND user_id=$2`, storyID, userID); err != nil {
			return wrap("clear reaction", err)
		}
		_, err := tx.Exec(ctx,
			`DELETE FROM inbox_events WHERE kind='reaction' AND story_item_id=$1 AND actor_user_id=$2`,
			storyID, userID)
		return wrap("clear reaction inbox", err)
	})
}

func (s *Store) MyReaction(ctx context.Context, storyID, userID uuid.UUID) (string, error) {
	var e string
	err := s.pool.QueryRow(ctx,
		`SELECT emoji FROM reactions WHERE story_item_id=$1 AND user_id=$2`, storyID, userID).Scan(&e)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return e, wrap("my reaction", err)
}

// MyReactions batches the caller's reactions for a set of items.
func (s *Store) MyReactions(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT story_item_id, emoji FROM reactions WHERE user_id=$1 AND story_item_id = ANY($2)`,
		userID, ids)
	if err != nil {
		return nil, wrap("my reactions", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var e string
		if err := rows.Scan(&id, &e); err != nil {
			return nil, err
		}
		out[id] = e
	}
	return out, rows.Err()
}

// --------------------------------------------------------------- replies

// Reply is a private message to a Story's author.
type Reply struct {
	ID           uuid.UUID
	StoryID      uuid.UUID
	Sender       domain.Identity
	Recipient    domain.Identity
	Body         string
	CreatedAt    time.Time
	ReadAt       *time.Time
	StoryExpired bool
}

// CreateReply stores a private reply and the author's inbox event.
func (s *Store) CreateReply(ctx context.Context, storyID, sender, recipient uuid.UUID,
	body string, retention time.Duration) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO replies (story_item_id, sender_user_id, recipient_user_id, body, purge_after)
			VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			storyID, sender, recipient, body, s.Clock.Now().Add(retention)).Scan(&id); err != nil {
			return wrap("create reply", err)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO inbox_events (user_id, kind, actor_user_id, story_item_id, reply_id)
			VALUES ($1,'reply',$2,$3,$4)`, recipient, sender, storyID, id)
		return wrap("reply inbox", err)
	})
	return id, err
}

// InboxEntry is one row of the private inbox.
type InboxEntry struct {
	ID           uuid.UUID
	Kind         domain.InboxKind
	Actor        domain.Identity
	StoryID      *uuid.UUID
	StoryExpired bool
	HasThumb     bool
	Body         string
	Emoji        string
	CreatedAt    time.Time
	ReadAt       *time.Time
	Outgoing     bool
}

// Inbox returns the caller's inbox. Only the participants of an exchange can
// see it; there is no way to enumerate someone else's.
func (s *Store) Inbox(ctx context.Context, userID uuid.UUID, limit int, before string) ([]InboxEntry, int, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	now := s.Clock.Now()
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.kind, e.story_item_id, COALESCE(rp.body,''), COALESCE(e.emoji,''),
			e.created_at, e.read_at,
			-- the Story is gone once expired, deleted or removed; no media survives
			(si.id IS NULL OR si.state <> 'published' OR si.expires_at <= $4) AS story_expired,
			`+identitySelect+`
		FROM inbox_events e
		JOIN users actor ON actor.id = e.actor_user_id
		JOIN github_identities gi ON gi.github_user_id = actor.github_user_id
		LEFT JOIN users u ON u.github_user_id = gi.github_user_id
		LEFT JOIN replies rp ON rp.id = e.reply_id AND rp.deleted_at IS NULL
		LEFT JOIN story_items si ON si.id = e.story_item_id
		WHERE e.user_id = $1 AND ($2 = '' OR e.created_at < $2::timestamptz)
		ORDER BY e.created_at DESC LIMIT $3`, userID, before, limit, now)
	if err != nil {
		return nil, 0, "", wrap("inbox", err)
	}
	defer rows.Close()
	var out []InboxEntry
	for rows.Next() {
		var e InboxEntry
		var kind string
		var ghID int64
		if err := rows.Scan(&e.ID, &kind, &e.StoryID, &e.Body, &e.Emoji,
			&e.CreatedAt, &e.ReadAt, &e.StoryExpired,
			&ghID, &e.Actor.Login, &e.Actor.AvatarURL, &e.Actor.ProfileURL,
			&e.Actor.AccountType, &e.Actor.RefreshedAt, &e.Actor.Registered); err != nil {
			return nil, 0, "", err
		}
		e.Kind = domain.InboxKind(kind)
		e.Actor.GitHubID = domain.GitHubID(ghID)
		e.HasThumb = !e.StoryExpired && e.StoryID != nil
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, "", err
	}
	unread, err := s.UnreadCount(ctx, userID)
	if err != nil {
		return nil, 0, "", err
	}
	next := ""
	if len(out) == limit {
		next = out[len(out)-1].CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return out, unread, next, nil
}

// Thread returns the private exchange between two accounts about one Story.
// Access is limited to the two participants.
func (s *Store) Thread(ctx context.Context, userID, otherID, storyID uuid.UUID) ([]Reply, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.story_item_id, r.body, r.created_at, r.read_at,
			(si.id IS NULL OR si.state <> 'published' OR si.expires_at <= $4) AS story_expired,
			(r.sender_user_id = $1) AS outgoing,
			sgi.login, sgi.avatar_url, sgi.profile_url,
			rgi.login, rgi.avatar_url, rgi.profile_url,
			su.github_user_id, ru.github_user_id
		FROM replies r
		JOIN users su ON su.id = r.sender_user_id
		JOIN users ru ON ru.id = r.recipient_user_id
		JOIN github_identities sgi ON sgi.github_user_id = su.github_user_id
		JOIN github_identities rgi ON rgi.github_user_id = ru.github_user_id
		LEFT JOIN story_items si ON si.id = r.story_item_id
		WHERE r.deleted_at IS NULL AND r.story_item_id = $3
		  AND ((r.sender_user_id = $1 AND r.recipient_user_id = $2)
		    OR (r.sender_user_id = $2 AND r.recipient_user_id = $1))
		ORDER BY r.created_at ASC`, userID, otherID, storyID, s.Clock.Now())
	if err != nil {
		return nil, wrap("thread", err)
	}
	defer rows.Close()
	var out []Reply
	for rows.Next() {
		var r Reply
		var outgoing bool
		var sGH, rGH int64
		if err := rows.Scan(&r.ID, &r.StoryID, &r.Body, &r.CreatedAt, &r.ReadAt,
			&r.StoryExpired, &outgoing,
			&r.Sender.Login, &r.Sender.AvatarURL, &r.Sender.ProfileURL,
			&r.Recipient.Login, &r.Recipient.AvatarURL, &r.Recipient.ProfileURL,
			&sGH, &rGH); err != nil {
			return nil, err
		}
		r.Sender.GitHubID = domain.GitHubID(sGH)
		r.Recipient.GitHubID = domain.GitHubID(rGH)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM inbox_events WHERE user_id=$1 AND read_at IS NULL`, userID).Scan(&n)
	return n, wrap("unread count", err)
}

func (s *Store) MarkInboxRead(ctx context.Context, userID uuid.UUID, ids []uuid.UUID, all bool) error {
	var err error
	if all {
		_, err = s.pool.Exec(ctx,
			`UPDATE inbox_events SET read_at=now() WHERE user_id=$1 AND read_at IS NULL`, userID)
	} else if len(ids) > 0 {
		_, err = s.pool.Exec(ctx,
			`UPDATE inbox_events SET read_at=now() WHERE user_id=$1 AND id = ANY($2) AND read_at IS NULL`,
			userID, ids)
		if err == nil {
			_, err = s.pool.Exec(ctx, `
				UPDATE replies SET read_at=now()
				WHERE recipient_user_id=$1 AND read_at IS NULL
				  AND id IN (SELECT reply_id FROM inbox_events WHERE id = ANY($2))`, userID, ids)
		}
	}
	return wrap("mark inbox read", err)
}

// NotifyFollow records the "new follower" inbox event.
func (s *Store) NotifyFollow(ctx context.Context, followee, follower uuid.UUID) error {
	if followee == follower {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO inbox_events (user_id, kind, actor_user_id)
		VALUES ($1,'follow',$2)
		ON CONFLICT (user_id, actor_user_id) WHERE kind = 'follow'
		DO UPDATE SET created_at = now(), read_at = NULL`, followee, follower)
	return wrap("notify follow", err)
}
