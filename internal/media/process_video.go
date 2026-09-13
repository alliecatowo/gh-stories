package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

// processVideo transcodes an uploaded video into the canonical H.264/AAC MP4
// variant plus a poster and a terminal-display still. See ffmpeg.go for the
// subprocess-safety rules every invocation here follows.
func processVideo(ctx context.Context, path string, info Info, opts Options, limits Limits) (Output, error) {
	if _, ok := FFmpegAvailable(); !ok {
		return Output{}, errFFmpegUnavailable("ffmpeg is not installed on this worker")
	}
	if info.Width > limits.MaxImageSide || info.Height > limits.MaxImageSide {
		return Output{}, errTooManyPixels(fmt.Sprintf(
			"video frame is %dx%d, a single side may not exceed %dpx", info.Width, info.Height, limits.MaxImageSide))
	}

	startMS, endMS := opts.StartMS, opts.EndMS
	durationMS := info.DurationMS
	switch {
	case endMS > startMS:
		durationMS = endMS - startMS
	case startMS > 0:
		durationMS = info.DurationMS - startMS
	}
	if durationMS <= 0 {
		return Output{}, errCorruptMedia("requested trim produces zero or negative duration", nil)
	}
	if durationMS > limits.MaxVideoSeconds*1000 {
		return Output{}, errTooLong(fmt.Sprintf(
			"video is %dms, limit is %dms", durationMS, limits.MaxVideoSeconds*1000))
	}

	tempDir, err := scratchDir(opts.TempDir)
	if err != nil {
		return Output{}, errInternal("create scratch dir", err)
	}
	videoPath := filepath.Join(tempDir, "video.mp4")

	var args []string
	if startMS > 0 {
		// -ss before -i for fast (keyframe-seek) trimming: at our duration
		// cap of a minute, exact-frame accuracy is not worth the extra
		// decode cost of seeking after -i.
		args = append(args, "-ss", fmtSeconds(float64(startMS)/1000))
	}
	args = append(args, "-i", path)
	args = append(args, "-t", fmtSeconds(float64(durationMS)/1000))
	// Even dimensions are required by yuv420p (4:2:0 chroma subsampling
	// needs both dimensions divisible by 2); odd source dimensions would
	// otherwise make libx264 refuse the encode outright.
	args = append(args, "-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2")
	args = append(args, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "veryfast")

	mute := opts.Muted || !info.HasAudio
	if mute {
		args = append(args, "-an")
	} else {
		// Single-pass EBU R128 loudness normalization to a conventional -16
		// LUFS target: good enough to stop wildly hot or quiet source audio
		// from dominating a feed of many stories, without the extra
		// processing time of a two-pass measure-then-normalize run.
		args = append(args, "-c:a", "aac", "-b:a", "128k", "-af", "loudnorm=I=-16:TP=-1.5:LRA=11")
	}
	// moov atom first, so a browser/CLI can start playback before the whole
	// file has downloaded.
	args = append(args, "-movflags", "+faststart", "-max_muxing_queue_size", "1024", videoPath)

	if err := runFFmpeg(ctx, 2*time.Minute, args); err != nil {
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
	var outHasAudio bool
	for _, s := range outInfo.Streams {
		switch s.CodecType {
		case "video":
			if outW == 0 {
				outW, outH = s.Width, s.Height
			}
		case "audio":
			outHasAudio = true
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
			Width: outW, Height: outH, DurationMS: outDurMS, ByteSize: size,
			Checksum: checksum, HasAudio: outHasAudio,
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
