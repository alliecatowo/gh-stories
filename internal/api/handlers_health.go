package api

import (
	"net/http"

	"github.com/alliecatowo/gh-stories/internal/httpx"
	"github.com/alliecatowo/gh-stories/internal/version"
)

// getLive answers whether the process is running. It touches no dependency, so
// a database outage does not cause a restart loop.
func (s *Server) getLive(w http.ResponseWriter, r *http.Request) error {
	httpx.WriteJSON(w, http.StatusOK, healthResponse{
		Status: "ok", Version: version.Version, Commit: version.Commit,
	})
	return nil
}

// getReady answers whether the process can serve traffic, and reports the
// operational signals that matter for this product: how far behind media
// processing is, and how far behind physical cleanup is.
func (s *Server) getReady(w http.ResponseWriter, r *http.Request) error {
	out := healthResponse{
		Status: "ok", Version: version.Version, Commit: version.Commit,
		Checks: map[string]string{},
	}
	status := http.StatusOK

	if err := s.Store.Pool().Ping(r.Context()); err != nil {
		out.Checks["database"] = "unreachable"
		out.Status = "down"
		status = http.StatusServiceUnavailable
	} else {
		out.Checks["database"] = "ok"
	}

	if s.Objects != nil {
		if err := s.Objects.Healthy(r.Context()); err != nil {
			out.Checks["object_storage"] = "unreachable"
			out.Status = "down"
			status = http.StatusServiceUnavailable
		} else {
			out.Checks["object_storage"] = "ok"
		}
	}
	if s.Auth != nil && s.Auth.Configured() {
		out.Checks["github_oauth"] = "configured"
	} else {
		// Not fatal: development, fixture testing and packaging must never be
		// blocked on an OAuth registration.
		out.Checks["github_oauth"] = "not_configured"
		if out.Status == "ok" {
			out.Status = "degraded"
		}
	}

	if ops, err := s.Store.Ops(r.Context()); err == nil {
		out.MediaJobsBacklog = ops.MediaBacklog
		out.OldestMediaJobAgeSeconds = int(ops.OldestMediaJobAge.Seconds())
		out.CleanupBacklog = ops.CleanupBacklog
		if ops.MediaFailures > 0 {
			out.Checks["media_failures"] = "present"
		}
		if ops.CleanupFailures > 0 {
			out.Checks["cleanup_failures"] = "present"
		}
	}

	httpx.WriteJSON(w, status, out)
	return nil
}
