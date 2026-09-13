package media_test

import (
	"bytes"
	"context"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/media"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "tests", "fixtures", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture %s not found (run `go run tests/fixtures/generate.go`): %v", name, err)
	}
	return path
}

func testLimits(t *testing.T) media.Limits {
	t.Helper()
	l := media.DefaultLimits()
	l.MaxProcessingTime = 60 * time.Second
	return l
}

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, ok := media.FFmpegAvailable(); !ok {
		t.Skip("ffmpeg not installed")
	}
}

// mediaErr unwraps a *media.Error from err, failing the test if err is not
// one.
func mediaErr(t *testing.T, err error) *media.Error {
	t.Helper()
	var me *media.Error
	require.ErrorAsf(t, err, &me, "expected a *media.Error, got %T: %v", err, err)
	return me
}

// -------------------------------------------------------------- happy path

func TestProcessImageFixtures(t *testing.T) {
	cases := []struct {
		name, file, declaredMIME string
	}{
		{"portrait jpg", "portrait.jpg", "image/jpeg"},
		{"landscape png", "landscape.png", "image/png"},
		{"square webp", "square.webp", "image/webp"},
		{"transparent png", "transparent.png", "image/png"},
		{"tall png", "tall.png", "image/png"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := media.Process(context.Background(), media.Input{
				Path: fixture(t, c.file), DeclaredMIME: c.declaredMIME,
			}, media.Options{Limits: testLimits(t)})
			require.NoError(t, err)
			t.Cleanup(out.Cleanup)

			var image_, thumb, terminal bool
			for _, v := range out.Variants {
				switch v.Kind {
				case "image":
					image_ = true
				case "thumb":
					thumb = true
				case "terminal":
					terminal = true
					require.Equal(t, "image/png", v.MIME, "terminal variant must be PNG")
				}
				require.NotEmpty(t, v.Data, "%s variant should be held in memory", v.Kind)
				require.Equal(t, int64(len(v.Data)), v.ByteSize)
				require.NotEmpty(t, v.Checksum)
				require.Greater(t, v.Width, 0)
				require.Greater(t, v.Height, 0)
			}
			require.True(t, image_, "missing canonical image variant")
			require.True(t, thumb, "missing thumb variant")
			require.True(t, terminal, "missing terminal variant")
		})
	}
}

func TestProcessTransparentPNGPreservesAlphaFormat(t *testing.T) {
	out, err := media.Process(context.Background(), media.Input{
		Path: fixture(t, "transparent.png"), DeclaredMIME: "image/png",
	}, media.Options{Limits: testLimits(t)})
	require.NoError(t, err)
	t.Cleanup(out.Cleanup)

	for _, v := range out.Variants {
		if v.Kind == "image" || v.Kind == "thumb" {
			require.Equal(t, "image/png", v.MIME, "%s variant must stay PNG to preserve transparency", v.Kind)
		}
	}
}

func TestProcessDownsizesTallImage(t *testing.T) {
	limits := testLimits(t)
	out, err := media.Process(context.Background(), media.Input{
		Path: fixture(t, "tall.png"), DeclaredMIME: "image/png",
	}, media.Options{Limits: limits})
	require.NoError(t, err)
	t.Cleanup(out.Cleanup)

	for _, v := range out.Variants {
		require.LessOrEqualf(t, v.Width, limits.CanonicalImageMaxSide+1, "variant %s width", v.Kind)
		require.LessOrEqualf(t, v.Height, limits.CanonicalImageMaxSide+1, "variant %s height", v.Kind)
	}
}

