# Test fixtures

Every file in this directory is synthetic and license-free: it was generated
by `generate.go` (a `//go:build ignore` program, not part of any build), not
downloaded, screenshotted, or copied from any third-party source.

Regenerate with:

```
go run tests/fixtures/generate.go
```

Pixel and audio data is produced by:

- Go's standard library `image`, `image/jpeg`, `image/png`, `image/gif`
  encoders, from procedurally generated gradient/color-band patterns defined
  in `generate.go`.
- `cwebp` (Google's libwebp CLI, `brew install webp`) to encode `square.webp`
  from a generated PNG — Go's standard library and `golang.org/x/image` can
  only *decode* WebP, and this repository intentionally carries no
  third-party WebP encoder dependency in `go.mod`.
- ffmpeg's built-in `testsrc2` (video) and `sine` (audio) lavfi test-pattern
  generators, for `sample.mp4` and `sample.webm` — no external footage or
  recordings are involved.

## Well-formed fixtures

| File               | Contents                                              |
|--------------------|--------------------------------------------------------|
| `portrait.jpg`     | 1080x1920 JPEG                                         |
| `landscape.png`    | 1920x1080 PNG                                          |
| `square.webp`      | 800x800 WebP (VP8)                                     |
| `transparent.png`  | 600x600 PNG with an alpha gradient                     |
| `tall.png`         | 600x4000 PNG                                           |
| `animated.gif`     | 240x240 animated GIF, 12 frames, cycling solid colors  |
| `sample.mp4`       | 640x360 H.264/AAC MP4, 3s, with a 440Hz tone            |
| `sample.webm`      | 640x360 VP9/Opus WebM, 3s, with a 440Hz tone            |

## Hostile fixtures

These exist to exercise `internal/media`'s "treat every byte as hostile"
validation, and must never be processed as if they were what their name or
extension suggests.

| File                | What it actually is                                                        |
|---------------------|------------------------------------------------------------------------------|
| `fake.png`           | A real JPEG file, saved with a `.png` name — tests that type detection is based on file signature, never on extension or declared Content-Type. |
| `bomb.png`           | A hand-built, structurally valid PNG whose IHDR chunk declares a 30000x30000 (900 megapixel) image, backed by a file of only ~65 bytes — tests that oversized dimensions are rejected from the header alone, before any pixel data is decoded. |
| `truncated.jpg`      | The first half of a valid JPEG's bytes — simulates an upload cut off mid-transfer. |
| `not-media.bin`      | Arbitrary non-media bytes with no recognizable file signature.             |
| `evil-caption.txt`   | Plain text containing terminal escape sequences (title-bar OSC injection, screen clear, an OSC 52 clipboard-write attempt, ANSI color/cursor codes) — for testing terminal-rendering sanitization elsewhere in the codebase. `internal/media` does not process caption text at all. |
