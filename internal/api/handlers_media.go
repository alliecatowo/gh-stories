package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/httpx"
	"github.com/alliecatowo/gh-stories/internal/store"
)

// getMedia is the authorization gateway for private media.
//
// Why a gateway rather than signed URLs: a signed URL keeps working after a
// block, an audience narrowing, a deletion or expiry, because the signature
// was minted before any of those happened. Streaming through an authenticated
// handler makes revocation exact — session, audience, block, hide, suspension,
// deletion and expiry are ALL re-checked on every single request, including
// thumbnails and video range requests.
//
// There are no public object URLs anywhere in this product.
func (s *Server) getMedia(w http.ResponseWriter, r *http.Request) error {
	u, err := mustCaller(r)
	if err != nil {
		return err
	}
	storyID, err := pathUUID(r, "storyId")
	if err != nil {
		return err
	}
	kind := domain.VariantKind(pathParam(r, "variant"))
	switch kind {
	case domain.VariantImage, domain.VariantVideo, domain.VariantPoster,
		domain.VariantThumb, domain.VariantTerminal:
	default:
		return httpx.NotFound()
	}

	// The single shared predicate. Absent, expired, deleted, removed, blocked,
	// hidden, suspended and out-of-audience all end here with the same 404.
	item, err := s.Store.StoryForViewer(r.Context(), u.ID, u.GitHubID, storyID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return httpx.NotFound()
		}
		return httpx.Internal(err)
	}
	variant := item.Variant(kind)
	if variant == nil {
		return httpx.NotFound()
	}

	obj, err := s.Objects.Get(r.Context(), variant.ObjectKey, r.Header.Get("Range"))
	if err != nil {
		// A missing object after a successful authorization means cleanup has
		// already removed it. That is still "gone", not a server error.
		return httpx.NotFound()
	}
	defer obj.Body.Close()

	// A view is recorded when content-bearing media is delivered to an
	// authorized non-owner. A poster shows the picture, so it counts; a small
	// inbox thumbnail does not. Recording is idempotent.
	//
	// A view reflects media DELIVERY, not proof that a human looked at every
	// pixel; clients acknowledge actual rendering separately via POST /view.
	if kind.ContentBearing() && item.AuthorUserID != u.ID {
		if err := s.Store.RecordView(r.Context(), storyID, u.ID); err != nil {
			s.Log.Warn("record view failed", "story_id", storyID, "err", err)
		}
	}

	h := w.Header()
	h.Set("Content-Type", firstNonEmpty(obj.ContentType, variant.MIME))
	// private + no-store keeps this out of shared caches. A CDN or browser
	// cache that served these bytes again would bypass the permission check
	// above, which is exactly the failure this product must not have.
	h.Set("Cache-Control", "private, no-store, max-age=0")
	h.Set("Accept-Ranges", "bytes")
	h.Set("X-Content-Type-Options", "nosniff")
	// Media is never rendered as a document; forbid it explicitly.
	h.Set("Content-Disposition", "inline")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if obj.ContentRange != "" {
		h.Set("Content-Range", obj.ContentRange)
	}
	if obj.ContentLength > 0 {
		h.Set("Content-Length", strconv.FormatInt(obj.ContentLength, 10))
	}

	status := obj.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return nil
	}
	if _, err := io.Copy(w, obj.Body); err != nil {
		// The client went away mid-stream. Nothing to report to them.
		s.Log.Debug("media stream interrupted", "story_id", storyID, "err", err)
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return "application/octet-stream"
}
