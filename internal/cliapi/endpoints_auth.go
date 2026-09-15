package cliapi

import (
	"context"
	"net/http"
)

// Me fetches the authenticated Stories account (GET /me).
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var out Me
	if err := c.get(ctx, "/me", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreatePendingLogin begins a device-flow-style CLI login (POST
// /auth/cli/pending). clientLabel is a human label shown on the browser
// approval screen, e.g. "gh stories on allies-mbp".
func (c *Client) CreatePendingLogin(ctx context.Context, clientKind, clientLabel string) (*PendingLogin, error) {
	req := struct {
		ClientKind  string `json:"client_kind"`
		ClientLabel string `json:"client_label,omitempty"`
	}{ClientKind: clientKind, ClientLabel: clientLabel}
	var out PendingLogin
	if err := c.postJSON(ctx, "/auth/cli/pending", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PollPendingLogin polls a pending authorization created by
// CreatePendingLogin (POST /auth/cli/poll). The token in the returned
// PendingLoginPoll is present exactly once, on the first successful poll
// after approval.
func (c *Client) PollPendingLogin(ctx context.Context, pendingLoginID, pollingSecret string) (*PendingLoginPoll, error) {
	req := struct {
		PendingLoginID string `json:"pending_login_id"`
		PollingSecret  string `json:"polling_secret"`
	}{PendingLoginID: pendingLoginID, PollingSecret: pollingSecret}
	var out PendingLoginPoll
	if err := c.postJSON(ctx, "/auth/cli/poll", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StartDeviceLogin begins a GitHub Device Authorization Grant login (POST
// /auth/device/start). clientLabel is a human label, e.g. "gh stories on
// allies-mbp". The device code stays server side; the response carries only
// the user code and GitHub's verification URI.
func (c *Client) StartDeviceLogin(ctx context.Context, clientKind, clientLabel string) (*DeviceLogin, error) {
	req := struct {
		ClientKind  string `json:"client_kind"`
		ClientLabel string `json:"client_label,omitempty"`
	}{ClientKind: clientKind, ClientLabel: clientLabel}
	var out DeviceLogin
	if err := c.postJSON(ctx, "/auth/device/start", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PollDeviceLogin polls a device authorization created by StartDeviceLogin
// (POST /auth/device/poll). The token is present exactly once, on the
// first successful poll after the user authorizes on GitHub.
func (c *Client) PollDeviceLogin(ctx context.Context, id string) (*DeviceLoginPoll, error) {
	req := struct {
		DeviceLoginID string `json:"device_login_id"`
	}{DeviceLoginID: id}
	var out DeviceLoginPoll
	if err := c.postJSON(ctx, "/auth/device/poll", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Logout revokes the calling session (POST /auth/logout).
func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, requestSpec{method: http.MethodPost, path: "/auth/logout"}, nil)
}

// Sessions lists the caller's active Stories sessions (GET /auth/sessions).
func (c *Client) Sessions(ctx context.Context) ([]SessionInfo, error) {
	var out struct {
		Sessions []SessionInfo `json:"sessions"`
	}
	if err := c.get(ctx, "/auth/sessions", nil, &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

// RevokeSession revokes one of the caller's sessions by id (DELETE
// /auth/sessions/{sessionId}).
func (c *Client) RevokeSession(ctx context.Context, sessionID string) error {
	return c.deleteReq(ctx, "/auth/sessions/"+pathEscape(sessionID))
}

// ImportSummary is the outcome of POST /onboarding/import-follows.
//
// Accepting the import cannot be completed by the API alone: the service
// discards the upstream GitHub token as soon as identity is established, so
// reading the GitHub follow graph always requires a fresh authorization in a
// browser. NeedsGitHubAuthorization says so, and AuthorizationURL is where to
// send the person.
type ImportSummary struct {
	Enabled                  bool         `json:"enabled"`
	Account                  PublicUser   `json:"account"`
	NeedsGitHubAuthorization bool         `json:"needs_github_authorization"`
	AuthorizationURL         string       `json:"authorization_url"`
	Added                    int          `json:"added"`
	AlreadyFollowing         int          `json:"already_following"`
	SkippedUnfollowed        int          `json:"skipped_unfollowed"`
	SkippedBlocked           int          `json:"skipped_blocked"`
	Sample                   []PublicUser `json:"sample"`
}

// ImportFollows accepts or declines the GitHub follow import. Declining is
// applied immediately and reads nothing from GitHub.
func (c *Client) ImportFollows(ctx context.Context, enabled bool) (*ImportSummary, error) {
	var out ImportSummary
	body := struct {
		Enabled bool `json:"enabled"`
	}{Enabled: enabled}
	if err := c.postJSON(ctx, "/onboarding/import-follows", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
