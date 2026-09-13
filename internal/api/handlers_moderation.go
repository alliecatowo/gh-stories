package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/httpx"
	"github.com/alliecatowo/gh-stories/internal/store"
)

var reportReasons = map[string]bool{
	"spam": true, "harassment": true, "nudity": true,
	"violence": true, "self_harm": true, "illegal": true, "other": true,
}

// postReport files a report. It deliberately does not echo back any of the
// reported content: a reporter must not be able to use this as a read channel
// for someone else's private Story.
func (s *Server) postReport(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	var req struct {
		SubjectKind string `json:"subject_kind"`
		StoryID     string `json:"story_id"`
		Login       string `json:"login"`
		Reason      string `json:"reason"`
		Details     string `json:"details"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 8<<10); err != nil {
		return err
	}
	if !reportReasons[req.Reason] {
		return httpx.FieldError("reason", "Choose a reason.")
	}
	if len([]rune(req.Details)) > 1000 {
		return httpx.FieldError("details", "Details are limited to 1000 characters.")
	}

	var storyID, subjectID *uuid.UUID
	switch req.SubjectKind {
	case "story":
		id, err := uuid.Parse(req.StoryID)
		if err != nil {
			return httpx.FieldError("story_id", "Not a valid Story.")
		}
		// Only a Story the reporter can actually see may be reported, so this
		// endpoint cannot be used to probe for private Story ids.
		if _, err := s.Store.StoryForViewer(r.Context(), u.ID, u.GitHubID, id); err != nil {
			return httpx.NotFound()
		}
		storyID = &id
	case "user":
		target, err := s.Store.UserByLogin(r.Context(), nil, strings.TrimPrefix(req.Login, "@"))
		if err != nil {
			return httpx.NotFound()
		}
		subjectID = &target.ID
	default:
		return httpx.FieldError("subject_kind", "Report a Story or a person.")
	}

	id, err := s.Store.CreateReport(r.Context(), u.ID, req.SubjectKind,
		storyID, subjectID, req.Reason, req.Details)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"id": id.String(), "state": "open", "reason": req.Reason,
	})
	return nil
}

// listReports is the minimal moderation queue. The route is wrapped in
// requireModerator, which is deny-by-default and answers 404 rather than 403
// so the existence of the queue is not advertised.
func (s *Server) listReports(w http.ResponseWriter, r *http.Request) error {
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "open"
	}
	switch state {
	case "open", "actioned", "dismissed":
	default:
		return httpx.BadRequest("Unknown report state.")
	}
	reports, err := s.Store.ListReports(r.Context(), state, 50)
	if err != nil {
		return httpx.Internal(err)
	}
	out := make([]map[string]any, 0, len(reports))
	for _, rep := range reports {
		entry := map[string]any{
			"id": rep.ID.String(), "subject_kind": rep.SubjectKind,
			"reason": rep.Reason, "details": rep.Details,
			"state": rep.State, "created_at": rep.CreatedAt,
			"reporter": presentIdentity(rep.Reporter),
		}
		if rep.StoryID != nil {
			entry["story_id"] = rep.StoryID.String()
		}
		if rep.Subject != nil {
			entry["subject"] = presentIdentity(*rep.Subject)
		}
		out = append(out, entry)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"reports": out})
	return nil
}

// postModerationAction records an action and applies it.
func (s *Server) postModerationAction(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	var req struct {
		Kind     string `json:"kind"`
		ReportID string `json:"report_id"`
		StoryID  string `json:"story_id"`
		Login    string `json:"login"`
		Reason   string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 8<<10); err != nil {
		return err
	}
	if len([]rune(req.Reason)) > 500 {
		return httpx.FieldError("reason", "Reason is limited to 500 characters.")
	}

	var reportID, storyID, targetID *uuid.UUID
	if req.ReportID != "" {
		id, err := uuid.Parse(req.ReportID)
		if err != nil {
			return httpx.FieldError("report_id", "Not a valid report.")
		}
		reportID = &id
	}
	if req.StoryID != "" {
		id, err := uuid.Parse(req.StoryID)
		if err != nil {
			return httpx.FieldError("story_id", "Not a valid Story.")
		}
		storyID = &id
	}
	if req.Login != "" {
		target, err := s.Store.UserByLogin(r.Context(), nil, strings.TrimPrefix(req.Login, "@"))
		if err != nil {
			return httpx.NotFound()
		}
		targetID = &target.ID
	}

	switch req.Kind {
	case "remove_story":
		if storyID == nil {
			return httpx.FieldError("story_id", "Which Story?")
		}
		if err := s.Store.DeleteStory(r.Context(), u.ID, *storyID, true, req.Reason); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return httpx.NotFound()
			}
			return httpx.Internal(err)
		}
	case "suspend_user":
		if targetID == nil {
			return httpx.FieldError("login", "Which account?")
		}
		if err := s.Store.SuspendUser(r.Context(), nil, *targetID, req.Reason); err != nil {
			return httpx.Internal(err)
		}
	case "unsuspend_user":
		if targetID == nil {
			return httpx.FieldError("login", "Which account?")
		}
		if err := s.Store.UnsuspendUser(r.Context(), nil, *targetID); err != nil {
			return httpx.Internal(err)
		}
	case "dismiss_report":
		if reportID == nil {
			return httpx.FieldError("report_id", "Which report?")
		}
	default:
		return httpx.FieldError("kind", "Unknown moderation action.")
	}

	if err := s.Store.RecordModerationAction(r.Context(), u.ID, req.Kind,
		reportID, storyID, targetID, req.Reason); err != nil {
		return httpx.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"kind": req.Kind, "recorded": true})
	return nil
}
