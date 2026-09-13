package cliapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Feed fetches one page of the authorized Story feed, grouped by author
// (GET /feed). limit <= 0 uses the server default.
func (c *Client) Feed(ctx context.Context, cursor string, limit int, includeMuted bool) (*Feed, error) {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if includeMuted {
		q.Set("include_muted", "true")
	}
	var out Feed
	if err := c.get(ctx, "/feed", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StoriesMine fetches the caller's own active Story items (GET
// /stories/mine).
func (c *Client) StoriesMine(ctx context.Context) (*AuthorGroup, error) {
	var out AuthorGroup
	if err := c.get(ctx, "/stories/mine", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UserStories fetches one author's active, authorized Story sequence (GET
// /users/{login}/stories).
func (c *Client) UserStories(ctx context.Context, login string) (*AuthorGroup, error) {
	var out AuthorGroup
	if err := c.get(ctx, "/users/"+pathEscape(login)+"/stories", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UsersLookup finds Stories accounts matching q (GET /users/lookup).
func (c *Client) UsersLookup(ctx context.Context, q string) ([]PublicUser, error) {
	query := url.Values{"q": []string{q}}
	var out struct {
		Users []PublicUser `json:"users"`
	}
	if err := c.get(ctx, "/users/lookup", query, &out); err != nil {
		return nil, err
	}
	return out.Users, nil
}

// GetStory fetches one Story item the caller is authorized to see (GET
// /stories/{id}).
func (c *Client) GetStory(ctx context.Context, storyID string) (*StoryItem, error) {
	var out StoryItem
	if err := c.get(ctx, "/stories/"+pathEscape(storyID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateStory updates a Story item's caption, description, audience or
// interaction settings (PATCH /stories/{id}, author only).
func (c *Client) UpdateStory(ctx context.Context, storyID string, upd StoryUpdate) (*StoryItem, error) {
	var out StoryItem
	if err := c.patchJSON(ctx, "/stories/"+pathEscape(storyID), nil, upd, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteStory deletes a Story item (DELETE /stories/{id}).
func (c *Client) DeleteStory(ctx context.Context, storyID string) error {
	return c.deleteReq(ctx, "/stories/"+pathEscape(storyID))
}

// Viewers fetches the named viewer list for a Story item (GET
// /stories/{id}/viewers, author only).
func (c *Client) Viewers(ctx context.Context, storyID string) (*ViewerList, error) {
	var out ViewerList
	if err := c.get(ctx, "/stories/"+pathEscape(storyID)+"/viewers", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AckView acknowledges that delivered media actually rendered (POST
// /stories/{id}/view).
func (c *Client) AckView(ctx context.Context, storyID string) error {
	return c.do(ctx, requestSpec{method: http.MethodPost, path: "/stories/" + pathEscape(storyID) + "/view"}, nil)
}

// SetReaction sets or replaces the caller's reaction to a Story item (PUT
// /stories/{id}/reaction).
func (c *Client) SetReaction(ctx context.Context, storyID, emoji, idempotencyKey string) (*Reaction, error) {
	req := struct {
		Emoji string `json:"emoji"`
	}{Emoji: emoji}
	var out Reaction
	if err := c.putJSON(ctx, "/stories/"+pathEscape(storyID)+"/reaction", idemHeader(idempotencyKey), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ClearReaction removes the caller's reaction (DELETE
// /stories/{id}/reaction).
func (c *Client) ClearReaction(ctx context.Context, storyID string) error {
	return c.deleteReq(ctx, "/stories/"+pathEscape(storyID)+"/reaction")
}

// Reply sends a private reply to a Story's author (POST
// /stories/{id}/replies).
func (c *Client) Reply(ctx context.Context, storyID, body, idempotencyKey string) (*Reply, error) {
	req := struct {
		Body string `json:"body"`
	}{Body: body}
	var out Reply
	if err := c.postJSON(ctx, "/stories/"+pathEscape(storyID)+"/replies", idemHeader(idempotencyKey), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Inbox fetches one page of the caller's private inbox (GET /inbox).
func (c *Client) Inbox(ctx context.Context, cursor string, limit int) (*Inbox, error) {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out Inbox
	if err := c.get(ctx, "/inbox", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// InboxRead marks inbox entries read (POST /inbox/read). When all is true,
// eventIDs is ignored by the server and every entry is marked read.
func (c *Client) InboxRead(ctx context.Context, eventIDs []string, all bool) error {
	req := struct {
		EventIDs []string `json:"event_ids,omitempty"`
		All      bool     `json:"all,omitempty"`
	}{EventIDs: eventIDs, All: all}
	return c.postJSON(ctx, "/inbox/read", nil, req, nil)
}
