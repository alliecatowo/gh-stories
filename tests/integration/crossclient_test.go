// Package integration exercises the whole vertical slice with real
// infrastructure: real PostgreSQL, real MinIO object storage, the real media
// worker (real ffmpeg), the real HTTP API and the real authorization rules.
//
// Only the external GitHub identity boundary is substituted.
package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/api"
	"github.com/alliecatowo/gh-stories/internal/clock"
	"github.com/alliecatowo/gh-stories/internal/config"
	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/media"
	"github.com/alliecatowo/gh-stories/internal/objstore"
	"github.com/alliecatowo/gh-stories/internal/store"
	"github.com/alliecatowo/gh-stories/internal/testdb"
	"github.com/alliecatowo/gh-stories/internal/worker"
)

const (
	minioEndpoint = "http://localhost:55900"
	minioBucket   = "stories-media"
	minioKey      = "storiesminio"
	minioSecret   = "storiesminio_local_dev"
)

type env struct {
	t       *testing.T
	srv     *httptest.Server
	store   *store.Store
	clock   *clock.Controllable
	objects objstore.Store
	auth    *identityStub
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, clk := testdb.New(t)

	objects, err := objstore.New(objstore.Config{
		Endpoint: minioEndpoint, Region: "us-east-1", Bucket: minioBucket,
		AccessKeyID: minioKey, SecretKey: minioSecret, ForcePathStyle: true,
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := objects.Healthy(ctx); err != nil {
		t.Skipf("MinIO unavailable at %s (%v); start it with `mise run infra:up`", minioEndpoint, err)
	}

	cfg := loadConfig(t)
	stub := &identityStub{store: st}
	srv := api.New(cfg, st, clk, &reader{objects}, objects, stub,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return &env{t: t, srv: ts, store: st, clock: clk, objects: objects, auth: stub}
}

func loadConfig(t *testing.T) *config.Config {
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
			return minioBucket, true
		case "GHS_S3_ACCESS_KEY_ID":
			return minioKey, true
		case "GHS_S3_SECRET_ACCESS_KEY":
			return minioSecret, true
		case "GHS_SECRET_KEY":
			return "integration-secret-key-long-enough-0123456789", true
		}
		return "", false
	})
	require.NoError(t, err)
	return cfg
}

// runWorker starts the real media+cleanup worker for the duration of the test.
func (e *env) runWorker() {
	e.t.Helper()
	w := worker.New(e.store, e.objects, e.clock,
		slog.New(slog.NewTextHandler(io.Discard, nil)), worker.Config{
			Role:               worker.RoleBoth,
			WorkerID:           "integration-" + uuid.NewString()[:8],
			MediaPollInterval:  50 * time.Millisecond,
			MediaLeaseDuration: time.Minute,
			MediaLimits:        media.DefaultLimits(),
			CleanupInterval:    200 * time.Millisecond,
			StoryLifetime:      24 * time.Hour,
		})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = w.Run(ctx) }()
	e.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			e.t.Log("worker did not shut down within 10s")
		}
	})
}

// ------------------------------------------------------------------ actors

type client struct {
	e     *env
	User  *domain.User
	Token string
}

func (e *env) client(gitHubID int64, login string) *client {
	e.t.Helper()
	u := testdb.Account(e.t, e.store, gitHubID, login)
	token := "itest-" + uuid.NewString()
	_, err := e.store.CreateSession(context.Background(), nil, u.ID, token,
		domain.ClientCLI, "integration", "integration", 365*24*time.Hour)
	require.NoError(e.t, err)
	return &client{e: e, User: u, Token: token}
}

func (c *client) req(method, path string, body any, headers map[string]string) *http.Response {
	c.e.t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(c.e.t, err)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.e.srv.URL+path, rdr)
	require.NoError(c.e.t, err)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.e.srv.Client().Do(req)
	require.NoError(c.e.t, err)
	return resp
}

func jsonOf(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var v map[string]any
	raw, _ := io.ReadAll(resp.Body)
	require.NoError(t, json.Unmarshal(raw, &v), "body was: %s", string(raw))
	return v
}

