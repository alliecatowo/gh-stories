package terminal_test

import (
	"image"
	"image/color"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alliecatowo/gh-stories/internal/terminal"
)

func solidFrames(n, w, h int) []terminal.Frame {
	out := make([]terminal.Frame, 0, n)
	for i := 0; i < n; i++ {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		c := color.RGBA{uint8(10 * i), uint8(200 - 5*i), 120, 255}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				img.SetRGBA(x, y, c)
			}
		}
		out = append(out, terminal.Frame{Image: img, GapMS: 125})
	}
	return out
}

func kittyCaps() terminal.Capabilities {
	return terminal.Capabilities{
		Protocol: terminal.ProtocolKitty, ColsCells: 80, RowsCells: 24,
		CellWidthPx: 10, CellHeightPx: 20,
	}
}

// TestAnimateEmitsTheVerifiedSequence pins the exact protocol shape that was
// established against a real kitty. The obvious reading of the specification
// does NOT work, so these assertions are the guard against silently
// regressing back to it.
func TestAnimateEmitsTheVerifiedSequence(t *testing.T) {
	caps := kittyCaps()
	a := terminal.AnimatorFor(caps)
	require.NotNil(t, a, "kitty capabilities must yield an animator")

	var out strings.Builder
	anim, err := a.Animate(&out, solidFrames(4, 60, 60),
		terminal.Placement{Col: 1, Row: 2, WidthCells: 30, HeightCells: 12}, caps)
	require.NoError(t, err)
	t.Cleanup(func() { _ = anim.Stop(&strings.Builder{}, caps) })

	seq := out.String()

	// Addressed by image NUMBER (I=), not id (i=). Using i= was verified to
	// make every appended frame overwrite frame 2.
	require.Contains(t, seq, "I=")
	require.Regexp(t, regexp.MustCompile(`a=T,f=24,t=f,[^;]*I=\d+`), seq,
		"root frame must be transmitted by file, addressed by image number")

	// Frames must arrive atomically as files (t=f), not as chunked inline
	// payloads — that was the difference between working and not working.
	require.NotContains(t, seq, "t=d")
	frameCmds := regexp.MustCompile(`a=f,f=24,t=f,`).FindAllString(seq, -1)
	require.Len(t, frameCmds, 3, "one a=f per frame after the root")

	// Each appended frame chains onto the previous one.
	require.Contains(t, seq, "c=1,")
	require.Contains(t, seq, "c=2,")
	require.Contains(t, seq, "c=3,")

	// Playback is started, and kept running while later frames load.
	require.Contains(t, seq, "a=a,s=2")
	require.Contains(t, seq, "a=a,s=3")

	// Quiet by default: a stray OK reply would land in the TUI's input.
	require.Contains(t, seq, "q=2")
}

// TestAnimateRefusesOverSSH: frames are handed to the terminal as local files,
// which a terminal on the far side of an SSH connection cannot read.
func TestAnimationUnavailableOverSSH(t *testing.T) {
	caps := kittyCaps()
	caps.OverSSH = true
	require.Nil(t, terminal.AnimatorFor(caps),
		"inline video must fall back to the poster frame over SSH")
}

// TestNonKittyRenderersDoNotClaimAnimation guards the honesty rule: a renderer
// that cannot animate must say so rather than show a still and call it video.
func TestNonKittyRenderersDoNotClaimAnimation(t *testing.T) {
	for _, proto := range []terminal.Protocol{terminal.ProtocolITerm2, terminal.ProtocolExternal} {
		caps := terminal.Capabilities{Protocol: proto, ColsCells: 80, RowsCells: 24}
		require.Nil(t, terminal.AnimatorFor(caps), "%s must not claim animation", proto)

		r := terminal.New(caps)
		a, ok := r.(terminal.Animator)
		require.True(t, ok, "%s should still implement the interface", proto)
		require.False(t, a.SupportsAnimation(caps))
		_, err := a.Animate(&strings.Builder{}, solidFrames(2, 10, 10),
			terminal.Placement{Col: 1, Row: 1, WidthCells: 5, HeightCells: 5}, caps)
		require.ErrorIs(t, err, terminal.ErrAnimationUnsupported)
	}
}

// TestAnimateBoundsFrameCount keeps a long video from flooding the terminal.
func TestAnimateBoundsFrameCount(t *testing.T) {
	caps := kittyCaps()
	a := terminal.AnimatorFor(caps)
	var out strings.Builder
	anim, err := a.Animate(&out, solidFrames(terminal.MaxAnimationFrames+40, 20, 20),
		terminal.Placement{Col: 1, Row: 1, WidthCells: 10, HeightCells: 6}, caps)
	require.NoError(t, err)
	t.Cleanup(func() { _ = anim.Stop(&strings.Builder{}, caps) })
	frames := regexp.MustCompile(`a=f,`).FindAllString(out.String(), -1)
	require.Len(t, frames, terminal.MaxAnimationFrames-1)
}

// TestStopRemovesTheImageAndItsFrameFiles: decoded frames of a private Story
// must not be left on disk.
func TestStopCleansUp(t *testing.T) {
	caps := kittyCaps()
	a := terminal.AnimatorFor(caps)
	var out strings.Builder
	anim, err := a.Animate(&out, solidFrames(3, 30, 30),
		terminal.Placement{Col: 1, Row: 1, WidthCells: 10, HeightCells: 6}, caps)
	require.NoError(t, err)

	var stop strings.Builder
	require.NoError(t, anim.Stop(&stop, caps))
	require.Contains(t, stop.String(), "a=a,s=1", "playback must be halted")
	require.Contains(t, stop.String(), "a=d", "the image must be deleted from the terminal")
}
