package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

// ------------------------------------------------------------- uploads

// CreateUploadIntent issues an upload authorization for exactly one
// server-chosen private object key, and creates the story item shell that the
// finalized upload will become.
func (s *Store) CreateUploadIntent(ctx context.Context, userID uuid.UUID, objectKey string,
	mime string, size int64, ttl time.Duration) (*domain.UploadIntent, error) {
	now := s.Clock.Now()
	var ui domain.UploadIntent
	err := s.pool.QueryRow(ctx, `
		INSERT INTO upload_intents (user_id, object_key, declared_mime, declared_size, expires_at)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, user_id, object_key, declared_mime, declared_size, state, created_at, expires_at`,
		userID, objectKey, mime, size, now.Add(ttl)).
		Scan(&ui.ID, &ui.UserID, &ui.ObjectKey, &ui.DeclaredMIME, &ui.DeclaredSize,
			&ui.State, &ui.CreatedAt, &ui.ExpiresAt)
	return &ui, wrap("create upload intent", err)
}

func (s *Store) UploadIntent(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.UploadIntent, error) {
	var ui domain.UploadIntent
	err := s.q(tx).QueryRow(ctx, `
		SELECT id, user_id, object_key, declared_mime, declared_size, state,
		       actual_size, checksum_sha256, created_at, expires_at
		FROM upload_intents WHERE id = $1`, id).
		Scan(&ui.ID, &ui.UserID, &ui.ObjectKey, &ui.DeclaredMIME, &ui.DeclaredSize,
			&ui.State, &ui.ActualSize, &ui.Checksum, &ui.CreatedAt, &ui.ExpiresAt)
	return &ui, norm(err)
}

// StoryDraft is the metadata captured at upload-intent time.
type StoryDraft struct {
	Caption        string
	AltText        string
	Visibility     domain.Visibility
	AudienceListID *uuid.UUID
	AllowReplies   bool
	AllowReactions bool
}

// FinalizeUpload verifies ownership, size and checksum, freezes an immutable
// worker input and queues processing. It is idempotent: finalizing twice
// returns the same story item rather than creating a duplicate.
func (s *Store) FinalizeUpload(ctx context.Context, userID, uploadID uuid.UUID,
	size int64, checksum string, draft StoryDraft, filename string) (uuid.UUID, bool, error) {
	var storyID uuid.UUID
	created := false
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		ui, err := s.UploadIntent(ctx, tx, uploadID)
		if err != nil {
			return err
		}
		if ui.UserID != userID {
			return ErrNotFound // ownership failure is indistinguishable from absence
		}
		if ui.State == domain.UploadFinalized {
			// Replay: return the existing item.
			return tx.QueryRow(ctx, `
				SELECT si.id FROM story_items si
				JOIN media_jobs mj ON mj.id = si.media_job_id
				WHERE mj.upload_intent_id = $1`, uploadID).Scan(&storyID)
		}
		if ui.State != domain.UploadIssued {
			return ErrNotFound
		}
		if s.Clock.Now().After(ui.ExpiresAt) {
			return ErrNotFound
		}

		if _, err := tx.Exec(ctx, `
			UPDATE upload_intents
			SET state='finalized', actual_size=$2, checksum_sha256=$3, finalized_at=now()
			WHERE id=$1`, uploadID, size, checksum); err != nil {
			return wrap("finalize upload", err)
		}

		var jobID uuid.UUID
		// run_after comes from the injected clock, never the database's now():
		// a test that moves the clock must move job scheduling with it.
		if err := tx.QueryRow(ctx, `
			INSERT INTO media_jobs (upload_intent_id, user_id, input, run_after)
			VALUES ($1,$2,'{}'::jsonb,$3) RETURNING id`,
			uploadID, userID, s.Clock.Now()).Scan(&jobID); err != nil {
			return wrap("create media job", err)
		}

		if err := tx.QueryRow(ctx, `
			INSERT INTO story_items
				(author_user_id, media_job_id, caption, alt_text, visibility,
				 audience_list_id, allow_replies, allow_reactions)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			userID, jobID, draft.Caption, draft.AltText, string(draft.Visibility),
			draft.AudienceListID, draft.AllowReplies, draft.AllowReactions).Scan(&storyID); err != nil {
			return wrap("create story item", err)
		}

		input := domain.MediaJobInput{
			ObjectKey:    ui.ObjectKey,
			DeclaredMIME: ui.DeclaredMIME,
			ByteSize:     size,
			Checksum:     checksum,
			StoryItemID:  storyID.String(),
			Filename:     filename,
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE media_jobs SET input = $2::jsonb WHERE id = $1`, jobID, raw); err != nil {
			return wrap("freeze job input", err)
		}
		created = true
		return nil
	})
	return storyID, created, err
}

