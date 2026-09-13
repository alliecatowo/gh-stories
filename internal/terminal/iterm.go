package terminal

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"sync"

	"github.com/disintegration/imaging"
)

// itermMultipartThreshold is the payload size, in raw (pre-base64) PNG
// bytes, above which Render switches to iTerm2's MultipartFile form. Some
// terminals — and tmux's passthrough in particular — drop or truncate very
// long single OSC sequences; splitting the transfer into bounded FilePart
// chunks avoids that failure mode. iTerm2's own docs recommend doing this
// above roughly a megabyte.
const itermMultipartThreshold = 1 << 20

// itermChunkSize bounds each FilePart's raw payload so a single OSC write
// stays a reasonable size even when multipart transfer is in use.
const itermChunkSize = 200 * 1024

// ITerm2 implements Renderer using iTerm2's inline image protocol:
// https://iterm2.com/documentation-images.html
type ITerm2 struct {
	caps Capabilities

	mu         sync.Mutex
	placements map[uint32]Placement // last-rendered region per non-zero id, for Clear
}

// Name reports the protocol this renderer implements.
func (t *ITerm2) Name() Protocol { return ProtocolITerm2 }

// Supported reports whether caps indicates an iTerm2-protocol terminal.
// Inside tmux this additionally requires verified passthrough, for the same
// reason as Kitty.Supported: an unverified wrap is not forwarded correctly
// and corrupts the pane instead of drawing anything.
func (t *ITerm2) Supported(caps Capabilities) bool {
	if caps.Protocol != ProtocolITerm2 {
		return false
	}
	if caps.InTmux && !caps.TmuxPassthroughOK {
		return false
	}
	return true
}

// Render resizes img to fit p (preserving aspect ratio, computed by
// FitCells), encodes it as PNG, and transmits it via OSC 1337 File (or
// MultipartFile, for large payloads). width=/height= are expressed in
// cells, as required for the box to line up with p regardless of the
// image's actual pixel size.
func (t *ITerm2) Render(w io.Writer, id uint32, img image.Image, p Placement, caps Capabilities) error {
	if img == nil {
		return fmt.Errorf("terminal: iterm render: image is nil")
	}
	b := img.Bounds()
	cols, rows, pxW, pxH := FitCells(b.Dx(), b.Dy(), p, caps)
	resized := imaging.Fit(img, pxW, pxH, imaging.Lanczos)

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, resized); err != nil {
		return fmt.Errorf("terminal: iterm render: encode png: %w", err)
	}
	raw := pngBuf.Bytes()

	var seq []byte
	if len(raw) > itermMultipartThreshold {
		seq = buildITermMultipart(raw, cols, rows)
	} else {
		seq = buildITermInline(raw, cols, rows)
	}
	if caps.InTmux && caps.TmuxPassthroughOK {
		seq = tmuxPassthrough(seq)
	}

	if id != 0 {
		t.mu.Lock()
		if t.placements == nil {
			t.placements = make(map[uint32]Placement)
		}
		t.placements[id] = p
		t.mu.Unlock()
	}

	_, err := w.Write(seq)
	return err
}

// Clear erases the terminal cells that a prior Render call occupied.
//
// Unlike kitty, iTerm2's inline image protocol has no documented "delete
// this image" escape: once drawn, the image is just pixels the terminal
// chose to render over some cells, indistinguishable from any other cell
// contents. The only honest way to remove it is to overwrite the region it
// occupied, so Clear looks up the Placement recorded by the matching
// Render call (id == 0 clears every region this renderer has recorded) and
// erases each row of that region with cursor-position + erase-to-end-of-
// line. If id is unknown — never rendered, or already cleared — Clear is a
// no-op: without a remembered region there is nothing honest to erase, and
// guessing risks damaging unrelated screen content.
func (t *ITerm2) Clear(w io.Writer, id uint32) error {
	t.mu.Lock()
	var regions []Placement
	if id == 0 {
		for _, p := range t.placements {
			regions = append(regions, p)
		}
		t.placements = make(map[uint32]Placement)
	} else if p, ok := t.placements[id]; ok {
		regions = append(regions, p)
		delete(t.placements, id)
	}
	t.mu.Unlock()

	if len(regions) == 0 {
		return nil
	}

	var out bytes.Buffer
	for _, p := range regions {
		for row := 0; row < p.HeightCells; row++ {
			// CUP is 1-based; p.Row/p.Col are 0-based cell coordinates.
			fmt.Fprintf(&out, "\x1b[%d;%dH\x1b[K", p.Row+row+1, p.Col+1)
		}
	}
	seq := out.Bytes()
	if t.caps.InTmux && t.caps.TmuxPassthroughOK {
		seq = tmuxPassthrough(seq)
	}
	_, err := w.Write(seq)
	return err
}

// buildITermInline builds the single-sequence OSC 1337 File form used for
// payloads at or below itermMultipartThreshold.
func buildITermInline(raw []byte, cols, rows int) []byte {
	b64 := base64.StdEncoding.EncodeToString(raw)
	var out bytes.Buffer
	fmt.Fprintf(&out, "\x1b]1337;File=inline=1;size=%d;width=%d;height=%d;preserveAspectRatio=1:", len(raw), cols, rows)
	out.WriteString(b64)
	out.WriteByte(0x07)
	return out.Bytes()
}

// buildITermMultipart builds the MultipartFile/FilePart.../FileEnd sequence
// iTerm2 documents for large transfers: one header establishing size and
// cell dimensions, N FilePart chunks (each independently base64-encoded, so
// no chunk boundary can land mid-base64-group and corrupt decoding), and a
// FileEnd terminator.
func buildITermMultipart(raw []byte, cols, rows int) []byte {
	var out bytes.Buffer
	fmt.Fprintf(&out, "\x1b]1337;MultipartFile=inline=1;size=%d;width=%d;height=%d;preserveAspectRatio=1\x07", len(raw), cols, rows)
	for i := 0; i < len(raw); i += itermChunkSize {
		end := i + itermChunkSize
		if end > len(raw) {
			end = len(raw)
		}
		out.WriteString("\x1b]1337;FilePart=")
		out.WriteString(base64.StdEncoding.EncodeToString(raw[i:end]))
		out.WriteByte(0x07)
	}
	out.WriteString("\x1b]1337;FileEnd\x07")
	return out.Bytes()
}
