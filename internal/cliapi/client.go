package cliapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alliecatowo/gh-stories/internal/version"
)

// ServiceURLEnvVar is the environment variable that overrides the default
// service URL, per the CLI's --service flag / GHS_SERVICE_URL precedence.
const ServiceURLEnvVar = "GHS_SERVICE_URL"

// ResolveServiceURL applies the documented precedence for choosing a
// service base URL: an explicit --service flag value wins, then
// $GHS_SERVICE_URL, then the compiled-in default. flagValue should be the
// verbatim --service flag value, or "" if it was not provided.
func ResolveServiceURL(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv(ServiceURLEnvVar); v != "" {
		return v
	}
	return version.DefaultServiceURL
}

const (
	defaultTimeout    = 30 * time.Second
	defaultMaxRetries = 3
	defaultBackoff    = 250 * time.Millisecond
	maxBackoff        = 5 * time.Second
)

// Client is a typed HTTP client for the GitHub Stories API described by
// packages/contracts/openapi.yaml.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	userAgent  string
	maxRetries int
	backoff    time.Duration
	sleep      func(ctx context.Context, d time.Duration) error
}

// Option configures a Client constructed with New.
type Option func(*Client)

// WithHTTPClient overrides the underlying *http.Client (e.g. for a custom
// transport in tests).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// WithUserAgent overrides the User-Agent header sent with every request.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.userAgent = ua }
}

// WithMaxRetries overrides how many additional attempts are made after a
// retryable failure (429, 5xx, or a transport error). The default is 3.
func WithMaxRetries(n int) Option {
	return func(c *Client) { c.maxRetries = n }
}

// WithBackoff overrides the base exponential-backoff delay used between
// retries when the server does not specify Retry-After.
func WithBackoff(d time.Duration) Option {
	return func(c *Client) { c.backoff = d }
}

// WithSleeper overrides how the client waits between retries. It exists so
// tests can make backoff instantaneous instead of asserting against real
// wall-clock delays; production code should never need this. The function
// must respect ctx cancellation.
func WithSleeper(f func(ctx context.Context, d time.Duration) error) Option {
	return func(c *Client) { c.sleep = f }
}

// New constructs a Client for baseURL (typically the result of
// ResolveServiceURL), authenticating requests with token when non-empty
// (some endpoints, like the login flow itself, are unauthenticated).
func New(baseURL, token string, opts ...Option) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		httpClient: &http.Client{Timeout: defaultTimeout},
		userAgent:  "gh-stories/" + version.Short(),
		maxRetries: defaultMaxRetries,
		backoff:    defaultBackoff,
		sleep:      ctxSleep,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// BaseURL returns the service base URL this client talks to.
func (c *Client) BaseURL() string { return c.baseURL }

func ctxSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// requestSpec fully describes one HTTP request, before retry/auth wrapping.
type requestSpec struct {
	method  string
	path    string // e.g. "/stories/mine"; joined onto baseURL + "/v1"
	query   url.Values
	headers map[string]string
	body    []byte // pre-encoded JSON, or nil
}

func (c *Client) url(spec requestSpec) string {
	u := c.baseURL + "/v1" + spec.path
	if len(spec.query) > 0 {
		u += "?" + spec.query.Encode()
	}
	return u
}

// do executes spec with retry-with-backoff on 429/5xx and transport errors,
// honouring Retry-After on 429, and decodes a JSON response into out (which
// may be nil for responses with no body, e.g. 204).
func (c *Client) do(ctx context.Context, spec requestSpec, out any) error {
	var lastErr error
	attempts := c.maxRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := c.retryDelay(attempt, lastErr)
			if err := c.sleep(ctx, delay); err != nil {
				return err
			}
		}

		resp, err := c.roundTrip(ctx, spec)
		if err != nil {
			lastErr = &NetworkError{Err: err}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = &NetworkError{Err: readErr}
			continue
		}

		if resp.StatusCode == http.StatusNoContent || len(body) == 0 {
			if resp.StatusCode >= 400 {
				lastErr = httpStatusError(resp.StatusCode)
				if isRetryable(resp.StatusCode) {
					continue
				}
				return lastErr
			}
			return nil
		}

		if resp.StatusCode >= 400 {
			apiErr := decodeAPIError(resp.StatusCode, body)
			if ra := resp.Header.Get("Retry-After"); ra != "" && apiErr.RetryAfter == 0 {
				if secs, err := strconv.Atoi(ra); err == nil {
					apiErr.RetryAfter = secs
				}
			}
			lastErr = apiErr
			if isRetryable(resp.StatusCode) {
				continue
			}
			return apiErr
		}

		if out != nil {
			if err := json.Unmarshal(body, out); err != nil {
				return fmt.Errorf("cliapi: decode response from %s %s: %w", spec.method, spec.path, err)
			}
		}
		return nil
	}
	return lastErr
}

