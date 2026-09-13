//go:build ghs_testidp

package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/alliecatowo/gh-stories/internal/config"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/httpx"
)

// This file exists ONLY in a build tagged `ghs_testidp`.
//
// The local demo must exercise the real API, database, object storage,
// processing worker, clients and privacy rules. The only thing it may replace
// is the external identity boundary — signing in as a sample account without
// going to github.com. That substitution lives behind a build tag so it is not
// merely disabled in a release artifact, it is not compiled into one at all.
//
// There are two independent guards:
//  1. this build tag, and
//  2. a runtime refusal when GHS_ENV=production or the test provider flag is
//     off, enforced in internal/auth.
//
// A release build therefore cannot be tricked into enabling it by an
// environment variable.

// testLoginService is implemented by the auth adapter only under the same tag.
type testLoginService interface {
	CompleteTestAuthorization(ctx context.Context, login string) (*AuthResult, error)
}

// registerTestRoutes adds the demo sign-in route.
func (s *Server) registerTestRoutes(r chi.Router) {
	svc, ok := s.Auth.(testLoginService)
	if !ok {
		return
	}
	if s.Cfg.Env == config.EnvProduction || !s.Cfg.TestIdentityProvider {
		return
	}
	s.Log.Warn("TEST IDENTITY PROVIDER ENABLED — this build must never be published",
		"env", string(s.Cfg.Env))

	r.Post("/auth/test/login", h(func(w http.ResponseWriter, req *http.Request) error {
		var body struct {
			Login string `json:"login"`
		}
		if err := httpx.DecodeJSON(w, req, &body, 1<<10); err != nil {
			return err
		}
		login := strings.TrimSpace(strings.TrimPrefix(body.Login, "@"))
		if login == "" || len(login) > 39 {
			return httpx.FieldError("login", "Give a sample account login.")
		}
		res, err := svc.CompleteTestAuthorization(req.Context(), login)
		if err != nil {
			return httpx.Internal(err)
		}
		token, _, err := s.Auth.IssueSession(req.Context(), res.User.ID,
			domain.ClientWeb, "Local demo", req.UserAgent())
		if err != nil {
			return httpx.Internal(err)
		}
		setSessionCookie(w, s.Cfg, token)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"token": token,
			"user":  presentUser(res.User),
			"note":  "test identity provider — local/test builds only",
		})
		return nil
	}))
}

// TestIdentityRoutesCompiled reports whether the demo-only sign-in route was
// compiled into this binary.
func TestIdentityRoutesCompiled() bool { return true }
