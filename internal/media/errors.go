// Package media validates and normalizes uploaded photos and videos. Every
// byte handled here originates from an anonymous HTTP upload and MUST be
// treated as hostile: nothing is trusted based on a declared Content-Type, a
// filename, or a file extension. Detection is always based on the bytes
// themselves, and the original bytes are never forwarded to a viewer — every
// accepted input is decoded and re-encoded from scratch.
package media

import "fmt"

// Stable error codes shared with the API and the CLI. A caller (the worker,
// ultimately the client) uses Code to decide what to tell the user and
// whether retrying makes sense; Message is for logs/diagnostics, never shown
// to another user's audience.
const (
	// CodeUnsupportedMedia: the sniffed type is not one of the accepted
	// image/video formats (this includes SVG, which is rejected outright).
	CodeUnsupportedMedia = "unsupported_media"
	// CodeMIMEMismatch: the declared Content-Type disagrees with what the
	// file's own bytes say it is.
	CodeMIMEMismatch = "mime_mismatch"
	// CodeCorruptMedia: the sniffed type looked accepted, but the file failed
	// to fully parse/decode/transcode.
	CodeCorruptMedia = "corrupt_media"
	// CodeTooLarge: the original exceeds the configured byte-size limit.
	CodeTooLarge = "too_large"
	// CodeTooLong: a video (after any trim) exceeds the duration limit.
	CodeTooLong = "too_long"
	// CodeTooManyPixels: decoded (or declared, pre-decode) pixel count, or a
	// single side, exceeds the configured bound.
	CodeTooManyPixels = "too_many_pixels"
	// CodeProcessingTimeout: the wall-clock processing budget was exceeded.
	CodeProcessingTimeout = "processing_timeout"
	// CodeFFmpegUnavailable: video processing was requested but ffmpeg is not
	// installed on this worker.
	CodeFFmpegUnavailable = "ffmpeg_unavailable"
	// CodeInternal: an unexpected failure not attributable to the input
	// itself (e.g. a temp-file I/O error).
	CodeInternal = "internal"
)

// Error is the error type every exported function in this package returns on
// failure. Code is stable and intended for API/CLI consumption; Retryable
// tells the worker whether re-queuing the job could plausibly succeed later
// (a corrupt or oversized upload never will; a timeout or a transient
// environment problem might).
type Error struct {
	Code      string
	Message   string
	Retryable bool
	Err       error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// errUnsupported and friends are small constructors used throughout the
// package so every call site states its retryability explicitly rather than
// leaving it to be inferred later.
func errUnsupportedMedia(msg string) *Error {
	return &Error{Code: CodeUnsupportedMedia, Message: msg, Retryable: false}
}
func errMIMEMismatch(msg string) *Error {
	return &Error{Code: CodeMIMEMismatch, Message: msg, Retryable: false}
}
func errCorruptMedia(msg string, err error) *Error {
	return &Error{Code: CodeCorruptMedia, Message: msg, Retryable: false, Err: err}
}
func errTooLarge(msg string) *Error {
	return &Error{Code: CodeTooLarge, Message: msg, Retryable: false}
}
func errTooLong(msg string) *Error {
	return &Error{Code: CodeTooLong, Message: msg, Retryable: false}
}
func errTooManyPixels(msg string) *Error {
	return &Error{Code: CodeTooManyPixels, Message: msg, Retryable: false}
}
func errProcessingTimeout(msg string, err error) *Error {
	// Retryable: a busy worker might succeed on a retry with less contention.
	return &Error{Code: CodeProcessingTimeout, Message: msg, Retryable: true, Err: err}
}
func errFFmpegUnavailable(msg string) *Error {
	// Retryable: this is an environment/deployment problem, not a property of
	// the input. A worker with ffmpeg installed can succeed on the same job.
	return &Error{Code: CodeFFmpegUnavailable, Message: msg, Retryable: true}
}
func errInternal(msg string, err error) *Error {
	return &Error{Code: CodeInternal, Message: msg, Retryable: true, Err: err}
}
