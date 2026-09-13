//go:build !ghs_testidp

package api

import "github.com/go-chi/chi/v5"

// registerTestRoutes is a no-op in every normal build. The demo sign-in route
// is compiled in only under the `ghs_testidp` build tag; see testlogin.go.
func (s *Server) registerTestRoutes(chi.Router) {}

// TestIdentityRoutesCompiled reports whether the demo-only sign-in route was
// compiled into this binary. A release gate asserts it is false.
func TestIdentityRoutesCompiled() bool { return false }
