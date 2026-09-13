package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/clock"
	"github.com/alliecatowo/gh-stories/internal/config"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/objstore"
	"github.com/alliecatowo/gh-stories/internal/store"
	"github.com/alliecatowo/gh-stories/internal/testdb"
)

// The harness runs the REAL router against a REAL PostgreSQL database and a
// real in-memory object store. Only the external GitHub identity boundary is
// substituted; every authorization rule under test is the production one.

type harness struct {
	t      *testing.T
	srv    *httptest.Server
	store  *store.Store
	clock  *clock.Controllable
	auth   *fakeAuth
	bucket objstore.Store
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, clk := testdb.New(t)
	bucket := objstore.NewMemory()
	cfg := testConfig(t)
	fa := &fakeAuth{store: st, clk: clk, tokens: map[string]uuid.UUID{}}

	srv := New(cfg, st, clk, &memReader{bucket}, bucket, fa, slog.New(slog.DiscardHandler))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &harness{t: t, srv: ts, store: st, clock: clk, auth: fa, bucket: bucket}
}

func testConfig(t *testing.T) *config.Config {
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
		}
		return "", false
	})
	require.NoError(t, err)
	return cfg
}

// memReader adapts the in-memory object store to the gateway's interface.
type memReader struct{ s objstore.Store }

func (m *memReader) Get(ctx context.Context, key, rangeHeader string) (*ObjectData, error) {
	obj, err := m.s.Get(ctx, key, rangeHeader)
	if err != nil {
		return nil, err
	}
	return &ObjectData{
		Body: obj.Body, ContentType: obj.ContentType,
		ContentLength: obj.ContentLength, ContentRange: obj.ContentRange,
		ETag: obj.ETag, StatusCode: obj.StatusCode,
	}, nil
}
func (m *memReader) Healthy(ctx context.Context) error { return m.s.Healthy(ctx) }

// fakeAuth substitutes ONLY the external identity boundary. Sessions,
// authorization and every product rule remain the real ones.
type fakeAuth struct {
	store  *store.Store
	clk    *clock.Controllable
	tokens map[string]uuid.UUID
}

func (f *fakeAuth) StartAuthorization(context.Context, string, string, *uuid.UUID) (string, error) {
	return "https://github.test/login/oauth/authorize", nil
}
func (f *fakeAuth) CompleteAuthorization(context.Context, string, string) (*AuthResult, error) {
	return nil, context.Canceled
}
func (f *fakeAuth) IssueSession(ctx context.Context, userID uuid.UUID,
	kind domain.ClientKind, label, ua string) (string, *domain.Session, error) {
	token := "test-" + uuid.NewString()
	// Deliberately far longer than any clock advance a test performs, so that
	// an expiry gate isolates STORY expiry rather than tripping over session
	// expiry. Session expiry itself is covered by internal/auth's own tests.
	sess, err := f.store.CreateSession(ctx, nil, userID, token, kind, label, ua, 365*24*time.Hour)
	if err != nil {
		return "", nil, err
	}
	f.tokens[token] = userID
	return token, sess, nil
}
func (f *fakeAuth) Authenticate(ctx context.Context, bearer string) (*domain.Session, *domain.User, error) {
	return f.store.SessionByToken(ctx, bearer)
}
func (f *fakeAuth) Logout(ctx context.Context, userID, sessionID uuid.UUID) error {
	return f.store.RevokeSession(ctx, userID, sessionID)
}
func (f *fakeAuth) CreatePendingLogin(context.Context, domain.ClientKind, string) (*PendingLoginResult, error) {
	return nil, context.Canceled
}
func (f *fakeAuth) ApprovePendingLogin(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}
func (f *fakeAuth) DenyPendingLogin(context.Context, uuid.UUID) error { return nil }
func (f *fakeAuth) Poll(context.Context, uuid.UUID, string) (*store.PollResult, error) {
	return nil, context.Canceled
}
func (f *fakeAuth) ImportFollows(context.Context, uuid.UUID, string, bool, bool) (*ImportSummary, error) {
	return &ImportSummary{}, nil
}
func (f *fakeAuth) Configured() bool { return true }

// ---------------------------------------------------------------- actors

type actor struct {
	h     *harness
	User  *domain.User
	Token string
}

