package cliapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// noSleep makes retry backoff instantaneous but still records how long the
// client asked to wait, so tests can assert Retry-After was honoured
// without actually blocking on it.
func noSleep(recorded *[]time.Duration) func(ctx context.Context, d time.Duration) error {
	return func(ctx context.Context, d time.Duration) error {
		*recorded = append(*recorded, d)
		return nil
	}
}

func newTestClient(t *testing.T, handler http.HandlerFunc, opts ...Option) (*Client, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	allOpts := append([]Option{WithMaxRetries(3)}, opts...)
	c := New(srv.URL, "test-token", allOpts...)
	return c, &calls
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
}

func TestClient_DecodesStructuredError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, http.StatusNotFound, "not_found", "Not found.")
	})
	_, err := c.GetStory(context.Background(), "story-1")
	if err == nil {
		t.Fatal("expected an error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusNotFound || apiErr.Code != "not_found" {
		t.Fatalf("got %+v", apiErr)
	}
}

func TestClient_ForbiddenWithFieldIsNotRetried(t *testing.T) {
	c, calls := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "forbidden", "message": "Reactions disabled.", "field": "allow_reactions"},
		})
	})
	_, err := c.SetReaction(context.Background(), "story-1", "❤️", "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Field != "allow_reactions" {
		t.Fatalf("got field %q", apiErr.Field)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("expected exactly 1 call for a non-retryable 403, got %d", got)
	}
}

func TestClient_RetryAfterIsRetriedThenSurfaced(t *testing.T) {
	var seenDelays []time.Duration
	attempt := int32(0)
	c, calls := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempt, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "7")
			writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Me{User: PublicUser{Login: "alice"}})
	}, WithSleeper(noSleep(&seenDelays)))

	me, err := c.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if me.User.Login != "alice" {
		t.Fatalf("got %+v", me)
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Fatalf("expected 2 calls (1 rate-limited + 1 success), got %d", got)
	}
	if len(seenDelays) != 1 || seenDelays[0] != 7*time.Second {
		t.Fatalf("expected a single 7s retry delay from Retry-After, got %v", seenDelays)
	}
}

func TestClient_RetryAfterExhaustedIsSurfacedAsAPIError(t *testing.T) {
	var seenDelays []time.Duration
	c, calls := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests.")
	}, WithMaxRetries(2), WithSleeper(noSleep(&seenDelays)))

	_, err := c.Me(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Code != "rate_limited" || apiErr.RetryAfter != 1 {
		t.Fatalf("got %+v", apiErr)
	}
	// maxRetries=2 means 3 total attempts.
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Fatalf("expected 3 total attempts, got %d", got)
	}
}

func TestClient_5xxIsRetried(t *testing.T) {
	attempt := int32(0)
	var seenDelays []time.Duration
	c, calls := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempt, 1)
		if n < 3 {
			writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "Try again.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Me{User: PublicUser{Login: "bob"}})
	}, WithSleeper(noSleep(&seenDelays)))

	me, err := c.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if me.User.Login != "bob" {
		t.Fatalf("got %+v", me)
	}
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Fatalf("expected 3 calls, got %d", got)
	}
}

func TestClient_IdempotencyKeyHeaderSentOnRetry(t *testing.T) {
	var seenKeys []string
	attempt := int32(0)
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenKeys = append(seenKeys, r.Header.Get("Idempotency-Key"))
		n := atomic.AddInt32(&attempt, 1)
		if n == 1 {
			writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "Try again.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Reply{ID: "reply-1"})
	}, WithSleeper(noSleep(&[]time.Duration{})))

	_, err := c.Reply(context.Background(), "story-1", "lmao", "fixed-idem-key")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if len(seenKeys) != 2 || seenKeys[0] != "fixed-idem-key" || seenKeys[1] != "fixed-idem-key" {
		t.Fatalf("expected the same idempotency key resent on retry, got %v", seenKeys)
	}
}

func TestClient_UploadFinalizePollProcessingToPublished(t *testing.T) {
	var uploadedBytes []byte
	pollCount := int32(0)
	mux := http.NewServeMux()
	var putURL string

	srv := httptest.NewServer(nil)
	t.Cleanup(srv.Close)
	putURL = srv.URL + "/put-here"

	mux.HandleFunc("/v1/uploads", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(UploadIntent{
			UploadID: "up-1", Method: "PUT", URL: putURL, MaxBytes: 1 << 20,
		})
	})
	mux.HandleFunc("/put-here", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		uploadedBytes = b
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v1/uploads/up-1/finalize", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(StoryItem{ID: "story-1", State: "processing"})
	})
	mux.HandleFunc("/v1/stories/story-1", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&pollCount, 1)
		w.Header().Set("Content-Type", "application/json")
		state := "processing"
		if n >= 2 {
			state = "published"
		}
		_ = json.NewEncoder(w).Encode(StoryItem{ID: "story-1", State: state})
	})
	srv.Config.Handler = mux

	c := New(srv.URL, "test-token", WithSleeper(func(ctx context.Context, d time.Duration) error { return nil }))

	var states []string
	payload := []byte("fake-image-bytes")
	item, err := c.Upload(context.Background(), UploadRequest{
		Meta:           UploadIntentRequest{MIME: "image/png", ByteSize: int64(len(payload))},
		Reader:         bytesReader(payload),
		Size:           int64(len(payload)),
		ChecksumSHA256: "deadbeef",
		IdempotencyKey: "upload-key-1",
		OnStateChange:  func(s *StoryItem) { states = append(states, s.State) },
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if item.State != "published" {
		t.Fatalf("final state = %q, want published", item.State)
	}
	if string(uploadedBytes) != string(payload) {
		t.Fatalf("uploaded bytes = %q, want %q", uploadedBytes, payload)
	}
	if len(states) < 2 || states[0] != "processing" || states[len(states)-1] != "published" {
		t.Fatalf("expected a processing -> published transition, got %v", states)
	}
}

func bytesReader(b []byte) io.Reader { return &sliceReader{b: b} }

type sliceReader struct {
	b []byte
	i int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
