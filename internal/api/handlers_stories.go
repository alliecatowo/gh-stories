package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/httpx"
	"github.com/alliecatowo/gh-stories/internal/store"
)

// storyForCaller resolves a Story the caller may see, or the caller's own item
// in any state. Every failure mode returns the same 404.
func (s *Server) storyForCaller(r *http.Request, u *domain.User, id uuid.UUID) (*domain.StoryItem, error) {
	it, err := s.Store.StoryForViewer(r.Context(), u.ID, u.GitHubID, id)
	if err == nil {
		return it, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, httpx.Internal(err)
	}
	// Fall back to the owner view so an author can poll a processing item.
	it, err = s.Store.OwnStory(r.Context(), u.ID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, httpx.NotFound()
		}
		return nil, httpx.Internal(err)
	}
	return it, nil
}

func (s *Server) getStory(w http.ResponseWriter, r *http.Request) error {
	id, err := pathUUID(r, "storyId")
	if err != nil {
		return err
	}
	if u := caller(r); u != nil {
		if u.Active() {
			it, err := s.storyForCaller(r, u, id)
			if err != nil {
				return err
			}
			seen, err := s.Store.Seen(r.Context(), id, u.ID)
			if err != nil {
				return httpx.Internal(err)
			}
			reaction, err := s.Store.MyReaction(r.Context(), id, u.ID)
			if err != nil {
				return httpx.Internal(err)
			}
			opts := presentOpts{viewer: u, seen: seen, myReaction: reaction}
			if it.AuthorUserID == u.ID {
				views, reacts, err := s.Store.Counts(r.Context(), id)
				if err != nil {
					return httpx.Internal(err)
				}
				if anon, err := s.Store.AnonymousViewCount(r.Context(), id); err == nil {
					views += anon
				}
				opts.views, opts.reactions = &views, &reacts
			}
			httpx.WriteJSON(w, http.StatusOK, presentStory(it, opts))
			return nil
		}
	}
	// Anonymous caller: only a live public Story is visible. Anything else
	// is the same 404 an unauthorized signed-in caller would get.
	it, err := s.Store.PublicStory(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return httpx.NotFound()
		}
		return httpx.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusOK, presentStory(it, presentOpts{}))
	return nil
}

type storyUpdateRequest struct {
	Caption        *string `json:"caption"`
	AltText        *string `json:"alt_text"`
	Visibility     *string `json:"visibility"`
	AudienceListID *string `json:"audience_list_id"`
	AllowReplies   *bool   `json:"allow_replies"`
	AllowReactions *bool   `json:"allow_reactions"`
}

// patchStory edits an author's own item. Narrowing the audience takes effect
// on the very next media request, because the gateway re-evaluates the
// visibility predicate every time rather than trusting an earlier grant.
func (s *Server) patchStory(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	id, err := pathUUID(r, "storyId")
	if err != nil {
		return err
	}
	var req storyUpdateRequest
	if err := httpx.DecodeJSON(w, r, &req, 8<<10); err != nil {
		return err
	}
	if req.Caption != nil && len(*req.Caption) > 500 {
		return httpx.FieldError("caption", "Captions are limited to 500 characters.")
	}
	if req.AltText != nil && len(*req.AltText) > 500 {
		return httpx.FieldError("alt_text", "Descriptions are limited to 500 characters.")
	}
	var vis *domain.Visibility
	if req.Visibility != nil {
		v := domain.Visibility(*req.Visibility)
		if !v.Valid() {
			return httpx.FieldError("visibility", "Unknown audience.")
		}
		vis = &v
	}
	listID, err := s.resolveAudienceList(r, u, vis, req.AudienceListID)
	if err != nil {
		return err
	}
	if err := s.Store.UpdateStory(r.Context(), u.ID, id,
		req.Caption, req.AltText, vis, listID, req.AllowReplies, req.AllowReactions); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return httpx.NotFound()
		}
		return httpx.Internal(err)
	}
	return s.getStory(w, r)
}

