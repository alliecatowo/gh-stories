package terminal

import (
	"encoding/base64"
	"fmt"
	"image"
	"io"

	"github.com/disintegration/imaging"
)

// kittyChunkSize is the maximum size, in bytes, of a single base64 payload
// chunk within one kitty graphics escape. The protocol requires splitting
// large transmissions into chunks no larger than 4096 bytes each.
const kittyChunkSize = 4096

// Kitty implements Renderer using the kitty terminal graphics protocol:
// https://sw.kovidgoyal.net/kitty/graphics-protocol/
type Kitty struct {
	// caps is the Capabilities this renderer was constructed with (via
	// New). Render receives its own caps argument for the placement math
	// below, but Clear does not (see the Renderer interface), so it relies
	// on this stored copy to decide whether to wrap in tmux passthrough.
	caps Capabilities
}

// Name reports the protocol this renderer implements.
func (k *Kitty) Name() Protocol { return ProtocolKitty }

// Supported reports whether caps indicates working kitty graphics support.
// Inside tmux, this additionally requires verified passthrough
// (TmuxPassthroughOK): emitting raw kitty escapes into a tmux session
// without allow-passthrough enabled does not draw an image, it corrupts the
// pane with garbage bytes tmux couldn't forward or interpret.
func (k *Kitty) Supported(caps Capabilities) bool {
	if caps.Protocol != ProtocolKitty {
		return false
	}
	if caps.InTmux && !caps.TmuxPassthroughOK {
		return false
	}
	return true
}

// Render resizes img to fit p (preserving aspect ratio, computed by
// FitCells) and transmits it as an RGBA (f=32) kitty graphics image, using
// c=/r= so the terminal performs the final scale into the requested cell
// box. The image is resized here, before transmission, specifically so a
// large source image is never shipped in full just to be displayed in a
// handful of cells.
func (k *Kitty) Render(w io.Writer, id uint32, img image.Image, p Placement, caps Capabilities) error {
	if img == nil {
		return fmt.Errorf("terminal: kitty render: image is nil")
	}
	b := img.Bounds()
	cols, rows, pxW, pxH := FitCells(b.Dx(), b.Dy(), p, caps)

	resized := imaging.Fit(img, pxW, pxH, imaging.Lanczos)
	rb := resized.Bounds()
	width, height := rb.Dx(), rb.Dy()
	if width <= 0 || height <= 0 {
		return fmt.Errorf("terminal: kitty render: resized image has invalid dimensions %dx%d", width, height)
	}

	payload := base64.StdEncoding.EncodeToString(rgbaPixels(resized))
	seq := buildKittyTransmit(id, uint32(width), uint32(height), uint32(cols), uint32(rows), payload)
	if caps.InTmux && caps.TmuxPassthroughOK {
		seq = tmuxPassthrough(seq)
	}
	_, err := w.Write(seq)
	return err
}

// Clear deletes a previously transmitted image by id using the protocol's
// a=d,d=i delete action, or every image kitty is tracking for this
// terminal (a=d,d=A) when id == 0.
func (k *Kitty) Clear(w io.Writer, id uint32) error {
	var ctrl string
	if id == 0 {
		ctrl = "a=d,d=A,q=2"
	} else {
		ctrl = fmt.Sprintf("a=d,d=i,i=%d,q=2", id)
	}
	seq := []byte("\x1b_G" + ctrl + "\x1b\\")
	if k.caps.InTmux && k.caps.TmuxPassthroughOK {
		seq = tmuxPassthrough(seq)
	}
	_, err := w.Write(seq)
	return err
}

// buildKittyTransmit builds the (possibly multi-chunk) escape sequence that
// transmits and displays (a=T) a WxH RGBA (f=32) image with identity id,
// scaled into cols x rows cells. q=2 suppresses the terminal's OK/error
// response: those replies would otherwise land in the TUI's input stream,
// indistinguishable from a real keypress, which is not a tradeoff worth
// making just to observe a response we don't act on.
//
// Only the first chunk carries the full control payload; continuation
// chunks carry only m= (more-data-follows), per the protocol's chunking
// rules. m=1 on every chunk but the last, m=0 on the last.
func buildKittyTransmit(id, width, height, cols, rows uint32, b64 string) []byte {
	chunks := chunkString(b64, kittyChunkSize)
	var out []byte
	for i, chunk := range chunks {
		more := 0
		if i < len(chunks)-1 {
			more = 1
		}
		var ctrl string
		if i == 0 {
			ctrl = fmt.Sprintf("a=T,f=32,s=%d,v=%d,i=%d,c=%d,r=%d,q=2,m=%d", width, height, id, cols, rows, more)
		} else {
			ctrl = fmt.Sprintf("m=%d", more)
		}
		out = append(out, "\x1b_G"...)
		out = append(out, ctrl...)
		out = append(out, ';')
		out = append(out, chunk...)
		out = append(out, "\x1b\\"...)
	}
	return out
}

// chunkString splits s into pieces no longer than size. An empty input
// yields a single empty chunk so callers always have at least one chunk to
// emit (necessary control data must be sent even for a 0-byte image).
func chunkString(s string, size int) []string {
	if len(s) == 0 {
		return []string{""}
	}
	var chunks []string
	for len(s) > size {
		chunks = append(chunks, s[:size])
		s = s[size:]
	}
	return append(chunks, s)
}

// rgbaPixels extracts tightly-packed RGBA bytes (4 bytes per pixel, no row
// padding) from img, respecting Stride so rows with trailing pad bytes
// (which image.NRGBA can have) are not accidentally included.
func rgbaPixels(img *image.NRGBA) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]byte, 0, w*h*4)
	for y := 0; y < h; y++ {
		rowStart := y * img.Stride
		out = append(out, img.Pix[rowStart:rowStart+w*4]...)
	}
	return out
}
