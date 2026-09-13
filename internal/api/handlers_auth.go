package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/config"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/httpx"
)

// getAuthStart redirects the browser into GitHub's authorization page. State
// and the PKCE verifier are created and kept server side; neither ever reaches
// a frontend bundle.
func (s *Server) getAuthStart(w http.ResponseWriter, r *http.Request) error {
	if s.Auth == nil || !s.Auth.Configured() {
		return httpx.Err(http.StatusServiceUnavailable, httpx.CodeUnavailable,
			"GitHub sign-in is not configured on this service.")
	}
	var pendingID *uuid.UUID
	if raw := r.URL.Query().Get("pending_login_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return httpx.BadRequest("Not a valid authorization request.")
		}
		pendingID = &id
	}
	purpose := "web_login"
	if pendingID != nil {
		purpose = "cli_approval"
	}
	authURL, err := s.Auth.StartAuthorization(r.Context(), purpose,
		safeReturnTo(r.URL.Query().Get("return_to")), pendingID)
	if err != nil {
		return httpx.Internal(err)
	}
	http.Redirect(w, r, authURL, http.StatusFound)
	return nil
}

// safeReturnTo accepts only a same-site relative path, so the callback cannot
// be turned into an open redirect.
func safeReturnTo(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") || strings.Contains(raw, ":") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/") {
		return ""
	}
	return u.String()
}

// getAuthCallback completes the authorization server side.
func (s *Server) getAuthCallback(w http.ResponseWriter, r *http.Request) error {
	if s.Auth == nil {
		return httpx.NotFound()
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		return httpx.BadRequest("That sign-in link is incomplete.")
	}
	res, err := s.Auth.CompleteAuthorization(r.Context(), code, state)
	if err != nil {
		// A replayed, expired or mismatched state ends here.
		return httpx.BadRequest("That sign-in link is no longer valid. Start again.")
	}

	// Where to go next is decided by the flow that was started, not by the
	// query string, so a crafted callback cannot redirect the user elsewhere.
	dest := s.Cfg.PublicURL.String() + "/account"
	if res.Flow != nil && res.Flow.Purpose == "cli_approval" && res.Flow.PendingLoginID != nil {
		dest = s.Cfg.PublicURL.String() + "/account/authorize?request=" + res.Flow.PendingLoginID.String()
	} else if res.Flow != nil && res.Flow.ReturnTo != "" {
		dest = s.Cfg.PublicURL.String() + res.Flow.ReturnTo
	}

	// Issue the web session for the account application.
	token, _, err := s.Auth.IssueSession(r.Context(), res.User.ID, domain.ClientWeb,
		"Account page", r.UserAgent())
	if err != nil {
		return httpx.Internal(err)
	}
	setSessionCookie(w, s.Cfg, token)
	http.Redirect(w, r, dest, http.StatusFound)
	return nil
}

