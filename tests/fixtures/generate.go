//go:build ignore

// Command generate produces the sample media committed under
// tests/fixtures/. Every pixel and every audio sample is synthesized by this
// program (Go's own image/gif/jpeg/png encoders, plus ffmpeg's built-in
// `testsrc2`/`sine` lavfi test-pattern generators) — nothing here is
// downloaded, screenshotted, or copied from any third-party source. See
// tests/fixtures/README.md for the provenance statement this program keeps
// true.
//
// Regenerate with:
//
//	go run tests/fixtures/generate.go
package main

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	dir := "tests/fixtures"
	must(os.MkdirAll(dir, 0o755))

	must(writeJPEG(filepath.Join(dir, "portrait.jpg"), pattern(1080, 1920), 90))
	must(writePNG(filepath.Join(dir, "landscape.png"), pattern(1920, 1080)))
	must(writePNG(filepath.Join(dir, "transparent.png"), transparentPattern(600, 600)))
	must(writePNG(filepath.Join(dir, "tall.png"), pattern(600, 4000)))
	must(writeAnimatedGIF(filepath.Join(dir, "animated.gif"), 12))

	must(writeWebP(filepath.Join(dir, "square.webp"), pattern(800, 800)))
	must(ffmpegLavfi(filepath.Join(dir, "sample.mp4"),
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30:duration=3",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest"))
	must(ffmpegLavfi(filepath.Join(dir, "sample.webm"),
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30:duration=3",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3",
		"-c:v", "libvpx-vp9", "-c:a", "libopus", "-shortest"))

	must(writeFakePNG(filepath.Join(dir, "fake.png")))
	must(writeBombPNG(filepath.Join(dir, "bomb.png")))
	must(writeTruncatedJPEG(filepath.Join(dir, "truncated.jpg")))
	must(os.WriteFile(filepath.Join(dir, "not-media.bin"),
		[]byte("this is not a media file, just arbitrary bytes: \x00\x01\x02\x03\xff\xfe hello\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "evil-caption.txt"), evilCaption(), 0o644))

	fmt.Println("fixtures generated in", dir)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "generate:", err)
		os.Exit(1)
	}
}

// pattern renders a deterministic, colorful, non-uniform image: a diagonal
// gradient plus a contrasting band across the top third. The top band makes
// orientation mistakes in ad hoc testing obvious at a glance, and the
// non-uniform pixels make "is this frame just solid black" assertions
// meaningful for anything derived from it.
func pattern(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r := uint8(255 * x / max(w-1, 1))
			g := uint8(255 * y / max(h-1, 1))
			b := uint8(128 + 127*math.Sin(float64(x+y)/40))
			if y < h/3 {
				img.Set(x, y, color.RGBA{R: 220, G: 60, B: 60, A: 255})
			} else {
				img.Set(x, y, color.RGBA{R: r, G: g, B: uint8(b), A: 255})
			}
		}
	}
	return img
}

func transparentPattern(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(255 * x / max(w-1, 1))
			img.Set(x, y, color.NRGBA{R: 60, G: 140, B: 220, A: a})
		}
	}
	return img
}

func writeJPEG(path string, img image.Image, quality int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, img, &jpeg.Options{Quality: quality})
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// writeAnimatedGIF builds a valid multi-frame animated GIF entirely with the
// standard library. Each frame is a checkerboard of blocks cycling through a
// small palette, phase-shifted by the frame index: every individual frame is
// internally non-uniform (so "is this frame just one flat color" assertions
// are meaningful against any single extracted frame), and the phase shift
// makes the animation itself visibly change frame to frame.
func writeAnimatedGIF(path string, frames int) error {
	palette := []color.Color{
		color.RGBA{220, 60, 60, 255}, color.RGBA{60, 200, 80, 255},
		color.RGBA{60, 100, 220, 255}, color.RGBA{230, 200, 40, 255},
		color.Black, color.White,
	}
	const w, h, block = 240, 240, 40
	g := &gif.GIF{}
	for i := 0; i < frames; i++ {
		img := image.NewPaletted(image.Rect(0, 0, w, h), palette)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				idx := (x/block + y/block + i) % len(palette)
				img.SetColorIndex(x, y, uint8(idx))
			}
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 10) // 10 * 10ms = 100ms per frame
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return gif.EncodeAll(f, g)
}

