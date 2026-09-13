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
