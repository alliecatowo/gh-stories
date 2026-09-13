package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"time"

	"github.com/disintegration/imaging"
)

func checksumBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// checksumFile streams the file through SHA-256 rather than reading it fully
// into memory first, so hashing a large video variant does not double its
// memory footprint on top of whatever the OS page cache is already holding.
func checksumFile(path string) (checksum string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// fitDown resizes img so neither side exceeds maxW/maxH, preserving aspect
// ratio, but never upscales: a source already smaller than the bound is
// returned unchanged (as an *image.NRGBA copy) rather than blown up.
func fitDown(img image.Image, maxW, maxH int) *image.NRGBA {
	b := img.Bounds()
	if b.Dx() <= maxW && b.Dy() <= maxH {
		return imaging.Clone(img)
	}
	return imaging.Fit(img, maxW, maxH, imaging.Lanczos)
}

// imageHasTransparency reports whether the canonical/thumb variant should be
// encoded as PNG (to preserve an alpha channel) instead of JPEG (which has
// none). JPEG sources never carry transparency, so they short-circuit
// without a pixel scan; everything else is scanned once, bounded by the same
// decoded-pixel budget already enforced before this is called.
func imageHasTransparency(mime string, img image.Image) bool {
	if mime == "image/jpeg" {
		return false
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a != 0xffff {
				return true
			}
		}
	}
	return false
}

// encodeImage re-encodes img as PNG (if it needs an alpha channel) or JPEG
// (otherwise), returning the bytes and the chosen MIME type. This is the
// step that actually strips metadata: the encoders here only ever see pixel
// data, never the original file's EXIF/XMP/ICC/container bytes.
func encodeImage(img image.Image, hasAlpha bool) ([]byte, string, error) {
	var buf bytes.Buffer
	if hasAlpha {
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/png", nil
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/jpeg", nil
}

// scratchDir creates a fresh, uniquely-named temp directory for one
// Process call's large outputs. base overrides the OS default temp
// directory; empty means use it.
func scratchDir(base string) (string, error) {
	if base == "" {
		base = os.TempDir()
	}
	return os.MkdirTemp(base, "ghs-media-*")
}

// posterOffsetSeconds picks a moment slightly into playback (10% in, capped
// at one second) rather than frame zero, because an opening frame is
// disproportionately likely to be a fade-in or a black title card — the
// spec requirement is a real representative frame, not literally the first
// one.
func posterOffsetSeconds(durationMS int) float64 {
	if durationMS <= 0 {
		return 0
	}
	d := float64(durationMS) / 1000
	off := d * 0.1
	if off > 1 {
		off = 1
	}
	if off >= d {
		off = d / 2
	}
	return off
}

// extractPoster grabs a single real decoded frame from videoPath as a PNG.
func extractPoster(ctx context.Context, videoPath string, durationMS int, outPath string) error {
	args := []string{
		"-ss", fmtSeconds(posterOffsetSeconds(durationMS)),
		"-i", videoPath,
		"-vframes", "1",
		outPath,
	}
	return runFFmpeg(ctx, 20*time.Second, args)
}

func readPNG(path string) (data []byte, w, h int, err error) {
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	return data, cfg.Width, cfg.Height, nil
}

// posterToTerminal downsizes an already-produced poster PNG into the
// terminal-display variant, sharing exactly the same resize path images use.
func posterToTerminal(posterData []byte, limits Limits) (data []byte, w, h int, err error) {
	img, err := imaging.Decode(bytes.NewReader(posterData))
	if err != nil {
		return nil, 0, 0, err
	}
	small := fitDown(img, limits.TerminalMaxSide, limits.TerminalMaxSide)
	var buf bytes.Buffer
	if err := png.Encode(&buf, small); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), small.Bounds().Dx(), small.Bounds().Dy(), nil
}
