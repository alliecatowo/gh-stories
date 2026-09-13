package store

import "github.com/google/uuid"

// ParseUUID is exported for integration tests that carry ids as strings.
func ParseUUID(s string) (uuid.UUID, error) { return uuid.Parse(s) }
