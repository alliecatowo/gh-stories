// Package config loads and validates service configuration from the
// environment. It reports every missing variable by name, never by value, and
// it refuses to start a production process that has any test affordance
// enabled.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Env string

const (
	EnvLocal      Env = "local"
	EnvTest       Env = "test"
	EnvProduction Env = "production"
)

type Config struct {
	Env Env

	HTTPAddr       string
	PublicURL      *url.URL
	AllowedOrigins []string

	DatabaseURL string

	S3Endpoint       string
	S3Region         string
	S3Bucket         string
	S3AccessKeyID    string
	S3SecretKey      string
	S3ForcePathStyle bool

	GitHubClientID     string
	GitHubClientSecret string

	SecretKey []byte

	MaxUploadBytes  int64
	MaxVideoSeconds int
	MaxImagePixels  int64

	// ModeratorGitHubIDs are the GitHub numeric ids granted moderator access.
	//
	// Declared in configuration rather than promoted through the product, so
	// that moderator status is an operator decision, is visible in the
	// deployment, and cannot be escalated through any API surface. Identified
	// by numeric id because logins are renameable.
	ModeratorGitHubIDs []int64

	// Local/test only. Production startup fails if either is true.
	TestIdentityProvider bool
	TestClock            bool

	// Derived, non-configurable product rules.
	StoryLifetime        time.Duration
	ReplyRetention       time.Duration
	ViewRetention        time.Duration
	UploadIntentTTL      time.Duration
	SessionTTL           time.Duration
	PendingLoginTTL      time.Duration
	PhysicalDeleteTarget time.Duration
}

// MissingError lists required variables that were absent or empty. It never
// includes any value.
type MissingError struct{ Names []string }

func (e *MissingError) Error() string {
	sort.Strings(e.Names)
	return "missing required configuration: " + strings.Join(e.Names, ", ")
}

// Load reads configuration from the process environment.
func Load() (*Config, error) { return load(os.LookupEnv) }

// LoadFrom is Load against an explicit lookup function, used by tests.
func LoadFrom(lookup func(string) (string, bool)) (*Config, error) { return load(lookup) }

