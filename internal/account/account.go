// Package account serves the small service-hosted application: OAuth handoff,
// CLI authorization approval, settings, moderation, and the authenticated
// external viewer that the terminal client opens for video.
//
// It is deliberately server-rendered Go templates rather than a fourth
// JavaScript build. It only needs forms and one media element, and keeping the
// dependency count modest was an explicit goal.
package account

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/config"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/store"
	"github.com/alliecatowo/gh-stories/internal/version"
)

//go:embed templates/*.html
var templateFS embed.FS

// Authenticator resolves the first-party session cookie. The account app uses
// a cookie because it is a first-party web page on the service's own origin;
// the extension and CLI use bearer tokens instead.
type Authenticator interface {
	Authenticate(ctx context.Context, bearer string) (*domain.Session, *domain.User, error)
	ApprovePendingLogin(ctx context.Context, pendingID, userID uuid.UUID, userCode string) error
	DenyPendingLogin(ctx context.Context, pendingID uuid.UUID) error
}

type Server struct {
	Cfg   *config.Config
	Store *store.Store
	Auth  Authenticator
	tmpl  *template.Template
}

func New(cfg *config.Config, st *store.Store, auth Authenticator) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"relative": relativeTime,
		"label":    func(v domain.Visibility) string { return v.Label() },
		"ptr":      func(t time.Time) *time.Time { return &t },
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{Cfg: cfg, Store: st, Auth: auth, tmpl: tmpl}, nil
}

const sessionCookieName = "ghs_session"

// caller resolves the signed-in account from the first-party cookie.
func (s *Server) caller(r *http.Request) *domain.User {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	_, u, err := s.Auth.Authenticate(r.Context(), c.Value)
	if err != nil {
		return nil
	}
	return u
}

// Routes mounts the account application.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/login", s.getLogin)
	r.Get("/account", s.getHome)
	r.Get("/account/authorize", s.getAuthorize)
	r.Post("/account/authorize", s.postAuthorize)
	r.Get("/account/settings", s.getSettings)
	r.Get("/account/moderation", s.getModeration)
	r.Get("/s/{storyId}", s.getExternalViewer)
	return r
}

