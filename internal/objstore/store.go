// Package objstore is the only place that talks to object storage. Every
// object it manages is PRIVATE media: no method in this package ever sets a
// public ACL, and no method ever returns a URL that lets someone read an
// object without a fresh, narrowly-scoped presigned request.
package objstore

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotExist is returned by Get and Head when the key does not exist. It is
// deliberately generic (not "no such key", not the raw S3 error) so callers
// can treat "never existed" and "already deleted" the same way — both are
// success cases for an idempotent cleanup delete.
var ErrNotExist = errors.New("objstore: object does not exist")

// Object is a retrieved (or HEAD-probed) piece of media. Body is nil for a
// Head response.
type Object struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
	// ContentRange is set (e.g. "bytes 0-99/1000") only when StatusCode is 206.
	ContentRange string
	ETag         string
	// StatusCode is 200 for a full read or 200 for a Head, and 206 when a
	// Range request was honored. Callers that proxy media to a browser or
	// video player need this to answer HTTP Range requests correctly.
	StatusCode int
}

// Store is private, server-side object storage for media bytes. Every key is
// server-chosen (see NewObjectKey); nothing in this interface accepts or
// derives a key from user-supplied input such as a filename.
type Store interface {
	// Put writes bytes to a private key, replacing any existing object at
	// that key. size must equal the number of bytes r will yield.
	Put(ctx context.Context, key, contentType string, r io.Reader, size int64) error

	// Get returns a reader for the object at key. rangeHeader, if non-empty,
	// is passed through verbatim as an HTTP Range request header value (e.g.
	// "bytes=0-1023") so the caller can serve partial content (video
	// scrubbing, resumable downloads) without buffering the whole object.
	Get(ctx context.Context, key string, rangeHeader string) (*Object, error)

	// Head returns metadata for key without transferring the body.
	Head(ctx context.Context, key string) (*Object, error)

	// Delete removes zero or more keys. Deleting a key that does not exist
	// is a success, not an error: cleanup jobs must be idempotent so a retry
	// after a partial failure never fails the whole batch.
	Delete(ctx context.Context, keys ...string) error

	// PresignPut authorizes exactly one client-side PUT to exactly one key,
	// for a short time, bound to the declared content type and size. A
	// client cannot use the resulting URL to write to a different key, claim
	// a different content type, or upload more bytes than declared: doing so
	// invalidates the request signature.
	PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (url string, headers map[string]string, err error)

	// Healthy reports whether the backing store is currently reachable and
	// the configured bucket exists. It is used by process health checks, not
	// by request handling.
	Healthy(ctx context.Context) error
}
