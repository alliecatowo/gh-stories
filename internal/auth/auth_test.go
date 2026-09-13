package auth_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/auth"
	"github.com/alliecatowo/gh-stories/internal/config"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/ghclient"
	"github.com/alliecatowo/gh-stories/internal/store"
	"github.com/alliecatowo/gh-stories/internal/testdb"
)

// ----------------------------------------------------------------- helpers

// stubUser is the identity the fake GitHub server currently reports.
type stubUser struct {
	ID    int64
	Login string
	Type  string
}

// stubGitHub is a minimal httptest stand-in for the two GitHub hosts this
// package talks to (github.com for token exchange, api.github.com for /user)
// collapsed onto one server, exactly as ghclient.Options.BaseURL expects for
// tests.
type stubGitHub struct {
	mu      sync.Mutex
	current stubUser
	tokens  map[string]stubUser
	seq     int
}

func newStubGitHub(t *testing.T) (*httptest.Server, *stubGitHub) {
	t.Helper()
	s := &stubGitHub{tokens: map[string]stubUser{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seq++
		tok := fmt.Sprintf("test-token-%d", s.seq)
		s.tokens[tok] = s.current
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": tok, "token_type": "bearer", "scope": "",
		})
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		s.mu.Lock()
		u, ok := s.tokens[tok]
		s.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": u.ID, "login": u.Login, "avatar_url": "https://avatars.example/x.png",
			"html_url": "https://github.com/" + u.Login, "type": u.Type,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, s
}

func (s *stubGitHub) setUser(id int64, login, accountType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = stubUser{ID: id, Login: login, Type: accountType}
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	u, err := url.Parse("https://stories.example.test")
	require.NoError(t, err)
	return &config.Config{
		Env:                config.EnvTest,
		PublicURL:          u,
		GitHubClientID:     "test-client-id",
		GitHubClientSecret: "test-client-secret",
		SessionTTL:         time.Hour,
		PendingLoginTTL:    10 * time.Minute,
	}
}

func extractState(t *testing.T, authURL string) string {
	t.Helper()
	u, err := url.Parse(authURL)
	require.NoError(t, err)
	state := u.Query().Get("state")
	require.NotEmpty(t, state)
	return state
}

func newService(t *testing.T, baseURL string) (*auth.Service, *store.Store, func(time.Duration)) {
	t.Helper()
	st, clk := testdb.New(t)
	cfg := testConfig(t)
	gh := ghclient.New(ghclient.Options{BaseURL: baseURL})
	return auth.NewService(st, gh, cfg), st, clk.Advance
}

// --------------------------------------------------------------------- PKCE

func TestNewVerifierShapeAndCharset(t *testing.T) {
	v, err := auth.NewVerifier()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(v), 43)
	assert.LessOrEqual(t, len(v), 128)
	for _, c := range v {
		assert.True(t, strings.ContainsRune(
			"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", c),
			"verifier contains disallowed character %q", c)
	}

	v2, err := auth.NewVerifier()
	require.NoError(t, err)
	assert.NotEqual(t, v, v2, "verifiers must not repeat")
}

// TestChallengeMatchesRFC7636Vector uses the worked example from RFC 7636
// Appendix B, so Challenge is checked against a fixed, independently
// verifiable expected output rather than only against itself.
func TestChallengeMatchesRFC7636Vector(t *testing.T) {
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const wantChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	assert.Equal(t, wantChallenge, auth.Challenge(verifier))
}

func TestStateAndHashState(t *testing.T) {
	s1, err := auth.NewState()
	require.NoError(t, err)
	s2, err := auth.NewState()
	require.NoError(t, err)
	assert.NotEqual(t, s1, s2)

	h1 := auth.HashState(s1)
	h2 := auth.HashState(s1)
	assert.Equal(t, h1, h2, "hashing the same state twice must be deterministic")
	assert.Len(t, h1, 32, "sha256 digest is 32 bytes")
	assert.NotEqual(t, h1, auth.HashState(s2))
}

func TestNewTokenEntropyAndUniqueness(t *testing.T) {
	tok, err := auth.NewToken()
	require.NoError(t, err)
	// base64url with no padding: 32 raw bytes (256 bits) encodes to 43 chars.
	assert.GreaterOrEqual(t, len(tok), 43)
	tok2, err := auth.NewToken()
	require.NoError(t, err)
	assert.NotEqual(t, tok, tok2)
}

