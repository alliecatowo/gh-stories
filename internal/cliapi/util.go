package cliapi

import "net/url"

// pathEscape escapes a single path segment (a login, a story id, ...) for
// safe inclusion in a request URL.
func pathEscape(s string) string { return url.PathEscape(s) }

// idemHeader builds the single-header map carrying an Idempotency-Key, or
// nil when key is empty so no header is sent at all.
func idemHeader(key string) map[string]string {
	if key == "" {
		return nil
	}
	return map[string]string{"Idempotency-Key": key}
}