// post walks the real client posting path: request an upload authorization,
// PUT the bytes straight to private object storage, then finalize.
func (c *client) post(t *testing.T, fixture string, extra map[string]any) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "fixtures", fixture))
	require.NoError(t, err)
	sum := sha256.Sum256(raw)

	body := map[string]any{
		"mime":      mimeOf(fixture),
		"byte_size": len(raw),
		"filename":  fixture,
	}
	for k, v := range extra {
		body[k] = v
	}
	resp := c.req(http.MethodPost, "/v1/uploads", body,
		map[string]string{"Idempotency-Key": "post-" + fixture})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	intent := jsonOf(t, resp)

	// Upload directly to private object storage using the short-lived
	// authorization, exactly as a real client would.
	putReq, err := http.NewRequest(http.MethodPut, intent["url"].(string), bytes.NewReader(raw))
	require.NoError(t, err)
	if hdrs, ok := intent["headers"].(map[string]any); ok {
		for k, v := range hdrs {
			putReq.Header.Set(k, fmt.Sprint(v))
		}
	}
	putResp, err := http.DefaultClient.Do(putReq)
	require.NoError(t, err)
	putBody, _ := io.ReadAll(putResp.Body)
	putResp.Body.Close()
	require.Equal(t, http.StatusOK, putResp.StatusCode,
		"presigned upload failed: %s", string(putBody))

	resp = c.req(http.MethodPost, "/v1/uploads/"+intent["upload_id"].(string)+"/finalize",
		map[string]any{
			"byte_size":       len(raw),
			"checksum_sha256": hex.EncodeToString(sum[:]),
		}, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode,
		"a finalized upload is accepted for processing, not yet published")
	item := jsonOf(t, resp)
	require.Equal(t, "processing", item["state"],
		"a successful upload is NOT yet 'Story posted'")
	return item["id"].(string)
}

// awaitPublished polls the way a real client does.
func (c *client) awaitPublished(t *testing.T, storyID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		resp := c.req(http.MethodGet, "/v1/stories/"+storyID, nil, nil)
		last = jsonOf(t, resp)
		switch last["state"] {
		case "published":
			return last
		case "failed":
			t.Fatalf("processing failed: %v — %v", last["failure_code"], last["failure_message"])
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("story %s did not publish within 90s; last state: %v", storyID, last)
	return nil
}

func mimeOf(name string) string {
	switch filepath.Ext(name) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	}
	return "application/octet-stream"
}

type reader struct{ s objstore.Store }

func (r *reader) Get(ctx context.Context, key, rng string) (*api.ObjectData, error) {
	o, err := r.s.Get(ctx, key, rng)
	if err != nil {
		return nil, err
	}
	return &api.ObjectData{Body: o.Body, ContentType: o.ContentType,
		ContentLength: o.ContentLength, ContentRange: o.ContentRange,
		ETag: o.ETag, StatusCode: o.StatusCode}, nil
}
func (r *reader) Healthy(ctx context.Context) error { return r.s.Healthy(ctx) }

// identityStub substitutes ONLY the external GitHub identity boundary.
type identityStub struct{ store *store.Store }

func (i *identityStub) StartAuthorization(context.Context, string, string, *uuid.UUID) (string, error) {
	return "", nil
}
func (i *identityStub) CompleteAuthorization(context.Context, string, string) (*api.AuthResult, error) {
	return nil, context.Canceled
}
func (i *identityStub) IssueSession(ctx context.Context, userID uuid.UUID,
	kind domain.ClientKind, label, ua string) (string, *domain.Session, error) {
	token := "itest-" + uuid.NewString()
	sess, err := i.store.CreateSession(ctx, nil, userID, token, kind, label, ua, 365*24*time.Hour)
	return token, sess, err
}
func (i *identityStub) Authenticate(ctx context.Context, bearer string) (*domain.Session, *domain.User, error) {
	return i.store.SessionByToken(ctx, bearer)
}
func (i *identityStub) Logout(ctx context.Context, u, s uuid.UUID) error {
	return i.store.RevokeSession(ctx, u, s)
}
func (i *identityStub) CreatePendingLogin(context.Context, domain.ClientKind, string) (*api.PendingLoginResult, error) {
	return nil, context.Canceled
}
func (i *identityStub) ApprovePendingLogin(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}
func (i *identityStub) DenyPendingLogin(context.Context, uuid.UUID) error { return nil }
func (i *identityStub) Poll(context.Context, uuid.UUID, string) (*store.PollResult, error) {
	return nil, context.Canceled
}
func (i *identityStub) ImportFollows(context.Context, uuid.UUID, string, bool, bool) (*api.ImportSummary, error) {
	return &api.ImportSummary{}, nil
}
func (i *identityStub) Configured() bool { return true }

// decodeImage fetches media through the authorization gateway and decodes it,
// proving the delivered bytes are a real, renderable picture.
func (c *client) decodeImage(t *testing.T, storyID, variant string) (image.Image, string) {
	t.Helper()
	resp := c.req(http.MethodGet, "/v1/media/"+storyID+"/"+variant, nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	defer resp.Body.Close()
	img, format, err := image.Decode(resp.Body)
	require.NoError(t, err, "the gateway must deliver decodable image bytes")
	return img, format
}
