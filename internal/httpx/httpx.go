// Package httpx holds the structured error shape, JSON helpers and small
// middleware shared by every route. Error codes here are the contract's codes.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
)

// Code is a stable machine-readable error code. Clients switch on these, and
// the CLI maps them onto exit codes, so they do not change casually.
type Code string

const (
	CodeBadRequest       Code = "bad_request"
	CodeUnauthorized     Code = "unauthorized"
	CodeForbidden        Code = "forbidden"
	CodeNotFound         Code = "not_found"
	CodeConflict         Code = "conflict"
	CodeGone             Code = "gone"
	CodePayloadTooLarge  Code = "payload_too_large"
	CodeUnsupportedMedia Code = "unsupported_media"
	CodeRateLimited      Code = "rate_limited"
	CodeInternal         Code = "internal"
	CodeUnavailable      Code = "unavailable"
	CodeProcessing       Code = "processing"
)

// Error is an API error carrying an HTTP status and a stable code.
type Error struct {
	Status     int
	Code       Code
	Message    string
	Field      string
	RetryAfter int
	// Internal is logged but never sent to the client.
	Internal error
}

func (e *Error) Error() string {
	if e.Internal != nil {
		return string(e.Code) + ": " + e.Message + ": " + e.Internal.Error()
	}
	return string(e.Code) + ": " + e.Message
}
func (e *Error) Unwrap() error { return e.Internal }

func Err(status int, code Code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

// Common constructors. NotFound is deliberately the answer for "exists but you
// may not see it" as well as "does not exist": the two must be
// indistinguishable to a caller probing for private Stories.
func BadRequest(msg string) *Error   { return Err(http.StatusBadRequest, CodeBadRequest, msg) }
func Unauthorized(msg string) *Error { return Err(http.StatusUnauthorized, CodeUnauthorized, msg) }
func Forbidden(msg string) *Error    { return Err(http.StatusForbidden, CodeForbidden, msg) }
func NotFound() *Error               { return Err(http.StatusNotFound, CodeNotFound, "Not found.") }
func Conflict(msg string) *Error     { return Err(http.StatusConflict, CodeConflict, msg) }
func TooLarge(msg string) *Error {
	return Err(http.StatusRequestEntityTooLarge, CodePayloadTooLarge, msg)
}
func Unsupported(msg string) *Error {
	return Err(http.StatusUnsupportedMediaType, CodeUnsupportedMedia, msg)
}
func RateLimited(retryAfterSeconds int) *Error {
	return &Error{
		Status: http.StatusTooManyRequests, Code: CodeRateLimited,
		Message: "Too many requests. Try again shortly.", RetryAfter: retryAfterSeconds,
	}
}
func Internal(err error) *Error {
	return &Error{
		Status: http.StatusInternalServerError, Code: CodeInternal,
		Message: "Something went wrong.", Internal: err,
	}
}
func FieldError(field, msg string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: CodeBadRequest, Message: msg, Field: field}
}

type errorBody struct {
	Error struct {
		Code       Code   `json:"code"`
		Message    string `json:"message"`
		Field      string `json:"field,omitempty"`
		RetryAfter int    `json:"retry_after_seconds,omitempty"`
	} `json:"error"`
}

// WriteError renders err as the contract's structured error. Internal detail
// is logged, never serialised.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		apiErr = Internal(err)
	}
	if apiErr.Status >= 500 {
		slog.ErrorContext(r.Context(), "request failed",
			"method", r.Method, "path", r.URL.Path,
			"code", apiErr.Code, "err", apiErr.Error())
	}
	var body errorBody
	body.Error.Code = apiErr.Code
	body.Error.Message = apiErr.Message
	body.Error.Field = apiErr.Field
	body.Error.RetryAfter = apiErr.RetryAfter
	if apiErr.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(apiErr.RetryAfter))
	}
	WriteJSON(w, apiErr.Status, body)
}

// WriteJSON writes v with no-store caching, which is the right default for
// every authorization-dependent response in this product.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if v == nil || status == http.StatusNoContent {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		slog.Error("write json", "err", err)
	}
}

func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// DecodeJSON reads a bounded JSON body and rejects unknown fields.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = 64 << 10
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return TooLarge("Request body is too large.")
		}
		return BadRequest("Request body is not valid JSON for this endpoint.")
	}
	// Reject trailing content so a request cannot smuggle a second document.
	if dec.More() {
		return BadRequest("Request body must contain exactly one JSON object.")
	}
	return nil
}

// Handler is a handler that may return an error, so routes can `return`
// instead of remembering to write-and-return.
type Handler func(http.ResponseWriter, *http.Request) error

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h(w, r); err != nil {
		WriteError(w, r, err)
	}
}
