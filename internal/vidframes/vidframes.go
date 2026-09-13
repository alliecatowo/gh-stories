// Package vidframes decodes a short video into still frames so a terminal that
// supports graphics animation can play it inline.
//
// This is the optional inline-video feature. It is strictly best effort: if
// ffmpeg is missing, the video is too long, or decoding fails for any reason,
// the caller falls back to the poster frame. The product never claims motion it
// did not deliver.
package vidframes

import (
	"context"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/alliecatowo/gh-stories/internal/terminal"
)

// Options bound the decode.
type Options struct {
	// FPS is how many frames per second to extract. Terminal animation does
	// not need cinema framerates, and every frame costs bandwidth and memory.
	FPS int
	// MaxFrames caps the sequence length.
	MaxFrames int
	// MaxWidth/MaxHeight bound each frame.
	MaxWidth, MaxHeight int
	// Timeout bounds the whole decode.
	Timeout time.Duration
}

func (o *Options) setDefaults() {
	if o.FPS <= 0 {
		// Eight is plenty for a terminal: every extra frame is another
		// full-size RGB buffer written to disk and pushed through the
		// terminal, and the whole sequence has to arrive before the Story
		// item's own duration elapses.
		o.FPS = 8
	}
	if o.MaxFrames <= 0 || o.MaxFrames > terminal.MaxAnimationFrames {
		o.MaxFrames = terminal.MaxAnimationFrames
	}
	if o.MaxWidth <= 0 {
		o.MaxWidth = 480
	}
	if o.MaxHeight <= 0 {
		o.MaxHeight = 480
	}
	if o.Timeout <= 0 {
		o.Timeout = 45 * time.Second
	}
}

// Available reports whether ffmpeg is on PATH, and its path.
func Available() (string, bool) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", false
	}
	return path, true
}

// ErrUnavailable means inline video cannot be produced on this machine.
var ErrUnavailable = fmt.Errorf("vidframes: ffmpeg is not available")

// Decode extracts frames from a local video file.
//
// Subprocess safety mirrors the server-side worker: an explicit argument slice
// (never a shell string), a generated output path (never anything derived from
// user input), a hard wall-clock timeout, a minimal environment, and no network
// protocols enabled — the input is a local file this process just wrote.
func Decode(ctx context.Context, videoPath string, opts Options) ([]terminal.Frame, func(), error) {
	opts.setDefaults()
	ffmpeg, ok := Available()
	if !ok {
		return nil, func() {}, ErrUnavailable
	}

	dir, err := os.MkdirTemp("", "gh-stories-decode-")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	pattern := filepath.Join(dir, "frame-%04d.png")
	cmd := exec.CommandContext(ctx, ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error",
		// The input is a file this process controls; no network protocols.
		"-protocol_whitelist", "file",
		"-i", videoPath,
		"-vf", fmt.Sprintf("fps=%d,scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease",
			opts.FPS, opts.MaxWidth, opts.MaxHeight),
		"-frames:v", fmt.Sprint(opts.MaxFrames),
		"-an", // no audio: a terminal cannot play it
		pattern,
	)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/local/bin"}
	// Bound the diagnostic output so a chatty failure cannot exhaust memory.
	var stderr boundedBuffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("vidframes: ffmpeg failed: %w: %s", err, stderr.String())
	}

	entries, err := filepath.Glob(filepath.Join(dir, "frame-*.png"))
	if err != nil || len(entries) == 0 {
		cleanup()
		return nil, func() {}, fmt.Errorf("vidframes: no frames were produced")
	}
	sort.Strings(entries)

	gap := 1000 / opts.FPS
	frames := make([]terminal.Frame, 0, len(entries))
	for _, path := range entries {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			continue
		}
		frames = append(frames, terminal.Frame{Image: img, GapMS: gap})
	}
	if len(frames) == 0 {
		cleanup()
		return nil, func() {}, fmt.Errorf("vidframes: no frames could be decoded")
	}
	return frames, cleanup, nil
}

// boundedBuffer keeps at most 8 KiB of subprocess diagnostics.
type boundedBuffer struct{ b []byte }

func (w *boundedBuffer) Write(p []byte) (int, error) {
	const max = 8 << 10
	if len(w.b) < max {
		room := max - len(w.b)
		if room > len(p) {
			room = len(p)
		}
		w.b = append(w.b, p[:room]...)
	}
	return len(p), nil
}

func (w *boundedBuffer) String() string { return string(w.b) }
