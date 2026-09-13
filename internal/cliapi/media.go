package cliapi

import (
	"context"
	"io"
	"net/http"
	"strconv"
)

// MediaInfo describes the response to a media fetch: the status code (200
// full content, 206 partial content), content type and, when known, size
// and the Content-Range the server actually served.
type MediaInfo struct {
	StatusCode    int
	ContentType   string
	ContentLength int64
	ContentRange  string
}

// GetMedia fetches one media variant of a Story item through the
// authorization gateway (GET /media/{storyId}/{variant}). rangeHeader, when
// non-empty, is sent verbatim as the Range header (e.g. "bytes=0-1023") so
// callers can resume or stream video. The caller must Close the returned
// ReadCloser.
//
// A 404 here is indistinguishable between "does not exist", "not
// authorized", "deleted" and "expired" by contract, and is returned as a
// *APIError like any other error response.
func (c *Client) GetMedia(ctx context.Context, storyID, variant, rangeHeader string) (io.ReadCloser, MediaInfo, error) {
	path := c.baseURL + "/v1/media/" + pathEscape(storyID) + "/" + pathEscape(variant)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, MediaInfo{}, err
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, MediaInfo{}, &NetworkError{Err: err}
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, MediaInfo{}, decodeAPIError(resp.StatusCode, body)
	}

	info := MediaInfo{
		StatusCode:   resp.StatusCode,
		ContentType:  resp.Header.Get("Content-Type"),
		ContentRange: resp.Header.Get("Content-Range"),
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
			info.ContentLength = n
		}
	}
	return resp.Body, info, nil
}
