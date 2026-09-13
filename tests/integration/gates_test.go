package integration

import (
	"image"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/media"
)

// GATE: Cross-client post.
//
// Alice posts a real image through the client posting path; the real worker
// processes it with real ffmpeg/decoders; eligible Bob then sees the same item
// in his feed, gets a ring, and receives decodable pixels through the
// authorization gateway.
func TestCrossClientImagePost(t *testing.T) {
	e := newEnv(t)
	e.runWorker()
	alice := e.client(3001, "alice")
	bob := e.client(3002, "bob")

	// Default audience is "People I follow", so Alice must follow Bob.
	require.NoError(t, e.store.Follow(t.Context(), nil,
		alice.User.ID, bob.User.GitHubID, domain.ProvenanceNative))

	storyID := alice.post(t, "portrait.jpg", map[string]any{
		"caption":  "concert",
		"alt_text": "A crowd lit by stage lights",
		"mime":     "image/jpeg",
		"filename": "portrait.jpg",
	})
	item := alice.awaitPublished(t, storyID)

	require.Equal(t, "image", item["media_kind"])
	require.NotNil(t, item["published_at"])
	require.NotNil(t, item["expires_at"])

	// Every variant the product needs exists.
	kinds := map[string]bool{}
	for _, raw := range item["variants"].([]any) {
		v := raw.(map[string]any)
		kinds[v["kind"].(string)] = true
		require.Contains(t, v["url"].(string), "/v1/media/",
			"variants must be authorization-gateway paths, never public object URLs")
	}
	require.True(t, kinds["image"], "canonical image variant")
	require.True(t, kinds["thumb"], "inbox thumbnail")
	require.True(t, kinds["terminal"], "terminal-friendly variant for the CLI")

	// Bob's ring lights up.
	statuses := jsonOf(t, bob.req(http.MethodPost, "/v1/stories/status",
		map[string]any{"github_user_ids": []int64{int64(alice.User.GitHubID)}}, nil))
	entry := statuses["entries"].([]any)[0].(map[string]any)
	require.True(t, entry["has_active"].(bool))
	require.True(t, entry["has_unseen"].(bool))

	// Bob's feed contains it.
	feed := jsonOf(t, bob.req(http.MethodGet, "/v1/feed", nil, nil))
	groups := feed["groups"].([]any)
	require.Len(t, groups, 1)
	group := groups[0].(map[string]any)
	require.Equal(t, "alice", group["author"].(map[string]any)["login"])

	// And the bytes are a real, decodable picture.
	img, format := bob.decodeImage(t, storyID, "image")
	require.Contains(t, []string{"jpeg", "png", "webp"}, format)
	require.Greater(t, img.Bounds().Dx(), 0)
	require.Greater(t, img.Bounds().Dy(), 0)
	require.Greater(t, img.Bounds().Dy(), img.Bounds().Dx(),
		"a portrait source must stay portrait — no destructive cropping")

	termImg, _ := bob.decodeImage(t, storyID, "terminal")
	require.LessOrEqual(t, termImg.Bounds().Dx(), 1600,
		"the terminal variant must be bounded for terminal display")

	// Delivering the media recorded exactly one view, visible to the author.
	viewers := jsonOf(t, alice.req(http.MethodGet, "/v1/stories/"+storyID+"/viewers", nil, nil))
	require.Equal(t, float64(1), viewers["total"])
}

// GATE: Video. Upload is validated and transcoded; the browser gets a playable
// canonical video; the CLI gets a real poster frame.
func TestVideoEndToEnd(t *testing.T) {
	if _, ok := media.FFmpegAvailable(); !ok {
		t.Skip("ffmpeg is not available; video processing cannot be verified here")
	}
	e := newEnv(t)
	e.runWorker()
	alice := e.client(3101, "alice")
	bob := e.client(3102, "bob")

	storyID := alice.post(t, "sample.mp4", map[string]any{"visibility": "public"})
	item := alice.awaitPublished(t, storyID)
	require.Equal(t, "video", item["media_kind"])

	variants := map[string]map[string]any{}
	for _, raw := range item["variants"].([]any) {
		v := raw.(map[string]any)
		variants[v["kind"].(string)] = v
	}
	video := variants["video"]
	require.NotNil(t, video, "a canonical video variant must exist")
	require.Equal(t, "video/mp4", video["mime"],
		"canonical video is widely playable H.264 MP4")
	require.Greater(t, video["duration_ms"].(float64), float64(0))

	require.NotNil(t, variants["poster"], "a real poster frame must exist")
	require.NotNil(t, variants["terminal"],
		"the CLI needs a terminal-renderable still, since inline video is not claimed")

	// The poster is a genuine decodable frame, not a placeholder.
	poster, _ := bob.decodeImage(t, storyID, "poster")
	require.Greater(t, poster.Bounds().Dx(), 0)
	require.False(t, isUniform(poster), "the poster must be a real frame, not a blank image")

	// Range requests work, which is what a video element actually issues.
	resp := bob.req(http.MethodGet, "/v1/media/"+storyID+"/video", nil,
		map[string]string{"Range": "bytes=0-1023"})
	defer resp.Body.Close()
	require.Equal(t, http.StatusPartialContent, resp.StatusCode,
		"the gateway must serve range requests for video playback")
	require.NotEmpty(t, resp.Header.Get("Content-Range"))
}

