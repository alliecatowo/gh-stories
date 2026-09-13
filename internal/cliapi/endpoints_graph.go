package cliapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Follow follows an account on Stories (PUT /following/{login}; never
// mutates the GitHub follow graph).
func (c *Client) Follow(ctx context.Context, login string) (*Relationship, error) {
	var out Relationship
	if err := c.putEmpty(ctx, "/following/"+pathEscape(login), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Unfollow removes a follow (DELETE /following/{login}, tombstoned so a
// later GitHub re-import will not resurrect it).
func (c *Client) Unfollow(ctx context.Context, login string) error {
	return c.deleteReq(ctx, "/following/"+pathEscape(login))
}

// Following lists accounts the caller follows on Stories (GET /following).
func (c *Client) Following(ctx context.Context, cursor string, limit int) (*RelationshipPage, error) {
	return c.relationshipPage(ctx, "/following", cursor, limit)
}

// Followers lists accounts following the caller on Stories (GET
// /followers).
func (c *Client) Followers(ctx context.Context, cursor string, limit int) (*RelationshipPage, error) {
	return c.relationshipPage(ctx, "/followers", cursor, limit)
}

func (c *Client) relationshipPage(ctx context.Context, path, cursor string, limit int) (*RelationshipPage, error) {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out RelationshipPage
	if err := c.get(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Mute mutes an account (PUT /mutes/{login}; affects only the muting
// user's presentation, not access).
func (c *Client) Mute(ctx context.Context, login string) error {
	return c.do(ctx, requestSpec{method: http.MethodPut, path: "/mutes/" + pathEscape(login)}, nil)
}

// Unmute removes a mute (DELETE /mutes/{login}).
func (c *Client) Unmute(ctx context.Context, login string) error {
	return c.deleteReq(ctx, "/mutes/"+pathEscape(login))
}

// Block blocks an account (PUT /blocks/{login}; overrides audience access
// in both directions).
func (c *Client) Block(ctx context.Context, login string) error {
	return c.do(ctx, requestSpec{method: http.MethodPut, path: "/blocks/" + pathEscape(login)}, nil)
}

// Unblock removes a block (DELETE /blocks/{login}).
func (c *Client) Unblock(ctx context.Context, login string) error {
	return c.deleteReq(ctx, "/blocks/"+pathEscape(login))
}

// Hide hides the caller's Stories from an account (PUT /hides/{login}).
func (c *Client) Hide(ctx context.Context, login string) error {
	return c.do(ctx, requestSpec{method: http.MethodPut, path: "/hides/" + pathEscape(login)}, nil)
}

// Unhide stops hiding the caller's Stories from an account (DELETE
// /hides/{login}).
func (c *Client) Unhide(ctx context.Context, login string) error {
	return c.deleteReq(ctx, "/hides/"+pathEscape(login))
}

// Mutes lists muted accounts (GET /mutes).
func (c *Client) Mutes(ctx context.Context) (*RelationshipPage, error) {
	var out RelationshipPage
	if err := c.get(ctx, "/mutes", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Blocks lists blocked accounts (GET /blocks).
func (c *Client) Blocks(ctx context.Context) (*RelationshipPage, error) {
	var out RelationshipPage
	if err := c.get(ctx, "/blocks", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Hides lists accounts hidden from the caller's Stories (GET /hides).
func (c *Client) Hides(ctx context.Context) (*RelationshipPage, error) {
	var out RelationshipPage
	if err := c.get(ctx, "/hides", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AudienceLists fetches the caller's custom audience lists (GET
// /audience-lists).
func (c *Client) AudienceLists(ctx context.Context) ([]AudienceList, error) {
	var out struct {
		Lists []AudienceList `json:"lists"`
	}
	if err := c.get(ctx, "/audience-lists", nil, &out); err != nil {
		return nil, err
	}
	return out.Lists, nil
}

// Settings fetches account settings (GET /settings).
func (c *Client) Settings(ctx context.Context) (*Settings, error) {
	var out Settings
	if err := c.get(ctx, "/settings", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSettings applies a partial settings update (PATCH /settings).
func (c *Client) UpdateSettings(ctx context.Context, upd SettingsUpdate) (*Settings, error) {
	var out Settings
	if err := c.patchJSON(ctx, "/settings", nil, upd, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateReport files a report against a Story or a user (POST /reports).
func (c *Client) CreateReport(ctx context.Context, req ReportRequest) (*Report, error) {
	var out Report
	if err := c.postJSON(ctx, "/reports", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