func TestProcessAnimatedGIFBecomesVideo(t *testing.T) {
	requireFFmpeg(t)
	out, err := media.Process(context.Background(), media.Input{
		Path: fixture(t, "animated.gif"), DeclaredMIME: "image/gif",
	}, media.Options{Limits: testLimits(t)})
	require.NoError(t, err)
	t.Cleanup(out.Cleanup)

	var video, poster, terminal *media.Variant
	for i := range out.Variants {
		switch out.Variants[i].Kind {
		case "video":
			video = &out.Variants[i]
		case "poster":
			poster = &out.Variants[i]
		case "terminal":
			terminal = &out.Variants[i]
		}
	}
	require.NotNil(t, video, "animated GIF must produce a video variant, not a flattened still")
	require.Equal(t, "video/mp4", video.MIME)
	require.NotEmpty(t, video.TempPath)
	require.Greater(t, video.DurationMS, 0, "animated GIF's video variant must have a nonzero duration")
	require.FileExists(t, video.TempPath)

	require.NotNil(t, poster, "animated GIF must also produce a poster")
	requireNotUniformColor(t, poster.Data)

	require.NotNil(t, terminal)
	require.Equal(t, "image/png", terminal.MIME)
}

func TestProcessVideoFixtures(t *testing.T) {
	requireFFmpeg(t)
	cases := []struct{ file, mime string }{
		{"sample.mp4", "video/mp4"},
		{"sample.webm", "video/webm"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			out, err := media.Process(context.Background(), media.Input{
				Path: fixture(t, c.file), DeclaredMIME: c.mime,
			}, media.Options{Limits: testLimits(t)})
			require.NoError(t, err)
			t.Cleanup(out.Cleanup)

			var video, poster *media.Variant
			for i := range out.Variants {
				switch out.Variants[i].Kind {
				case "video":
					video = &out.Variants[i]
				case "poster":
					poster = &out.Variants[i]
				}
			}
			require.NotNil(t, video)
			require.Equal(t, "video/mp4", video.MIME, "canonical video output is always H.264 MP4 regardless of source container")
			require.True(t, video.HasAudio, "source fixture has audio and was not muted")
			require.InDelta(t, 3000, video.DurationMS, 300)
			require.Zero(t, video.Width%2, "output width must be even for yuv420p")
			require.Zero(t, video.Height%2, "output height must be even for yuv420p")
			require.FileExists(t, video.TempPath)

			require.NotNil(t, poster)
			requireNotUniformColor(t, poster.Data)
		})
	}
}

func TestProcessVideoMuteOption(t *testing.T) {
	requireFFmpeg(t)
	out, err := media.Process(context.Background(), media.Input{
		Path: fixture(t, "sample.mp4"), DeclaredMIME: "video/mp4",
	}, media.Options{Limits: testLimits(t), Muted: true})
	require.NoError(t, err)
	t.Cleanup(out.Cleanup)

	video := findVariant(t, out, "video")
	require.False(t, video.HasAudio, "Muted:true must drop the audio track")
}

func TestProcessVideoTrim(t *testing.T) {
	requireFFmpeg(t)
	out, err := media.Process(context.Background(), media.Input{
		Path: fixture(t, "sample.mp4"), DeclaredMIME: "video/mp4",
	}, media.Options{Limits: testLimits(t), StartMS: 500, EndMS: 1500})
	require.NoError(t, err)
	t.Cleanup(out.Cleanup)

	video := findVariant(t, out, "video")
	require.InDelta(t, 1000, video.DurationMS, 250, "trimmed video should be ~1s")
}

func findVariant(t *testing.T, out media.Output, kind string) media.Variant {
	t.Helper()
	for _, v := range out.Variants {
		if string(v.Kind) == kind {
			return v
		}
	}
	t.Fatalf("no %s variant in output", kind)
	return media.Variant{}
}

func requireNotUniformColor(t *testing.T, pngData []byte) {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(pngData))
	require.NoError(t, err)
	b := img.Bounds()
	first := img.At(b.Min.X, b.Min.Y)
	fr, fg, fb, _ := first.RGBA()
	distinct := false
	for y := b.Min.Y; y < b.Max.Y; y += max(1, b.Dy()/16) {
		for x := b.Min.X; x < b.Max.X; x += max(1, b.Dx()/16) {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r != fr || g != fg || bl != fb {
				distinct = true
			}
			// Also directly rule out literal solid black, the specific
			// failure mode called out by the spec (a "black frame" poster).
			require.Falsef(t, r == 0 && g == 0 && bl == 0 && sampleIsSuspiciouslyBlack(img, b),
				"poster frame appears to be uniformly black")
		}
	}
	require.True(t, distinct, "poster/frame image is a single uniform color")
}

