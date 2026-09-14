package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CleanupJob is a leased background task. Leases plus retries give us
// at-least-once execution with idempotent handlers, which is all this product
// needs — no Redis, no separate queue service.
type CleanupJob struct {
	ID       uuid.UUID
	Kind     string
	Payload  json.RawMessage
	Attempts int
	Max      int
}

func (s *Store) EnqueueCleanup(ctx context.Context, kind string, payload any, runAfter time.Time) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO cleanup_jobs (kind, payload, run_after) VALUES ($1,$2::jsonb,$3)`,
		kind, raw, runAfter)
	return wrap("enqueue cleanup", err)
}

func (s *Store) ClaimCleanupJob(ctx context.Context, owner string, lease time.Duration) (*CleanupJob, error) {
	now := s.Clock.Now()
	var j CleanupJob
	err := s.pool.QueryRow(ctx, `
		UPDATE cleanup_jobs SET state='leased', lease_owner=$1, lease_expires_at=$2,
			attempts = attempts + 1, updated_at = now()
		WHERE id = (
			SELECT id FROM cleanup_jobs
			WHERE run_after <= $3
			  AND (state='queued' OR (state='leased' AND lease_expires_at < $3))
			ORDER BY run_after
			FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING id, kind, payload, attempts, max_attempts`,
		owner, now.Add(lease), now).Scan(&j.ID, &j.Kind, &j.Payload, &j.Attempts, &j.Max)
	return &j, norm(err)
}

func (s *Store) CompleteCleanupJob(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE cleanup_jobs SET state='succeeded', updated_at=now(),
		        lease_owner=NULL, lease_expires_at=NULL WHERE id=$1`, id)
	return wrap("complete cleanup job", err)
}

// FailCleanupJob retries with exponential backoff until the budget is spent.
// A permanently failing job stays visible in the backlog metric rather than
// disappearing silently.
func (s *Store) FailCleanupJob(ctx context.Context, id uuid.UUID, attempts, max int, msg string) error {
	if attempts >= max {
		_, err := s.pool.Exec(ctx,
			`UPDATE cleanup_jobs SET state='failed', error_message=$2, updated_at=now(),
			        lease_owner=NULL, lease_expires_at=NULL WHERE id=$1`, id, msg)
		return wrap("fail cleanup job", err)
	}
	backoff := time.Duration(1<<uint(attempts)) * 30 * time.Second
	_, err := s.pool.Exec(ctx,
		`UPDATE cleanup_jobs SET state='queued', run_after=$2, error_message=$3, updated_at=now(),
		        lease_owner=NULL, lease_expires_at=NULL WHERE id=$1`,
		id, s.Clock.Now().Add(backoff), msg)
	return wrap("retry cleanup job", err)
}

// ExpireStories schedules physical object cleanup for items whose 24 hours are
// up. Logical expiry never depends on this running: every read already refuses
// an expired item. This job only removes bytes.
func (s *Store) ExpireStories(ctx context.Context, limit int) (int, error) {
	now := s.Clock.Now()
	tag, err := s.pool.Exec(ctx, `
		WITH due AS (
			SELECT id FROM story_items
			WHERE state = 'published' AND expires_at IS NOT NULL AND expires_at <= $1
			ORDER BY expires_at LIMIT $2
			FOR UPDATE SKIP LOCKED
		), scheduled AS (
			INSERT INTO cleanup_jobs (kind, payload, run_after)
			SELECT 'delete_objects', jsonb_build_object(
				'object_keys', coalesce(jsonb_agg(mv.object_key), '[]'::jsonb),
				'story_id', d.id::text), $1
			FROM due d LEFT JOIN media_variants mv ON mv.story_item_id = d.id
			GROUP BY d.id
			RETURNING 1
		)
		UPDATE story_items SET state = 'deleted', deleted_at = $1
		WHERE id IN (SELECT id FROM due)`, now, limit)
	if err != nil {
		return 0, wrap("expire stories", err)
	}
	return int(tag.RowsAffected()), nil
}

