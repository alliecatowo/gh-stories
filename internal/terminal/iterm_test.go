package terminal

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var itermFileRe = regexp.MustCompile(`^\x1b\]1337;File=inline=1;size=(\d+);width=(\d+);height=(\d+);preserveAspectRatio=1:(.*)\x07$`)

func TestITermRender_OSCStructureAndPayload(t *testing.T) {
	img := solidImage(32, 16, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	caps := Capabilities{Protocol: ProtocolITerm2, CellWidthPx: 8, CellHeightPx: 8}
	it := &ITerm2{caps: caps}

	var buf bytes.Buffer
	require.NoError(t, it.Render(&buf, 1, img, Placement{WidthCells: 4, HeightCells: 2}, caps))

	out := buf.String()
	m := itermFileRe.FindStringSubmatch(out)
	require.NotNil(t, m, "output must match the OSC 1337 File inline form: %q", out)

	size, err := strconv.Atoi(m[1])
	require.NoError(t, err)
	width, err := strconv.Atoi(m[2])
	require.NoError(t, err)
	height, err := strconv.Atoi(m[3])
	require.NoError(t, err)
	b64 := m[4]

	// width=/height= are expressed in cells and must not exceed the
	// requested placement box.
	assert.LessOrEqual(t, width, 4)
	assert.LessOrEqual(t, height, 2)
	assert.GreaterOrEqual(t, width, 1)
	assert.GreaterOrEqual(t, height, 1)

	raw, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err, "payload must be valid base64")
	assert.Equal(t, size, len(raw), "size= must match the actual decoded payload length")

	decoded, err := png.Decode(bytes.NewReader(raw))
	require.NoError(t, err, "payload must decode as a valid PNG")
	assert.Greater(t, decoded.Bounds().Dx(), 0)
	assert.Greater(t, decoded.Bounds().Dy(), 0)
}

func TestITermRender_MultipartForLargePayloads(t *testing.T) {
	// A large, high-entropy (noise) image so PNG compression can't shrink it
	// back under the multipart threshold.
	img := image.NewNRGBA(image.Rect(0, 0, 900, 900))
	seed := uint32(12345)
	for y := 0; y < 900; y++ {
		for x := 0; x < 900; x++ {
			seed = seed*1664525 + 1013904223
			img.SetNRGBA(x, y, color.NRGBA{
				R: byte(seed), G: byte(seed >> 8), B: byte(seed >> 16), A: 255,
			})
		}
	}
	caps := Capabilities{Protocol: ProtocolITerm2, CellWidthPx: 8, CellHeightPx: 8}
	it := &ITerm2{caps: caps}

	var buf bytes.Buffer
	require.NoError(t, it.Render(&buf, 1, img, Placement{WidthCells: 200, HeightCells: 200}, caps))

	out := buf.String()
	assert.Contains(t, out, "\x1b]1337;MultipartFile=")
	assert.Contains(t, out, "\x1b]1337;FilePart=")
	assert.True(t, strings.HasSuffix(out, "\x1b]1337;FileEnd\x07"))

	// Every FilePart chunk's payload must independently be valid base64
	// (chunk boundaries must not split a base64 group).
	parts := regexp.MustCompile(`\x1b\]1337;FilePart=([^\x07]*)\x07`).FindAllStringSubmatch(out, -1)
	require.Greater(t, len(parts), 1, "a 900x900 noise image should require more than one FilePart chunk")
	for _, p := range parts {
		_, err := base64.StdEncoding.DecodeString(p[1])
		assert.NoError(t, err)
	}
}

func TestITermClear_ErasesRecordedRegionAndIsNoOpForUnknownID(t *testing.T) {
	it := &ITerm2{caps: Capabilities{Protocol: ProtocolITerm2}, placements: make(map[uint32]Placement)}
	img := solidImage(8, 8, color.NRGBA{A: 255})
	caps := Capabilities{Protocol: ProtocolITerm2, CellWidthPx: 8, CellHeightPx: 8}

	var render bytes.Buffer
	require.NoError(t, it.Render(&render, 9, img, Placement{Col: 2, Row: 3, WidthCells: 5, HeightCells: 2}, caps))

	var clearBuf bytes.Buffer
	require.NoError(t, it.Clear(&clearBuf, 9))
	out := clearBuf.String()
	assert.Contains(t, out, "\x1b[4;3H\x1b[K", "first row of the recorded placement should be erased (1-based CUP)")
	assert.Contains(t, out, "\x1b[5;3H\x1b[K", "second row of the recorded placement should be erased")

	// Clearing an id that was never rendered (or already cleared) is a
	// documented no-op, not an error and not a guess at what to erase.
	clearBuf.Reset()
	require.NoError(t, it.Clear(&clearBuf, 9))
	assert.Empty(t, clearBuf.String())
}

func TestITerm_SupportedRequiresVerifiedTmuxPassthrough(t *testing.T) {
	it := &ITerm2{}
	assert.False(t, it.Supported(Capabilities{Protocol: ProtocolITerm2, InTmux: true, TmuxPassthroughOK: false}))
	assert.True(t, it.Supported(Capabilities{Protocol: ProtocolITerm2, InTmux: true, TmuxPassthroughOK: true}))
	assert.True(t, it.Supported(Capabilities{Protocol: ProtocolITerm2}))
	assert.False(t, it.Supported(Capabilities{Protocol: ProtocolKitty}))
}
