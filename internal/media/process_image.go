package media

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/disintegration/imaging"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

func processImage(ctx context.Context, path string, info Info, opts Options, limits Limits) (Output, error) {
	if info.Width > limits.MaxImageSide || info.Height > limits.MaxImageSide {
		return Output{}, errTooManyPixels(fmt.Sprintf(
			"image is %dx%d, a single side may not exceed %dpx", info.Width, info.Height, limits.MaxImageSide))
	}
	pixels := int64(info.Width) * int64(info.Height)
	if pixels > limits.MaxImagePixels {
		return Output{}, errTooManyPixels(fmt.Sprintf(
			"image is %d pixels, limit is %d — rejected before decode", pixels, limits.MaxImagePixels))
	}

	if info.Animated {
		if info.FrameCount > limits.MaxAnimationFrames {
			return Output{}, errTooManyPixels(fmt.Sprintf(
				"animation has %d frames, limit is %d", info.FrameCount, limits.MaxAnimationFrames))
		}
		return processAnimatedImage(ctx, path, info, opts, limits)
	}

	f, err := os.Open(path)
	if err != nil {
		return Output{}, errInternal("open image", err)
	}
	defer f.Close()

	// imaging.Decode with AutoOrientation reads the EXIF orientation tag (if
	// any) itself and applies the corresponding rotation/flip to the decoded
	// pixels before returning — this is what "normalize orientation by
	// actually rotating pixels, then drop the tag" means in practice: the
	// tag is consulted once, here, and the image returned to every
	// subsequent step already has it baked in and nowhere left to read it
	// from, because none of our encoders ever write EXIF back out.
	img, err := imaging.Decode(f, imaging.AutoOrientation(true))
	if err != nil {
		return Output{}, errCorruptMedia("could not decode image", err)
	}

	hasAlpha := imageHasTransparency(info.MIME, img)

	canonical := fitDown(img, limits.CanonicalImageMaxSide, limits.CanonicalImageMaxSide)
	canonicalData, canonicalMIME, err := encodeImage(canonical, hasAlpha)
	if err != nil {
		return Output{}, errInternal("encode canonical image", err)
	}

	thumb := fitDown(img, limits.ThumbMaxSide, limits.ThumbMaxSide)
	thumbData, thumbMIME, err := encodeImage(thumb, hasAlpha)
	if err != nil {
		return Output{}, errInternal("encode thumb image", err)
	}

	terminal := fitDown(img, limits.TerminalMaxSide, limits.TerminalMaxSide)
	var terminalBuf bytes.Buffer
	if err := png.Encode(&terminalBuf, terminal); err != nil {
		return Output{}, errInternal("encode terminal image", err)
	}

	return Output{Variants: []Variant{
		{
			Kind: domain.VariantImage, Data: canonicalData, MIME: canonicalMIME,
			Width: canonical.Bounds().Dx(), Height: canonical.Bounds().Dy(),
			ByteSize: int64(len(canonicalData)), Checksum: checksumBytes(canonicalData),
		},
		{
			Kind: domain.VariantThumb, Data: thumbData, MIME: thumbMIME,
			Width: thumb.Bounds().Dx(), Height: thumb.Bounds().Dy(),
			ByteSize: int64(len(thumbData)), Checksum: checksumBytes(thumbData),
		},
		{
			Kind: domain.VariantTerminal, Data: terminalBuf.Bytes(), MIME: "image/png",
			Width: terminal.Bounds().Dx(), Height: terminal.Bounds().Dy(),
			ByteSize: int64(terminalBuf.Len()), Checksum: checksumBytes(terminalBuf.Bytes()),
		},
	}}, nil
}

// processAnimatedImage converts an animated GIF/WebP into an MP4 video
// variant instead of silently flattening it to a single still. ffmpeg's
// gif/webp demuxers decode the animation's own per-frame timing, so the
// output plays back at the source's speed rather than a guessed frame rate.
func processAnimatedImage(ctx context.Context, path string, info Info, opts Options, limits Limits) (Output, error) {
	if _, ok := FFmpegAvailable(); !ok {
		return Output{}, errFFmpegUnavailable("ffmpeg is required to preserve this animation as video")
	}

	tempDir, err := scratchDir(opts.TempDir)
	if err != nil {
		return Output{}, errInternal("create scratch dir", err)
	}
	videoPath := filepath.Join(tempDir, "video.mp4")

	args := []string{
		"-i", path,
		"-an", // animated stills never carry audio
		"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "veryfast",
		"-t", fmt.Sprintf("%d", limits.MaxVideoSeconds),
		"-max_muxing_queue_size", "1024",
		"-movflags", "+faststart",
		videoPath,
	}
	if err := runFFmpeg(ctx, 90*time.Second, args); err != nil {
		_ = os.RemoveAll(tempDir)
		return Output{}, err
	}

	outInfo, err := runFFprobe(ctx, 20*time.Second, videoPath)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return Output{}, err
	}
	outDurMS := int(parseDurationSeconds(outInfo.Format.Duration) * 1000)
	var outW, outH int
	for _, s := range outInfo.Streams {
		if s.CodecType == "video" {
			outW, outH = s.Width, s.Height
			break
		}
	}

	checksum, size, err := checksumFile(videoPath)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return Output{}, errInternal("checksum video output", err)
	}

	posterPath := filepath.Join(tempDir, "poster.png")
	if err := extractPoster(ctx, videoPath, outDurMS, posterPath); err != nil {
		_ = os.RemoveAll(tempDir)
		return Output{}, err
	}
	posterData, posterW, posterH, err := readPNG(posterPath)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return Output{}, errInternal("read poster", err)
	}
	terminalData, termW, termH, err := posterToTerminal(posterData, limits)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return Output{}, errInternal("build terminal still from poster", err)
	}

	return Output{Variants: []Variant{
		{
			Kind: domain.VariantVideo, TempPath: videoPath, MIME: "video/mp4",
			Width: outW, Height: outH, DurationMS: outDurMS, ByteSize: size, Checksum: checksum,
		},
		{
			Kind: domain.VariantPoster, Data: posterData, MIME: "image/png",
			Width: posterW, Height: posterH, ByteSize: int64(len(posterData)), Checksum: checksumBytes(posterData),
		},
		{
			Kind: domain.VariantTerminal, Data: terminalData, MIME: "image/png",
			Width: termW, Height: termH, ByteSize: int64(len(terminalData)), Checksum: checksumBytes(terminalData),
		},
	}}, nil
}
