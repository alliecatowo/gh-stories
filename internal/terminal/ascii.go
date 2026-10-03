package terminal

import (
	"fmt"
	"image"
	"io"
	"os"
	"strings"
)

// This file is the universal text fallback for pictures, following the same
// ladder every tool in this space uses (terminal-image, ink-picture): a
// native graphics protocol when one was positively detected, otherwise
// Unicode half-blocks with color. A half-block cell draws TWO image rows in
// one terminal row — the upper pixel as the foreground color, the lower as
// the background — so a picture stays recognisable at any size a Story
// needs, in any terminal that can do color at all.
//
// Video uses the same path: callers hand Render the poster frame, so a
// video without graphics support shows its poster as art rather than motion
// the terminal cannot deliver. The product never claims motion it did not
// deliver; the pane still labels the item as video.

// halfBlock is the upper-half-block glyph: foreground paints the top pixel,
// background paints the bottom pixel of the cell.
const halfBlock = "▀"

// RenderHalfBlock draws img into w as colored half-block art, cols cells
// wide, preserving aspect ratio. Each output row consumes two image rows.
// truecolor selects 24-bit escapes; otherwise colors are mapped onto the
// 256-color palette. Every line ends with a reset so a truncated stream can
// never leak attributes into the caller's later output.
func RenderHalfBlock(w io.Writer, img image.Image, cols int, truecolor bool) error {
	if img == nil {
		return fmt.Errorf("terminal: half-block render: image is nil")
	}
	if cols < 2 {
		cols = 2
	}
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return fmt.Errorf("terminal: half-block render: empty image")
	}
	// A terminal cell is roughly twice as tall as it is wide; sample each
	// cell from a 1x2 pixel block of the scaled image so the art keeps the
	// source's aspect ratio instead of stretching vertically.
	const cellAspect = 2.0
	rows := int(float64(sh) * float64(cols) / float64(sw) / cellAspect)
	if rows < 1 {
		rows = 1
	}
	var sb strings.Builder
	for r := 0; r < rows; r++ {
		y0 := (r * 2 * sh) / (rows * 2)
		y1 := ((r*2 + 1) * sh) / (rows * 2)
		for c := 0; c < cols; c++ {
			x := (c*sw + sw/2) / cols
			top := sampleRGB(img, b.Min.X+x, b.Min.Y+y0)
			bot := sampleRGB(img, b.Min.X+x, b.Min.Y+y1)
			sb.WriteString(colorCell(top, bot, truecolor))
		}
		sb.WriteString("\x1b[0m\n")
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

// rgb is a sampled pixel in 16-bit-per-channel form, as image.At returns.
type rgb struct{ r, g, bl uint32 }

// sampleRGB reads one pixel, clamping into bounds so rounding at the edges
// can never panic.
func sampleRGB(img image.Image, x, y int) rgb {
	b := img.Bounds()
	if x < b.Min.X {
		x = b.Min.X
	}
	if x >= b.Max.X {
		x = b.Max.X - 1
	}
	if y < b.Min.Y {
		y = b.Min.Y
	}
	if y >= b.Max.Y {
		y = b.Max.Y - 1
	}
	r, g, bl, _ := img.At(x, y).RGBA()
	return rgb{r, g, bl}
}

// colorCell renders one half-block cell: fg=top pixel, bg=bottom pixel.
func colorCell(top, bot rgb, truecolor bool) string {
	if truecolor {
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm%s",
			top.r>>8, top.g>>8, top.bl>>8, bot.r>>8, bot.g>>8, bot.bl>>8, halfBlock)
	}
	return fmt.Sprintf("\x1b[38;5;%dm\x1b[48;5;%dm%s",
		to256(top), to256(bot), halfBlock)
}

// to256 maps a pixel onto the 6x6x6 color cube (codes 16-231), the same
// quantization terminals themselves apply to out-of-range requests.
func to256(c rgb) int {
	r, g, b := int(c.r>>8)*5/255, int(c.g>>8)*5/255, int(c.bl>>8)*5/255
	return 16 + 36*r + 6*g + b
}

// artOK reports whether colored half-block art is worth emitting under caps.
// Truecolor is preferred; 256-color terminals (TERM *256color*) get the
// palette-mapped variant. Anything else — dumb terminals, no color hint at
// all — keeps the plain-text placeholder box, which is always safe.
func artOK(caps Capabilities) bool {
	if caps.TruecolorOK {
		return true
	}
	term := os.Getenv("TERM")
	if term == "" || term == "dumb" {
		return false
	}
	return strings.Contains(term, "256color")
}