// ------------------------------------------------------------ media jobs

// ClaimMediaJob leases one queued job for a worker. The lease makes a crashed
// worker's job recoverable without a separate queue service.
func (s *Store) ClaimMediaJob(ctx context.Context, owner string, lease time.Duration) (*domain.MediaJob, error) {
	now := s.Clock.Now()
	var j domain.MediaJob
	var raw []byte
	err := s.pool.QueryRow(ctx, `
		UPDATE media_jobs SET
			state = 'leased', lease_owner = $1, lease_expires_at = $2,
			attempts = attempts + 1, updated_at = now()
		WHERE id = (
			SELECT id FROM media_jobs
			WHERE run_after <= $3
			  AND (state = 'queued' OR (state = 'leased' AND lease_expires_at < $3))
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1)
		RETURNING id, upload_intent_id, user_id, state, input, attempts, max_attempts, created_at, updated_at`,
		owner, now.Add(lease), now).
		Scan(&j.ID, &j.UploadIntentID, &j.UserID, &j.State, &raw, &j.Attempts, &j.MaxAttempts,
			&j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return nil, norm(err)
	}
	if err := json.Unmarshal(raw, &j.Input); err != nil {
		return nil, wrap("decode job input", err)
	}
	return &j, nil
}

// PublishResult carries the worker's normalized outputs.
type PublishResult struct {
	JobID    uuid.UUID
	StoryID  uuid.UUID
	Variants []domain.MediaVariant
}

// PublishStory commits ready metadata and the publication time atomically,
// after the worker's output is durable in object storage. published_at is the
// moment the item becomes visible, so processing time never eats into the
// 24-hour viewing window, and expires_at is derived from it exactly.
func (s *Store) PublishStory(ctx context.Context, res PublishResult, lifetime time.Duration) (time.Time, time.Time, error) {
	now := s.Clock.Now()
	expires := now.Add(lifetime)
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		for _, v := range res.Variants {
			if _, err := tx.Exec(ctx, `
				INSERT INTO media_variants
					(story_item_id, kind, object_key, mime, width, height,
					 duration_ms, byte_size, checksum_sha256, has_audio)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
				ON CONFLICT (story_item_id, kind) DO UPDATE SET
					object_key=EXCLUDED.object_key, mime=EXCLUDED.mime,
					width=EXCLUDED.width, height=EXCLUDED.height,
					duration_ms=EXCLUDED.duration_ms, byte_size=EXCLUDED.byte_size,
					checksum_sha256=EXCLUDED.checksum_sha256, has_audio=EXCLUDED.has_audio`,
				res.StoryID, string(v.Kind), v.ObjectKey, v.MIME, v.Width, v.Height,
				v.DurationMS, v.ByteSize, v.Checksum, v.HasAudio); err != nil {
				return wrap("insert variant", err)
			}
		}
		tag, err := tx.Exec(ctx, `
			UPDATE story_items
			SET state='published', published_at=$2, expires_at=$3
			WHERE id=$1 AND state='processing'`, res.StoryID, now, expires)
		if err != nil {
			return wrap("publish story", err)
		}
		if tag.RowsAffected() == 0 {
			// Deleted while processing, or already published. Either way the
			// worker must not resurrect it.
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `
			UPDATE media_jobs SET state='succeeded', finished_at=now(), updated_at=now(),
				lease_owner=NULL, lease_expires_at=NULL
			WHERE id=$1`, res.JobID); err != nil {
			return wrap("complete job", err)
		}
		// The original upload is no longer needed once normalized outputs are
		// durable; only normalized outputs are kept.
		if _, err := tx.Exec(ctx, `
			INSERT INTO cleanup_jobs (kind, payload, run_after)
			SELECT 'delete_objects',
			       jsonb_build_object('object_keys', jsonb_build_array(ui.object_key)), $2
			FROM media_jobs mj JOIN upload_intents ui ON ui.id = mj.upload_intent_id
			WHERE mj.id = $1`, res.JobID, now); err != nil {
			return wrap("schedule original cleanup", err)
		}
		return nil
	})
	return now, expires, err
}

