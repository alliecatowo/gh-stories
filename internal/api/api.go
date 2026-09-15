// Package api implements the versioned /v1 HTTP contract.
//
// Authorization is never re-derived here. Handlers call the shared store
// predicates, so a route cannot invent its own weaker rule, and adding a route
// cannot accidentally skip a check.
package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/clock"
	"github.com/alliecatowo/gh-stories/internal/config"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/ratelimit"
	"github.com/alliecatowo/gh-stories/internal/store"
)

// ObjectData is one streamed object from private storage.
type ObjectData struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
	ContentRange  string
	ETag          string
	StatusCode    int
}

// ObjectReader is the narrow slice of object storage the media gateway needs.
// Declaring it here keeps the API package independent of the storage driver.
type ObjectReader interface {
	Get(ctx context.Context, key string, rangeHeader string) (*ObjectData, error)
	Healthy(ctx context.Context) error
}

// AuthService is the identity boundary the API depends on. The concrete
// implementation performs real GitHub OAuth; tests can substitute a fake
// without weakening any authorization rule, because authorization lives in the
// store, not here.
type AuthService interface {
	StartAuthorization(ctx context.Context, purpose, returnTo string, pendingLoginID *uuid.UUID) (string, error)
	CompleteAuthorization(ctx context.Context, code, state string) (*AuthResult, error)
	IssueSession(ctx context.Context, userID uuid.UUID, kind domain.ClientKind, label, userAgent string) (string, *domain.Session, error)
	Authenticate(ctx context.Context, bearer string) (*domain.Session, *domain.User, error)
	Logout(ctx context.Context, userID, sessionID uuid.UUID) error
	CreatePendingLogin(ctx context.Context, kind domain.ClientKind, label string) (*PendingLoginResult, error)
	ApprovePendingLogin(ctx context.Context, pendingID, userID uuid.UUID, userCode string) error
	DenyPendingLogin(ctx context.Context, pendingID uuid.UUID) error
	Poll(ctx context.Context, pendingID uuid.UUID, pollingSecret string) (*store.PollResult, error)
	StartDeviceLogin(ctx context.Context, kind domain.ClientKind, label string) (*DeviceLoginStart, error)
	PollDeviceLogin(ctx context.Context, id uuid.UUID) (*store.PollResult, error)
	ImportFollows(ctx context.Context, userID uuid.UUID, upstreamToken string, enabled, preview bool) (*ImportSummary, error)
	Configured() bool
}

// DeviceLoginStart is a GitHub Device Authorization Grant request.
type DeviceLoginStart struct {
	ID              uuid.UUID
	UserCode        string
	VerificationURI string
	ExpiresAt       time.Time
	IntervalSeconds int
}

// AuthResult is the outcome of a completed OAuth authorization.
type AuthResult struct {
	User          *domain.User
	Flow          *store.OAuthFlow
	IsNewAccount  bool
	UpstreamToken string
}

// PendingLoginResult is a freshly created service-mediated client
// authorization. The polling secret is returned exactly once, to the
// requesting client only.
type PendingLoginResult struct {
	ID              uuid.UUID
	PollingSecret   string
	UserCode        string
	VerificationURL string
	ExpiresAt       time.Time
	IntervalSeconds int
}

// ImportSummary reports what a GitHub follow import did, or would do.
type ImportSummary struct {
	Enabled              bool
	Preview              bool
	Account              domain.Identity
	GitHubFollowingCount int
	Added                int
	AlreadyFollowing     int
	SkippedUnfollowed    int
	SkippedBlocked       int
	Sample               []domain.Identity
}

// Server holds the API's dependencies.
type Server struct {
	Cfg     *config.Config
	Store   *store.Store
	Clock   clock.Clock
	Objects ObjectReader
	Uploads Uploader
	Auth    AuthService
	Log     *slog.Logger

	limiter *ratelimit.Limiter
	mux     http.Handler
}

// New wires a server and its router.
func New(cfg *config.Config, st *store.Store, clk clock.Clock,
	objects ObjectReader, uploads Uploader, auth AuthService, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	if clk == nil {
		clk = clock.Real{}
	}
	s := &Server{
		Cfg: cfg, Store: st, Clock: clk, Objects: objects, Uploads: uploads, Auth: auth, Log: log,
		limiter: ratelimit.New(clk.Now),
	}
	s.mux = s.routes()
	return s
}

// Handler returns the fully wired router.
func (s *Server) Handler() http.Handler { return s.mux }
