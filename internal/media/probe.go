package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"  // register GIF for image.DecodeConfig/Decode format sniffing
	_ "image/jpeg" // register JPEG
	_ "image/png"  // register PNG
	"os"
	"time"

	_ "golang.org/x/image/webp" // register WebP (decode-only; no encoder exists in x/image)
)

// Kind is the coarse media category derived from a sniffed MIME type.
type Kind string

const (
	KindImage Kind = "image"
	KindVideo Kind = "video"
)

// Info is what Probe determines about a file by inspecting its own bytes.
type Info struct {
	Kind Kind
	MIME string

	Width  int
	Height int

	DurationMS int
	HasAudio   bool

	// Animated and FrameCount describe an animated image container (GIF,
	// WebP) BEFORE any decision to convert it to video. FrameCount is
	// determined by a bounded structural scan (container/chunk headers
	// only), never by fully decoding every frame.
	Animated   bool
	FrameCount int

	// Rotation is a display-rotation hint (degrees, e.g. 90/180/270) read
	// from a video container's rotation metadata. It is informational; the
	// transcode step bakes rotation into pixels rather than relying on a
	// player to honor this tag.
	Rotation int
}

// sniffHeadBytes is how much of a file Sniff/Probe read to identify it. It is
// generous enough to reach the ftyp box of an MP4/MOV/WebM's EBML+DocType
// header and a GIF/WebP/PNG/JPEG signature, while remaining a fixed, tiny
// budget regardless of the file's declared or actual size.
const sniffHeadBytes = 4096

// acceptedMIMEs is the exhaustive allow-list of media types this product
// accepts. Nothing outside this set is ever processed — notably SVG is
// rejected outright (it is not a raster format decoders here can safely
// rasterize, and its XML/script surface is exactly the kind of thing a
// "treat every byte as hostile" pipeline must not parse).
var acceptedMIMEs = map[string]Kind{
	"image/jpeg": KindImage,
	"image/png":  KindImage,
	"image/webp": KindImage,
	"image/gif":  KindImage,
	"video/mp4":  KindVideo,
	"video/webm": KindVideo,
	// video/quicktime (MOV) is accepted: verified end-to-end against the
	// system ffmpeg (mov,mp4,m4a demuxer, which natively parses QuickTime
	// files) during development of this package. See package doc / worker
	// report for the exact verification performed.
	"video/quicktime": KindVideo,
}

