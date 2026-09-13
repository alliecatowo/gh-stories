package terminal

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kittyChunkParsed is one parsed `\x1b_G<ctrl>;<payload>\x1b\\` chunk.
type kittyChunkParsed struct {
	ctrl    string
	payload string
}

// parseKittyChunks splits a raw kitty graphics escape stream (which may
// contain several chunked `\x1b_G...\x1b\\` sequences back to back) into
// its individual chunks.
func parseKittyChunks(t *testing.T, raw string) []kittyChunkParsed {
	t.Helper()
	require.True(t, strings.HasPrefix(raw, "\x1b_G"), "stream must start with the kitty APC introducer")
	require.True(t, strings.HasSuffix(raw, "\x1b\\"), "stream must end with ST")

	pieces := strings.Split(raw, "\x1b_G")
	var out []kittyChunkParsed
	for _, piece := range pieces {
		if piece == "" {
			continue
		}
		require.True(t, strings.HasSuffix(piece, "\x1b\\"), "each chunk must terminate with ST")
		body := strings.TrimSuffix(piece, "\x1b\\")
		idx := strings.IndexByte(body, ';')
		require.GreaterOrEqual(t, idx, 0, "chunk must have a ctrl;payload separator")
		out = append(out, kittyChunkParsed{ctrl: body[:idx], payload: body[idx+1:]})
	}
	return out
}

func chunkField(ctrl, key string) (string, bool) {
	re := regexp.MustCompile(`(?:^|,)` + regexp.QuoteMeta(key) + `=([^,]*)`)
	m := re.FindStringSubmatch(ctrl)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func solidImage(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func TestKittyRender_EscapeStructureAndChunking(t *testing.T) {
	col := color.NRGBA{R: 200, G: 100, B: 50, A: 255}
	img := solidImage(64, 64, col)

	// CellWidthPx/CellHeightPx and box are chosen so the target pixel size
	// matches the source exactly (8*8 == 64), keeping the resize a no-op on
	// a uniform-color image so we can assert on exact decoded pixel values.
	caps := Capabilities{Protocol: ProtocolKitty, CellWidthPx: 8, CellHeightPx: 8}
	k := &Kitty{caps: caps}

	var buf bytes.Buffer
	err := k.Render(&buf, 7, img, Placement{WidthCells: 8, HeightCells: 8}, caps)
	require.NoError(t, err)

	out := buf.String()
	chunks := parseKittyChunks(t, out)
	require.GreaterOrEqual(t, len(chunks), 2, "a 64x64 RGBA payload should not fit in a single 4096-byte chunk")

	first := chunks[0]
	a, _ := chunkField(first.ctrl, "a")
	assert.Equal(t, "T", a)
	f, _ := chunkField(first.ctrl, "f")
	assert.Equal(t, "32", f)
	i, _ := chunkField(first.ctrl, "i")
	assert.Equal(t, "7", i)
	q, _ := chunkField(first.ctrl, "q")
	assert.Equal(t, "2", q)
	sStr, ok := chunkField(first.ctrl, "s")
	require.True(t, ok)
	vStr, ok := chunkField(first.ctrl, "v")
	require.True(t, ok)
	s, err := strconv.Atoi(sStr)
	require.NoError(t, err)
	v, err := strconv.Atoi(vStr)
	require.NoError(t, err)
	require.Equal(t, 64, s)
	require.Equal(t, 64, v)

	var payload strings.Builder
	for idx, c := range chunks {
		m, ok := chunkField(c.ctrl, "m")
		require.True(t, ok, "every chunk must carry m=")
		if idx < len(chunks)-1 {
			assert.Equal(t, "1", m, "chunk %d should have m=1 (more data follows)", idx)
		} else {
			assert.Equal(t, "0", m, "final chunk must have m=0")
		}
		payload.WriteString(c.payload)
	}

	raw, err := base64.StdEncoding.DecodeString(payload.String())
	require.NoError(t, err, "concatenated chunk payloads must round-trip through base64")
	require.Equal(t, s*v*4, len(raw), "decoded payload must be exactly WxHx4 (RGBA) bytes")
	for px := 0; px < len(raw); px += 4 {
		require.Equal(t, byte(200), raw[px], "R channel at pixel %d", px/4)
		require.Equal(t, byte(100), raw[px+1], "G channel at pixel %d", px/4)
		require.Equal(t, byte(50), raw[px+2], "B channel at pixel %d", px/4)
		require.Equal(t, byte(255), raw[px+3], "A channel at pixel %d", px/4)
	}
}

func TestKittyRender_TmuxWrapsWholeSequence(t *testing.T) {
	img := solidImage(4, 4, color.NRGBA{R: 1, G: 2, B: 3, A: 255})
	caps := Capabilities{Protocol: ProtocolKitty, CellWidthPx: 8, CellHeightPx: 8, InTmux: true, TmuxPassthroughOK: true}
	k := &Kitty{caps: caps}

	var buf bytes.Buffer
	require.NoError(t, k.Render(&buf, 1, img, Placement{WidthCells: 1, HeightCells: 1}, caps))

	out := buf.Bytes()
	assert.True(t, bytes.HasPrefix(out, []byte("\x1bPtmux;")))
	assert.True(t, bytes.HasSuffix(out, []byte("\x1b\\")))
	// tmux passthrough doubles every literal ESC inside the payload, so the
	// kitty introducer (ESC _ G) must appear as a *doubled* ESC pair
	// followed by _G, not a single bare ESC.
	assert.Contains(t, string(out), "\x1b\x1b_G", "the inner kitty introducer's ESC must be doubled for tmux passthrough")
}

func TestKittyClear_DeleteByIDAndAll(t *testing.T) {
	k := &Kitty{caps: Capabilities{Protocol: ProtocolKitty}}

	var buf bytes.Buffer
	require.NoError(t, k.Clear(&buf, 42))
	assert.Contains(t, buf.String(), "a=d,d=i,i=42")

	buf.Reset()
	require.NoError(t, k.Clear(&buf, 0))
	assert.Contains(t, buf.String(), "a=d,d=A")
}

func TestKitty_SupportedRequiresVerifiedTmuxPassthrough(t *testing.T) {
	k := &Kitty{}
	assert.False(t, k.Supported(Capabilities{Protocol: ProtocolKitty, InTmux: true, TmuxPassthroughOK: false}))
	assert.True(t, k.Supported(Capabilities{Protocol: ProtocolKitty, InTmux: true, TmuxPassthroughOK: true}))
	assert.True(t, k.Supported(Capabilities{Protocol: ProtocolKitty}))
	assert.False(t, k.Supported(Capabilities{Protocol: ProtocolExternal}))
}
