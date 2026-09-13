package ghclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

func newTestClient(baseURL string) *Client {
	c := New(Options{BaseURL: baseURL, HTTPClient: http.DefaultClient})
	// Tests never want to wait for real backoff; the retry paths are
	// exercised in wall-clock terms of microseconds instead.
	c.sleep = func(time.Duration) {}
	return c
}

func TestFollowingPaginatesAcrossLinkHeaders(t *testing.T) {
	pages := [][]ghUser{
		{{ID: 1, Login: "alice", Type: "User"}, {ID: 2, Login: "bob", Type: "User"}},
		{{ID: 3, Login: "carol", Type: "User"}, {ID: 4, Login: "dave", Type: "User"}},
		{{ID: 5, Login: "erin", Type: "User"}},
	}
	var hits int32

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		require.Equal(t, "/user/following", r.URL.Path)
		pageParam := r.URL.Query().Get("page")
		idx := 0
		if pageParam != "" {
			idx, _ = strconv.Atoi(pageParam)
		}
		if idx >= len(pages) {
			w.Write([]byte(`[]`))
			return
		}
		if idx < len(pages)-1 {
			w.Header().Set("Link", fmt.Sprintf(`<%s/user/following?per_page=100&page=%d>; rel="next", <%s/user/following?per_page=100&page=%d>; rel="last"`,
				srv.URL, idx+1, srv.URL, len(pages)-1))
		}
		json.NewEncoder(w).Encode(pages[idx])
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	got, err := c.Following(context.Background(), "tok", 100)
	require.NoError(t, err)
	require.Len(t, got, 5)
	assert.Equal(t, domain.GitHubID(1), got[0].GitHubID)
	assert.Equal(t, "alice", got[0].Login)
	assert.Equal(t, domain.GitHubID(5), got[4].GitHubID)
	assert.Equal(t, int32(3), atomic.LoadInt32(&hits))
}

func TestFollowingDeduplicatesByID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Same id reappears (simulating a flaky upstream); must be counted once.
		json.NewEncoder(w).Encode([]ghUser{
			{ID: 1, Login: "alice", Type: "User"},
			{ID: 1, Login: "alice", Type: "User"},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	got, err := c.Following(context.Background(), "tok", 100)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestFollowingRespectsMax(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<`+r.Host+`/user/following?page=2>; rel="next"`)
		json.NewEncoder(w).Encode([]ghUser{
			{ID: 1, Login: "a", Type: "User"},
			{ID: 2, Login: "b", Type: "User"},
			{ID: 3, Login: "c", Type: "User"},
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	got, err := c.Following(context.Background(), "tok", 2)
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestRateLimitRetriedThenSurfaced(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.CurrentUser(context.Background(), "tok")
	require.Error(t, err)

	var rlErr *RateLimitError
	require.ErrorAs(t, err, &rlErr)
	assert.Equal(t, maxRetries+1, rlErr.Attempts)
	assert.Equal(t, maxRetries+1, int(atomic.LoadInt32(&hits)))
	assert.False(t, rlErr.Reset.IsZero())
}

func TestRateLimitRetriesThenSucceeds(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10))
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"message":"API rate limit exceeded"}`))
			return
		}
		json.NewEncoder(w).Encode(ghUser{ID: 42, Login: "octocat", Type: "User"})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	id, err := c.CurrentUser(context.Background(), "tok")
	require.NoError(t, err)
	assert.Equal(t, domain.GitHubID(42), id.GitHubID)
	assert.Equal(t, int32(2), atomic.LoadInt32(&hits))
}

func TestCurrentUserUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.CurrentUser(context.Background(), "bad-token")
	require.ErrorIs(t, err, ErrUnauthorized)
}

func TestCurrentUserForbiddenNotRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 403 without rate-limit headers: a real permission refusal, not
		// throttling, so it must not be retried and must surface as
		// ErrForbidden rather than *RateLimitError.
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"account is blocked"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.CurrentUser(context.Background(), "tok")
	require.ErrorIs(t, err, ErrForbidden)
}

func TestBodyLargerThanCapIsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("a", maxBodyBytes+1)))
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.CurrentUser(context.Background(), "tok")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestCurrentUserOrganizationAccountType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ghUser{ID: 9, Login: "acme-corp", Type: "Organization"})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	id, err := c.CurrentUser(context.Background(), "tok")
	require.NoError(t, err)
	assert.Equal(t, "Organization", id.AccountType)
}

func TestExchangeCodeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/login/oauth/access_token", r.URL.Path)
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "client-123", r.Form.Get("client_id"))
		assert.Equal(t, "secret-xyz", r.Form.Get("client_secret"))
		assert.Equal(t, "code-abc", r.Form.Get("code"))
		assert.Equal(t, "verifier-1", r.Form.Get("code_verifier"))
		assert.Equal(t, "https://example.test/callback", r.Form.Get("redirect_uri"))
		json.NewEncoder(w).Encode(map[string]string{
			"access_token": "gho_abc123",
			"token_type":   "bearer",
			"scope":        "",
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	tok, err := c.ExchangeCode(context.Background(), "client-123", "secret-xyz",
		"code-abc", "https://example.test/callback", "verifier-1")
	require.NoError(t, err)
	assert.Equal(t, "gho_abc123", tok.AccessToken)
}

func TestExchangeCodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "bad_verification_code",
			"error_description": "The code passed is incorrect or expired.",
		})
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.ExchangeCode(context.Background(), "id", "secret", "bad-code", "https://x/cb", "v")
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
}

func TestRedactNeverReturnsSecret(t *testing.T) {
	assert.NotContains(t, redact("super-secret-token"), "super-secret-token")
	assert.Equal(t, "", redact(""))
}
