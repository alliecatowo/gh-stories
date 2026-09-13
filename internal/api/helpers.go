package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/httpx"
)

func pathParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

// pathUUID parses an id from the path. A malformed id is answered with the
// same 404 as a valid-but-unauthorized one, so probing tells an attacker
// nothing about which ids exist.
func pathUUID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, httpx.NotFound()
	}
	return id, nil
}

func idempotencyKey(r *http.Request) string {
	k := r.Header.Get("Idempotency-Key")
	if len(k) > 128 {
		return k[:128]
	}
	return k
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

// writeRaw replays a stored idempotent response verbatim.
func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Idempotent-Replay", "true")
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
