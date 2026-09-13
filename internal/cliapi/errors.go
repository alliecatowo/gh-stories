package cliapi

import "fmt"

// APIError is the decoded form of the contract's structured error body
// (packages/contracts/openapi.yaml components.schemas.Error), plus the HTTP
// status it arrived with. Callers switch on Code, which mirrors the stable
// codes defined in internal/httpx (bad_request, unauthorized, forbidden,
// not_found, conflict, gone, payload_too_large, unsupported_media,
// rate_limited, internal, unavailable, processing).
type APIError struct {
	// Status is the HTTP status code the error arrived with.
	Status int
	// Code is the stable machine-readable error code.
	Code string
	// Message is a human-readable explanation, safe to show a person as-is
	// (it still passes through terminal.Sanitize before reaching a
	// terminal, since it originates from the network).
	Message string
	// Field names the offending request field, when the error is about one
	// specific field.
	Field string
	// RetryAfter is the number of seconds the server asked the client to
	// wait before retrying, from the Retry-After header or the error body's
	// retry_after_seconds, when present.
	RetryAfter int
}

func (e *APIError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s (%s: %s)", e.Message, e.Code, e.Field)
	}
	return fmt.Sprintf("%s (%s)", e.Message, e.Code)
}

// NetworkError wraps a transport-level failure (DNS, connection refused,
// TLS, timeout, context cancellation) that happened before any HTTP
// response was received, after retries were exhausted.
type NetworkError struct {
	Err error
}

func (e *NetworkError) Error() string { return "network error: " + e.Err.Error() }
func (e *NetworkError) Unwrap() error { return e.Err }
