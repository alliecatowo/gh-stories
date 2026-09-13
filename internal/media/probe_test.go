package media_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/media"
)

func TestSniffFixtures(t *testing.T) {
	cases := []struct {
		file, want string
	}{
		{"portrait.jpg", "image/jpeg"},
		{"landscape.png", "image/png"},
		{"square.webp", "image/webp"},
		{"transparent.png", "image/png"},
		{"tall.png", "image/png"},
		{"animated.gif", "image/gif"},
		{"sample.mp4", "video/mp4"},
		{"sample.webm", "video/webm"},
		{"fake.png", "image/jpeg"}, // real signature wins over the filename
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			head := readHead(t, fixture(t, c.file))
			mime, ok := media.Sniff(head)
			require.True(t, ok)
			require.Equal(t, c.want, mime)
		})
	}
}

func TestSniffRejectsUnrecognized(t *testing.T) {
	head := readHead(t, fixture(t, "not-media.bin"))
	_, ok := media.Sniff(head)
	require.False(t, ok)
}

// TestSniffRejectsSVG confirms SVG is not on the accept list at all: an SVG
// document (plain XML text) has no binary magic-byte signature this package
// recognizes, so it is rejected as unsupported rather than being parsed as
// markup.
func TestSniffRejectsSVG(t *testing.T) {
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	_, ok := media.Sniff(svg)
	require.False(t, ok)
}

func TestSniffEmptyAndShortInput(t *testing.T) {
	_, ok := media.Sniff(nil)
	require.False(t, ok)
	_, ok = media.Sniff([]byte{0xFF})
	require.False(t, ok)
}

func readHead(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	return buf[:n]
}

func TestProbeAnimatedGIFFrameCount(t *testing.T) {
	info, err := media.Probe(context.Background(), fixture(t, "animated.gif"))
	require.NoError(t, err)
	require.Equal(t, media.KindImage, info.Kind)
	require.True(t, info.Animated)
	require.Equal(t, 12, info.FrameCount, "animated.gif was generated with exactly 12 frames")
}

func TestProbeStaticImagesAreNotAnimated(t *testing.T) {
	for _, f := range []string{"portrait.jpg", "landscape.png", "square.webp", "transparent.png"} {
		t.Run(f, func(t *testing.T) {
			info, err := media.Probe(context.Background(), fixture(t, f))
			require.NoError(t, err)
			require.False(t, info.Animated)
		})
	}
}

func TestProbeImageDimensions(t *testing.T) {
	info, err := media.Probe(context.Background(), fixture(t, "portrait.jpg"))
	require.NoError(t, err)
	require.Equal(t, 1080, info.Width)
	require.Equal(t, 1920, info.Height)
}

func TestProbeBombPNGDimensionsWithoutDecoding(t *testing.T) {
	info, err := media.Probe(context.Background(), fixture(t, "bomb.png"))
	require.NoError(t, err) // Probe itself only reads the header; the pixel
	// budget is enforced by Process, not by Probe — see
	// TestProcessRejectsPixelBombBeforeDecode.
	require.Equal(t, 30000, info.Width)
	require.Equal(t, 30000, info.Height)
}

func TestProbeVideoFixtures(t *testing.T) {
	requireFFmpeg(t)
	for _, f := range []string{"sample.mp4", "sample.webm"} {
		t.Run(f, func(t *testing.T) {
			info, err := media.Probe(context.Background(), fixture(t, f))
			require.NoError(t, err)
			require.Equal(t, media.KindVideo, info.Kind)
			require.Equal(t, 640, info.Width)
			require.Equal(t, 360, info.Height)
			require.True(t, info.HasAudio)
			require.InDelta(t, 3000, info.DurationMS, 300)
		})
	}
}
