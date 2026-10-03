package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/auth"
	"github.com/alliecatowo/gh-stories/internal/config"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/ghclient"
	"github.com/alliecatowo/gh-stories/internal/objstore"
	"github.com/alliecatowo/gh-stories/internal/store"
	"github.com/alliecatowo/gh-stories/internal/testdb"
)

// deviceGitHub fakes the GitHub Device Authorization Grant surface plus the
// identity endpoint, following the ghclient test pattern of pointing
// Options.BaseURL at a single httptest server.
type deviceGitHub struct {
	mu       sync.Mutex
	approved bool
	login    string
}

func newDeviceGitHub(t *testing.T) (*httptest.Server, *deviceGitHub) {
	t.Helper()
	d := &deviceGitHub{login: "device-alice"}
	mux := http.NewServeMux()
	mux.HandleFunc("/login/device/code", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code": "server-device-code", "user_code": "WDJB-MJHT",
			"verification_uri": "https://github.com/login/device",
			"expires_in":       900, "interval": 5,
		})
	})
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		approved := d.approved
		d.mu.Unlock()
		if !approved {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": "gho_device_token", "token_type": "bearer", "scope": "",
		})
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gho_device_token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		d.mu.Lock()
		login := d.login
		d.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 4242, "login": login, "avatar_url": "https://avatars.example/x.png",
			"html_url": "https://github.com/" + login, "type": "User",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, d
}

// realAuth adapts the production auth service to the API's AuthService
// boundary. Unlike fakeAuth it performs the real device flow against the
// faked GitHub above, while sessions and authorization stay real.
type realAuth struct {
	svc *auth.Service
	cfg *config.Config
}

func (a *realAuth) StartAuthorization(ctx context.Context, purpose, returnTo string, pendingID *uuid.UUID) (string, error) {
	return a.svc.StartAuthorization(ctx, purpose, returnTo, pendingID)
}
func (a *realAuth) CompleteAuthorization(ctx context.Context, code, state string) (*AuthResult, error) {
	res, err := a.svc.CompleteAuthorization(ctx, code, state)
	if err != nil {
		return nil, err
	}
	return &AuthResult{User: res.User, Flow: res.Flow, IsNewAccount: res.IsNewAccount, UpstreamToken: res.UpstreamToken}, nil
}
func (a *realAuth) IssueSession(ctx context.Context, userID uuid.UUID, kind domain.ClientKind, label, ua string) (string, *domain.Session, error) {
	return a.svc.IssueSession(ctx, userID, kind, label, ua)
}
func (a *realAuth) Authenticate(ctx context.Context, bearer string) (*domain.Session, *domain.User, error) {
	return a.svc.Authenticate(ctx, bearer)
}
func (a *realAuth) Logout(ctx context.Context, userID, sessionID uuid.UUID) error {
	return a.svc.Logout(ctx, userID, sessionID)
}
func (a *realAuth) CreatePendingLogin(ctx context.Context, kind domain.ClientKind, label string) (*PendingLoginResult, error) {
	res, err := a.svc.CreatePendingLogin(ctx, kind, label)
	if err != nil {
		return nil, err
	}
	return &PendingLoginResult{ID: res.ID, PollingSecret: res.PollingSecret, UserCode: res.UserCode,
		VerificationURL: res.VerificationURL, ExpiresAt: res.ExpiresAt, IntervalSeconds: res.IntervalSeconds}, nil
}
func (a *realAuth) ApprovePendingLogin(ctx context.Context, pendingID, userID uuid.UUID, userCode string) error {
	return a.svc.ApprovePendingLogin(ctx, pendingID, userID, userCode)
}
func (a *realAuth) DenyPendingLogin(ctx context.Context, pendingID uuid.UUID) error {
	return a.svc.DenyPendingLogin(ctx, pendingID)
}
func (a *realAuth) Poll(ctx context.Context, pendingID uuid.UUID, secret string) (*store.PollResult, error) {
	return a.svc.Poll(ctx, pendingID, secret)
}
func (a *realAuth) StartDeviceLogin(ctx context.Context, kind domain.ClientKind, label string) (*DeviceLoginStart, error) {
	res, err := a.svc.StartDeviceLogin(ctx, kind, label)
	if err != nil {
		return nil, err
	}
	return &DeviceLoginStart{ID: res.ID, UserCode: res.UserCode, VerificationURI: res.VerificationURI,
		ExpiresAt: res.ExpiresAt, IntervalSeconds: res.IntervalSeconds}, nil
}
func (a *realAuth) PollDeviceLogin(ctx context.Context, id uuid.UUID) (*store.PollResult, error) {
	return a.svc.PollDeviceLogin(ctx, id)
}
func (a *realAuth) ImportFollows(ctx context.Context, userID uuid.UUID, token string, enabled, preview bool) (*ImportSummary, error) {
	return &ImportSummary{}, nil
}
func (a *realAuth) Configured() bool { return a.cfg.GitHubOAuthConfigured() }

func deviceConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadFrom(func(k string) (string, bool) {
		switch k {
		case "GHS_ENV":
			return "test", true
		case "GHS_PUBLIC_URL":
			return "http://localhost:8787", true
		case "GHS_DATABASE_URL":
			return testdb.AdminURL(), true
		case "GHS_S3_BUCKET":
			return "stories-media", true
		case "GHS_S3_ACCESS_KEY_ID":
			return "test", true
		case "GHS_S3_SECRET_ACCESS_KEY":
			return "test", true
		case "GHS_SECRET_KEY":
			return "test-secret-key-that-is-long-enough-000000", true
		case "GHS_GITHUB_CLIENT_ID":
			return "test-client-id", true
		case "GHS_GITHUB_CLIENT_SECRET":
			return "test-client-secret", true
		}
		return "", false
	})
	require.NoError(t, err)
	return cfg
}

// newDeviceHarness runs the REAL router, REAL store and REAL auth service;
// only GitHub itself is faked. This exercises the routes, rate limits,
// handlers and the one-shot token consume exactly as production does.
func newDeviceHarness(t *testing.T) (*harness, *deviceGitHub) {
	t.Helper()
	st, clk := testdb.New(t)
	bucket := objstore.NewMemory()
	cfg := deviceConfig(t)
	ghsrv, gh := newDeviceGitHub(t)
	ghClient := ghclient.New(ghclient.Options{BaseURL: ghsrv.URL, HTTPClient: http.DefaultClient})
	svc := auth.NewService(st, ghClient, cfg)
	srv := New(cfg, st, clk, &memReader{bucket}, bucket,
		&realAuth{svc: svc, cfg: cfg}, slog.New(slog.DiscardHandler))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &harness{t: t, srv: ts, store: st, clock: clk, auth: nil, bucket: bucket}, gh
}

func devicePost(t *testing.T, h *harness, path string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+path, rdr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.srv.Client().Do(req)
	require.NoError(t, err)
	return resp
}

func TestDeviceLoginEndToEnd(t *testing.T) {
	h, gh := newDeviceHarness(t)

	raw := devicePost(t, h, "/v1/auth/device/start",
		map[string]string{"client_kind": "cli", "client_label": "test"})
	require.Equal(t, http.StatusCreated, raw.StatusCode)
	start := decode[struct {
		DeviceLoginID   string `json:"device_login_id"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		IntervalSeconds int    `json:"interval_seconds"`
	}](t, raw)
	require.NotEmpty(t, start.DeviceLoginID)
	assert.Equal(t, "WDJB-MJHT", start.UserCode)
	assert.Equal(t, "https://github.com/login/device", start.VerificationURI)
	assert.Equal(t, 5, start.IntervalSeconds)

	// Before the user authorizes on GitHub: pending, and no token.
	first := decode[struct {
		Status string `json:"status"`
		Token  string `json:"token"`
	}](t, devicePost(t, h, "/v1/auth/device/poll",
		map[string]string{"device_login_id": start.DeviceLoginID}))
	assert.Equal(t, "pending", first.Status)
	assert.Empty(t, first.Token)

	// The user authorizes on GitHub; the next poll completes the login.
	gh.mu.Lock()
	gh.approved = true
	gh.mu.Unlock()
	done := decode[struct {
		Status string `json:"status"`
		Token  string `json:"token"`
		User   struct {
			Login string `json:"login"`
		} `json:"user"`
	}](t, devicePost(t, h, "/v1/auth/device/poll",
		map[string]string{"device_login_id": start.DeviceLoginID}))
	assert.Equal(t, "approved", done.Status)
	require.NotEmpty(t, done.Token)
	assert.Equal(t, "device-alice", done.User.Login)

	// The token is a real session: it authenticates /v1/me.
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+"/v1/me", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+done.Token)
	resp, err := h.srv.Client().Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	drain(resp)

	// Single use: a second poll must not return the token again.
	again := decode[struct {
		Status string `json:"status"`
		Token  string `json:"token"`
	}](t, devicePost(t, h, "/v1/auth/device/poll",
		map[string]string{"device_login_id": start.DeviceLoginID}))
	assert.NotEqual(t, "approved", again.Status)
	assert.Empty(t, again.Token)
}

func TestDeviceLoginPollUnknownIDIs404(t *testing.T) {
	h, _ := newDeviceHarness(t)
	resp := devicePost(t, h, "/v1/auth/device/poll",
		map[string]string{"device_login_id": uuid.NewString()})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	drain(resp)

	// A malformed id is equally a 404, indistinguishable from unknown.
	resp = devicePost(t, h, "/v1/auth/device/poll",
		map[string]string{"device_login_id": "not-a-uuid"})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	drain(resp)
}
