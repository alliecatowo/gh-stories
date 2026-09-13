package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/httpx"
	"github.com/alliecatowo/gh-stories/internal/store"
)

// getFeed returns the caller's authorized feed.
//
// Fetching the feed never records a view: views are recorded when the media
// authorization gateway actually delivers content-bearing bytes.
func (s *Server) getFeed(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	includeMuted := r.URL.Query().Get("include_muted") == "true"

	groups, me, err := s.Store.Feed(r.Context(), u.ID, u.GitHubID, includeMuted, limit)
	if err != nil {
		return httpx.Internal(err)
	}
	version, err := s.Store.FeedVersion(r.Context(), u.ID, u.GitHubID)
	if err != nil {
		return httpx.Internal(err)
	}

	reactions, err := s.reactionsFor(r, u, groups, me)
	if err != nil {
		return err
	}

	out := feedResponse{ServerTime: s.Clock.Now(), Version: version}
	out.Groups = make([]authorGroup, 0, len(groups))
	for _, g := range groups {
		out.Groups = append(out.Groups, presentGroup(g, u, reactions))
	}
	if me != nil {
		g := presentGroup(*me, u, reactions)
		// The author sees their own counts.
		if err := s.attachOwnerCounts(r, g.Items); err != nil {
			return err
		}
		out.Me = &g
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) reactionsFor(r *http.Request, u *domain.User,
	groups []store.FeedGroup, me *store.FeedGroup) (map[uuid.UUID]string, error) {
	var ids []uuid.UUID
	for _, g := range groups {
		for _, it := range g.Items {
			ids = append(ids, it.ID)
		}
	}
	if me != nil {
		for _, it := range me.Items {
			ids = append(ids, it.ID)
		}
	}
	reactions, err := s.Store.MyReactions(r.Context(), u.ID, ids)
	if err != nil {
		return nil, httpx.Internal(err)
	}
	return reactions, nil
}

func (s *Server) attachOwnerCounts(r *http.Request, items []storyItem) error {
	for i := range items {
		id, err := uuid.Parse(items[i].ID)
		if err != nil {
			continue
		}
		views, reacts, err := s.Store.Counts(r.Context(), id)
		if err != nil {
			return httpx.Internal(err)
		}
		v, rc := views, reacts
		items[i].ViewerCount = &v
		items[i].ReactionCount = &rc
	}
	return nil
}

// postStatus answers a batch avatar lookup.
//
// It reveals only Stories the caller may access: an inaccessible active Story
// and no Story at all are indistinguishable, with no counts, timestamps or
// audiences. Otherwise this endpoint would be a private-Story oracle.
func (s *Server) postStatus(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	var req struct {
		GitHubUserIDs []int64  `json:"github_user_ids"`
		Logins        []string `json:"logins"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 16<<10); err != nil {
		return err
	}
	const maxBatch = 100
	if len(req.GitHubUserIDs) > maxBatch || len(req.Logins) > maxBatch {
		return httpx.BadRequest("Look up at most 100 accounts per request.")
	}
	if len(req.GitHubUserIDs) == 0 && len(req.Logins) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"entries": []ringStatus{}})
		return nil
	}

	ids := make([]domain.GitHubID, 0, len(req.GitHubUserIDs)+len(req.Logins))
	seen := map[domain.GitHubID]bool{}
	for _, raw := range req.GitHubUserIDs {
		id := domain.GitHubID(raw)
		if raw > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(req.Logins) > 0 {
		byLogin, err := s.Store.IdentitiesByLogins(r.Context(), req.Logins)
		if err != nil {
			return httpx.Internal(err)
		}
		for _, id := range byLogin {
			if !seen[id.GitHubID] {
				seen[id.GitHubID] = true
				ids = append(ids, id.GitHubID)
			}
		}
	}

	statuses, err := s.Store.RingStatuses(r.Context(), u.ID, u.GitHubID, ids)
	if err != nil {
		return httpx.Internal(err)
	}
	entries := make([]ringStatus, 0, len(statuses))
	for _, st := range statuses {
		entries = append(entries, ringStatus{
			GitHubUserID: int64(st.GitHubID), Login: st.Login,
			HasActive: st.HasActive, HasUnseen: st.HasUnseen,
			Muted: st.Muted, IsSelf: st.IsSelf,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"entries": entries})
	return nil
}

// getAuthorStories returns one author's authorized active sequence.
func (s *Server) getAuthorStories(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	login := strings.TrimPrefix(pathParam(r, "login"), "@")
	group, err := s.Store.AuthorSequence(r.Context(), u.ID, u.GitHubID, login)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// No such account, or nothing this caller may see. Deliberately
			// the same answer.
			return httpx.NotFound()
		}
		return httpx.Internal(err)
	}
	reactions, err := s.reactionsFor(r, u, []store.FeedGroup{*group}, nil)
	if err != nil {
		return err
	}
	out := presentGroup(*group, u, reactions)
	if group.Author.GitHubID == u.GitHubID {
		if err := s.attachOwnerCounts(r, out.Items); err != nil {
			return err
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// getOwnStories returns the caller's own items, including ones still
// processing, so a client can poll a publication it started.
func (s *Server) getOwnStories(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	items, err := s.Store.OwnSequence(r.Context(), u.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	out := authorGroup{Author: presentUser(u)}
	for _, it := range items {
		out.Items = append(out.Items, presentStory(it, presentOpts{viewer: u}))
	}
	if err := s.attachOwnerCounts(r, out.Items); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// getUserLookup backs username search. There is no Explore feed; discovery is
// imports, this lookup, and authorized rings on GitHub.
func (s *Server) getUserLookup(w http.ResponseWriter, r *http.Request) error {
	if _, err := mustCaller(r); err != nil {
		return err
	}
	q := r.URL.Query().Get("q")
	if len(q) > 64 {
		return httpx.BadRequest("Search term is too long.")
	}
	ids, err := s.Store.SearchIdentities(r.Context(), q, 10)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": presentIdentities(ids)})
	return nil
}