// writeWebP encodes img as WebP via the system `cwebp` tool (part of
// Google's libwebp, e.g. `brew install webp`): Go's standard library and
// golang.org/x/image can only decode WebP, not encode it, and this repo's
// go.mod deliberately has no third-party WebP encoder dependency. The pixels
// are still entirely our own; only the container encoding step is external.
func writeWebP(path string, img image.Image) error {
	tmpPNG := path + ".tmp.png"
	if err := writePNG(tmpPNG, img); err != nil {
		return err
	}
	defer os.Remove(tmpPNG)
	cmd := exec.Command("cwebp", "-quiet", tmpPNG, "-o", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cwebp: %w\n%s", err, out)
	}
	return nil
}

// ffmpegLavfi renders args (which must produce exactly one output stream set)
// through the system ffmpeg into path, using only built-in lavfi test-pattern
// and test-tone sources — never a network URL, a real photo, or third-party
// footage.
func ffmpegLavfi(path string, args ...string) error {
	full := append([]string{"-y", "-hide_banner", "-loglevel", "error"}, args...)
	full = append(full, path)
	cmd := exec.Command("ffmpeg", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg %v: %w\n%s", args, err, out)
	}
	return nil
}

// writeFakePNG writes real JPEG bytes to a file named fake.png, to test that
// type detection is based on file signature, never on the extension or a
// declared Content-Type.
func writeFakePNG(path string) error {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, pattern(64, 64), &jpeg.Options{Quality: 80}); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// writeBombPNG hand-builds a minimal, structurally valid PNG whose IHDR
// chunk declares an enormous image (30000x30000 = 900 million pixels) while
// the file itself is only a few hundred bytes. A correct pipeline must
// reject this from the declared header alone, without ever attempting to
// inflate pixel data.
func writeBombPNG(path string) error {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})

	writeChunk(&buf, "IHDR", func() []byte {
		b := make([]byte, 13)
		putU32(b[0:4], 30000)
		putU32(b[4:8], 30000)
		b[8] = 8  // bit depth
		b[9] = 2  // color type: truecolor
		b[10] = 0 // compression
		b[11] = 0 // filter
		b[12] = 0 // interlace
		return b
	}())

	// A tiny, structurally-present but otherwise meaningless IDAT: a
	// conforming decoder that actually attempted to honor the declared
	// dimensions would fail trying to inflate this, which is exactly why the
	// pipeline must never reach that point for a file like this.
	writeChunk(&buf, "IDAT", []byte{0x78, 0x9c, 0x03, 0x00, 0x00, 0x00, 0x00, 0x01})
	writeChunk(&buf, "IEND", nil)

	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func writeChunk(buf *bytes.Buffer, typ string, data []byte) {
	var lenB [4]byte
	putU32(lenB[:], uint32(len(data)))
	buf.Write(lenB[:])
	body := append([]byte(typ), data...)
	buf.Write(body)
	var crcB [4]byte
	putU32(crcB[:], crc32.ChecksumIEEE(body))
	buf.Write(crcB[:])
}

func putU32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}

// writeTruncatedJPEG writes only the first half of a valid JPEG's bytes, to
// simulate an upload that was cut off mid-transfer.
func writeTruncatedJPEG(path string) error {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, pattern(400, 400), &jpeg.Options{Quality: 90}); err != nil {
		return err
	}
	full := buf.Bytes()
	return os.WriteFile(path, full[:len(full)/2], 0o644)
}

// evilCaption contains terminal escape sequences a hostile caption or reply
// body might carry: a title-bar OSC injection, a screen clear, an OSC 52
// clipboard write attempt, and raw ANSI color/cursor codes. It exists so
// terminal rendering code can be tested against it; this package's own
// pipeline never interprets caption text at all.
func evilCaption() []byte {
	return []byte("Look at this\x1b]0;pwned\x07\x1b[2J\x1b[H" +
		"\x1b]52;c;cHVibGljIHBhc3RlYm9hcmQgaGlqYWNr\x07" +
		"\x1b[31mred\x1b[0m\x1b[8m(hidden)\x1b[0m\ntrailing line\r\n")
}
