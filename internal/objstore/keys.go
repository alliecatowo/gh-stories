package objstore

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// NewObjectKey returns an unguessable, server-chosen key for a newly
// authorized upload original: "u/<ownerID>/<uuid>". The key is never derived
// from a user-supplied filename — a filename is attacker-controlled input
// that could otherwise be used to collide with another object, smuggle path
// separators, or leak into logs and URLs verbatim. kind is a short,
// code-controlled discriminator kept only for readability when browsing the
// bucket by hand (e.g. "upload"); it is not attacker input either.
func NewObjectKey(kind, ownerID string) string {
	return fmt.Sprintf("u/%s/%s", sanitizeSegment(ownerID), uuid.NewString())
}

// VariantKey returns the deterministic output key for one published media
// variant: "m/<storyID>/<variant>.<ext>". Unlike an upload original, this key
// is built directly from server-controlled values (a UUID primary key and a
// closed VariantKind enum) rather than randomized, which lets a retried
// worker attempt overwrite the same key instead of orphaning a new one.
func VariantKey(storyID, variant, ext string) string {
	return fmt.Sprintf("m/%s/%s.%s", sanitizeSegment(storyID), sanitizeSegment(variant), sanitizeSegment(ext))
}

// sanitizeSegment defends in depth against a path segment ever containing a
// separator or control character, even though every current caller only
// passes a UUID string or a closed enum value. Anything outside the safe set
// is replaced, never dropped, so a caller's key stays predictable in length.
func sanitizeSegment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}