// GATE: Animated uploads keep their animation rather than being flattened.
func TestAnimatedGifBecomesVideoNotStill(t *testing.T) {
	if _, ok := media.FFmpegAvailable(); !ok {
		t.Skip("ffmpeg is not available")
	}
	e := newEnv(t)
	e.runWorker()
	alice := e.client(3201, "alice")

	storyID := alice.post(t, "animated.gif", map[string]any{"visibility": "public"})
	item := alice.awaitPublished(t, storyID)

	require.Equal(t, "video", item["media_kind"],
		"an animated upload must keep its animation via a video variant, not be flattened")
	for _, raw := range item["variants"].([]any) {
		v := raw.(map[string]any)
		if v["kind"] == "video" {
			require.Greater(t, v["duration_ms"].(float64), float64(0))
		}
	}
}

// GATE: Upload abuse. Spoofed MIME, corrupt files, pixel bombs and non-media
// cannot publish.
func TestHostileUploadsCannotPublish(t *testing.T) {
	e := newEnv(t)
	e.runWorker()
	alice := e.client(3301, "alice")

	for _, tc := range []struct {
		fixture string
		mime    string
		why     string
	}{
		{"fake.png", "image/png", "a JPEG served as PNG must be caught by signature sniffing"},
		{"truncated.jpg", "image/jpeg", "a truncated file must fail to fully parse"},
		{"bomb.png", "image/png", "a pixel bomb must be rejected on declared dimensions before decode"},
		{"not-media.bin", "image/png", "arbitrary bytes are not media"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			storyID := alice.post(t, tc.fixture, map[string]any{"mime": tc.mime})
			deadline := time.Now().Add(60 * time.Second)
			var state string
			var last map[string]any
			for time.Now().Before(deadline) {
				last = jsonOf(t, alice.req(http.MethodGet, "/v1/stories/"+storyID, nil, nil))
				state, _ = last["state"].(string)
				if state == "failed" || state == "published" {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			require.Equal(t, "failed", state, tc.why)
			require.NotEmpty(t, last["failure_code"],
				"a rejection must carry a stable, explainable code")
			require.NotEqual(t, "internal", last["failure_code"],
				"hostile input must be a clean rejection, not an internal error")
		})
	}
}

// GATE: A filename that would be catastrophic if it ever reached a shell is
// handled as inert advisory metadata.
func TestHostileFilenameIsInert(t *testing.T) {
	e := newEnv(t)
	e.runWorker()
	alice := e.client(3401, "alice")

	storyID := alice.post(t, "portrait.jpg", map[string]any{
		"filename":   "$(touch /tmp/ghs-pwned); rm -rf /\nevil.jpg",
		"visibility": "public",
	})
	alice.awaitPublished(t, storyID)
	require.NoFileExists(t, "/tmp/ghs-pwned",
		"a filename must never reach a shell")
}

// GATE: Physical cleanup. Deleting a Story removes its objects from storage,
// not merely its rows.
func TestDeletionRemovesObjectsFromStorage(t *testing.T) {
	e := newEnv(t)
	e.runWorker()
	alice := e.client(3501, "alice")

	storyID := alice.post(t, "landscape.png", map[string]any{"visibility": "public"})
	item := alice.awaitPublished(t, storyID)

	sid, err := parseUUID(storyID)
	require.NoError(t, err)
	variants, err := e.store.VariantKeys(t.Context(), sid)
	require.NoError(t, err)
	require.NotEmpty(t, variants)

	for _, key := range variants {
		_, err := e.objects.Head(t.Context(), key)
		require.NoError(t, err, "variant %s should exist before deletion", key)
	}

	resp := alice.req(http.MethodDelete, "/v1/stories/"+storyID, nil, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	_ = item

	// The cleanup loop should physically remove them shortly.
	require.Eventually(t, func() bool {
		for _, key := range variants {
			if _, err := e.objects.Head(t.Context(), key); err == nil {
				return false
			}
		}
		return true
	}, 30*time.Second, 250*time.Millisecond,
		"deleted Story objects must actually leave object storage")
}

// isUniform reports whether every sampled pixel is identical, which is how a
// blank or all-black poster would look. A real keyframe has variation.
func isUniform(img image.Image) bool {
	b := img.Bounds()
	if b.Empty() {
		return true
	}
	r0, g0, b0, _ := img.At(b.Min.X, b.Min.Y).RGBA()
	const samples = 24
	for i := 0; i < samples; i++ {
		x := b.Min.X + (b.Dx()-1)*i/samples
		for j := 0; j < samples; j++ {
			y := b.Min.Y + (b.Dy()-1)*j/samples
			r, g, bb, _ := img.At(x, y).RGBA()
			if r != r0 || g != g0 || bb != b0 {
				return false
			}
		}
	}
	return true
}

func parseUUID(s string) (uuid.UUID, error) { return uuid.Parse(s) }
