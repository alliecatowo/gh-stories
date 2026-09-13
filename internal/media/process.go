package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alliecatowo/gh-stories/internal/domain"
)

// Input is one uploaded original, already downloaded to a local path with a
// server-generated temp filename. Process never downloads anything itself
// and never trusts the caller's filename for anything but display.
type Input struct {
	// Path is a local file already containing exactly the bytes the client
	// uploaded.
	Path string
	// DeclaredMIME is the Content-Type the client declared at upload time.
	// It is used ONLY to detect disagreement with the sniffed type
	// (mime_mismatch); it never influences how the file is decoded.
	DeclaredMIME string
	// Filename is advisory metadata only (e.g. for logging/diagnostics). It
	// is never used to build an object key, a filesystem path, or a
	// subprocess argument — Process always writes/reads through paths it
	// generates itself. A filename may be attacker-controlled and is not
	// otherwise inspected.
	Filename string
}

// Options carries processing limits and the optional trim/mute edit the
// composer previewed, so the worker's output matches what the client saw.
type Options struct {
	Limits Limits

	// StartMS/EndMS trim a video to [StartMS, EndMS) milliseconds. Both zero
	// means "no trim". Ignored for images (animated images have no composer
	// trim/mute step).
	StartMS, EndMS int
	// Muted drops the audio track entirely. Ignored for images.
	Muted bool

	// TempDir overrides where large (video) variant output files are
	// written; empty uses the OS default temp directory. The caller owns
	// deleting them via Output.Cleanup once they are durably uploaded.
	TempDir string
}

// Variant is one produced output. Small variants (thumb, terminal, poster,
// and the canonical image variant for photos) are held in memory in Data.
// Large variants (video) are spooled to disk at TempPath instead of being
// held as an extra in-memory copy. Exactly one of Data or TempPath is set.
type Variant struct {
	Kind       domain.VariantKind
	Data       []byte
	TempPath   string
	MIME       string
	Width      int
	Height     int
	DurationMS int
	ByteSize   int64
	Checksum   string // hex-encoded SHA-256 of the variant's bytes
	HasAudio   bool
}

// Output is the complete set of normalized variants produced from one input.
type Output struct {
	Variants []Variant
}

// Cleanup removes every temp directory backing this Output's variants. The
// caller must call this after every attempt — success or failure, including
// on panic — which is why Process itself calls it on every internal error
// path before returning; a caller only needs Cleanup for the success path
// once its own upload step has finished with the temp files.
func (o Output) Cleanup() {
	seen := map[string]bool{}
	for _, v := range o.Variants {
		if v.TempPath == "" {
			continue
		}
		dir := filepath.Dir(v.TempPath)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		_ = os.RemoveAll(dir)
	}
}

// Process validates and normalizes one uploaded original into the variant
// set the product needs, or returns a *Error with a stable, API/CLI-facing
// code. It never forwards a byte of the original to any variant: every
// output is produced by fully decoding (or demuxing) and re-encoding the
// input, which is what strips EXIF/XMP/ICC/container metadata as an
// unavoidable side effect of construction, rather than as a separate
// scrub-the-bytes step that could be forgotten or done incompletely.
func Process(ctx context.Context, in Input, opts Options) (out Output, err error) {
	limits := opts.Limits
	if limits == (Limits{}) {
		limits = DefaultLimits()
	}
	cctx, cancel := context.WithTimeout(ctx, limits.MaxProcessingTime)
	defer cancel()

	// A decoder or an ffmpeg-adjacent helper panicking on a hostile input
	// must not take the whole worker process down with it, and must not
	// leak the temp files already written for this job.
	defer func() {
		if r := recover(); r != nil {
			out.Cleanup()
			out = Output{}
			err = errInternal(fmt.Sprintf("panic during media processing: %v", r), nil)
		}
	}()

	st, statErr := os.Stat(in.Path)
	if statErr != nil {
		return Output{}, errInternal("stat input file", statErr)
	}
	if st.Size() == 0 {
		return Output{}, errCorruptMedia("original is empty", nil)
	}
	if st.Size() > limits.MaxOriginalBytes {
		return Output{}, errTooLarge(fmt.Sprintf("original is %d bytes, limit is %d", st.Size(), limits.MaxOriginalBytes))
	}

	info, err := Probe(cctx, in.Path)
	if err != nil {
		return Output{}, err
	}

	if in.DeclaredMIME != "" && in.DeclaredMIME != info.MIME {
		return Output{}, errMIMEMismatch(fmt.Sprintf("declared %s but the file's own bytes are %s", in.DeclaredMIME, info.MIME))
	}

	switch info.Kind {
	case KindImage:
		out, err = processImage(cctx, in.Path, info, opts, limits)
	case KindVideo:
		out, err = processVideo(cctx, in.Path, info, opts, limits)
	default:
		return Output{}, errUnsupportedMedia("unknown media kind")
	}
	if err != nil {
		out.Cleanup()
		return Output{}, err
	}

	for _, v := range out.Variants {
		if v.ByteSize > limits.MaxOutputBytes {
			out.Cleanup()
			return Output{}, errInternal(
				fmt.Sprintf("variant %s is %d bytes, exceeds the %d byte output bound", v.Kind, v.ByteSize, limits.MaxOutputBytes), nil)
		}
	}
	return out, nil
}
