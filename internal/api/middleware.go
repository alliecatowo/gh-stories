package api

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alliecatowo/gh-stories/internal/domain"
	"github.com/alliecatowo/gh-stories/internal/httpx"
	"github.com/alliecatowo/gh-stories/internal/ratelimit"
)

type ctxKey int

const (
	ctxKeyUser ctxKey = iota
	ctxKeySession
	ctxKeyRequestID
)

// caller returns the authenticated account, or nil.
func caller(r *http.Request) *domain.User {
	u, _ := r.Context().Value(ctxKeyUser).(*domain.User)
	return u
}

func callerSession(r *http.Request) *domain.Session {
	s, _ := r.Context().Value(ctxKeySession).(*domain.Session)
	return s
}

// mustCaller returns the authenticated account or an unauthorized error.
func mustCaller(r *http.Request) (*domain.User, error) {
	u := caller(r)
	if u == nil {
		return nil, httpx.Unauthorized("Sign in to GitHub Stories to do that.")
	}
	if !u.Active() {
		// A suspended account can neither act nor be seen.
		return nil, httpx.Forbidden("This account is suspended.")
	}
	return u, nil
}

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(ctxKeyRequestID).(string)
	return id
}

// withRequestID assigns an id used to correlate structured logs. It never
// trusts a client-supplied id for anything but correlation.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.NewString()
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyRequestID, id)))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Flush lets the media gateway stream without buffering the whole object.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logging emits one structured line per request.
//
// It deliberately records no tokens, no captions, no reply bodies, no media
// URLs with credentials and no query strings, because those are exactly the
// places private content leaks into logs.
func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.Clock.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"bytes", sw.bytes,
			"duration_ms", s.Clock.Now().Sub(start).Milliseconds(),
			"request_id", requestID(r),
		}
		if u := caller(r); u != nil {
			// The account id, never the login or any content.
			attrs = append(attrs, "user_id", u.ID.String())
		}
		s.Log.LogAttrs(r.Context(), slog.LevelInfo, "request", toAttrs(attrs)...)
	})
}

func toAttrs(kv []any) []slog.Attr {
	out := make([]slog.Attr, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		k, _ := kv[i].(string)
		out = append(out, slog.Any(k, kv[i+1]))
	}
	return out
}

// recovery turns a panic into a 500 without taking the process down.
func (s *Server) recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.Log.Error("panic recovered",
					"path", r.URL.Path, "request_id", requestID(r),
					"panic", rec, "stack", string(debug.Stack()))
				httpx.WriteError(w, r, httpx.Internal(nil))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// cors allows exactly the configured origins to send credentialed requests.
// There is no wildcard: the browser extension's origins are configured
// explicitly, and an unknown origin gets no CORS headers at all.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.Cfg.OriginAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers",
				"Authorization, Content-Type, Idempotency-Key, X-Stories-Client")
			w.Header().Set("Access-Control-Allow-Methods",
				"GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Expose-Headers",
				"X-Request-Id, Retry-After, Content-Range, Accept-Ranges")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		w.Header().Add("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders applies conservative defaults to every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// authenticate resolves an optional bearer token. It never fails the request:
// individual routes decide whether they require a caller, so that a public
// health check and an authenticated feed can share the same chain.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" || s.Auth == nil {
			next.ServeHTTP(w, r)
			return
		}
		sess, user, err := s.Auth.Authenticate(r.Context(), token)
		if err != nil || user == nil {
			// An invalid token is simply an anonymous caller. Routes that need
			// a caller will answer 401 themselves.
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyUser, user)
		ctx = context.WithValue(ctx, ctxKeySession, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// limit applies a token bucket keyed by account when signed in, and by client
// address otherwise.
func (s *Server) limit(rule ratelimit.Rule, scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := scope + "|" + clientKey(r)
			ok, wait := s.limiter.Allow(key, rule)
			if !ok {
				retry := int(wait/time.Second) + 1
				httpx.WriteError(w, r, httpx.RateLimited(retry))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientKey(r *http.Request) string {
	if u := caller(r); u != nil {
		return "u:" + u.ID.String()
	}
	// Only the socket peer is used. A forwarded header is attacker-controlled
	// unless a trusted proxy sets it, so it is not consulted here.
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return "ip:" + host
}

// requireAuth rejects anonymous callers before the handler runs.
func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := mustCaller(r); err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireModerator is deny-by-default: moderator status is a server-side flag,
// and not every signed-in user gets an admin dashboard.
func requireModerator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := mustCaller(r)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		if !u.IsModerator {
			// Indistinguishable from a route that does not exist.
			httpx.WriteError(w, r, httpx.NotFound())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// timeout bounds every request so a slow dependency cannot pin a connection.
func timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
