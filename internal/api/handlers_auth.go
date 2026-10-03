package api

import (
	"fmt"
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
	switch {
	case pendingID != nil:
		purpose = "cli_approval"
	case r.URL.Query().Get("purpose") == "import":
		// A manual re-import: we hold no GitHub token, so we ask again.
		purpose = "import"
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

	// An import authorization exists only to read the follow graph once. Do it
	// now, while the freshly exchanged token is in hand, then discard it.
	//
	// This still falls through to issuing a session cookie below: the import
	// can be started from the CLI or the extension, where this browser may
	// have no session at all, and landing on a summary page that immediately
	// bounces to sign-in would be a bug.
	switch {
	case res.Flow != nil && res.Flow.Purpose == "import":
		summary, err := s.Auth.ImportFollows(r.Context(), res.User.ID, res.UpstreamToken, true, false)
		if err != nil {
			dest = s.Cfg.PublicURL.String() + "/account?import=failed"
			break
		}
		if err := s.Store.MarkOnboarded(r.Context(), nil, res.User.ID); err != nil {
			return httpx.Internal(err)
		}
		dest = fmt.Sprintf("%s/account?import=done&added=%d&already=%d&unfollowed=%d&blocked=%d",
			s.Cfg.PublicURL.String(), summary.Added, summary.AlreadyFollowing,
			summary.SkippedUnfollowed, summary.SkippedBlocked)
		if names := sampleLogins(summary.Sample); names != "" {
			dest += "&sample=" + url.QueryEscape(names)
		}

	case res.Flow != nil && res.Flow.Purpose == "cli_approval" && res.Flow.PendingLoginID != nil:
		dest = s.Cfg.PublicURL.String() + "/account/authorize?request=" + res.Flow.PendingLoginID.String()

	case res.Flow != nil && res.Flow.ReturnTo != "":
		dest = s.Cfg.PublicURL.String() + res.Flow.ReturnTo

	case res.IsNewAccount || res.User.OnboardedAt == nil:
		// A brand-new account lands on onboarding, where the import is offered.
		dest = s.Cfg.PublicURL.String() + "/account/onboarding"
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

// postDeviceLoginStart begins a GitHub Device Authorization Grant login.
//
// The CLI receives a device login id, a short user code and GitHub's
// verification URI. The device code itself stays server side: the client
// never needs it, because the server polls GitHub on the client's behalf.
func (s *Server) postDeviceLoginStart(w http.ResponseWriter, r *http.Request) error {
	if s.Auth == nil || !s.Auth.Configured() {
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
	res, err := s.Auth.StartDeviceLogin(r.Context(), kind, label)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"device_login_id":  res.ID.String(),
		"user_code":        res.UserCode,
		"verification_uri": res.VerificationURI,
		"expires_at":       res.ExpiresAt,
		"interval_seconds": res.IntervalSeconds,
	})
	return nil
}

// postDeviceLoginPoll polls a device authorization started by
// postDeviceLoginStart. It is anonymous, rate limited, expiring and single
// use, exactly like the pending-login poll above it.
func (s *Server) postDeviceLoginPoll(w http.ResponseWriter, r *http.Request) error {
	if s.Auth == nil {
		return httpx.NotFound()
	}
	var req struct {
		DeviceLoginID string `json:"device_login_id"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 4<<10); err != nil {
		return err
	}
	id, err := uuid.Parse(req.DeviceLoginID)
	if err != nil {
		return httpx.NotFound()
	}
	res, err := s.Auth.PollDeviceLogin(r.Context(), id)
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

// postImportFollows drives the opt-out first-login import from a client that
// is not a browser — the CLI and the extension popup.
//
// It cannot perform the import itself. The service deliberately discards the
// upstream GitHub token the moment identity is established, so reading who
// someone follows on GitHub always needs a fresh GitHub authorization in a
// browser. Accepting means "send me there"; declining is the only outcome
// this endpoint can finish on its own, and it finishes it without touching
// GitHub at all.
func (s *Server) postImportFollows(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 4<<10); err != nil {
		return err
	}
	enabled := true // opt-out: enabled unless the user turns it off
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if s.Auth == nil || !s.Auth.Configured() {
		return httpx.Err(http.StatusServiceUnavailable, httpx.CodeUnavailable,
			"Importing is unavailable on this service.")
	}

	if !enabled {
		if err := s.Store.MarkOnboarded(r.Context(), nil, u.ID); err != nil {
			return httpx.Internal(err)
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"enabled":                    false,
			"account":                    presentUser(u),
			"needs_github_authorization": false,
			"authorization_url":          "",
			"added":                      0,
			"already_following":          0,
			"skipped_unfollowed":         0,
			"skipped_blocked":            0,
			"sample":                     []publicUser{},
		})
		return nil
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"enabled":                    true,
		"account":                    presentUser(u),
		"needs_github_authorization": true,
		"authorization_url":          s.Cfg.PublicURL.String() + "/v1/auth/github/start?purpose=import",
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

// sampleLogins joins a few logins for an import summary. Logins come from
// GitHub, but they are still other people's strings landing in a URL and then
// a page, so anything that is not a GitHub-shaped login is dropped rather
// than escaped-and-hoped-for.
func sampleLogins(ids []domain.Identity) string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if isGitHubLogin(id.Login) {
			names = append(names, id.Login)
		}
	}
	return strings.Join(names, ",")
}

// isGitHubLogin reports whether s matches GitHub's own username rule:
// alphanumerics and single hyphens, not leading or trailing, up to 39 chars.
func isGitHubLogin(s string) bool {
	if s == "" || len(s) > 39 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' && s[i-1] != '-':
		default:
			return false
		}
	}
	return true
}