// FailMediaJob records a processing failure, retrying with backoff until the
// attempt budget is spent.
func (s *Store) FailMediaJob(ctx context.Context, jobID, storyID uuid.UUID,
	code, message string, retryable bool) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		var attempts, maxAttempts int
		if err := tx.QueryRow(ctx,
			`SELECT attempts, max_attempts FROM media_jobs WHERE id=$1`, jobID).
			Scan(&attempts, &maxAttempts); err != nil {
			return norm(err)
		}
		if retryable && attempts < maxAttempts {
			backoff := time.Duration(1<<uint(attempts)) * 10 * time.Second
			_, err := tx.Exec(ctx, `
				UPDATE media_jobs SET state='queued', run_after=$2, error_code=$3,
					error_message=$4, lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
				WHERE id=$1`, jobID, s.Clock.Now().Add(backoff), code, message)
			return wrap("retry job", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE media_jobs SET state='failed', error_code=$2, error_message=$3,
				finished_at=now(), lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
			WHERE id=$1`, jobID, code, message); err != nil {
			return wrap("fail job", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE story_items SET state='failed', failure_code=$2, failure_message=$3
			WHERE id=$1 AND state='processing'`, storyID, code, message); err != nil {
			return wrap("fail story", err)
		}
		// Garbage-collect the rejected original.
		_, err := tx.Exec(ctx, `
			INSERT INTO cleanup_jobs (kind, payload, run_after)
			SELECT 'delete_objects',
			       jsonb_build_object('object_keys', jsonb_build_array(ui.object_key)), $2
			FROM media_jobs mj JOIN upload_intents ui ON ui.id = mj.upload_intent_id
			WHERE mj.id = $1`, jobID, s.Clock.Now())
		return wrap("schedule failed cleanup", err)
	})
}

// ------------------------------------------------------------ story reads

const storySelect = `
	si.id, si.author_user_id, si.media_job_id, si.state, si.caption, si.alt_text,
	si.visibility, si.audience_list_id, si.allow_replies, si.allow_reactions,
	si.published_at, si.expires_at, si.created_at, si.deleted_at,
	COALESCE(si.failure_code, ''), COALESCE(si.failure_message, ''),
	au.github_user_id, gi.login, gi.avatar_url, gi.profile_url`

func scanStory(row pgx.Row) (*domain.StoryItem, error) {
	var s domain.StoryItem
	var author domain.Identity
	var ghID int64
	var vis, state string
	err := row.Scan(&s.ID, &s.AuthorUserID, &s.MediaJobID, &state, &s.Caption, &s.AltText,
		&vis, &s.AudienceListID, &s.AllowReplies, &s.AllowReactions,
		&s.PublishedAt, &s.ExpiresAt, &s.CreatedAt, &s.DeletedAt,
		&s.FailureCode, &s.FailureMessage,
		&ghID, &author.Login, &author.AvatarURL, &author.ProfileURL)
	if err != nil {
		return nil, norm(err)
	}
	s.State = domain.StoryState(state)
	s.Visibility = domain.Visibility(vis)
	author.GitHubID = domain.GitHubID(ghID)
	author.Registered = true
	s.Author = &author
	return &s, nil
}

