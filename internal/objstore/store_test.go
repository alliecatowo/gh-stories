package objstore_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/objstore"
)

// realConfig points at the local MinIO instance started by
// `mise run infra:up` (see infra/compose/docker-compose.yml). Tests in this
// file are integration tests against a real S3-compatible service, not
// against internal/objstore's in-memory test double.
func realConfig() objstore.Config {
	return objstore.Config{
		Endpoint:       "http://localhost:55900",
		Region:         "us-east-1",
		Bucket:         "stories-media",
		AccessKeyID:    "storiesminio",
		SecretKey:      "storiesminio_local_dev",
		ForcePathStyle: true,
	}
}

// requireMinIO skips (never fails) the test when the local MinIO instance is
// unreachable, so this suite does not break a machine that hasn't run
// `mise run infra:up`.
func requireMinIO(t *testing.T) objstore.Store {
	t.Helper()
	st, err := objstore.New(realConfig())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := st.Healthy(ctx); err != nil {
		t.Skipf("MinIO unreachable at %s (%v); start it with `mise run infra:up`", realConfig().Endpoint, err)
	}
	return st
}

func testKey(t *testing.T) string {
	t.Helper()
	return "test/" + t.Name() + "/" + time.Now().Format("20060102T150405.000000000")
}

func TestPutGetHeadDelete(t *testing.T) {
	st := requireMinIO(t)
	ctx := context.Background()
	key := testKey(t)
	body := []byte("hello private media")

	require.NoError(t, st.Put(ctx, key, "text/plain", bytes.NewReader(body), int64(len(body))))

	head, err := st.Head(ctx, key)
	require.NoError(t, err)
	require.Equal(t, int64(len(body)), head.ContentLength)
	require.Equal(t, "text/plain", head.ContentType)
	require.Equal(t, http.StatusOK, head.StatusCode)

	obj, err := st.Get(ctx, key, "")
	require.NoError(t, err)
	defer obj.Body.Close()
	got, err := io.ReadAll(obj.Body)
	require.NoError(t, err)
	require.Equal(t, body, got)
	require.Equal(t, http.StatusOK, obj.StatusCode)

	require.NoError(t, st.Delete(ctx, key))

	_, err = st.Head(ctx, key)
	require.ErrorIs(t, err, objstore.ErrNotExist)

	// Deleting an already-gone key must still succeed: cleanup jobs retry and
	// must be idempotent.
	require.NoError(t, st.Delete(ctx, key))
}

func TestGetRange(t *testing.T) {
	st := requireMinIO(t)
	ctx := context.Background()
	key := testKey(t)
	body := []byte("0123456789ABCDEFGHIJ")
	require.NoError(t, st.Put(ctx, key, "application/octet-stream", bytes.NewReader(body), int64(len(body))))
	t.Cleanup(func() { _ = st.Delete(context.Background(), key) })

	obj, err := st.Get(ctx, key, "bytes=5-9")
	require.NoError(t, err)
	defer obj.Body.Close()
	require.Equal(t, http.StatusPartialContent, obj.StatusCode)
	require.NotEmpty(t, obj.ContentRange)
	got, err := io.ReadAll(obj.Body)
	require.NoError(t, err)
	require.Equal(t, []byte("56789"), got)
}

func TestPresignPutRoundTrip(t *testing.T) {
	st := requireMinIO(t)
	ctx := context.Background()
	key := testKey(t)
	body := []byte("uploaded via a presigned PUT")

	url, headers, err := st.PresignPut(ctx, key, "text/plain", int64(len(body)), 5*time.Minute)
	require.NoError(t, err)
	require.NotEmpty(t, url)

	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	require.NoError(t, err)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.ContentLength = int64(len(body))

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	require.Truef(t, resp.StatusCode >= 200 && resp.StatusCode < 300,
		"presigned PUT failed: %d %s", resp.StatusCode, string(respBody))
	t.Cleanup(func() { _ = st.Delete(context.Background(), key) })

	obj, err := st.Get(ctx, key, "")
	require.NoError(t, err)
	defer obj.Body.Close()
	got, err := io.ReadAll(obj.Body)
	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestPresignPutRejectsWrongContentLength(t *testing.T) {
	st := requireMinIO(t)
	ctx := context.Background()
	key := testKey(t)
	declaredSize := int64(4)

	url, headers, err := st.PresignPut(ctx, key, "text/plain", declaredSize, 5*time.Minute)
	require.NoError(t, err)

	// Try to smuggle a much larger body than was declared at presign time.
	oversized := strings.Repeat("A", 4096)
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(oversized))
	require.NoError(t, err)
	for k, v := range headers {
		if k == "Content-Length" {
			continue // let Go set the real (mismatched) length for this request
		}
		req.Header.Set(k, v)
	}
	req.ContentLength = int64(len(oversized))

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.NotEqualf(t, http.StatusOK, resp.StatusCode,
		"presigned PUT for %d bytes must reject a %d-byte body", declaredSize, len(oversized))
}

func TestHealthy(t *testing.T) {
	st := requireMinIO(t)
	require.NoError(t, st.Healthy(context.Background()))
}
