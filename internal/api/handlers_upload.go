package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/httpx"
	"github.com/alliecatowo/gh-stories/internal/store"
)

// Uploader issues short-lived authorizations to write exactly one
// server-chosen private object key.
type Uploader interface {
	PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (string, map[string]string, error)
}

// acceptedInputs are the formats a client may declare. The declared type is
// advisory only: the worker verifies the real file signature and fully parses
// the media before anything is published.
var acceptedInputs = map[string]bool{
	"image/jpeg":      true,
	"image/png":       true,
	"image/webp":      true,
	"image/gif":       true,
	"video/mp4":       true,
	"video/webm":      true,
	"video/quicktime": true,
}

var checksumPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type uploadIntentRequest struct {
	MIME           string  `json:"mime"`
	ByteSize       int64   `json:"byte_size"`
	Filename       string  `json:"filename"`
	Caption        string  `json:"caption"`
	AltText        string  `json:"alt_text"`
	Visibility     *string `json:"visibility"`
	AudienceListID *string `json:"audience_list_id"`
	AllowReplies   *bool   `json:"allow_replies"`
	AllowReactions *bool   `json:"allow_reactions"`
}

// postUpload authorizes an upload and records the Story's intended metadata.
func (s *Server) postUpload(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	var req uploadIntentRequest
	if err := httpx.DecodeJSON(w, r, &req, 16<<10); err != nil {
		return err
	}
	mime := strings.ToLower(strings.TrimSpace(req.MIME))
	if !acceptedInputs[mime] {
		// SVG is deliberately absent: it is a scriptable document format.
		return httpx.Unsupported("That file type is not supported. Use JPEG, PNG, WebP, GIF, MP4 or WebM.")
	}
	if req.ByteSize <= 0 {
		return httpx.FieldError("byte_size", "The file appears to be empty.")
	}
	if req.ByteSize > s.Cfg.MaxUploadBytes {
		return httpx.TooLarge("Files are limited to 100 MB.")
	}
	if len([]rune(req.Caption)) > 500 {
		return httpx.FieldError("caption", "Captions are limited to 500 characters.")
	}
	if len([]rune(req.AltText)) > 500 {
		return httpx.FieldError("alt_text", "Descriptions are limited to 500 characters.")
	}

	draft := store.StoryDraft{
		Caption:        req.Caption,
		AltText:        req.AltText,
		Visibility:     u.DefaultVisibility,
		AudienceListID: u.DefaultAudienceListID,
		AllowReplies:   u.DefaultAllowReplies,
		AllowReactions: u.DefaultAllowReactions,
	}
	if req.Visibility != nil {
		v := domain.Visibility(*req.Visibility)
		if !v.Valid() {
			return httpx.FieldError("visibility", "Unknown audience.")
		}
		draft.Visibility = v
		listID, err := s.resolveAudienceList(r, u, &v, req.AudienceListID)
		if err != nil {
			return err
		}
		draft.AudienceListID = listID
	}
	if draft.Visibility == domain.VisibilityCustomList && draft.AudienceListID == nil {
		return httpx.FieldError("audience_list_id", "Choose a list for a custom audience.")
	}
	if req.AllowReplies != nil {
		draft.AllowReplies = *req.AllowReplies
	}
	if req.AllowReactions != nil {
		draft.AllowReactions = *req.AllowReactions
	}

	// The object key is chosen by the server and never derived from the
	// client's filename, so a hostile filename cannot escape the key space.
	key := newUploadKey(u.ID.String())

	intent, err := s.Store.CreateUploadIntent(r.Context(), u.ID, key, mime,
		req.ByteSize, s.Cfg.UploadIntentTTL)
	if err != nil {
		return httpx.Internal(err)
	}
	if err := s.Store.StashDraft(r.Context(), intent.ID, draft, sanitizeFilename(req.Filename)); err != nil {
		return httpx.Internal(err)
	}

	url, headers, err := s.Uploads.PresignPut(r.Context(), key, mime,
		req.ByteSize, s.Cfg.UploadIntentTTL)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"upload_id":  intent.ID.String(),
		"method":     "PUT",
		"url":        url,
		"headers":    headers,
		"expires_at": intent.ExpiresAt,
		"max_bytes":  req.ByteSize,
	})
	return nil
}

// postFinalize freezes an immutable worker input and queues processing.
//
// A successful upload is NOT yet "Story posted": this returns 202 with a
// processing item, and the client polls until it is published or failed.
func (s *Server) postFinalize(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	uploadID, err := pathUUID(r, "uploadId")
	if err != nil {
		return err
	}
	var req struct {
		ByteSize int64  `json:"byte_size"`
		Checksum string `json:"checksum_sha256"`
	}
	if err := httpx.DecodeJSON(w, r, &req, 4<<10); err != nil {
		return err
	}
	if !checksumPattern.MatchString(strings.ToLower(req.Checksum)) {
		return httpx.FieldError("checksum_sha256", "Expected a lowercase hex SHA-256.")
	}
	if req.ByteSize <= 0 || req.ByteSize > s.Cfg.MaxUploadBytes {
		return httpx.FieldError("byte_size", "That size is not acceptable.")
	}

	draft, filename, err := s.Store.LoadDraft(r.Context(), uploadID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return httpx.NotFound()
		}
		return httpx.Internal(err)
	}

	storyID, created, err := s.Store.FinalizeUpload(r.Context(), u.ID, uploadID,
		req.ByteSize, strings.ToLower(req.Checksum), draft, filename)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Not yours, unknown, expired or already aborted — same answer.
			return httpx.NotFound()
		}
		return httpx.Internal(err)
	}
	_ = created // a replayed finalize returns the original item, not a duplicate

	item, err := s.Store.OwnStory(r.Context(), u.ID, storyID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.WriteJSON(w, http.StatusAccepted, presentStory(item, presentOpts{viewer: u}))
	return nil
}

// sanitizeFilename keeps an advisory display name only. It is never used to
// build an object key, a filesystem path, or a subprocess argument, but it is
// still stripped of path separators and control characters before storage.
func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.NewReplacer("/", "_", "\\", "_", "\x00", "").Replace(name)
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if b.Len() > 200 {
			break
		}
	}
	return b.String()
}

// newUploadKey returns an unguessable, server-chosen private object key.
//
// The key is generated here, not derived from anything the client sent. A
// filename like "../../etc/passwd" or one containing a newline therefore
// cannot influence where bytes land.
func newUploadKey(ownerID string) string {
	return "u/" + ownerID + "/" + uuid.NewString()
}