func TestNewUserCodeAvoidsConfusableCharacters(t *testing.T) {
	code, err := auth.NewUserCode()
	require.NoError(t, err)
	assert.Regexp(t, `^[A-Z0-9]{4}-[A-Z0-9]{4}$`, code)
	for _, bad := range []string{"0", "O", "1", "I", "L"} {
		assert.NotContains(t, code, bad)
	}
}

// ------------------------------------------------------------------- state

func TestCompleteAuthorizationRejectsUnknownReplayedAndExpiredState(t *testing.T) {
	ctx := context.Background()
	srv, stub := newStubGitHub(t)
	svc, _, advance := newService(t, srv.URL)
	stub.setUser(1, "alice", "User")

	// Unknown state: never issued.
	_, err := svc.CompleteAuthorization(ctx, "code", "not-a-real-state")
	require.ErrorIs(t, err, auth.ErrInvalidState)

	authURL, err := svc.StartAuthorization(ctx, auth.PurposeWebLogin, "/", nil)
	require.NoError(t, err)
	state := extractState(t, authURL)

	// First use succeeds and consumes the state.
	_, err = svc.CompleteAuthorization(ctx, "code", state)
	require.NoError(t, err)

	// Replay of the same state must be rejected.
	_, err = svc.CompleteAuthorization(ctx, "code", state)
	require.ErrorIs(t, err, auth.ErrInvalidState)

	// A fresh flow, allowed to expire, must also be rejected.
	authURL2, err := svc.StartAuthorization(ctx, auth.PurposeWebLogin, "/", nil)
	require.NoError(t, err)
	state2 := extractState(t, authURL2)
	advance(11 * time.Minute) // > PendingLoginTTL (10m), which flows reuse as their TTL
	_, err = svc.CompleteAuthorization(ctx, "code", state2)
	require.ErrorIs(t, err, auth.ErrInvalidState)
}

// ---------------------------------------------------------------- identity

func TestRenamedLoginUpdatesSameAccountNotANewOne(t *testing.T) {
	ctx := context.Background()
	srv, stub := newStubGitHub(t)
	svc, _, _ := newService(t, srv.URL)

	stub.setUser(100, "alice", "User")
	u1, err := svc.StartAuthorization(ctx, auth.PurposeWebLogin, "/", nil)
	require.NoError(t, err)
	res1, err := svc.CompleteAuthorization(ctx, "code-1", extractState(t, u1))
	require.NoError(t, err)
	require.True(t, res1.IsNewAccount)
	assert.Equal(t, "alice", res1.User.Login)
	firstAccountID := res1.User.ID

	// Same GitHub account, renamed.
	stub.setUser(100, "alice-renamed", "User")
	u2, err := svc.StartAuthorization(ctx, auth.PurposeWebLogin, "/", nil)
	require.NoError(t, err)
	res2, err := svc.CompleteAuthorization(ctx, "code-2", extractState(t, u2))
	require.NoError(t, err)
	assert.False(t, res2.IsNewAccount, "same numeric github id must resolve to the same account")
	assert.Equal(t, firstAccountID, res2.User.ID)
	assert.Equal(t, "alice-renamed", res2.User.Login)

	// A different numeric id later claims the old "alice" username. It must
	// NOT inherit the first account or its Stories.
	stub.setUser(200, "alice", "User")
	u3, err := svc.StartAuthorization(ctx, auth.PurposeWebLogin, "/", nil)
	require.NoError(t, err)
	res3, err := svc.CompleteAuthorization(ctx, "code-3", extractState(t, u3))
	require.NoError(t, err)
	assert.True(t, res3.IsNewAccount)
	assert.NotEqual(t, firstAccountID, res3.User.ID)
	assert.Equal(t, "alice", res3.User.Login)
}

