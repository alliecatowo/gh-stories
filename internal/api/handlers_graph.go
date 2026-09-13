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

// resolveTarget turns a :login path parameter into a known GitHub identity.
// Relationships to people who have not joined Stories are preserved, so the
// target does not need to be a registered account.
func (s *Server) resolveTarget(r *http.Request) (domain.Identity, error) {
	login := strings.TrimPrefix(pathParam(r, "login"), "@")
	if login == "" || len(login) > 64 {
		return domain.Identity{}, httpx.NotFound()
	}
	id, err := s.Store.IdentityByLogin(r.Context(), nil, login)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return domain.Identity{}, httpx.NotFound()
		}
		return domain.Identity{}, httpx.Internal(err)
	}
	return id, nil
}

// putFollow follows on Stories. It never mutates GitHub's follow graph.
func (s *Server) putFollow(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	target, err := s.resolveTarget(r)
	if err != nil {
		return err
	}
	if target.GitHubID == u.GitHubID {
		return httpx.BadRequest("You cannot follow yourself.")
	}
	blocked, err := s.Store.BlockedEitherWay(r.Context(), nil, u.ID, u.GitHubID, uuid.Nil, target.GitHubID)
	if err != nil {
		return httpx.Internal(err)
	}
	if blocked {
		return httpx.Forbidden("You cannot follow this account.")
	}
	if err := s.Store.Follow(r.Context(), nil, u.ID, target.GitHubID, domain.ProvenanceNative); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return httpx.NotFound()
		}
		return httpx.Internal(err)
	}
	// Quiet in-product notification for the followed account, if they have one.
	if followee, err := s.Store.UserByGitHubID(r.Context(), nil, target.GitHubID); err == nil {
		_ = s.Store.NotifyFollow(r.Context(), followee.ID, u.ID)
	}
	httpx.WriteJSON(w, http.StatusOK, relationship{
		User: presentIdentity(target), Provenance: string(domain.ProvenanceNative),
	})
	return nil
}

// deleteFollow unfollows. The store leaves a tombstone so a later GitHub
// re-import will not silently resurrect the relationship.
func (s *Server) deleteFollow(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	target, err := s.resolveTarget(r)
	if err != nil {
		return err
	}
	if err := s.Store.Unfollow(r.Context(), u.ID, target.GitHubID); err != nil {
		return httpx.Internal(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) listFollowing(w http.ResponseWriter, r *http.Request) error {
	return s.listRelationships(w, r, true)
}

func (s *Server) listFollowers(w http.ResponseWriter, r *http.Request) error {
	return s.listRelationships(w, r, false)
}

func (s *Server) listRelationships(w http.ResponseWriter, r *http.Request, following bool) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 50)
	cursor := r.URL.Query().Get("cursor")
	var rows []store.RelationshipRow
	var next string
	if following {
		rows, next, err = s.Store.ListFollowing(r.Context(), u.ID, u.GitHubID, limit, cursor)
	} else {
		rows, next, err = s.Store.ListFollowers(r.Context(), u.ID, u.GitHubID, limit, cursor)
	}
	if err != nil {
		return httpx.Internal(err)
	}
	out := relationshipPage{NextCursor: next}
	out.Relationships = make([]relationship, 0, len(rows))
	for _, row := range rows {
		out.Relationships = append(out.Relationships, presentRelationship(row))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// toggleRelation backs mute, block and hide.
func (s *Server) toggleRelation(kind string, on bool) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		u, err := mustCaller(r)
		if err != nil {
			return err
		}
		target, err := s.resolveTarget(r)
		if err != nil {
			return err
		}
		if target.GitHubID == u.GitHubID {
			return httpx.BadRequest("You cannot do that to yourself.")
		}
		switch kind {
		case "mute":
			// Mute changes only the muting user's presentation, never the
			// other person's access permissions.
			err = s.Store.SetMute(r.Context(), u.ID, target.GitHubID, on)
		case "block":
			err = s.Store.SetBlock(r.Context(), u.ID, target.GitHubID, on)
		case "hide":
			err = s.Store.SetHide(r.Context(), u.ID, target.GitHubID, on)
		}
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return httpx.NotFound()
			}
			return httpx.Internal(err)
		}
		httpx.NoContent(w)
		return nil
	}
}

func (s *Server) listSimple(table, ownerCol, targetCol string) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		u, err := mustCaller(r)
		if err != nil {
			return err
		}
		ids, err := s.Store.ListSimple(r.Context(), table, ownerCol, targetCol, u.ID)
		if err != nil {
			return httpx.Internal(err)
		}
		out := relationshipPage{}
		out.Relationships = make([]relationship, 0, len(ids))
		for _, id := range ids {
			out.Relationships = append(out.Relationships, relationship{User: presentIdentity(id)})
		}
		httpx.WriteJSON(w, http.StatusOK, out)
		return nil
	}
}
