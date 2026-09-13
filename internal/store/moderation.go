package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

type Report struct {
	ID          uuid.UUID
	SubjectKind string
	StoryID     *uuid.UUID
	Subject     *domain.Identity
	Reporter    domain.Identity
	Reason      string
	Details     string
	State       string
	CreatedAt   time.Time
}

// CreateReport files a report. Reported evidence is only reachable by
// moderators; a reporter cannot read back other people's content through it.
func (s *Store) CreateReport(ctx context.Context, reporter uuid.UUID, kind string,
	storyID *uuid.UUID, subject *uuid.UUID, reason, details string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO reports (reporter_user_id, subject_kind, story_item_id, subject_user_id, reason, details)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		reporter, kind, storyID, subject, reason, details).Scan(&id)
	return id, wrap("create report", err)
}

// ListReports is the moderation queue. Authorization is the caller's
// responsibility and is deny-by-default at the route.
func (s *Store) ListReports(ctx context.Context, state string, limit int) ([]Report, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.subject_kind, r.story_item_id, r.reason, r.details, r.state, r.created_at,
			rgi.login, rgi.avatar_url, rgi.profile_url, ru.github_user_id,
			COALESCE(sgi.login,''), COALESCE(sgi.avatar_url,''), COALESCE(sgi.profile_url,''),
			COALESCE(su.github_user_id, 0)
		FROM reports r
		JOIN users ru ON ru.id = r.reporter_user_id
		JOIN github_identities rgi ON rgi.github_user_id = ru.github_user_id
		LEFT JOIN users su ON su.id = COALESCE(r.subject_user_id,
			(SELECT author_user_id FROM story_items WHERE id = r.story_item_id))
		LEFT JOIN github_identities sgi ON sgi.github_user_id = su.github_user_id
		WHERE r.state = $1 ORDER BY r.created_at DESC LIMIT $2`, state, limit)
	if err != nil {
		return nil, wrap("list reports", err)
	}
	defer rows.Close()
	var out []Report
	for rows.Next() {
		var r Report
		var subject domain.Identity
		var rGH, sGH int64
		if err := rows.Scan(&r.ID, &r.SubjectKind, &r.StoryID, &r.Reason, &r.Details,
			&r.State, &r.CreatedAt,
			&r.Reporter.Login, &r.Reporter.AvatarURL, &r.Reporter.ProfileURL, &rGH,
			&subject.Login, &subject.AvatarURL, &subject.ProfileURL, &sGH); err != nil {
			return nil, err
		}
		r.Reporter.GitHubID = domain.GitHubID(rGH)
		if sGH != 0 {
			subject.GitHubID = domain.GitHubID(sGH)
			r.Subject = &subject
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecordModerationAction writes the audit row. Moderation records are kept
// minimal and their retention is documented in docs/privacy.md.
func (s *Store) RecordModerationAction(ctx context.Context, moderator uuid.UUID, kind string,
	reportID, storyID, target *uuid.UUID, reason string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO moderation_actions
			(moderator_user_id, kind, report_id, story_item_id, target_user_id, reason)
		VALUES ($1,$2,$3,$4,$5,$6)`, moderator, kind, reportID, storyID, target, reason)
	if err != nil {
		return wrap("record moderation action", err)
	}
	if reportID != nil {
		newState := "actioned"
		if kind == "dismiss_report" {
			newState = "dismissed"
		}
		_, err = s.pool.Exec(ctx, `
			UPDATE reports SET state=$2, resolved_at=now(), resolved_by_user_id=$3
			WHERE id=$1 AND state='open'`, *reportID, newState, moderator)
	}
	return wrap("resolve report", err)
}