func (s *Server) getLogin(w http.ResponseWriter, r *http.Request) {
	dest := "/v1/auth/github/start"
	if rt := r.URL.Query().Get("return_to"); rt != "" && strings.HasPrefix(rt, "/") &&
		!strings.HasPrefix(rt, "//") {
		dest += "?return_to=" + template.URLQueryEscaper(rt)
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

type pageData struct {
	Title      string
	User       *domain.User
	Version    string
	PublicURL  string
	Data       any
	Error      string
	SignedIn   bool
	OAuthReady bool
}

func (s *Server) page(w http.ResponseWriter, r *http.Request, name string, d pageData) {
	d.Version = version.Short()
	d.PublicURL = s.Cfg.PublicURL.String()
	d.SignedIn = d.User != nil
	d.OAuthReady = s.Cfg.GitHubOAuthConfigured()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The account app renders private content; never let it be cached or framed.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; img-src 'self'; media-src 'self'; style-src 'self' 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	if err := s.tmpl.ExecuteTemplate(w, name, d); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) *domain.User {
	u := s.caller(r)
	if u == nil {
		http.Redirect(w, r, "/login?return_to="+template.URLQueryEscaper(r.URL.Path), http.StatusFound)
		return nil
	}
	return u
}

func (s *Server) getHome(w http.ResponseWriter, r *http.Request) {
	u := s.caller(r)
	if u == nil {
		s.page(w, r, "signin.html", pageData{Title: "Sign in"})
		return
	}
	items, err := s.Store.OwnSequence(r.Context(), u.ID)
	if err != nil {
		s.page(w, r, "home.html", pageData{Title: "Your Stories", User: u, Error: "Could not load your Stories."})
		return
	}
	unread, _ := s.Store.UnreadCount(r.Context(), u.ID)
	s.page(w, r, "home.html", pageData{
		Title: "Your Stories", User: u,
		Data: map[string]any{"Items": items, "Unread": unread},
	})
}

// getAuthorize shows the CLI approval screen.
//
// The user must see WHICH client is asking and a code that matches what their
// terminal printed. That match is what stops an attacker from starting a login
// and tricking someone else into approving it.
func (s *Server) getAuthorize(w http.ResponseWriter, r *http.Request) {
	u := s.requireUser(w, r)
	if u == nil {
		return
	}
	id, err := uuid.Parse(r.URL.Query().Get("request"))
	if err != nil {
		s.page(w, r, "authorize.html", pageData{Title: "Authorize", User: u,
			Error: "That authorization request is not valid."})
		return
	}
	pending, err := s.Store.PendingLogin(r.Context(), id)
	if err != nil || pending.State != "pending" {
		s.page(w, r, "authorize.html", pageData{Title: "Authorize", User: u,
			Error: "That authorization request has expired or was already used."})
		return
	}
	s.page(w, r, "authorize.html", pageData{
		Title: "Authorize a device", User: u,
		Data: map[string]any{"Pending": pending, "RequestID": id.String()},
	})
}

func (s *Server) postAuthorize(w http.ResponseWriter, r *http.Request) {
	u := s.requireUser(w, r)
	if u == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(r.PostFormValue("request"))
	if err != nil {
		http.Error(w, "bad request id", http.StatusBadRequest)
		return
	}
	if r.PostFormValue("action") == "deny" {
		_ = s.Auth.DenyPendingLogin(r.Context(), id)
		s.page(w, r, "authorized.html", pageData{Title: "Denied", User: u,
			Data: map[string]any{"Approved": false}})
		return
	}
	code := strings.TrimSpace(r.PostFormValue("user_code"))
	if err := s.Auth.ApprovePendingLogin(r.Context(), id, u.ID, code); err != nil {
		s.page(w, r, "authorize.html", pageData{Title: "Authorize", User: u,
			Error: "That code does not match the request. Check your terminal and try again."})
		return
	}
	s.page(w, r, "authorized.html", pageData{Title: "Authorized", User: u,
		Data: map[string]any{"Approved": true}})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	u := s.requireUser(w, r)
	if u == nil {
		return
	}
	ctx := r.Context()
	sessions, _ := s.Store.ListSessions(ctx, u.ID)
	blocked, _ := s.Store.ListSimple(ctx, "blocks", "blocker_user_id", "blocked_github_user_id", u.ID)
	muted, _ := s.Store.ListSimple(ctx, "mutes", "muter_user_id", "muted_github_user_id", u.ID)
	hidden, _ := s.Store.ListSimple(ctx, "hide_rules", "author_user_id", "hidden_github_user_id", u.ID)
	s.page(w, r, "settings.html", pageData{
		Title: "Settings", User: u,
		Data: map[string]any{
			"Sessions": sessions, "Blocked": blocked, "Muted": muted, "Hidden": hidden,
			"ReplyRetentionDays": int(s.Cfg.ReplyRetention.Hours() / 24),
		},
	})
}

// getModeration is deny-by-default. A non-moderator gets a 404, so the queue's
// existence is not advertised.
func (s *Server) getModeration(w http.ResponseWriter, r *http.Request) {
	u := s.requireUser(w, r)
	if u == nil {
		return
	}
	if !u.IsModerator {
		http.NotFound(w, r)
		return
	}
	reports, err := s.Store.ListReports(r.Context(), "open", 50)
	if err != nil {
		http.Error(w, "could not load queue", http.StatusInternalServerError)
		return
	}
	s.page(w, r, "moderation.html", pageData{
		Title: "Moderation", User: u, Data: map[string]any{"Reports": reports},
	})
}

// getExternalViewer is what `gh stories` opens with Enter, and what a private
// Story's "open externally" leads to.
//
// It works for private Stories because it goes through normal authorization:
// the same visibility predicate as everything else, and the media itself is
// still fetched through the authenticated gateway. It is never a permanent
// public media link.
func (s *Server) getExternalViewer(w http.ResponseWriter, r *http.Request) {
	u := s.requireUser(w, r)
	if u == nil {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "storyId"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	item, err := s.Store.StoryForViewer(r.Context(), u.ID, u.GitHubID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Expired, deleted, blocked, out of audience or never existed.
			s.page(w, r, "gone.html", pageData{Title: "Story expired", User: u})
			return
		}
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	kind := domain.VariantImage
	if item.MediaKind() == domain.MediaVideo {
		kind = domain.VariantVideo
	}
	s.page(w, r, "story.html", pageData{
		Title: "Story", User: u,
		Data: map[string]any{
			"Item":     item,
			"IsVideo":  item.MediaKind() == domain.MediaVideo,
			"MediaURL": "/v1/media/" + item.ID.String() + "/" + string(kind),
			"Poster":   "/v1/media/" + item.ID.String() + "/poster",
			"Expires":  item.ExpiresAt,
		},
	})
}

func relativeTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	d := time.Since(*t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return itoa(int(d.Hours())) + "h"
	default:
		return itoa(int(d.Hours()/24)) + "d"
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
