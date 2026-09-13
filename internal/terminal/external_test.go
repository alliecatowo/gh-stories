package terminal

import (
	"bytes"
	"image"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExternal_RenderPlaceholder_ContainsMetadata(t *testing.T) {
	e := &External{}
	meta := PlaceholderMeta{
		Kind:        "image",
		Width:       1080,
		Height:      1920,
		ByteSize:    2_400_000,
		Description: "A screenshot of the terminal",
		Instruction: "Press Enter to open in your browser",
	}

	var buf bytes.Buffer
	require.NoError(t, e.RenderPlaceholder(&buf, Placement{WidthCells: 60, HeightCells: 8}, meta))

	out := buf.String()
	assert.Contains(t, out, "1080")
	assert.Contains(t, out, "1920")
	assert.Contains(t, out, "2.3 MB") // 2,400,000 bytes ≈ 2.3 MiB
	assert.Contains(t, out, "A screenshot of the terminal")
	assert.Contains(t, out, "Press Enter to open in your browser")
}

func TestExternal_RenderPlaceholder_NeverContainsESC(t *testing.T) {
	e := &External{}
	meta := PlaceholderMeta{
		Kind:        "video",
		Width:       640,
		Height:      480,
		Description: "hostile\x1b[2Jdescription\x1b]0;pwned\x07",
		Instruction: "click \x1b]8;;http://evil\x1b\\here\x1b]8;;\x1b\\",
		Reason:      "server said: \x1b[31merror\x1b[0m",
	}

	var buf bytes.Buffer
	require.NoError(t, e.RenderPlaceholder(&buf, Placement{WidthCells: 40, HeightCells: 10}, meta))

	assert.NotContains(t, buf.String(), "\x1b", "placeholder output must never contain a raw ESC byte")
}

func TestExternal_RenderPlaceholder_TinyBoxStillValid(t *testing.T) {
	e := &External{}
	var buf bytes.Buffer
	err := e.RenderPlaceholder(&buf, Placement{WidthCells: 0, HeightCells: 0}, PlaceholderMeta{Kind: "image"})
	require.NoError(t, err)
	assert.NotEmpty(t, buf.String())
}

func TestExternal_Render_DerivesMetaFromImageBounds(t *testing.T) {
	e := &External{}
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	var buf bytes.Buffer
	require.NoError(t, e.Render(&buf, 0, img, Placement{WidthCells: 40, HeightCells: 8}, Capabilities{}))
	assert.Contains(t, buf.String(), "200")
	assert.Contains(t, buf.String(), "100")
}

func TestExternal_SupportedAlwaysTrue(t *testing.T) {
	e := &External{}
	assert.True(t, e.Supported(Capabilities{}))
	assert.True(t, e.Supported(Capabilities{Protocol: ProtocolKitty}))
}

func TestExternal_ClearIsNoOp(t *testing.T) {
	e := &External{}
	var buf bytes.Buffer
	require.NoError(t, e.Clear(&buf, 1))
	assert.Empty(t, buf.String())
}
