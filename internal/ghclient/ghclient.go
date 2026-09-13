// Package ghclient is a small, purpose-built client for the slice of the
// GitHub REST and OAuth APIs that GitHub Stories needs: exchanging an
// authorization code for a token, reading the authenticated user's own
// profile, and reading who that user follows. It never writes to GitHub.
//
// Every network call is bounded: a per-attempt timeout, a response body size
// cap, a page cap and an identity cap on pagination, and a bounded number of
// retries on rate limiting. Nothing here ever logs a raw token; use redact.
package ghclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// DefaultAPIBaseURL is GitHub's REST API host used for /user and
// /user/following.
const DefaultAPIBaseURL = "https://api.github.com"

// DefaultAuthBaseURL is GitHub's web host used for the OAuth authorize and
// token-exchange endpoints. It is a different host from the API host in
// production; Options.BaseURL overrides both together so a single
// httptest.Server can stand in for the whole surface in tests.
const DefaultAuthBaseURL = "https://github.com"

// Scope is the OAuth scope string requested when starting the
// authorization-code flow.
//
// It is intentionally empty. GitHub grants a token issued with NO scopes at
// all read access to the authenticated user's own public profile (GET /user)
// and their public follow relationships (GET /user/following) — that is
// everything this product needs to identify a signer and, opt-in, import
// who they already follow. We never write to GitHub (no follow/unfollow, no
// repo access, no email, no admin scopes), so requesting anything broader —
// user:follow, repo, read:org, etc. — would only inflate the consent screen
// GitHub shows the user with permissions we do not use.
const Scope = ""

const (
	// maxBodyBytes bounds every response body we will read. GitHub responses
	// for the endpoints we call are small (a handful of KB per page); a
	// response past this cap is treated as an error rather than read fully
	// into memory.
	maxBodyBytes = 5 << 20 // 5 MiB

	// perRequestTimeout bounds a single HTTP round trip.
	perRequestTimeout = 15 * time.Second

	// maxRetries bounds how many times a retryable request (rate limiting,
	// or a transient network error) is retried before we give up. Holding an
	// HTTP handler goroutine open for GitHub's full rate-limit window
	// (potentially up to an hour) would be worse than surfacing a typed
	// error the caller can act on (e.g. defer an import job).
	maxRetries = 3

	// backoff bounds. We honor Retry-After / X-RateLimit-Reset when present,
	// but never sleep longer than maxBackoff in one attempt.
	minBackoff = 100 * time.Millisecond
	maxBackoff = 2 * time.Second
)

// Options configures a Client.
type Options struct {
	// BaseURL, when set, overrides BOTH the REST API base and the OAuth web
	// base. Production leaves this empty and the client uses GitHub's real
	// hosts (api.github.com and github.com). Tests point it at a single
	// httptest.Server that serves both surfaces.
	BaseURL string
	// HTTPClient is the transport used for every request. If nil, a client
	// with a generous overall timeout is constructed.
	HTTPClient *http.Client
	// UserAgent is sent on every request. GitHub rejects unauthenticated
	// requests without one. If empty, a default is used.
	UserAgent string
}

// Client is a bounded, minimal-scope GitHub API client.
type Client struct {
	apiBase   string
	authBase  string
	http      *http.Client
	userAgent string
	// sleep is the backoff sleep function. Overridable only in tests so a
	// rate-limit retry test does not have to wait for real wall-clock time.
	sleep func(time.Duration)
}

// New constructs a Client from Options.
func New(opts Options) *Client {
	apiBase := DefaultAPIBaseURL
	authBase := DefaultAuthBaseURL
	if opts.BaseURL != "" {
		apiBase = opts.BaseURL
		authBase = opts.BaseURL
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: perRequestTimeout + 5*time.Second}
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = "github-stories/1.0 (+https://github.com/alliecatowo/gh-stories)"
	}
	return &Client{
		apiBase:   apiBase,
		authBase:  authBase,
		http:      httpClient,
		userAgent: ua,
		sleep:     time.Sleep,
	}
}

// ----------------------------------------------------------------- errors

// ErrUnauthorized means GitHub rejected the token (HTTP 401).
var ErrUnauthorized = errors.New("ghclient: unauthorized")

// ErrForbidden means GitHub refused the request for a reason other than rate
// limiting (HTTP 403 without rate-limit headers) — most commonly an
// insufficient scope or a blocked account.
var ErrForbidden = errors.New("ghclient: forbidden")

// APIError is a GitHub API error response that isn't one of the sentinel
// cases above.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("ghclient: github api error (status %d): %s", e.Status, e.Message)
}

// RateLimitError is returned when GitHub is rate limiting us and the bounded
// retry budget has been exhausted.
type RateLimitError struct {
	// Reset is when GitHub reports the limit will lift, if known.
	Reset time.Time
	// Attempts is how many requests were made before giving up.
	Attempts int
}