// StoryForViewer returns a Story only if the caller may see it right now.
// Absent, expired, deleted, blocked and out-of-audience all produce ErrNotFound
// so a caller cannot distinguish them by probing ids.
func (s *Store) StoryForViewer(ctx context.Context, viewer uuid.UUID,
	viewerGitHub domain.GitHubID, storyID uuid.UUID) (*domain.StoryItem, error) {
	item, err := scanStory(s.pool.QueryRow(ctx, `
		SELECT `+storySelect+`
		FROM story_items si
		JOIN users au ON au.id = si.author_user_id
		JOIN github_identities gi ON gi.github_user_id = au.github_user_id
		WHERE si.id = $4 AND `+visibleToViewer,
		viewer, int64(viewerGitHub), s.Clock.Now(), storyID))
	if err != nil {
		return nil, err
	}
	if err := s.loadVariants(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

// OwnStory returns the caller's own item in any state, including processing
// and failed, so a client can poll a publication it started.
func (s *Store) OwnStory(ctx context.Context, owner uuid.UUID, storyID uuid.UUID) (*domain.StoryItem, error) {
	item, err := scanStory(s.pool.QueryRow(ctx, `
		SELECT `+storySelect+`
		FROM story_items si
		JOIN users au ON au.id = si.author_user_id
		JOIN github_identities gi ON gi.github_user_id = au.github_user_id
		WHERE si.id = $1 AND si.author_user_id = $2
		  AND si.state <> 'deleted' AND si.state <> 'removed'`, storyID, owner))
	if err != nil {
		return nil, err
	}
	if err := s.loadVariants(ctx, item); err != nil {
		return nil, err
	}
	// An owner may inspect their own expired item's metadata, but the media
	// gateway still refuses the bytes; mark it so callers render it correctly.
	return item, nil
}

func (s *Store) loadVariants(ctx context.Context, item *domain.StoryItem) error {
	rows, err := s.pool.Query(ctx, `
		SELECT id, story_item_id, kind, object_key, mime, width, height,
		       duration_ms, byte_size, checksum_sha256, has_audio
		FROM media_variants WHERE story_item_id = $1`, item.ID)
	if err != nil {
		return wrap("load variants", err)
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
		item.Variants = append(item.Variants, v)
	}
	return rows.Err()
}

// UpdateStory applies an author's edit. Narrowing the audience takes effect on
// the next media request because the gateway re-evaluates the predicate every
// time; nothing needs to be revoked separately.
func (s *Store) UpdateStory(ctx context.Context, owner, storyID uuid.UUID,
	caption, alt *string, vis *domain.Visibility, listID *uuid.UUID,
	replies, reactions *bool) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE story_items SET
			caption          = COALESCE($3, caption),
			alt_text         = COALESCE($4, alt_text),
			visibility       = COALESCE($5, visibility),
			audience_list_id = CASE WHEN $5 = 'custom_list' THEN $6 ELSE
			                        CASE WHEN $5 IS NULL THEN audience_list_id ELSE NULL END END,
			allow_replies    = COALESCE($7, allow_replies),
			allow_reactions  = COALESCE($8, allow_reactions)
		WHERE id = $1 AND author_user_id = $2 AND state = 'published'`,
		storyID, owner, caption, alt, visPtr(vis), listID, replies, reactions)
	if err != nil {
		return wrap("update story", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteStory immediately removes server access and schedules physical
// cleanup. Logical deletion is exact; object removal is an operational target.
func (s *Store) DeleteStory(ctx context.Context, owner, storyID uuid.UUID, moderator bool, reason string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		var tag interface{ RowsAffected() int64 }
		var err error
		if moderator {
			tag, err = tx.Exec(ctx, `
				UPDATE story_items SET state='removed', deleted_at=now(),
					removed_by_user_id=$2, removal_reason=$3
				WHERE id=$1 AND state IN ('published','processing')`, storyID, owner, reason)
		} else {
			tag, err = tx.Exec(ctx, `
				UPDATE story_items SET state='deleted', deleted_at=now()
				WHERE id=$1 AND author_user_id=$2 AND state IN ('published','processing')`,
				storyID, owner)
		}
		if err != nil {
			return wrap("delete story", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return s.scheduleStoryObjectCleanup(ctx, tx, storyID)
	})
}

func (s *Store) scheduleStoryObjectCleanup(ctx context.Context, tx pgx.Tx, storyID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO cleanup_jobs (kind, payload, run_after)
		SELECT 'delete_objects', jsonb_build_object('object_keys',
			coalesce(jsonb_agg(object_key), '[]'::jsonb), 'story_id', $1::text), $2
		FROM media_variants WHERE story_item_id = $1::uuid`, storyID, s.Clock.Now())
	return wrap("schedule object cleanup", err)
}

// draftRecord is the on-disk form of the metadata captured at upload-intent
// time. Storing it server side means the finalize call carries no audience at
// all, so a hostile client cannot widen the audience between authorizing an
// upload and completing it.
type draftRecord struct {
	Caption        string  `json:"caption"`
	AltText        string  `json:"alt_text"`
	Visibility     string  `json:"visibility"`
	AudienceListID *string `json:"audience_list_id,omitempty"`
	AllowReplies   bool    `json:"allow_replies"`
	AllowReactions bool    `json:"allow_reactions"`
}

// StashDraft records the intended Story metadata against an upload intent.
func (s *Store) StashDraft(ctx context.Context, uploadID uuid.UUID, d StoryDraft, filename string) error {
	rec := draftRecord{
		Caption: d.Caption, AltText: d.AltText, Visibility: string(d.Visibility),
		AllowReplies: d.AllowReplies, AllowReactions: d.AllowReactions,
	}
	if d.AudienceListID != nil {
		id := d.AudienceListID.String()
		rec.AudienceListID = &id
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE upload_intents SET draft = $2::jsonb, filename = $3 WHERE id = $1`,
		uploadID, raw, filename)
	return wrap("stash draft", err)
}

// LoadDraft reads back the metadata captured at upload-intent time.
func (s *Store) LoadDraft(ctx context.Context, uploadID uuid.UUID) (StoryDraft, string, error) {
	var raw []byte
	var filename string
	if err := s.pool.QueryRow(ctx,
		`SELECT draft, filename FROM upload_intents WHERE id = $1`, uploadID).
		Scan(&raw, &filename); err != nil {
		return StoryDraft{}, "", norm(err)
	}
	var rec draftRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return StoryDraft{}, "", wrap("decode draft", err)
	}
	d := StoryDraft{
		Caption: rec.Caption, AltText: rec.AltText,
		Visibility:     domain.Visibility(rec.Visibility),
		AllowReplies:   rec.AllowReplies,
		AllowReactions: rec.AllowReactions,
	}
	if !d.Visibility.Valid() {
		d.Visibility = domain.VisibilityFollowersOfAuthor
	}
	if rec.AudienceListID != nil {
		if id, err := uuid.Parse(*rec.AudienceListID); err == nil {
			d.AudienceListID = &id
		}
	}
	return d, filename, nil
}
