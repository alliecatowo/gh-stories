package terminal

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// solid returns a w×h image of one color.
func solid(w, h int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestHalfBlockGeometry(t *testing.T) {
	// 100x100 square at 20 cols: rows = 100*20/100/2 = 10.
	var sb strings.Builder
	require.NoError(t, RenderHalfBlock(&sb, solid(100, 100, color.White), 20, true))
	lines := strings.Split(strings.TrimSuffix(sb.String(), "\n"), "\n")
	require.Len(t, lines, 10)
	for _, l := range lines {
		require.True(t, strings.HasSuffix(l, "\x1b[0m"), "every line ends reset")
		require.Contains(t, l, "▀")
	}
}

func TestHalfBlockTruecolorVs256(t *testing.T) {
	var tc, pal strings.Builder
	img := solid(8, 4, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	require.NoError(t, RenderHalfBlock(&tc, img, 4, true))
	require.NoError(t, RenderHalfBlock(&pal, img, 4, false))
	require.Contains(t, tc.String(), "38;2;255;0;0m", "truecolor fg")
	require.NotContains(t, pal.String(), "38;2;", "palette mode emits no truecolor")
	require.Contains(t, pal.String(), "38;5;", "palette mode uses 256-color")
}

func TestHalfBlockRejectsNilAndEmpty(t *testing.T) {
	var sb strings.Builder
	require.Error(t, RenderHalfBlock(&sb, nil, 20, true))
	require.Error(t, RenderHalfBlock(&sb,
		image.NewRGBA(image.Rect(0, 0, 0, 0)), 20, true))
}

func TestExternalRendersArtWhenColored(t *testing.T) {
	caps := Capabilities{Protocol: ProtocolExternal, TruecolorOK: true}
	var sb strings.Builder
	e := &External{}
	require.NoError(t, e.Render(&sb, 1, solid(40, 20, color.White),
		Placement{WidthCells: 20, HeightCells: 10}, caps))
	require.Contains(t, sb.String(), "▀", "colored terminal gets art, not a box")
	require.NotContains(t, sb.String(), "┌", "no placeholder border when art drew")
}

func TestExternalKeepsPlaceholderWithoutColor(t *testing.T) {
	t.Setenv("TERM", "dumb")
	caps := Capabilities{Protocol: ProtocolExternal}
	var sb strings.Builder
	e := &External{}
	require.NoError(t, e.Render(&sb, 1, solid(40, 20, color.White),
		Placement{WidthCells: 20, HeightCells: 10}, caps))
	require.Contains(t, sb.String(), "┌", "dumb terminal keeps the honest box")
}

func TestGhosttyHintSelectsKittyOnLiveChannel(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	caps := Capabilities{}
	pr := probeResult{daSeen: true} // fence answered, kitty query ignored
	decideProtocol(&caps, pr)
	require.Equal(t, ProtocolKitty, caps.Protocol)
}

func TestGhosttyHintNeedsLiveChannel(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	caps := Capabilities{}
	decideProtocol(&caps, probeResult{}) // nothing round-tripped
	require.Equal(t, ProtocolExternal, caps.Protocol,
		"no fence reply means no proof of anything; stay external")
}

func TestGhosttyNeverMapsToITerm(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "ghostty")
	t.Setenv("LC_TERMINAL", "")
	require.False(t, isITermLikeEnv(),
		"Ghostty does not implement OSC 1337 and must not take the iTerm path")
}