// postPendingLogin creates a service-mediated client authorization.
//
// The CLI receives an unguessable polling secret and a short user code. The
// user opens a service URL, completes GitHub OAuth, sees the requesting client
// and the matching code, and explicitly approves. This is what makes CLI login
// work over SSH without ever touching the user's existing gh token.
func (s *Server) postPendingLogin(w http.ResponseWriter, r *http.Request) error {
	if s.Auth == nil {
		return httpx.NotFound()
	}
	var req struct {
		ClientKind  string `json:"client_kind"`
		ClientLabel string `json:"client_label"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 4<<10); err != nil {
		return err
	}
	kind := domain.ClientKind(req.ClientKind)
	if kind != domain.ClientCLI && kind != domain.ClientBrowserExtension {
		return httpx.FieldError("client_kind", "Unknown client.")
	}
	label := req.ClientLabel
	if len([]rune(label)) > 120 {
		label = string([]rune(label)[:120])
	}
	res, err := s.Auth.CreatePendingLogin(r.Context(), kind, label)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"pending_login_id": res.ID.String(),
		"polling_secret":   res.PollingSecret,
		"user_code":        res.UserCode,
		"verification_url": res.VerificationURL,
		"expires_at":       res.ExpiresAt,
		"interval_seconds": res.IntervalSeconds,
	})
	return nil
}

// postPollLogin is bounded, rate limited, expiring and single use.
func (s *Server) postPollLogin(w http.ResponseWriter, r *http.Request) error {
	if s.Auth == nil {
		return httpx.NotFound()
	}
	var req struct {
		PendingLoginID string `json:"pending_login_id"`
		PollingSecret  string `json:"polling_secret"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 4<<10); err != nil {
		return err
	}
	id, err := uuid.Parse(req.PendingLoginID)
	if err != nil || req.PollingSecret == "" {
		return httpx.NotFound()
	}
	res, err := s.Auth.Poll(r.Context(), id, req.PollingSecret)
	if err != nil {
		return httpx.NotFound()
	}
	out := map[string]any{"status": res.Status}
	if res.Status == "approved" && res.Token != "" {
		out["token"] = res.Token
		if res.UserID != nil {
			if u, err := s.Store.UserByID(r.Context(), nil, *res.UserID); err == nil {
				out["user"] = presentUser(u)
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) postLogout(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	if sess := callerSession(r); sess != nil && s.Auth != nil {
		if err := s.Auth.Logout(r.Context(), u.ID, sess.ID); err != nil {
			return httpx.Internal(err)
		}
	}
	clearSessionCookie(w, s.Cfg)
	httpx.NoContent(w)
	return nil
}

func (s *Server) listAuthSessions(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	sessions, err := s.Store.ListSessions(r.Context(), u.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	current := uuid.Nil
	if sess := callerSession(r); sess != nil {
		current = sess.ID
	}
	out := make([]sessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, presentSession(sess, current))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sessions": out})
	return nil
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	id, err := pathUUID(r, "sessionId")
	if err != nil {
		return err
	}
	if err := s.Store.RevokeSession(r.Context(), u.ID, id); err != nil {
		return httpx.NotFound()
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) getMe(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	unread, err := s.Store.UnreadCount(r.Context(), u.ID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusOK, meResponse{
		User:            presentUser(u),
		NeedsOnboarding: u.OnboardedAt == nil,
		IsModerator:     u.IsModerator,
		UnreadInbox:     unread,
		ServerTime:      s.Clock.Now(),
	})
	return nil
}

// postImportFollows performs the opt-out first-login import.
func (s *Server) postImportFollows(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	var req struct {
		Enabled *bool `json:"enabled"`
		Preview bool  `json:"preview"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 4<<10); err != nil {
		return err
	}
	enabled := true // opt-out: enabled unless the user turns it off
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if s.Auth == nil {
		return httpx.Err(http.StatusServiceUnavailable, httpx.CodeUnavailable,
			"Importing is unavailable on this service.")
	}
	// The upstream GitHub token is not retained, so an import after the
	// initial sign-in re-authorizes rather than reusing a stored token.
	summary, err := s.Auth.ImportFollows(r.Context(), u.ID, "", enabled, req.Preview)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"enabled":                summary.Enabled,
		"preview":                summary.Preview,
		"account":                presentIdentity(summary.Account),
		"github_following_count": summary.GitHubFollowingCount,
		"added":                  summary.Added,
		"already_following":      summary.AlreadyFollowing,
		"skipped_unfollowed":     summary.SkippedUnfollowed,
		"skipped_blocked":        summary.SkippedBlocked,
		"sample":                 presentIdentities(summary.Sample),
	})
	return nil
}

const sessionCookieName = "ghs_session"

// setSessionCookie issues a first-party cookie for the service-hosted account
// application only. The browser extension and CLI use bearer tokens instead,
// because a third-party cookie on github.com would be blocked anyway.
func setSessionCookie(w http.ResponseWriter, cfg *config.Config, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.PublicURL.Scheme == "https",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(cfg.SessionTTL.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, cfg *config.Config) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: cfg.PublicURL.Scheme == "https",
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}