// Sniff identifies the true media type of a file from its first bytes only,
// using each format's own file-signature magic bytes. The declared
// Content-Type and any filename/extension are NEVER consulted here or
// anywhere else in this package: both are attacker-controlled metadata, not
// evidence about what the bytes actually are.
func Sniff(head []byte) (mime string, ok bool) {
	switch {
	case len(head) >= 3 && bytes.Equal(head[:3], []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg", true

	case len(head) >= 8 && bytes.Equal(head[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png", true

	case len(head) >= 6 && (bytes.Equal(head[:6], []byte("GIF87a")) || bytes.Equal(head[:6], []byte("GIF89a"))):
		return "image/gif", true

	case len(head) >= 12 && bytes.Equal(head[:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return "image/webp", true

	case len(head) >= 4 && bytes.Equal(head[:4], []byte{0x1A, 0x45, 0xDF, 0xA3}):
		// EBML header: either Matroska or WebM. Only WebM is on the accept
		// list; distinguish by the DocType element, which appears as a plain
		// ASCII string near the start of the header within our read budget.
		if bytes.Contains(head, []byte("webm")) {
			return "video/webm", true
		}
		return "", false

	case len(head) >= 12 && bytes.Equal(head[4:8], []byte("ftyp")):
		brand := string(bytes.TrimRight(head[8:12], " "))
		switch brand {
		case "qt":
			return "video/quicktime", true
		case "isom", "iso2", "mp41", "mp42", "mp71", "M4V", "M4A", "avc1", "3gp4", "3gp5", "dash", "MSNV":
			return "video/mp4", true
		default:
			// An ISO-BMFF box we don't recognize the brand of. Reject rather
			// than guess: this keeps the accept list exhaustive instead of
			// best-effort.
			return "", false
		}
	}
	return "", false
}

// Probe inspects the file at path and returns everything Process needs to
// decide how to validate and normalize it. It reads only a small header for
// type sniffing and, for images, only the format's own header (never full
// pixel data) to learn dimensions — this is what lets a declared-huge "pixel
// bomb" be rejected before any decode is attempted. Video files are
// inspected with ffprobe, itself invoked under the same subprocess-safety
// rules as ffmpeg (see ffmpeg.go).
func Probe(ctx context.Context, path string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, errInternal("open file for probing", err)
	}
	defer f.Close()

	head := make([]byte, sniffHeadBytes)
	n, err := f.Read(head)
	if n == 0 && err != nil {
		return Info{}, errCorruptMedia("file is empty or unreadable", err)
	}
	head = head[:n]

	mime, ok := Sniff(head)
	if !ok {
		return Info{}, errUnsupportedMedia("file signature does not match any accepted media type")
	}
	kind, accepted := acceptedMIMEs[mime]
	if !accepted {
		return Info{}, errUnsupportedMedia(fmt.Sprintf("sniffed type %s is not accepted", mime))
	}

	if kind == KindImage {
		return probeImage(f, mime)
	}
	return probeVideo(ctx, path, mime)
}

func probeImage(f *os.File, mime string) (Info, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return Info{}, errInternal("seek file for probing", err)
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return Info{}, errCorruptMedia("could not parse image header", err)
	}
	info := Info{Kind: KindImage, MIME: mime, Width: cfg.Width, Height: cfg.Height}

	if _, err := f.Seek(0, 0); err != nil {
		return Info{}, errInternal("seek file for probing", err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		return Info{}, errInternal("read file for animation scan", err)
	}
	switch mime {
	case "image/gif":
		frames, err := countGIFFrames(data, DefaultLimits().MaxAnimationFrames+1)
		if err != nil {
			return Info{}, errCorruptMedia("could not parse GIF structure", err)
		}
		info.FrameCount = frames
		info.Animated = frames > 1
	case "image/webp":
		frames, err := countWebPFrames(data, DefaultLimits().MaxAnimationFrames+1)
		if err != nil {
			return Info{}, errCorruptMedia("could not parse WebP structure", err)
		}
		info.FrameCount = frames
		info.Animated = frames > 1
	}
	return info, nil
}

func probeVideo(ctx context.Context, path, mime string) (Info, error) {
	pr, err := runFFprobe(ctx, 20*time.Second, path)
	if err != nil {
		return Info{}, err
	}
	info := Info{Kind: KindVideo, MIME: mime}
	info.DurationMS = int(parseDurationSeconds(pr.Format.Duration) * 1000)
	for _, s := range pr.Streams {
		switch s.CodecType {
		case "video":
			if info.Width == 0 && info.Height == 0 {
				info.Width, info.Height = s.Width, s.Height
			}
			if s.Tags.Rotate != "" {
				var rot int
				fmt.Sscanf(s.Tags.Rotate, "%d", &rot)
				info.Rotation = rot
			}
		case "audio":
			info.HasAudio = true
		}
	}
	if info.Width == 0 || info.Height == 0 {
		return Info{}, errCorruptMedia("video has no decodable video stream", nil)
	}
	return info, nil
}

// countGIFFrames walks a GIF's block structure to count image (frame) blocks
// WITHOUT running LZW decompression on any frame's pixel data: frame data is
// stored as length-prefixed sub-blocks that can be skipped by their declared
// length alone. This bounds the cost of learning "how many frames does this
// claim to have" to a linear scan of block headers, so a hostile file cannot
// force a full animation decode just to be rejected for having too many
// frames. Scanning stops (successfully) as soon as frameLimit is reached,
// since Process only needs to know "at most frameLimit-1" to enforce the
// budget.
func countGIFFrames(data []byte, frameLimit int) (int, error) {
	if len(data) < 13 || !(bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))) {
		return 0, fmt.Errorf("not a GIF")
	}
	i := 6
	// Logical Screen Descriptor: width(2) height(2) packed(1) bg(1) aspect(1)
	if i+7 > len(data) {
		return 0, fmt.Errorf("truncated logical screen descriptor")
	}
	packed := data[i+4]
	i += 7
	if packed&0x80 != 0 {
		size := 3 * (1 << ((packed & 0x07) + 1))
		i += size
	}
	frames := 0
	for i < len(data) {
		if frames >= frameLimit {
			return frames, nil
		}
		switch data[i] {
		case 0x21: // extension
			if i+2 > len(data) {
				return 0, fmt.Errorf("truncated extension")
			}
			i += 2 // introducer + label
			var err error
			i, err = skipSubBlocks(data, i)
			if err != nil {
				return 0, err
			}
		case 0x2C: // image descriptor
			frames++
			if i+10 > len(data) {
				return 0, fmt.Errorf("truncated image descriptor")
			}
			lpacked := data[i+9]
			i += 10
			if lpacked&0x80 != 0 {
				size := 3 * (1 << ((lpacked & 0x07) + 1))
				i += size
			}
			if i >= len(data) {
				return 0, fmt.Errorf("truncated image data")
			}
			i++ // LZW minimum code size
			var err error
			i, err = skipSubBlocks(data, i)
			if err != nil {
				return 0, err
			}
		case 0x3B: // trailer
			return frames, nil
		default:
			return 0, fmt.Errorf("unexpected GIF block %#x at offset %d", data[i], i)
		}
	}
	return frames, nil
}

func skipSubBlocks(data []byte, i int) (int, error) {
	for {
		if i >= len(data) {
			return 0, fmt.Errorf("truncated sub-block sequence")
		}
		n := int(data[i])
		i++
		if n == 0 {
			return i, nil
		}
		if i+n > len(data) {
			return 0, fmt.Errorf("truncated sub-block")
		}
		i += n
	}
}

// countWebPFrames walks a WebP file's RIFF chunk structure to count ANMF
// (animation frame) chunks, again without decoding any frame's pixel data —
// each RIFF chunk declares its own byte length up front.
func countWebPFrames(data []byte, frameLimit int) (int, error) {
	if len(data) < 12 || !bytes.Equal(data[0:4], []byte("RIFF")) || !bytes.Equal(data[8:12], []byte("WEBP")) {
		return 0, fmt.Errorf("not a WebP")
	}
	riffSize := int(binary.LittleEndian.Uint32(data[4:8]))
	end := 8 + riffSize
	if end > len(data) || end < 12 {
		end = len(data)
	}
	frames := 0
	off := 12
	for off+8 <= end {
		fourCC := data[off : off+4]
		size := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		if size < 0 || off+8+size > len(data) {
			return 0, fmt.Errorf("truncated WebP chunk")
		}
		if bytes.Equal(fourCC, []byte("ANMF")) {
			frames++
			if frames >= frameLimit {
				return frames, nil
			}
		}
		off += 8 + size
		if size%2 == 1 {
			off++ // RIFF chunks are padded to an even length
		}
	}
	if frames == 0 {
		return 1, nil // static WebP: exactly one implicit "frame"
	}
	return frames, nil
}
