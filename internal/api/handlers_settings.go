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

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	lists, err := s.Store.ListAudienceLists(ctx, u.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	hidden, err := s.Store.ListSimple(ctx, "hide_rules", "author_user_id", "hidden_github_user_id", u.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	muted, err := s.Store.ListSimple(ctx, "mutes", "muter_user_id", "muted_github_user_id", u.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	blocked, err := s.Store.ListSimple(ctx, "blocks", "blocker_user_id", "blocked_github_user_id", u.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	sessions, err := s.Store.ListSessions(ctx, u.ID)
	if err != nil {
		return httpx.Internal(err)
	}

	out := settingsResponse{
		DefaultVisibility:     string(u.DefaultVisibility),
		DefaultAllowReplies:   u.DefaultAllowReplies,
		DefaultAllowReactions: u.DefaultAllowReactions,
		HiddenFrom:            presentIdentities(hidden),
		Muted:                 presentIdentities(muted),
		Blocked:               presentIdentities(blocked),
		ReplyRetentionDays:    int(s.Cfg.ReplyRetention.Hours() / 24),
	}
	if u.DefaultAudienceListID != nil {
		out.DefaultAudienceListID = u.DefaultAudienceListID.String()
	}
	out.AudienceLists = make([]audienceList, 0, len(lists))
	for _, l := range lists {
		out.AudienceLists = append(out.AudienceLists, presentAudienceList(l))
	}
	current := uuid.Nil
	if sess := callerSession(r); sess != nil {
		current = sess.ID
	}
	out.Sessions = make([]sessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		out.Sessions = append(out.Sessions, presentSession(sess, current))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) patchSettings(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	var req struct {
		DefaultVisibility     *string `json:"default_visibility"`
		DefaultAudienceListID *string `json:"default_audience_list_id"`
		DefaultAllowReplies   *bool   `json:"default_allow_replies"`
		DefaultAllowReactions *bool   `json:"default_allow_reactions"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 4<<10); err != nil {
		return err
	}
	var vis *domain.Visibility
	if req.DefaultVisibility != nil {
		v := domain.Visibility(*req.DefaultVisibility)
		if !v.Valid() {
			return httpx.FieldError("default_visibility", "Unknown audience.")
		}
		vis = &v
	}
	listID, err := s.resolveAudienceList(r, u, vis, req.DefaultAudienceListID)
	if err != nil {
		return err
	}
	clearList := vis != nil && *vis != domain.VisibilityCustomList
	if err := s.Store.UpdateSettings(r.Context(), u.ID, vis, listID, clearList,
		req.DefaultAllowReplies, req.DefaultAllowReactions); err != nil {
		return httpx.Internal(err)
	}
	return s.getSettings(w, r)
}

func (s *Server) listAudienceLists(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	lists, err := s.Store.ListAudienceLists(r.Context(), u.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	out := make([]audienceList, 0, len(lists))
	for _, l := range lists {
		out = append(out, presentAudienceList(l))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"lists": out})
	return nil
}

func (s *Server) createAudienceList(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	var req struct {
		Name   string   `json:"name"`
		Logins []string `json:"logins"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 64<<10); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 60 {
		return httpx.FieldError("name", "Give the list a name of up to 60 characters.")
	}
	if len(req.Logins) > 500 {
		return httpx.FieldError("logins", "A list holds at most 500 people.")
	}
	list, err := s.Store.CreateAudienceList(r.Context(), u.ID, name, req.Logins)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return httpx.Conflict("You already have a list with that name.")
		}
		return httpx.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusCreated, presentAudienceList(*list))
	return nil
}

func (s *Server) patchAudienceList(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	id, err := pathUUID(r, "listId")
	if err != nil {
		return err
	}
	var req struct {
		Name   *string   `json:"name"`
		Logins *[]string `json:"logins"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 64<<10); err != nil {
		return err
	}
	var logins []string
	replace := req.Logins != nil
	if replace {
		logins = *req.Logins
		if len(logins) > 500 {
			return httpx.FieldError("logins", "A list holds at most 500 people.")
		}
	}
	list, err := s.Store.UpdateAudienceList(r.Context(), u.ID, id, req.Name, logins, replace)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return httpx.NotFound()
		}
		return httpx.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusOK, presentAudienceList(*list))
	return nil
}

func (s *Server) deleteAudienceList(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	id, err := pathUUID(r, "listId")
	if err != nil {
		return err
	}
	if err := s.Store.DeleteAudienceList(r.Context(), u.ID, id); err != nil {
		return httpx.Internal(err)
	}
	httpx.NoContent(w)
	return nil
}

// deleteAccount removes the account and everything that cascades from it.
func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteAccount(r.Context(), u.ID); err != nil {
		return httpx.Internal(err)
	}
	httpx.NoContent(w)
	return nil
}