func load(lookup func(string) (string, bool)) (*Config, error) {
	get := func(k, def string) string {
		if v, ok := lookup(k); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return def
	}
	var missing []string
	req := func(k string) string {
		v := get(k, "")
		if v == "" {
			missing = append(missing, k)
		}
		return v
	}

	c := &Config{
		Env:      Env(get("GHS_ENV", string(EnvLocal))),
		HTTPAddr: get("GHS_HTTP_ADDR", ":8787"),

		DatabaseURL: req("GHS_DATABASE_URL"),

		S3Endpoint:    get("GHS_S3_ENDPOINT", ""),
		S3Region:      get("GHS_S3_REGION", "us-east-1"),
		S3Bucket:      req("GHS_S3_BUCKET"),
		S3AccessKeyID: req("GHS_S3_ACCESS_KEY_ID"),
		S3SecretKey:   req("GHS_S3_SECRET_ACCESS_KEY"),

		GitHubClientID:     get("GHS_GITHUB_CLIENT_ID", ""),
		GitHubClientSecret: get("GHS_GITHUB_CLIENT_SECRET", ""),

		StoryLifetime:        24 * time.Hour,
		ReplyRetention:       30 * 24 * time.Hour,
		ViewRetention:        7 * 24 * time.Hour,
		UploadIntentTTL:      30 * time.Minute,
		SessionTTL:           90 * 24 * time.Hour,
		PendingLoginTTL:      10 * time.Minute,
		PhysicalDeleteTarget: 15 * time.Minute,
	}

	switch c.Env {
	case EnvLocal, EnvTest, EnvProduction:
	default:
		return nil, fmt.Errorf("GHS_ENV must be one of local, test, production")
	}

	raw := req("GHS_PUBLIC_URL")
	if raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, errors.New("GHS_PUBLIC_URL must be an absolute URL")
		}
		u.Path = strings.TrimSuffix(u.Path, "/")
		c.PublicURL = u
	}

	for _, o := range strings.Split(get("GHS_ALLOWED_ORIGINS", ""), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.AllowedOrigins = append(c.AllowedOrigins, strings.TrimSuffix(o, "/"))
		}
	}

	secret := req("GHS_SECRET_KEY")
	c.SecretKey = []byte(secret)

	c.S3ForcePathStyle = boolOf(get("GHS_S3_FORCE_PATH_STYLE", "false"))
	c.TestIdentityProvider = boolOf(get("GHS_TEST_IDENTITY_PROVIDER", "false"))
	c.TestClock = boolOf(get("GHS_TEST_CLOCK", "false"))

	for _, raw := range strings.Split(get("GHS_MODERATOR_GITHUB_IDS", ""), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf(
				"GHS_MODERATOR_GITHUB_IDS must be a comma-separated list of GitHub numeric user ids")
		}
		c.ModeratorGitHubIDs = append(c.ModeratorGitHubIDs, id)
	}

	c.MaxUploadBytes = int64Of(get("GHS_MAX_UPLOAD_BYTES", "104857600"))
	c.MaxVideoSeconds = intOf(get("GHS_MAX_VIDEO_SECONDS", "60"))
	c.MaxImagePixels = int64Of(get("GHS_MAX_IMAGE_PIXELS", "50000000"))

	if len(missing) > 0 {
		return nil, &MissingError{Names: missing}
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) validate() error {
	if len(c.SecretKey) < 32 {
		return errors.New("GHS_SECRET_KEY must be at least 32 bytes")
	}
	if c.MaxUploadBytes <= 0 || c.MaxVideoSeconds <= 0 || c.MaxImagePixels <= 0 {
		return errors.New("upload limits must be positive")
	}
	if c.Env != EnvProduction {
		return nil
	}

	// Production safety. These are hard refusals, not warnings: a release
	// artifact must never be able to run with a test identity provider, a
	// controllable clock, a development secret or a localhost service URL.
	var problems []string
	if c.TestIdentityProvider {
		problems = append(problems, "GHS_TEST_IDENTITY_PROVIDER must be false in production")
	}
	if c.TestClock {
		problems = append(problems, "GHS_TEST_CLOCK must be false in production")
	}
	if c.GitHubClientID == "" || c.GitHubClientSecret == "" {
		problems = append(problems, "GHS_GITHUB_CLIENT_ID and GHS_GITHUB_CLIENT_SECRET are required in production")
	}
	if c.PublicURL != nil {
		if c.PublicURL.Scheme != "https" {
			problems = append(problems, "GHS_PUBLIC_URL must use https in production")
		}
		if isLoopback(c.PublicURL.Hostname()) {
			problems = append(problems, "GHS_PUBLIC_URL must not be a loopback address in production")
		}
	}
	if strings.Contains(strings.ToLower(string(c.SecretKey)), "change-me") {
		problems = append(problems, "GHS_SECRET_KEY is still the development placeholder")
	}
	for _, o := range c.AllowedOrigins {
		if strings.HasPrefix(o, "http://") && !isLoopbackOrigin(o) {
			problems = append(problems, "GHS_ALLOWED_ORIGINS contains a plaintext origin: "+o)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("refusing to start in production:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// GitHubOAuthConfigured reports whether real GitHub OAuth can be performed.
// When false in a non-production environment the service still runs, so that
// development, fixture testing and packaging are never blocked on an OAuth
// registration.
func (c *Config) GitHubOAuthConfigured() bool {
	return c.GitHubClientID != "" && c.GitHubClientSecret != ""
}

// OAuthCallbackURL is the exact redirect registered with GitHub.
func (c *Config) OAuthCallbackURL() string {
	return c.PublicURL.String() + "/v1/auth/github/callback"
}

// OriginAllowed reports whether an exact origin may send credentialed
// requests. Extension origins are configured explicitly; there is no wildcard.
func (c *Config) OriginAllowed(origin string) bool {
	origin = strings.TrimSuffix(origin, "/")
	for _, o := range c.AllowedOrigins {
		if o == origin {
			return true
		}
	}
	return false
}

func isLoopback(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return strings.HasSuffix(strings.ToLower(host), ".localhost")
}

func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return isLoopback(u.Hostname())
}

func boolOf(s string) bool {
	v, _ := strconv.ParseBool(strings.TrimSpace(s))
	return v
}
func intOf(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
}
func int64Of(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}