// resolveAudienceList validates that a custom-list selection names a list the
// caller actually owns.
func (s *Server) resolveAudienceList(r *http.Request, u *domain.User,
	vis *domain.Visibility, raw *string) (*uuid.UUID, error) {
	if vis == nil || *vis != domain.VisibilityCustomList {
		return nil, nil
	}
	if raw == nil || *raw == "" {
		return nil, httpx.FieldError("audience_list_id", "Choose a list for a custom audience.")
	}
	id, err := uuid.Parse(*raw)
	if err != nil {
		return nil, httpx.FieldError("audience_list_id", "Not a valid list.")
	}
	if _, err := s.Store.AudienceList(r.Context(), u.ID, id); err != nil {
		// Not yours, or does not exist.
		return nil, httpx.FieldError("audience_list_id", "Not a valid list.")
	}
	return &id, nil
}

// deleteStory removes server access immediately and schedules physical
// cleanup. Logical deletion is exact; object removal is an operational target.
func (s *Server) deleteStory(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	id, err := pathUUID(r, "storyId")
	if err != nil {
		return err
	}
	if err := s.Store.DeleteStory(r.Context(), u.ID, id, false, ""); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return httpx.NotFound()
		}
		return httpx.Internal(err)
	}
	httpx.NoContent(w)
	return nil
}

