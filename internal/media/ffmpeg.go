package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// This file is the only place in the product that shells out to an external
// binary, and it exists to do so safely against a hostile, untrusted input
// file:
//
//   - Every invocation uses exec.CommandContext with an explicit argument
//     slice. Nothing here ever builds a shell command string or passes
//     anything through /bin/sh, so there is no argument-injection surface
//     regardless of what a filename or caption contains.
//   - The input is always a path to a file this package itself wrote to a
//     generated temp name (see process.go); a caller-supplied filename is
//     never placed on disk under its own name and never appears in an
//     argument.
//   - -protocol_whitelist file forbids ffmpeg from opening anything but a
//     local file, so a crafted input that references a network URL (e.g. an
//     HLS playlist pointing at an internal service) cannot make ffmpeg issue
//     outbound requests. There is no remote-URL ingestion in this product.
//   - cmd.Env is a minimal, fixed environment, not the worker process's own
//     environment — the worker's environment carries database and object
//     storage credentials that a subprocess spawned to parse a hostile file
//     has no business seeing.
//   - stdout/stderr are captured into a fixed-size ring so a chatty failure
//     (or a crafted input designed to make ffmpeg log megabytes of
//     diagnostics) cannot exhaust worker memory.
//   - A context deadline plus cmd.WaitDelay bounds wall-clock time; on
//     timeout the process is killed rather than left to run indefinitely.

var (
	ffmpegOnce sync.Once
	ffmpegPath string
	ffmpegVer  string
	ffmpegOK   bool

	ffprobeOnce sync.Once
	ffprobePath string
	ffprobeOK   bool
)

// FFmpegAvailable reports whether ffmpeg is installed on PATH, and its
// reported version string. Video processing depends on it; image processing
// never does, so a worker with no ffmpeg still serves photos correctly and
// fails video jobs with the stable ffmpeg_unavailable code instead of
// crashing.
func FFmpegAvailable() (string, bool) {
	ffmpegOnce.Do(func() {
		p, err := exec.LookPath("ffmpeg")
		if err != nil {
			return
		}
		out, err := exec.Command(p, "-version").Output()
		if err != nil {
			return
		}
		ffmpegPath = p
		ffmpegVer = firstLine(string(out))
		ffmpegOK = true
	})
	return ffmpegVer, ffmpegOK
}

func ffprobeAvailable() bool {
	ffprobeOnce.Do(func() {
		p, err := exec.LookPath("ffprobe")
		if err != nil {
			return
		}
		ffprobePath = p
		ffprobeOK = true
	})
	return ffprobeOK
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

// capturedOutputLimit bounds how much of a subprocess's stdout/stderr this
// package will hold in memory, regardless of how much the process writes.
const capturedOutputLimit = 64 * 1024

// ringBuffer is an io.Writer that keeps only the last capturedOutputLimit
// bytes written to it, so a subprocess that produces unbounded diagnostic
// output cannot be used to exhaust worker memory. It always reports a full,
// successful write to the subprocess (never applies backpressure or errors),
// which matches how os/exec expects an output sink to behave.
type ringBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.buf.Write(p)
	if r.buf.Len() > r.limit {
		excess := r.buf.Len() - r.limit
		b := r.buf.Bytes()
		copy(b, b[excess:])
		r.buf.Truncate(r.limit)
	}
	return len(p), nil
}

func (r *ringBuffer) String() string { return r.buf.String() }

// runFFmpeg runs ffmpeg with args appended after a fixed set of safety flags,
// bounded by timeout. args must not include "ffmpeg" itself or any of the
// global safety flags already applied here.
func runFFmpeg(ctx context.Context, timeout time.Duration, args []string) error {
	if _, ok := FFmpegAvailable(); !ok {
		return errFFmpegUnavailable("ffmpeg is not installed on this worker")
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	full := make([]string, 0, len(args)+6)
	full = append(full,
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", "file",
	)
	full = append(full, args...)

	cmd := exec.CommandContext(cctx, ffmpegPath, full...)
	cmd.Env = []string{"HOME=/nonexistent"}
	cmd.WaitDelay = 5 * time.Second // bound time spent waiting after a kill
	var stderr ringBuffer
	stderr.limit = capturedOutputLimit
	cmd.Stdout = nil
	cmd.Stderr = &stderr

	err := cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return errProcessingTimeout("ffmpeg exceeded its processing time budget", cctx.Err())
	}
	if err != nil {
		return errCorruptMedia("ffmpeg failed: "+stderr.String(), err)
	}
	return nil
}

// probeResult is the subset of `ffprobe -show_format -show_streams -of json`
// output this package needs.
type probeResult struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		// Rotate/DisplayMatrix rotation may appear as a stream tag or as
		// side_data; ffprobe reports the tag form as a string.
		Tags struct {
			Rotate string `json:"rotate"`
		} `json:"tags"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// runFFprobe inspects a local file and returns its stream/format metadata.
// It never opens anything but the given local path (-protocol_whitelist
// file), and its output is bounded the same way ffmpeg's is.
func runFFprobe(ctx context.Context, timeout time.Duration, path string) (probeResult, error) {
	var pr probeResult
	if !ffprobeAvailable() {
		return pr, errFFmpegUnavailable("ffprobe is not installed on this worker")
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", "file",
		"-print_format", "json",
		"-show_format", "-show_streams",
		path,
	}
	cmd := exec.CommandContext(cctx, ffprobePath, args...)
	cmd.Env = []string{"HOME=/nonexistent"}
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr ringBuffer
	stdout.limit = capturedOutputLimit
	stderr.limit = capturedOutputLimit
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return pr, errProcessingTimeout("ffprobe exceeded its processing time budget", cctx.Err())
	}
	if err != nil {
		return pr, errCorruptMedia("ffprobe failed: "+stderr.String(), err)
	}
	if err := json.Unmarshal(stdout.buf.Bytes(), &pr); err != nil {
		return pr, errCorruptMedia("ffprobe produced unparseable output", err)
	}
	return pr, nil
}

func parseDurationSeconds(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func fmtSeconds(seconds float64) string {
	return fmt.Sprintf("%.3f", seconds)
}
