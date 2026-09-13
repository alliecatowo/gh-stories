package terminal

import (
	"encoding/base64"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/disintegration/imaging"
)

// Frame is one decoded frame and how long it should stay on screen.
type Frame struct {
	Image image.Image
	// GapMS is the delay before the next frame. Zero means a sensible default.
	GapMS int
}

// Animator plays a decoded frame sequence inline.
//
// This is deliberately a separate interface from Renderer: a terminal can be
// perfectly capable of drawing a still image and have no animation support at
// all, and this product must never advertise motion it cannot deliver. Callers
// check SupportsAnimation first and fall back to the poster frame otherwise.
type Animator interface {
	// Animate transmits every frame as one animated image and starts playback.
	Animate(w io.Writer, frames []Frame, p Placement, caps Capabilities) (Animation, error)
	// SupportsAnimation reports whether this terminal can actually animate.
	SupportsAnimation(caps Capabilities) bool
}

// Animation is a handle to something currently playing.
type Animation struct {
	// Number is the kitty image NUMBER the animation was registered under.
	Number uint32
	// tempDir holds the frame files; Stop removes it.
	tempDir string
}

// Stop halts playback, deletes the image from the terminal, and removes the
// frame files from disk. Those files are decoded frames of someone's private
// Story, so they are not left lying around.
func (a Animation) Stop(w io.Writer, caps Capabilities) error {
	seq := []byte(fmt.Sprintf("\x1b_Ga=a,s=1,I=%d,q=2\x1b\\\x1b_Ga=d,d=I,I=%d,q=2\x1b\\",
		a.Number, a.Number))
	if caps.InTmux && caps.TmuxPassthroughOK {
		seq = tmuxPassthrough(seq)
	}
	_, err := w.Write(seq)
	if a.tempDir != "" {
		_ = os.RemoveAll(a.tempDir)
	}
	return err
}

// Limits on a single animation.
//
// Frames are transferred as raw RGB, so a sequence costs width*height*3 bytes
// per frame on disk. These bounds keep a 60-second video from turning into
// hundreds of megabytes of temp files and a stalled terminal.
const (
	MaxAnimationFrames = 150
	MaxAnimationWidth  = 720
	MaxAnimationHeight = 720
	DefaultGapMS       = 100
)

// animationQuiet is the q= value used on animation escapes. q=2 suppresses
// both OK and ERROR replies, which is right in production (a stray reply would
// land in the TUI's input stream) but hides protocol mistakes. Setting
// GHS_DEBUG_ANIMATION=1 switches errors back on so they can be read.
func animationQuiet() string {
	if os.Getenv("GHS_DEBUG_ANIMATION") != "" {
		return "q=0"
	}
	return "q=2"
}

// animationNumber hands out distinct kitty image numbers per process.
var animationNumber atomic.Uint32

func nextAnimationNumber() uint32 {
	// Start high to avoid colliding with the still-image ids the viewer uses.
	return 2_000_000 + animationNumber.Add(1)
}

// SupportsAnimation reports whether kitty graphics animation is usable here.
//
// It requires local file access: frames are handed to the terminal as files,
// because that is the only transfer mode under which kitty reliably appends
// frames (chunked inline transmission was verified against kitty 0.43.1 to
// rewrite frame 2 over and over instead of creating new frames). A terminal on
// the far side of an SSH connection cannot read this machine's files, so
// animation is deliberately unavailable over SSH and the caller falls back to
// the poster frame.
func (k *Kitty) SupportsAnimation(caps Capabilities) bool {
	return k.Supported(caps) && !caps.OverSSH
}