func (c *Client) retryDelay(attempt int, lastErr error) time.Duration {
	var apiErr *APIError
	if errors.As(lastErr, &apiErr) && apiErr.RetryAfter > 0 {
		return time.Duration(apiErr.RetryAfter) * time.Second
	}
	d := c.backoff << uint(attempt-1)
	if d > maxBackoff || d <= 0 {
		d = maxBackoff
	}
	return d
}

func isRetryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func httpStatusError(status int) *APIError {
	return &APIError{Status: status, Code: "unavailable", Message: fmt.Sprintf("Request failed with HTTP %d.", status)}
}

func decodeAPIError(status int, body []byte) *APIError {
	var parsed struct {
		Error struct {
			Code       string `json:"code"`
			Message    string `json:"message"`
			Field      string `json:"field"`
			RetryAfter int    `json:"retry_after_seconds"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Error.Code == "" {
		return &APIError{
			Status:  status,
			Code:    "internal",
			Message: fmt.Sprintf("The service returned an unexpected error (HTTP %d).", status),
		}
	}
	return &APIError{
		Status:     status,
		Code:       parsed.Error.Code,
		Message:    parsed.Error.Message,
		Field:      parsed.Error.Field,
		RetryAfter: parsed.Error.RetryAfter,
	}
}

func (c *Client) roundTrip(ctx context.Context, spec requestSpec) (*http.Response, error) {
	var bodyReader io.Reader
	if spec.body != nil {
		bodyReader = bytes.NewReader(spec.body)
	}
	req, err := http.NewRequestWithContext(ctx, spec.method, c.url(spec), bodyReader)
	if err != nil {
		return nil, err
	}
	if spec.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range spec.headers {
		req.Header.Set(k, v)
	}
	return c.httpClient.Do(req)
}

// get, postJSON, patchJSON, putJSON, deleteReq are small helpers used by the
// endpoint methods in endpoints*.go to build a requestSpec without repeating
// JSON-encoding boilerplate at every call site.

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, requestSpec{method: http.MethodGet, path: path, query: query}, out)
}

func (c *Client) postJSON(ctx context.Context, path string, headers map[string]string, body, out any) error {
	return c.jsonReq(ctx, http.MethodPost, path, headers, body, out)
}

func (c *Client) patchJSON(ctx context.Context, path string, headers map[string]string, body, out any) error {
	return c.jsonReq(ctx, http.MethodPatch, path, headers, body, out)
}

func (c *Client) putJSON(ctx context.Context, path string, headers map[string]string, body, out any) error {
	return c.jsonReq(ctx, http.MethodPut, path, headers, body, out)
}

func (c *Client) putEmpty(ctx context.Context, path string, out any) error {
	return c.do(ctx, requestSpec{method: http.MethodPut, path: path}, out)
}

func (c *Client) deleteReq(ctx context.Context, path string) error {
	return c.do(ctx, requestSpec{method: http.MethodDelete, path: path}, nil)
}

func (c *Client) jsonReq(ctx context.Context, method, path string, headers map[string]string, body, out any) error {
	var encoded []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("cliapi: encode request body for %s %s: %w", method, path, err)
		}
		encoded = b
	}
	return c.do(ctx, requestSpec{method: method, path: path, headers: headers, body: encoded}, out)
}
