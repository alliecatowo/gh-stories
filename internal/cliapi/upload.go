package cliapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// UploadProgress reports upload progress as bytes are streamed to the
// presigned URL. sent is cumulative; total is the declared byte size (may
// be 0 if unknown).
type UploadProgress func(sent, total int64)

// terminalStoryStates are the states PollStoryUntilTerminal stops at: the
// item has finished processing (one way or another) or is gone.
var terminalStoryStates = map[string]bool{
	"published": true,
	"failed":    true,
	"deleted":   true,
	"removed":   true,
}

// CreateUpload requests an upload authorization for one server-chosen
// private object key (POST /uploads). Send the same idempotencyKey on a
// retry after a dropped connection so the server returns the original
// authorization instead of minting a second one.
func (c *Client) CreateUpload(ctx context.Context, req UploadIntentRequest, idempotencyKey string) (*UploadIntent, error) {
	var out UploadIntent
	if err := c.postJSON(ctx, "/uploads", idemHeader(idempotencyKey), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutUploadBytes uploads r (size bytes) to the presigned URL in intent,
// reporting progress via progress (may be nil). It does not go through the
// retry machinery used for the JSON API: a partial PUT to a presigned URL
// cannot generally be resumed, so a dropped connection here should surface
// to the caller, who retries the whole Upload call with the same
// idempotency key.
func (c *Client) PutUploadBytes(ctx context.Context, intent *UploadIntent, r io.Reader, size int64, progress UploadProgress) error {
	body := r
	if progress != nil {
		body = &progressReader{r: r, total: size, onProgress: progress}
	}
	req, err := http.NewRequestWithContext(ctx, intent.Method, intent.URL, body)
	if err != nil {
		return fmt.Errorf("cliapi: build upload request: %w", err)
	}
	req.ContentLength = size
	for k, v := range intent.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &NetworkError{Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("cliapi: upload was rejected (HTTP %d): %s", resp.StatusCode, string(msg))
	}
	return nil
}

// FinalizeUpload freezes an immutable worker input and queues processing
// (POST /uploads/{id}/finalize). The response's state is "processing"; it
// is not yet "Story posted" — poll with PollStoryUntilTerminal (or call
// Upload, which does this for you) until it settles.
func (c *Client) FinalizeUpload(ctx context.Context, uploadID string, byteSize int64, checksumSHA256, idempotencyKey string) (*StoryItem, error) {
	req := struct {
		ByteSize       int64  `json:"byte_size"`
		ChecksumSHA256 string `json:"checksum_sha256"`
	}{ByteSize: byteSize, ChecksumSHA256: checksumSHA256}
	var out StoryItem
	if err := c.postJSON(ctx, "/uploads/"+pathEscape(uploadID)+"/finalize", idemHeader(idempotencyKey), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PollStoryUntilTerminal polls GET /stories/{id} at interval (default 1.5s)
// until the item reaches a terminal state (published, failed, deleted or
// removed) or ctx is cancelled. onUpdate, when non-nil, is called with every
// intermediate and final observation.
func (c *Client) PollStoryUntilTerminal(ctx context.Context, storyID string, interval time.Duration, onUpdate func(*StoryItem)) (*StoryItem, error) {
	if interval <= 0 {
		interval = 1500 * time.Millisecond
	}
	for {
		item, err := c.GetStory(ctx, storyID)
		if err != nil {
			return nil, err
		}
		if onUpdate != nil {
			onUpdate(item)
		}
		if terminalStoryStates[item.State] {
			return item, nil
		}
		if err := c.sleep(ctx, interval); err != nil {
			return nil, err
		}
	}
}

// UploadRequest bundles everything Upload needs to run the full
// request-authorization -> PUT bytes -> finalize -> poll-until-published
// pipeline in one call.
type UploadRequest struct {
	// Meta declares the media and its caption/audience/interaction
	// settings.
	Meta UploadIntentRequest
	// Reader supplies exactly Size bytes of media content.
	Reader io.Reader
	// Size is the exact byte size of the content Reader will produce; it
	// must match Meta.ByteSize.
	Size int64
	// ChecksumSHA256 is the lowercase hex SHA-256 of the content, computed
	// by the caller (typically while spooling it to a temp file so it can
	// be sent without buffering the whole thing twice).
	ChecksumSHA256 string
	// IdempotencyKey is reused for both the /uploads and .../finalize
	// calls. Passing the same key on a retry after a dropped connection
	// returns the original result instead of creating a duplicate Story.
	IdempotencyKey string
	// Progress reports upload byte progress, for a stderr progress bar.
	Progress UploadProgress
	// PollInterval overrides the default polling interval used while
	// waiting for processing to finish.
	PollInterval time.Duration
	// OnStateChange is called with every observed StoryItem state,
	// including the initial "processing" one right after finalize, so
	// callers can print "uploaded, processing..." before the final
	// "published"/"failed".
	OnStateChange func(*StoryItem)
}

// Upload runs the full POST /uploads -> PUT bytes -> POST finalize -> poll
// GET /stories/{id} pipeline described by the contract, returning the
// finished (published or failed) StoryItem.
func (c *Client) Upload(ctx context.Context, req UploadRequest) (*StoryItem, error) {
	intent, err := c.CreateUpload(ctx, req.Meta, req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if err := c.PutUploadBytes(ctx, intent, req.Reader, req.Size, req.Progress); err != nil {
		return nil, err
	}
	item, err := c.FinalizeUpload(ctx, intent.UploadID, req.Size, req.ChecksumSHA256, req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if req.OnStateChange != nil {
		req.OnStateChange(item)
	}
	if terminalStoryStates[item.State] {
		return item, nil
	}
	return c.PollStoryUntilTerminal(ctx, item.ID, req.PollInterval, req.OnStateChange)
}

// progressReader wraps an io.Reader, invoking onProgress after every Read
// with the cumulative byte count.
type progressReader struct {
	r          io.Reader
	total      int64
	sent       int64
	onProgress UploadProgress
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	if n > 0 {
		p.sent += int64(n)
		p.onProgress(p.sent, p.total)
	}
	return n, err
}
