package ghclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

// ghUser is the subset of GitHub's user object we care about. The `type`
// field is what lets us tell a human account from an Organization or Bot —
// callers (internal/auth) refuse to register anything but a human.
type ghUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
	HTMLURL   string `json:"html_url"`
	Type      string `json:"type"`
}

func (u ghUser) toIdentity() domain.Identity {
	return domain.Identity{
		GitHubID:    domain.GitHubID(u.ID),
		Login:       u.Login,
		AvatarURL:   u.AvatarURL,
		ProfileURL:  u.HTMLURL,
		AccountType: u.Type,
	}
}

// CurrentUser reads the profile of the account that owns token. This is the
// ONLY authoritative source of who just authorized: callers must call this
// with the freshly exchanged token on every authorization rather than trust
// anything supplied by the client.
func (c *Client) CurrentUser(ctx context.Context, token string) (domain.Identity, error) {
	resp, body, err := c.doWithRetry(ctx, token, func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodGet, c.apiBase+"/user", nil)
		if err != nil {
			return nil, fmt.Errorf("ghclient: build current-user request: %w", err)
		}
		c.applyHeaders(req, token)
		return req, nil
	})
	if err != nil {
		return domain.Identity{}, err
	}
	if err := classify(resp, body); err != nil {
		return domain.Identity{}, err
	}
	var u ghUser
	if err := json.Unmarshal(body, &u); err != nil {
		return domain.Identity{}, fmt.Errorf("ghclient: decode current user: %w", err)
	}
	return u.toIdentity(), nil
}

// applyHeaders sets the headers every authenticated GitHub API request needs.
func (c *Client) applyHeaders(req *http.Request, token string) {
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}