// GCAbandonedUploads removes upload intents that were authorized but never
// finalized, along with whatever bytes were pushed to their object key.
func (s *Store) GCAbandonedUploads(ctx context.Context, limit int) (int, error) {
	now := s.Clock.Now()
	tag, err := s.pool.Exec(ctx, `
		WITH due AS (
			SELECT id, object_key FROM upload_intents
			WHERE state = 'issued' AND expires_at <= $1
			ORDER BY expires_at LIMIT $2
			FOR UPDATE SKIP LOCKED
		), scheduled AS (
			INSERT INTO cleanup_jobs (kind, payload, run_after)
			SELECT 'delete_objects', jsonb_build_object('object_keys', jsonb_agg(object_key)), $1
			FROM due
			-- An aggregate over zero rows still yields one row. Without this
			-- guard every idle sweep would enqueue an empty cleanup job.
			HAVING count(*) > 0
			RETURNING 1
		)
		UPDATE upload_intents SET state='expired' WHERE id IN (SELECT id FROM due)`, now, limit)
	if err != nil {
		return 0, wrap("gc abandoned uploads", err)
	}
	return int(tag.RowsAffected()), nil
}

// PurgeReplies deletes private replies past their disclosed retention period.
func (s *Store) PurgeReplies(ctx context.Context) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM replies WHERE purge_after <= $1`, s.Clock.Now())
	return int(tag.RowsAffected()), wrap("purge replies", err)
}

// PurgeViews deletes view records on their short disclosed schedule, once the
// Story they belong to is long gone.
func (s *Store) PurgeViews(ctx context.Context, retention time.Duration) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM story_views WHERE delivered_at <= $1`, s.Clock.Now().Add(-retention))
	return int(tag.RowsAffected()), wrap("purge views", err)
}

// PurgeExpiredAuth removes spent OAuth flows and pending authorizations.
func (s *Store) PurgeExpiredAuth(ctx context.Context) error {
	now := s.Clock.Now()
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM oauth_flows WHERE expires_at < $1 - make_interval(secs => 3600)`, now); err != nil {
		return wrap("purge oauth flows", err)
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM pending_logins WHERE expires_at < $1 - make_interval(secs => 3600)`, now)
	return wrap("purge pending logins", err)
}

// OpsSnapshot is the operational visibility the health endpoint reports.
type OpsSnapshot struct {
	MediaBacklog         int
	OldestMediaJobAge    time.Duration
	MediaFailures        int
	CleanupBacklog       int
	CleanupFailures      int
	PublishedStories     int
	PendingObjectDeletes int
}

func (s *Store) Ops(ctx context.Context) (OpsSnapshot, error) {
	var o OpsSnapshot
	var oldest *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM media_jobs   WHERE state IN ('queued','leased')),
			(SELECT min(created_at) FROM media_jobs WHERE state IN ('queued','leased')),
			(SELECT count(*) FROM media_jobs   WHERE state = 'failed'),
			(SELECT count(*) FROM cleanup_jobs WHERE state IN ('queued','leased')),
			(SELECT count(*) FROM cleanup_jobs WHERE state = 'failed'),
			(SELECT count(*) FROM story_items  WHERE state = 'published' AND expires_at > now()),
			(SELECT count(*) FROM cleanup_jobs WHERE kind='delete_objects' AND state IN ('queued','leased'))`).
		Scan(&o.MediaBacklog, &oldest, &o.MediaFailures, &o.CleanupBacklog,
			&o.CleanupFailures, &o.PublishedStories, &o.PendingObjectDeletes)
	if err != nil {
		return o, wrap("ops snapshot", err)
	}
	if oldest != nil {
		o.OldestMediaJobAge = s.Clock.Now().Sub(*oldest)
	}
	return o, nil
}

// ReapExpiredLeases is a safety net: a worker that died mid-job has its lease
// released so another worker can pick the job up.
func (s *Store) ReapExpiredLeases(ctx context.Context) error {
	now := s.Clock.Now()
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE media_jobs SET state='queued', lease_owner=NULL, lease_expires_at=NULL
			WHERE state='leased' AND lease_expires_at < $1 AND attempts < max_attempts`, now); err != nil {
			return wrap("reap media leases", err)
		}
		_, err := tx.Exec(ctx, `
			UPDATE cleanup_jobs SET state='queued', lease_owner=NULL, lease_expires_at=NULL
			WHERE state='leased' AND lease_expires_at < $1 AND attempts < max_attempts`, now)
		return wrap("reap cleanup leases", err)
	})
}