func TestOrganizationAndBotAccountsAreRefused(t *testing.T) {
	ctx := context.Background()
	srv, stub := newStubGitHub(t)
	svc, _, _ := newService(t, srv.URL)

	for i, accType := range []string{"Organization", "Bot"} {
		stub.setUser(int64(500+i), fmt.Sprintf("machine-%d", i), accType)
		u, err := svc.StartAuthorization(ctx, auth.PurposeWebLogin, "/", nil)
		require.NoError(t, err)
		_, err = svc.CompleteAuthorization(ctx, "code", extractState(t, u))
		require.ErrorIsf(t, err, auth.ErrAccountTypeNotAllowed, "account type %s must be refused", accType)
	}
}

// ------------------------------------------------------------ pending login

func TestPendingLoginApproveThenSingleUsePoll(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newService(t, "")
	user := testdb.Account(t, st, 1, "alice")

	pl, err := svc.CreatePendingLogin(ctx, domain.ClientCLI, "alice's laptop")
	require.NoError(t, err)
	assert.NotEmpty(t, pl.PollingSecret)
	assert.Regexp(t, `^[A-Z0-9]{4}-[A-Z0-9]{4}$`, pl.UserCode)

	// Wrong polling secret must not reveal anything.
	_, err = svc.Poll(ctx, pl.ID, "not-the-real-secret")
	require.Error(t, err)

	// Wrong user code must not be accepted.
	err = svc.ApprovePendingLogin(ctx, pl.ID, user.ID, "WRONG-CODE")
	require.ErrorIs(t, err, auth.ErrUserCodeMismatch)

	// Correct approval.
	require.NoError(t, svc.ApprovePendingLogin(ctx, pl.ID, user.ID, pl.UserCode))

	// First poll collects the token.
	res, err := svc.Poll(ctx, pl.ID, pl.PollingSecret)
	require.NoError(t, err)
	assert.Equal(t, "approved", res.Status)
	require.NotEmpty(t, res.Token)

	// Second poll must NOT return the token again (single use).
	res2, err := svc.Poll(ctx, pl.ID, pl.PollingSecret)
	require.NoError(t, err)
	assert.NotEqual(t, "approved", res2.Status)
	assert.Empty(t, res2.Token)

	// The collected token is a real, working session.
	_, gotUser, err := svc.Authenticate(ctx, res.Token)
	require.NoError(t, err)
	assert.Equal(t, user.ID, gotUser.ID)
}

func TestPendingLoginExpiresUsingControllableClock(t *testing.T) {
	ctx := context.Background()
	svc, _, advance := newService(t, "")

	pl, err := svc.CreatePendingLogin(ctx, domain.ClientCLI, "laptop")
	require.NoError(t, err)

	advance(11 * time.Minute) // > 10 minute PendingLoginTTL

	res, err := svc.Poll(ctx, pl.ID, pl.PollingSecret)
	require.NoError(t, err)
	assert.Equal(t, "expired", res.Status)
}

// ----------------------------------------------------------------- session

func TestSessionIssueAuthenticateRevoke(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newService(t, "")
	user := testdb.Account(t, st, 1, "alice")

	token, sess, err := svc.IssueSession(ctx, user.ID, domain.ClientWeb, "browser", "UA/1.0")
	require.NoError(t, err)
	require.NotEmpty(t, token)

	_, gotUser, err := svc.Authenticate(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, user.ID, gotUser.ID)

	require.NoError(t, svc.Logout(ctx, user.ID, sess.ID))

	_, _, err = svc.Authenticate(ctx, token)
	require.ErrorIs(t, err, auth.ErrUnauthenticated)
}

func TestSuspendedUserCannotAuthenticate(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := newService(t, "")
	user := testdb.Account(t, st, 1, "alice")

	token, _, err := svc.IssueSession(ctx, user.ID, domain.ClientWeb, "browser", "")
	require.NoError(t, err)

	require.NoError(t, st.SuspendUser(ctx, nil, user.ID, "abuse"))

	_, _, err = svc.Authenticate(ctx, token)
	require.ErrorIs(t, err, auth.ErrUnauthenticated)
}

func TestAuthenticateRejectsEmptyBearer(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t, "")
	_, _, err := svc.Authenticate(ctx, "")
	require.ErrorIs(t, err, auth.ErrUnauthenticated)
}

// ---------------------------------------------------------------- test idp

func TestTestIdentityProviderCompiledIsFalseInNormalBuild(t *testing.T) {
	assert.False(t, auth.TestIdentityProviderCompiled(),
		"a normal build must not link the test identity provider")
}
