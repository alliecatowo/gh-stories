package terminal

import (
	"fmt"
	"image"
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// PlaceholderMeta describes the media an External placeholder stands in
// for, so a person can understand and act on it without ever seeing the
// actual pixels.
type PlaceholderMeta struct {
	Kind        string
	Width       int
	Height      int
	ByteSize    int64
	DurationMS  int
	Description string
	Instruction string
	Reason      string
}

// External is the honest fallback renderer: it never pretends to draw a
// picture it cannot actually draw. Instead it prints a bordered text block
// describing the media and how to view it elsewhere (e.g. in a browser).
// It is always Supported, on any writer, TTY or not, since it emits plain
// text.
type External struct {
	caps Capabilities
}

// Name reports the protocol this renderer implements.
func (e *External) Name() Protocol { return ProtocolExternal }

// Supported always returns true: External is the universal fallback.
func (e *External) Supported(caps Capabilities) bool { return true }

// Render draws img as half-block color art when the terminal can do color,
// falling back to the placeholder box otherwise.
//
// The art path is what makes Stories viewable in tmux without passthrough,
// over plain SSH clients, and in any color terminal whose graphics probe
// came back negative: a picture the terminal cannot draw natively is still
// a picture worth seeing. Video arrives here as its poster frame, so it
// renders as a still — the pane (not this renderer) keeps labelling the
// item as video, and the product never claims motion it did not deliver.
//
// Callers with richer metadata (byte size, an accessibility description, a
// URL) should call RenderPlaceholder directly instead, since Render has no
// way to receive that information through the shared Renderer interface.
func (e *External) Render(w io.Writer, id uint32, img image.Image, p Placement, caps Capabilities) error {
	if img != nil && artOK(caps) {
		cols := p.WidthCells
		if cols < 2 {
			cols = 2
		}
		if err := RenderHalfBlock(w, img, cols, caps.TruecolorOK); err == nil {
			return nil
		}
		// Art failed; fall through to the placeholder rather than drawing
		// nothing at all.
	}
	meta := PlaceholderMeta{Kind: "image"}
	if img != nil {
		b := img.Bounds()
		meta.Width, meta.Height = b.Dx(), b.Dy()
	}
	return e.RenderPlaceholder(w, p, meta)
}

// Clear is a no-op. A placeholder is plain text that has already scrolled
// into the normal terminal buffer like any other output, not an
// addressable overlay the way a graphics protocol image is, so there is
// nothing this method could remove.
func (e *External) Clear(w io.Writer, id uint32) error { return nil }

// RenderPlaceholder draws a border box sized to p containing a metadata
// summary line (kind, dimensions, byte size, duration), an optional
// accessibility description, an optional reason (e.g. why graphics weren't
// used), and an instruction for reaching the real content.
//
// Every piece of caller-supplied text (Description, Instruction, Reason) is
// untrusted — it may originate from a story author's caption, a filename,
// or a server error message — and is run through SanitizeTruncate before
// being written, so a hostile caption can neither inject terminal escapes
// nor overflow the box.
func (e *External) RenderPlaceholder(w io.Writer, p Placement, meta PlaceholderMeta) error {
	width := p.WidthCells
	if width < 3 {
		width = 3
	}
	height := p.HeightCells
	if height < 2 {
		height = 2
	}
	inner := width - 2

	lines := placeholderLines(meta, inner)

	var b strings.Builder
	b.WriteString("┌" + strings.Repeat("─", inner) + "┐\n")
	maxContentRows := height - 2
	for i := 0; i < maxContentRows; i++ {
		var line string
		if i < len(lines) {
			line = lines[i]
		}
		b.WriteString("│" + padCells(line, inner) + "│\n")
	}
	b.WriteString("└" + strings.Repeat("─", inner) + "┘\n")

	_, err := io.WriteString(w, b.String())
	return err
}

func placeholderLines(meta PlaceholderMeta, width int) []string {
	kind := meta.Kind
	if kind == "" {
		kind = "image"
	}
	summary := "[" + kind + "]"
	if meta.Width > 0 || meta.Height > 0 {
		summary += fmt.Sprintf(" %dx%d", meta.Width, meta.Height)
	}
	if meta.ByteSize > 0 {
		summary += " · " + formatBytes(meta.ByteSize)
	}
	if meta.DurationMS > 0 {
		summary += fmt.Sprintf(" · %.1fs", float64(meta.DurationMS)/1000.0)
	}

	lines := []string{SanitizeTruncate(summary, width)}
	if meta.Description != "" {
		lines = append(lines, SanitizeTruncate(meta.Description, width))
	}
	if meta.Reason != "" {
		lines = append(lines, SanitizeTruncate(meta.Reason, width))
	}
	if meta.Instruction != "" {
		lines = append(lines, SanitizeTruncate(meta.Instruction, width))
	}
	return lines
}

// padCells right-pads s with spaces to exactly width terminal cells, using
// ansi.StringWidth so wide glyphs in a sanitized caption don't throw off
// the box's right border.
func padCells(s string, width int) string {
	w := ansi.StringWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// formatBytes renders n as a short, human-readable size (e.g. "2.4 MB").
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
