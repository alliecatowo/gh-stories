package ghclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

// maxFollowingPages bounds how many pages we will ever follow, independent of
// max, so a misbehaving or malicious Link header cannot cause an unbounded
// crawl.
const maxFollowingPages = 100

const followingPerPage = 100

// linkNextRE extracts the URL of the rel="next" entry from a Link header,
// e.g. `<https://api.github.com/user/following?page=2>; rel="next"`.
var linkNextRE = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// Following pages through everyone the token's user follows on GitHub, via
// GET /user/following. It NEVER writes to GitHub's follow graph. Pagination
// follows the Link response header's rel="next" entry rather than guessing
// page numbers, since GitHub does not guarantee page semantics beyond that
// header. Results are deduplicated by numeric GitHub id and capped at max.
func (c *Client) Following(ctx context.Context, token string, max int) ([]domain.Identity, error) {
	if max <= 0 {
		return nil, nil
	}

	url := fmt.Sprintf("%s/user/following?per_page=%d", c.apiBase, followingPerPage)
	seen := make(map[domain.GitHubID]struct{})
	var out []domain.Identity

	for pageNum := 0; url != "" && pageNum < maxFollowingPages && len(out) < max; pageNum++ {
		reqURL := url
		resp, body, err := c.doWithRetry(ctx, token, func() (*http.Request, error) {
			req, err := http.NewRequest(http.MethodGet, reqURL, nil)
			if err != nil {
				return nil, fmt.Errorf("ghclient: build following request: %w", err)
			}
			c.applyHeaders(req, token)
			return req, nil
		})
		if err != nil {
			return nil, err
		}
		if err := classify(resp, body); err != nil {
			return nil, err
		}

		var users []ghUser
		if err := json.Unmarshal(body, &users); err != nil {
			return nil, fmt.Errorf("ghclient: decode following page: %w", err)
		}
		for _, u := range users {
			id := domain.GitHubID(u.ID)
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, u.toIdentity())
			if len(out) >= max {
				break
			}
		}

		url = nextLink(resp.Header.Get("Link"))
	}
	return out, nil
}

// nextLink extracts the rel="next" URL from a Link header, or "" if there is
// none (i.e. this was the last page).
func nextLink(link string) string {
	if link == "" {
		return ""
	}
	m := linkNextRE.FindStringSubmatch(link)
	if m == nil {
		return ""
	}
	return m[1]
}