// Animate implements kitty's animation protocol.
//
// The exact shape was derived by capturing what kitty's own `icat` emits for an
// animated GIF, because the obvious reading of the specification does not work:
//
//	a=T,f=24,t=f,I=<number>,C=1   root frame, addressed by image NUMBER
//	a=a,v=1,r=1,I=<number>,z=<ms> set frame 1's gap
//	a=f,f=24,t=f,c=<prev>,I=…     append a frame, based on the previous one
//	a=a,s=2,…                     "running, more frames still loading"
//	a=a,s=3,v=0,I=<number>        run, looping forever
//
// Two details matter and are easy to get wrong: the image must be addressed by
// NUMBER (`I`) rather than id (`i`), and each frame's pixels must arrive
// atomically as a file (`t=f`) rather than as a chunked inline payload.
func (k *Kitty) Animate(w io.Writer, frames []Frame, p Placement, caps Capabilities) (Animation, error) {
	if len(frames) == 0 || frames[0].Image == nil {
		return Animation{}, fmt.Errorf("terminal: kitty animate: no frames")
	}
	if len(frames) > MaxAnimationFrames {
		frames = frames[:MaxAnimationFrames]
	}

	b := frames[0].Image.Bounds()
	cols, rows, pxW, pxH := FitCells(b.Dx(), b.Dy(), p, caps)
	if pxW > MaxAnimationWidth {
		pxW = MaxAnimationWidth
	}
	if pxH > MaxAnimationHeight {
		pxH = MaxAnimationHeight
	}

	// Every frame must be exactly the same size or kitty rejects it.
	base := imaging.Fit(frames[0].Image, pxW, pxH, imaging.Lanczos)
	width, height := base.Bounds().Dx(), base.Bounds().Dy()
	if width <= 0 || height <= 0 {
		return Animation{}, fmt.Errorf("terminal: kitty animate: invalid frame size %dx%d", width, height)
	}

	// 0700 because these files are decoded frames of private media.
	dir, err := os.MkdirTemp("", "gh-stories-frames-")
	if err != nil {
		return Animation{}, fmt.Errorf("terminal: kitty animate: temp dir: %w", err)
	}
	anim := Animation{Number: nextAnimationNumber(), tempDir: dir}

	emit := func(ctrl, payload string) error {
		seq := []byte(fmt.Sprintf("\x1b_G%s;%s\x1b\\", ctrl, payload))
		if caps.InTmux && caps.TmuxPassthroughOK {
			seq = tmuxPassthrough(seq)
		}
		_, err := w.Write(seq)
		return err
	}

	writeFrame := func(idx int, img image.Image) (string, error) {
		path := filepath.Join(dir, fmt.Sprintf("f%04d.rgb", idx))
		if err := os.WriteFile(path, rgbPixels(img), 0o600); err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString([]byte(path)), nil
	}

	gapOf := func(f Frame) int {
		if f.GapMS > 0 {
			return f.GapMS
		}
		return DefaultGapMS
	}

	rootPath, err := writeFrame(1, base)
	if err != nil {
		anim.cleanup()
		return Animation{}, err
	}
	if err := emit(fmt.Sprintf("a=T,f=24,t=f,s=%d,v=%d,c=%d,r=%d,I=%d,C=1,%s",
		width, height, cols, rows, anim.Number, animationQuiet()), rootPath); err != nil {
		anim.cleanup()
		return Animation{}, err
	}
	if err := emit(fmt.Sprintf("a=a,v=1,r=1,I=%d,z=%d,%s",
		anim.Number, gapOf(frames[0]), animationQuiet()), ""); err != nil {
		anim.cleanup()
		return Animation{}, err
	}

	for i := 1; i < len(frames); i++ {
		if frames[i].Image == nil {
			continue
		}
		resized := imaging.Resize(frames[i].Image, width, height, imaging.Lanczos)
		path, err := writeFrame(i+1, resized)
		if err != nil {
			anim.cleanup()
			return Animation{}, err
		}
		// c=<previous frame> chains each new frame onto the one before it.
		if err := emit(fmt.Sprintf("a=f,f=24,t=f,s=%d,v=%d,c=%d,I=%d,z=%d,%s",
			width, height, i, anim.Number, gapOf(frames[i]), animationQuiet()), path); err != nil {
			anim.cleanup()
			return Animation{}, err
		}
		if i == 1 {
			// "Running, but more frames are still arriving" — this is what
			// starts playback without waiting for the whole sequence.
			if err := emit(fmt.Sprintf("a=a,s=2,v=1,r=1,I=%d,z=%d,%s",
				anim.Number, gapOf(frames[0]), animationQuiet()), ""); err != nil {
				anim.cleanup()
				return Animation{}, err
			}
		}
	}

	// s=3 run, v=0 loop forever.
	if err := emit(fmt.Sprintf("a=a,s=3,v=0,I=%d,%s", anim.Number, animationQuiet()), ""); err != nil {
		anim.cleanup()
		return Animation{}, err
	}
	return anim, nil
}

func (a Animation) cleanup() {
	if a.tempDir != "" {
		_ = os.RemoveAll(a.tempDir)
	}
}

// rgbPixels returns 3-bytes-per-pixel data for kitty's f=24 format.
func rgbPixels(img image.Image) []byte {
	b := img.Bounds()
	out := make([]byte, 0, b.Dx()*b.Dy()*3)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bb, _ := img.At(x, y).RGBA()
			out = append(out, byte(r>>8), byte(g>>8), byte(bb>>8))
		}
	}
	return out
}

// ErrAnimationUnsupported is returned by renderers that cannot animate, so a
// caller can fall back to the poster frame deliberately rather than by
// accident.
var ErrAnimationUnsupported = fmt.Errorf("terminal: this renderer cannot play inline animation")

// iTerm2's inline-image protocol has no frame concept, and the external
// fallback draws no pixels at all. Both say so rather than silently showing a
// still image and calling it video.
func (i *ITerm2) SupportsAnimation(Capabilities) bool { return false }

func (i *ITerm2) Animate(io.Writer, []Frame, Placement, Capabilities) (Animation, error) {
	return Animation{}, ErrAnimationUnsupported
}

func (e *External) SupportsAnimation(Capabilities) bool { return false }

func (e *External) Animate(io.Writer, []Frame, Placement, Capabilities) (Animation, error) {
	return Animation{}, ErrAnimationUnsupported
}

// AnimatorFor returns an Animator for caps, or nil when nothing here can
// animate.
func AnimatorFor(caps Capabilities) Animator {
	a, ok := New(caps).(Animator)
	if ok && a.SupportsAnimation(caps) {
		return a
	}
	return nil
}