func (e *RateLimitError) Error() string {
	if e.Reset.IsZero() {
		return fmt.Sprintf("ghclient: rate limited by github after %d attempt(s)", e.Attempts)
	}
	return fmt.Sprintf("ghclient: rate limited by github after %d attempt(s), resets at %s",
		e.Attempts, e.Reset.UTC().Format(time.RFC3339))
}

// redact returns a fixed placeholder for a secret value. It exists so a
// token can never end up verbatim in a log line or debug message, even by
// accident — call sites use this instead of interpolating the token itself.
func redact(secret string) string {
	if secret == "" {
		return ""
	}
	return "***redacted***"
}

// ------------------------------------------------------------ body reading

// readLimited reads at most limit+1 bytes so we can distinguish "exactly at
// the cap" from "over the cap" without buffering an unbounded body.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("ghclient: read response body: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("ghclient: response body exceeds %d byte cap", limit)
	}
	return data, nil
}

// ------------------------------------------------------------- retry core

// doWithRetry executes buildReq (which must produce a fresh, unsent request
// each call, since a body reader can only be consumed once) with a bounded
// per-attempt timeout, retrying on rate limiting and on transient network
// errors up to maxRetries times. token is used only for redacted logging.
func (c *Client) doWithRetry(ctx context.Context, token string, buildReq func() (*http.Request, error)) (*http.Response, []byte, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := buildReq()
		if err != nil {
			return nil, nil, err
		}
		reqCtx, cancel := context.WithTimeout(ctx, perRequestTimeout)
		req = req.WithContext(reqCtx)

		resp, err := c.http.Do(req)
		if err != nil {
			cancel()
			lastErr = fmt.Errorf("ghclient: request failed: %w", err)
			if attempt < maxRetries && ctx.Err() == nil {
				slog.Debug("ghclient: retrying after transport error",
					"attempt", attempt+1, "token", redact(token), "err", err)
				c.sleep(backoffFor(attempt))
				continue
			}
			return nil, nil, lastErr
		}

		body, sizeErr := readLimited(resp.Body, maxBodyBytes)
		_ = resp.Body.Close()
		cancel()
		if sizeErr != nil {
			return nil, nil, sizeErr
		}

		if rateLimited(resp) {
			reset := rateLimitReset(resp)
			if attempt < maxRetries {
				wait := rateLimitWait(resp, reset)
				slog.Debug("ghclient: retrying after rate limit",
					"attempt", attempt+1, "token", redact(token), "wait", wait)
				c.sleep(wait)
				continue
			}
			return nil, nil, &RateLimitError{Reset: reset, Attempts: attempt + 1}
		}

		return resp, body, nil
	}
	return nil, nil, lastErr
}

// rateLimited reports whether a response indicates GitHub is throttling us,
// as opposed to a plain 403/429 for some other reason.
func rateLimited(resp *http.Response) bool {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return false
	}
	if resp.Header.Get("Retry-After") != "" {
		return true
	}
	return resp.Header.Get("X-RateLimit-Remaining") == "0"
}

// rateLimitReset resolves the instant GitHub says the limit lifts, preferring
// Retry-After (relative seconds) and falling back to X-RateLimit-Reset (a
// unix timestamp).
func rateLimitReset(resp *http.Response) time.Time {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := parseSeconds(ra); err == nil {
			return time.Now().Add(time.Duration(secs) * time.Second)
		}
	}
	if rl := resp.Header.Get("X-RateLimit-Reset"); rl != "" {
		if secs, err := parseSeconds(rl); err == nil {
			return time.Unix(secs, 0)
		}
	}
	return time.Time{}
}

// rateLimitWait turns a reset instant into a bounded sleep duration.
func rateLimitWait(resp *http.Response, reset time.Time) time.Duration {
	var wait time.Duration
	if !reset.IsZero() {
		wait = time.Until(reset)
	}
	if wait <= 0 {
		wait = minBackoff
	}
	if wait > maxBackoff {
		wait = maxBackoff
	}
	return wait
}

// backoffFor is the transient-network-error backoff schedule: a short,
// bounded linear ramp.
func backoffFor(attempt int) time.Duration {
	wait := minBackoff * time.Duration(attempt+1)
	if wait > maxBackoff {
		wait = maxBackoff
	}
	return wait
}

func parseSeconds(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

// classify maps a completed, non-rate-limited response to a Go error, or nil
// on success.
func classify(resp *http.Response, body []byte) error {
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		return ErrForbidden
	default:
		return &APIError{Status: resp.StatusCode, Message: apiErrorMessage(body)}
	}
}

func apiErrorMessage(body []byte) string {
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Message != "" {
		return payload.Message
	}
	if len(body) == 0 {
		return "no error body"
	}
	if len(body) > 200 {
		body = body[:200]
	}
	return string(body)
}