func (h *harness) actor(gitHubID int64, login string) *actor {
	h.t.Helper()
	u := testdb.Account(h.t, h.store, gitHubID, login)
	token, _, err := h.auth.IssueSession(context.Background(), u.ID, domain.ClientCLI, "test", "test")
	require.NoError(h.t, err)
	return &actor{h: h, User: u, Token: token}
}

// do performs an authenticated request against the real router.
func (a *actor) do(method, path string, body any) *http.Response {
	a.h.t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(a.h.t, err)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, a.h.srv.URL+path, rdr)
	require.NoError(a.h.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	resp, err := a.h.srv.Client().Do(req)
	require.NoError(a.h.t, err)
	return resp
}

func (a *actor) get(path string) *http.Response { return a.do(http.MethodGet, path, nil) }

// getRange performs a ranged media request, as a video player would.
func (a *actor) getRange(path, rng string) *http.Response {
	a.h.t.Helper()
	req, err := http.NewRequest(http.MethodGet, a.h.srv.URL+path, nil)
	require.NoError(a.h.t, err)
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Range", rng)
	resp, err := a.h.srv.Client().Do(req)
	require.NoError(a.h.t, err)
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var v T
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&v))
	return v
}

func drain(resp *http.Response) []byte {
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b
}

// publish walks the real intent → finalize → worker-publish path and puts real
// bytes in the object store, so media requests exercise the whole gateway.
func (h *harness) publish(author *actor, vis domain.Visibility, listID *uuid.UUID, caption string) uuid.UUID {
	h.t.Helper()
	ctx := context.Background()
	key := "u/" + author.User.ID.String() + "/" + uuid.NewString()
	ui, err := h.store.CreateUploadIntent(ctx, author.User.ID, key, "image/jpeg", 32, 30*time.Minute)
	require.NoError(h.t, err)
	require.NoError(h.t, h.store.StashDraft(ctx, ui.ID, store.StoryDraft{
		Caption: caption, Visibility: vis, AudienceListID: listID,
		AllowReplies: true, AllowReactions: true,
	}, "fixture.jpg"))
	storyID, _, err := h.store.FinalizeUpload(ctx, author.User.ID, ui.ID, 32,
		"aa11bb22cc33dd44ee55ff6600112233445566778899aabbccddeeff00112233",
		store.StoryDraft{Caption: caption, Visibility: vis, AudienceListID: listID,
			AllowReplies: true, AllowReactions: true}, "fixture.jpg")
	require.NoError(h.t, err)

	job, err := h.store.ClaimMediaJob(ctx, "test", time.Minute)
	require.NoError(h.t, err)

	imageKey := "m/" + storyID.String() + "/image.jpg"
	thumbKey := "m/" + storyID.String() + "/thumb.jpg"
	imageBytes := bytes.Repeat([]byte("IMAGEDATA"), 64)
	require.NoError(h.t, h.bucket.Put(ctx, imageKey, "image/jpeg",
		bytes.NewReader(imageBytes), int64(len(imageBytes))))
	require.NoError(h.t, h.bucket.Put(ctx, thumbKey, "image/jpeg",
		bytes.NewReader([]byte("THUMB")), 5))

	_, _, err = h.store.PublishStory(ctx, store.PublishResult{
		JobID: job.ID, StoryID: storyID,
		Variants: []domain.MediaVariant{
			{Kind: domain.VariantImage, ObjectKey: imageKey, MIME: "image/jpeg",
				Width: 1080, Height: 1920, ByteSize: int64(len(imageBytes))},
			{Kind: domain.VariantThumb, ObjectKey: thumbKey, MIME: "image/jpeg",
				Width: 108, Height: 192, ByteSize: 5},
		},
	}, 24*time.Hour)
	require.NoError(h.t, err)
	return storyID
}

// doWithKey sends a request carrying an Idempotency-Key.
func (a *actor) doWithKey(method, path string, body any, key string) *http.Response {
	a.h.t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(a.h.t, err)
	req, err := http.NewRequest(method, a.h.srv.URL+path, bytes.NewReader(raw))
	require.NoError(a.h.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Idempotency-Key", key)
	resp, err := a.h.srv.Client().Do(req)
	require.NoError(a.h.t, err)
	return resp
}