// getViewers returns the named viewer list, to the author only.
func (s *Server) getViewers(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	id, err := pathUUID(r, "storyId")
	if err != nil {
		return err
	}
	viewers, err := s.Store.Viewers(r.Context(), u.ID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Not the author, or no such Story. Same answer either way.
			return httpx.NotFound()
		}
		return httpx.Internal(err)
	}
	out := viewerList{StoryID: id.String(), Total: len(viewers)}
	out.Viewers = make([]viewerEntry, 0, len(viewers))
	for _, v := range viewers {
		out.Viewers = append(out.Viewers, viewerEntry{
			User: presentIdentity(v.Identity), ViewedAt: v.ViewedAt, Reaction: v.Reaction,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// postView records the client's separate rendering acknowledgement.
//
// The view itself is recorded by the media gateway when content-bearing bytes
// are delivered. This endpoint cannot create a view for an unauthorized
// caller, because it resolves the Story through the same predicate first.
// Anonymous callers may acknowledge a public Story; it is a no-op 204 and
// stores nothing, per the launch privacy decision (count anonymous delivery
// only, retain no per-viewer or IP record).
func (s *Server) postView(w http.ResponseWriter, r *http.Request) error {
	id, err := pathUUID(r, "storyId")
	if err != nil {
		return err
	}
	u := caller(r)
	if u == nil {
		if _, err := s.Store.PublicStory(r.Context(), id); err != nil {
			return httpx.NotFound()
		}
		httpx.NoContent(w)
		return nil
	}
	if !u.Active() {
		return httpx.Forbidden("This account is suspended.")
	}
	it, err := s.Store.StoryForViewer(r.Context(), u.ID, u.GitHubID, id)
	if err != nil {
		return httpx.NotFound()
	}
	if it.AuthorUserID == u.ID {
		// An author viewing their own Story is not a viewer.
		httpx.NoContent(w)
		return nil
	}
	if err := s.Store.AcknowledgeView(r.Context(), id, u.ID); err != nil {
		return httpx.Internal(err)
	}
	httpx.NoContent(w)
	return nil
}

// putReaction sets or replaces the caller's single reaction.
func (s *Server) putReaction(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	id, err := pathUUID(r, "storyId")
	if err != nil {
		return err
	}
	var req struct {
		Emoji string `json:"emoji"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 1<<10); err != nil {
		return err
	}
	if !domain.ValidReaction(req.Emoji) {
		return httpx.FieldError("emoji", "That reaction is not available.")
	}
	it, err := s.Store.StoryForViewer(r.Context(), u.ID, u.GitHubID, id)
	if err != nil {
		return httpx.NotFound()
	}
	if !it.AllowReactions {
		return httpx.Forbidden("Reactions are turned off for this Story.")
	}
	if err := s.Store.SetReaction(r.Context(), id, u.ID, it.AuthorUserID, req.Emoji); err != nil {
		return httpx.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"story_id": id.String(), "emoji": req.Emoji, "created_at": s.Clock.Now(),
	})
	return nil
}

func (s *Server) deleteReaction(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	id, err := pathUUID(r, "storyId")
	if err != nil {
		return err
	}
	if err := s.Store.ClearReaction(r.Context(), id, u.ID); err != nil {
		return httpx.Internal(err)
	}
	httpx.NoContent(w)
	return nil
}

// postReply sends a private reply to the Story's author.
func (s *Server) postReply(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	id, err := pathUUID(r, "storyId")
	if err != nil {
		return err
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 4<<10); err != nil {
		return err
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return httpx.FieldError("body", "Write something first.")
	}
	if len([]rune(body)) > 500 {
		return httpx.FieldError("body", "Replies are limited to 500 characters.")
	}

	key := idempotencyKey(r)
	if found, status, stored, conflict, err := s.Store.IdempotentReplay(
		r.Context(), u.ID, "reply", key, map[string]any{"story": id.String(), "body": body}); err != nil {
		return httpx.Internal(err)
	} else if conflict {
		return httpx.Conflict("That idempotency key was used for a different request.")
	} else if found {
		writeRaw(w, status, stored)
		return nil
	}

	it, err := s.Store.StoryForViewer(r.Context(), u.ID, u.GitHubID, id)
	if err != nil {
		return httpx.NotFound()
	}
	if it.AuthorUserID == u.ID {
		return httpx.Forbidden("You cannot reply to your own Story.")
	}
	if !it.AllowReplies {
		return httpx.Forbidden("Replies are turned off for this Story.")
	}

	replyID, err := s.Store.CreateReply(r.Context(), id, u.ID, it.AuthorUserID,
		body, s.Cfg.ReplyRetention)
	if err != nil {
		return httpx.Internal(err)
	}
	out := replyResponse{
		ID: replyID.String(), StoryID: id.String(),
		Sender: presentUser(u), Body: body, CreatedAt: s.Clock.Now(),
	}
	if it.Author != nil {
		out.Recipient = presentIdentity(*it.Author)
	}
	_ = s.Store.RememberIdempotent(r.Context(), u.ID, "reply", key,
		map[string]any{"story": id.String(), "body": body}, http.StatusCreated, out)
	httpx.WriteJSON(w, http.StatusCreated, out)
	return nil
}

// getInbox returns the caller's private inbox. Only the participants of an
// exchange can read it; there is no way to enumerate anyone else's.
func (s *Server) getInbox(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 30)
	entries, unread, next, err := s.Store.Inbox(r.Context(), u.ID, limit, r.URL.Query().Get("cursor"))
	if err != nil {
		return httpx.Internal(err)
	}
	out := inboxResponse{Unread: unread, NextCursor: next}
	out.Entries = make([]inboxEntry, 0, len(entries))
	for _, e := range entries {
		out.Entries = append(out.Entries, presentInboxEntry(e))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) postInboxRead(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	var req struct {
		EventIDs []string `json:"event_ids"`
		All      bool     `json:"all"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 16<<10); err != nil {
		return err
	}
	if len(req.EventIDs) > 200 {
		return httpx.BadRequest("Mark at most 200 entries at a time.")
	}
	ids := make([]uuid.UUID, 0, len(req.EventIDs))
	for _, raw := range req.EventIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return httpx.BadRequest("Not a valid inbox entry id.")
		}
		ids = append(ids, id)
	}
	if err := s.Store.MarkInboxRead(r.Context(), u.ID, ids, req.All); err != nil {
		return httpx.Internal(err)
	}
	httpx.NoContent(w)
	return nil
}
