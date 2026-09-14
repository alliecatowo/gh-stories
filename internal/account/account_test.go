package account_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/account"
	"github.com/alliecatowo/gh-stories/internal/config"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/store"
	"github.com/alliecatowo/gh-stories/internal/testdb"
)

// The account application is a real client surface — it is where first login
// lands and where the GitHub follow import is offered and summarised. These
// render it for real, through its real templates, against a real database.

// cookieAuth resolves the first-party cookie by looking the session up in the
// store, exactly as the production authenticator does. It is a stub only in
// that it performs no OAuth.
type cookieAuth struct{ store *store.Store }

func (a *cookieAuth) Authenticate(ctx context.Context, bearer string) (*domain.Session, *domain.User, error) {
	return a.store.SessionByToken(ctx, bearer)
}
func (a *cookieAuth) ApprovePendingLogin(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}
func (a *cookieAuth) DenyPendingLogin(context.Context, uuid.UUID) error { return nil }

type app struct {
	t     *testing.T
	srv   *httptest.Server
	store *store.Store
}

func newApp(t *testing.T) *app {
	t.Helper()
	st, _ := testdb.New(t)
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
			return "storiesminio", true
		case "GHS_S3_SECRET_ACCESS_KEY":
			return "storiesminio_local_dev", true
		case "GHS_SECRET_KEY":
			return "account-suite-secret-key-long-enough-0123", true
		}
		return "", false
	})
	require.NoError(t, err)

	s, err := account.New(cfg, st, &cookieAuth{st})
	require.NoError(t, err)
	srv := httptest.NewServer(s.Routes())
	t.Cleanup(srv.Close)
	return &app{t: t, srv: srv, store: st}
}

// signIn creates an account and returns its session cookie.
func (a *app) signIn(login string, gitHubID int64) (*domain.User, *http.Cookie) {
	a.t.Helper()
	u := testdb.Account(a.t, a.store, gitHubID, login)
	token := "account-suite-" + login + "-" + uuid.NewString()
	_, err := a.store.CreateSession(context.Background(), nil, u.ID, token,
		"web", "account suite", "test", time.Hour)
	require.NoError(a.t, err)
	return u, &http.Cookie{Name: "ghs_session", Value: token}
}

// get performs a request without following redirects, so the redirect itself
// is assertable.
func (a *app) get(path string, c *http.Cookie) *http.Response {
	a.t.Helper()
	return a.do(http.MethodGet, path, nil, c)
}

func (a *app) do(method, path string, body url.Values, c *http.Cookie) *http.Response {
	a.t.Helper()
	var r io.Reader
	if body != nil {
		r = strings.NewReader(body.Encode())
	}
	req, err := http.NewRequest(method, a.srv.URL+path, r)
	require.NoError(a.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if c != nil {
		req.AddCookie(c)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	require.NoError(a.t, err)
	a.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func bodyOf(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

func TestOnboardingOffersTheImportWithAVisibleAccount(t *testing.T) {
	a := newApp(t)
	u, c := a.signIn("onboard-maya", 880101)
	require.Nil(t, u.OnboardedAt)

	resp := a.get("/account/onboarding", c)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	page := bodyOf(t, resp)

	// The offer, the account it would act as, and an unambiguous way out.
	require.Contains(t, page, "onboard-maya", "the page must show which account this is")
	require.Contains(t, page, "purpose=import")
	require.Contains(t, page, "/account/onboarding/skip")
	require.Contains(t, strings.ToLower(page), "never changes")
}

func TestSkippingOnboardingReadsNothingFromGitHub(t *testing.T) {
	a := newApp(t)
	u, c := a.signIn("onboard-skipper", 880102)

	resp := a.do(http.MethodPost, "/account/onboarding/skip", url.Values{}, c)
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	require.Equal(t, "/account", resp.Header.Get("Location"))

	after, err := a.store.UserByGitHubID(context.Background(), nil, u.GitHubID)
	require.NoError(t, err)
	require.NotNil(t, after.OnboardedAt, "skipping must record that onboarding is done")

	// And the offer does not come back.
	resp = a.get("/account/onboarding", c)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "/account", resp.Header.Get("Location"))
}

func TestOnboardingRequiresSigningIn(t *testing.T) {
	a := newApp(t)
	for _, path := range []string{"/account", "/account/onboarding"} {
		resp := a.get(path, nil)
		require.Equal(t, http.StatusFound, resp.StatusCode, path)
		require.Contains(t, resp.Header.Get("Location"), "/login", path)
	}
	resp := a.do(http.MethodPost, "/account/onboarding/skip", url.Values{}, nil)
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Location"), "/login")
}

func TestImportSummaryRenders(t *testing.T) {
	a := newApp(t)
	_, c := a.signIn("onboard-summary", 880103)

	resp := a.get("/account?import=done&added=12&already=3&unfollowed=1&blocked=2&sample=maya,jules", c)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	page := bodyOf(t, resp)

	require.Contains(t, page, "12")
	require.Contains(t, page, "3 you already followed")
	require.Contains(t, page, "1 skipped because you unfollowed them here before")
	require.Contains(t, page, "2 skipped because of a block")
	require.Contains(t, page, "For example: maya, jules")
	require.Contains(t, page, "did not change who you follow on GitHub")
}

func TestImportSummaryRefusesAHostileSample(t *testing.T) {
	a := newApp(t)
	_, c := a.signIn("onboard-hostile", 880104)

	resp := a.get("/account?import=done&added=1&already=0&unfollowed=0&blocked=0"+
		"&sample="+url.QueryEscape("<img src=x onerror=alert(1)>,ok-name"), c)
	page := bodyOf(t, resp)

	// Not merely escaped — a value that is not a GitHub-shaped login is not
	// rendered at all.
	require.NotContains(t, page, "onerror")
	require.NotContains(t, page, "alert(1)")
	require.Contains(t, page, "For example: ok-name")
}

func TestFailedImportSaysNothingChanged(t *testing.T) {
	a := newApp(t)
	_, c := a.signIn("onboard-failed", 880105)

	page := bodyOf(t, a.get("/account?import=failed", c))
	require.Contains(t, page, "did not complete")
	require.Contains(t, page, "Nothing was changed")
}

func TestAccountPagesAreNeverCachedOrFramed(t *testing.T) {
	a := newApp(t)
	_, c := a.signIn("onboard-headers", 880106)

	for _, path := range []string{"/account", "/account/onboarding", "/account/settings"} {
		resp := a.get(path, c)
		require.Equal(t, "no-store", resp.Header.Get("Cache-Control"), path)
		require.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"), path)
		require.Contains(t, resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'", path)
	}
}