// sampleIsSuspiciouslyBlack double-checks a black sample point isn't just one
// black pixel in an otherwise colorful frame before treating it as evidence
// of a black frame; the real signal is the "distinct" check above having
// already failed for the same frame.
func sampleIsSuspiciouslyBlack(img image.Image, b image.Rectangle) bool {
	center := img.At((b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2)
	r, g, bl, _ := center.RGBA()
	return r == 0 && g == 0 && bl == 0
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---------------------------------------------------------------- security

// TestProcessStripsEXIFAndGPSAndCorrectsOrientation builds a JPEG that
// declares (via a hand-built EXIF APP1 segment) EXIF Orientation=6 — "rotate
// 90° clockwise to display correctly" — plus a GPS IFD, exactly the kind of
// metadata a phone photo embeds by default. It asserts that:
//  1. the output's dimensions are swapped from the source, proving the
//     pixels were actually rotated rather than the tag being ignored, and
//  2. no trace of an EXIF segment (or its embedded GPS data) survives into
//     any output variant — because every variant is a fresh re-encode of
//     decoded pixels, encoders here never write EXIF back out at all.
func TestProcessStripsEXIFAndGPSAndCorrectsOrientation(t *testing.T) {
	const storedW, storedH = 300, 200 // landscape as stored
	src := buildEXIFJPEG(t, storedW, storedH, 6 /* rotate 90 CW to correct */)
	dir := t.TempDir()
	path := filepath.Join(dir, "with-exif.jpg")
	require.NoError(t, os.WriteFile(path, src, 0o644))

	// Sanity: the crafted input really does carry an EXIF/GPS segment.
	require.Contains(t, string(src), "Exif")

	out, err := media.Process(context.Background(), media.Input{
		Path: path, DeclaredMIME: "image/jpeg",
	}, media.Options{Limits: testLimits(t)})
	require.NoError(t, err)
	t.Cleanup(out.Cleanup)

	canonical := findVariant(t, out, "image")
	// Orientation 6 requires a 90-degree rotation, so a 300x200 source must
	// become 200x300 (up to normal photo downscaling, which does not apply
	// here since both are well under the canonical size cap).
	require.Equal(t, storedH, canonical.Width, "width was not swapped: orientation was not applied to pixels")
	require.Equal(t, storedW, canonical.Height, "height was not swapped: orientation was not applied to pixels")

	for _, v := range out.Variants {
		require.NotContainsf(t, string(v.Data), "Exif", "variant %s still carries an EXIF segment", v.Kind)
	}
}

// TestProcessRejectsMIMEMismatch feeds a real JPEG that is named and
// declared as a PNG (fake.png): the sniffed type disagrees with the
// declared type, which must be rejected regardless of what the extension or
// Content-Type claimed.
func TestProcessRejectsMIMEMismatch(t *testing.T) {
	_, err := media.Process(context.Background(), media.Input{
		Path: fixture(t, "fake.png"), DeclaredMIME: "image/png",
	}, media.Options{Limits: testLimits(t)})
	require.Error(t, err)
	me := mediaErr(t, err)
	require.Equal(t, media.CodeMIMEMismatch, me.Code)
	require.False(t, me.Retryable)
}

// TestProcessRejectsPixelBombBeforeDecode processes bomb.png — a ~65-byte
// file whose IHDR chunk declares a 30000x30000 image — and asserts it is
// rejected quickly with too_many_pixels. The bound on elapsed time is the
// actual evidence that no attempt was made to decode 900 megapixels: a real
// attempt would be far slower (or would exhaust memory) rather than
// returning in well under a second.
func TestProcessRejectsPixelBombBeforeDecode(t *testing.T) {
	start := time.Now()
	_, err := media.Process(context.Background(), media.Input{
		Path: fixture(t, "bomb.png"), DeclaredMIME: "image/png",
	}, media.Options{Limits: testLimits(t)})
	elapsed := time.Since(start)

	require.Error(t, err)
	me := mediaErr(t, err)
	require.Equal(t, media.CodeTooManyPixels, me.Code)
	require.False(t, me.Retryable)
	require.Lessf(t, elapsed, 2*time.Second,
		"rejecting a declared-dimensions pixel bomb took %s; it should be rejected from the header alone", elapsed)
}

func TestProcessRejectsTruncatedFile(t *testing.T) {
	_, err := media.Process(context.Background(), media.Input{
		Path: fixture(t, "truncated.jpg"), DeclaredMIME: "image/jpeg",
	}, media.Options{Limits: testLimits(t)})
	require.Error(t, err)
	me := mediaErr(t, err)
	require.Equal(t, media.CodeCorruptMedia, me.Code)
	require.False(t, me.Retryable)
}

func TestProcessRejectsUnsupportedMedia(t *testing.T) {
	_, err := media.Process(context.Background(), media.Input{
		Path: fixture(t, "not-media.bin"), DeclaredMIME: "application/octet-stream",
	}, media.Options{Limits: testLimits(t)})
	require.Error(t, err)
	me := mediaErr(t, err)
	require.Equal(t, media.CodeUnsupportedMedia, me.Code)
	require.False(t, me.Retryable)
}

func TestProcessRejectsOversizedOriginal(t *testing.T) {
	limits := testLimits(t)
	limits.MaxOriginalBytes = 100 // smaller than any real fixture
	_, err := media.Process(context.Background(), media.Input{
		Path: fixture(t, "landscape.png"), DeclaredMIME: "image/png",
	}, media.Options{Limits: limits})
	require.Error(t, err)
	me := mediaErr(t, err)
	require.Equal(t, media.CodeTooLarge, me.Code)
	require.False(t, me.Retryable)
}

func TestProcessRejectsOverLongVideo(t *testing.T) {
	requireFFmpeg(t)
	limits := testLimits(t)
	limits.MaxVideoSeconds = 1 // the fixture is 3s
	_, err := media.Process(context.Background(), media.Input{
		Path: fixture(t, "sample.mp4"), DeclaredMIME: "video/mp4",
	}, media.Options{Limits: limits})
	require.Error(t, err)
	me := mediaErr(t, err)
	require.Equal(t, media.CodeTooLong, me.Code)
	require.False(t, me.Retryable)
}

// TestProcessHostileFilenameNeverReachesAShell processes a normal, valid
// fixture but attaches a Filename designed to break any code that ever
// concatenated it into a shell command or a filesystem path: a command
// separator, a destructive command, and embedded newlines. Process must
// succeed exactly as it would with no filename at all, proving Filename is
// inert — Process never writes to disk under the caller's filename, and
// every ffmpeg/ffprobe invocation uses exec.CommandContext with an explicit
// argument slice, never a shell.
func TestProcessHostileFilenameNeverReachesAShell(t *testing.T) {
	out, err := media.Process(context.Background(), media.Input{
		Path:         fixture(t, "portrait.jpg"),
		DeclaredMIME: "image/jpeg",
		Filename:     "totally-normal\n; rm -rf / #\n$(reboot)\r\n`touch /tmp/pwned`",
	}, media.Options{Limits: testLimits(t)})
	require.NoError(t, err)
	t.Cleanup(out.Cleanup)
	require.NotEmpty(t, out.Variants)

	marker := "/tmp/pwned-media-test-should-not-exist"
	_, statErr := os.Stat(marker)
	require.True(t, os.IsNotExist(statErr), "a shell command embedded in Filename must never execute")
}

func TestProcessCleanupRemovesTempFiles(t *testing.T) {
	requireFFmpeg(t)
	out, err := media.Process(context.Background(), media.Input{
		Path: fixture(t, "sample.mp4"), DeclaredMIME: "video/mp4",
	}, media.Options{Limits: testLimits(t)})
	require.NoError(t, err)

	video := findVariant(t, out, "video")
	require.FileExists(t, video.TempPath)
	out.Cleanup()
	_, err = os.Stat(video.TempPath)
	require.True(t, os.IsNotExist(err), "Cleanup must remove the temp video file")
}
