package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	"github.com/google/uuid"
)

// IdempotentReplay returns a stored response for a repeated mutation.
//
// A retry after a dropped connection must not create a duplicate Story, a
// duplicate reply, or a duplicate anything. Same key + same request body
// returns the original response; same key + a *different* body is a conflict,
// because silently returning an unrelated result would be worse.
func (s *Store) IdempotentReplay(ctx context.Context, userID uuid.UUID,
	scope, key string, request any) (found bool, status int, body json.RawMessage, conflict bool, err error) {
	if key == "" {
		return false, 0, nil, false, nil
	}
	hash, err := hashRequest(request)
	if err != nil {
		return false, 0, nil, false, err
	}
	var storedHash []byte
	err = s.pool.QueryRow(ctx,
		`SELECT request_hash, status_code, response_body FROM idempotency_keys
		 WHERE user_id=$1 AND scope=$2 AND key=$3`, userID, scope, key).
		Scan(&storedHash, &status, &body)
	if err != nil {
		if norm(err) == ErrNotFound {
			return false, 0, nil, false, nil
		}
		return false, 0, nil, false, wrap("idempotency lookup", err)
	}
	if !equalBytes(storedHash, hash) {
		return true, 0, nil, true, nil
	}
	return true, status, body, false, nil
}

// RememberIdempotent stores the response for a completed mutation.
func (s *Store) RememberIdempotent(ctx context.Context, userID uuid.UUID,
	scope, key string, request any, status int, response any) error {
	if key == "" {
		return nil
	}
	hash, err := hashRequest(request)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO idempotency_keys (user_id, scope, key, request_hash, status_code, response_body)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb)
		ON CONFLICT (user_id, scope, key) DO NOTHING`,
		userID, scope, key, hash, status, raw)
	return wrap("remember idempotent", err)
}

func hashRequest(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
