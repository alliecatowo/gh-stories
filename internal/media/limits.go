package media

import "time"

// Limits bounds every resource-consuming dimension of processing one upload:
// bytes read, pixels decoded, frames decoded, seconds of video, output size,
// and total wall-clock time. Every one of these exists because the input is
// hostile: without them, a single upload could exhaust worker memory, CPU or
// disk, or wedge the media loop indefinitely and starve every other job.
//
// MaxOriginalBytes, MaxVideoSeconds and MaxImagePixels come from
// internal/config.Config (product-configurable). Everything else is an
// implementation bound chosen here, in one place, rather than scattered
// across call sites, so it can be reasoned about and changed in one place.
type Limits struct {
	// MaxOriginalBytes bounds the size of the uploaded original. This is the
	// worker's own backstop; the API's upload-intent path enforces the same
	// number (config.MaxUploadBytes) before the bytes are even accepted.
	MaxOriginalBytes int64

	// MaxVideoSeconds bounds decoded video duration, before and after any
	// composer-supplied trim. A 24-hour ephemeral Story never needs a video
	// longer than a minute or so of viewing time to make its point, and a
	// tighter cap keeps transcode time and output storage predictable.
	MaxVideoSeconds int

	// MaxImagePixels bounds decoded width*height for a single image frame
	// (and, for animated images, is also applied per frame). Checked BEFORE
	// any pixel is decoded, using only the format's own header — this is
	// what makes a "pixel bomb" (a tiny file that declares an enormous
	// image) rejected for free instead of allocating gigabytes to find out.
	MaxImagePixels int64

	// MaxImageSide additionally bounds any single dimension, independent of
	// the total pixel count, because a 1×2,000,000,000 image would pass a
	// naive width*height check while still being pathological to decode,
	// resample or display. 12000px covers any camera or scan in current
	// consumer use with wide headroom, while remaining small enough that a
	// Lanczos resample of an image at this bound finishes in well under a
	// second.
	MaxImageSide int

	// MaxAnimationFrames bounds how many frames of an animated GIF/WebP the
	// worker will walk or transcode. It is enforced by a bounded structural
	// scan of the container (chunk/block headers only, no per-frame pixel
	// decode) before any frame is decoded, for the same reason as
	// MaxImagePixels above. 512 frames covers essentially every animated
	// sticker or short loop actually used in the wild (a 512-frame GIF at a
	// typical 10fps is nearly a minute of animation) while bounding worst-case
	// transcode time.
	MaxAnimationFrames int

	// MaxOutputBytes bounds the size of any single produced variant. It
	// exists as a backstop against a pathological input that decodes fine
	// but re-encodes to something absurd (e.g. adversarial noise that defeats
	// video compression); a variant that exceeds it is treated as a
	// processing failure rather than being stored and served.
	MaxOutputBytes int64

	// MaxProcessingTime bounds total wall-clock time spent on one job: probe
	// + decode + transcode + encode for every variant combined. It is applied
	// as a context deadline around the whole Process call, and ffmpeg
	// invocations additionally receive their own share of the remaining
	// budget so a single subprocess cannot silently consume all of it.
	MaxProcessingTime time.Duration

	// CanonicalImageMaxSide bounds the longest side of the canonical "image"
	// output variant. A 24-hour ephemeral Story has no lasting archival value
	// that justifies storing/serving a multi-thousand-pixel photo at full
	// camera resolution; 2048px is comfortably larger than any device viewport
	// this product targets (phone, browser extension popup, terminal image
	// protocols) while cutting typical photo storage size dramatically.
	CanonicalImageMaxSide int

	// ThumbMaxSide bounds the small variant used for inbox/list rows.
	ThumbMaxSide int

	// TerminalMaxSide bounds the "terminal" variant, sized for the largest
	// graphical terminal image protocols (iTerm2, Kitty, Sixel) reasonably
	// render without downscaling themselves. ~1600px matches a typical
	// terminal window rendered at a high-DPI backing scale.
	TerminalMaxSide int
}

// DefaultLimits returns the implementation-chosen bounds documented on
// Limits, with the product-configurable fields left at their config.Config
// defaults (MaxUploadBytes=100MB, MaxVideoSeconds=60, MaxImagePixels=50MP).
// Callers wired to real configuration should override the first three fields
// from config.Config rather than relying on these defaults staying in sync.
func DefaultLimits() Limits {
	return Limits{
		MaxOriginalBytes: 100 * 1024 * 1024,
		MaxVideoSeconds:  60,
		MaxImagePixels:   50_000_000,

		MaxImageSide:       12000,
		MaxAnimationFrames: 512,
		MaxOutputBytes:     250 * 1024 * 1024,
		MaxProcessingTime:  3 * time.Minute,

		CanonicalImageMaxSide: 2048,
		ThumbMaxSide:          320,
		TerminalMaxSide:       1600,
	}
}
