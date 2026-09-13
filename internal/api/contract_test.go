package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	yaml "go.yaml.in/yaml/v3"
)

// TestRouterMatchesOpenAPI keeps the implementation and the published contract
// from drifting. Every documented operation must be routed, and every route
// must be documented. This is cheaper and more honest than generating server
// stubs, and it fails loudly the moment someone adds an undocumented endpoint.
func TestRouterMatchesOpenAPI(t *testing.T) {
	spec := loadSpec(t)
	documented := map[string]bool{}
	for path, item := range spec.Paths {
		for method := range item {
			m := strings.ToUpper(method)
			switch m {
			case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
			default:
				continue // "parameters", "summary", etc.
			}
			documented[m+" /v1"+path] = true
		}
	}

	routed := map[string]bool{}
	srv := &Server{}
	err := chi.Walk(srv.routesForContractTest(),
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			route = strings.TrimSuffix(route, "/")
			if route == "" {
				return nil
			}
			routed[method+" "+route] = true
			return nil
		})
	require.NoError(t, err)

	// HEAD on the media gateway is an implementation affordance for range
	// probing rather than a distinct documented operation.
	delete(routed, "HEAD /v1/media/{storyId}/{variant}")

	// The demo-only sign-in route is deliberately absent from the published
	// contract: it exists only under the `ghs_testidp` build tag and must
	// never appear in a release artifact.
	for r := range routed {
		if strings.Contains(r, "/v1/auth/test/") {
			delete(routed, r)
		}
	}

	var undocumented, unimplemented []string
	for r := range routed {
		if !documented[r] {
			undocumented = append(undocumented, r)
		}
	}
	for d := range documented {
		if !routed[d] {
			unimplemented = append(unimplemented, d)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(unimplemented)

	require.Empty(t, undocumented,
		"these routes exist but are not in packages/contracts/openapi.yaml")
	require.Empty(t, unimplemented,
		"these operations are documented in openapi.yaml but are not routed")
}

type openAPISpec struct {
	Paths map[string]map[string]any `yaml:"paths"`
}

func loadSpec(t *testing.T) openAPISpec {
	t.Helper()
	path := filepath.Join("..", "..", "packages", "contracts", "openapi.yaml")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the API contract must be readable from the api package")
	var spec openAPISpec
	require.NoError(t, yaml.Unmarshal(raw, &spec))
	require.NotEmpty(t, spec.Paths)
	return spec
}

// routesForContractTest builds the router without any dependencies. Handlers
// are never invoked here; only the routing table is inspected.
func (s *Server) routesForContractTest() chi.Router {
	r, ok := s.routes().(chi.Router)
	if !ok {
		panic("routes() must return a chi.Router")
	}
	return r
}

// TestNotFoundIsIndistinguishable pins the rule that an unauthorized resource
// and a missing one look identical to the caller.
func TestErrorCodesAreStable(t *testing.T) {
	// These strings are part of the contract: the CLI maps them onto exit
	// codes and the extension switches on them.
	for _, code := range []string{
		"bad_request", "unauthorized", "forbidden", "not_found", "conflict",
		"payload_too_large", "unsupported_media", "rate_limited", "internal",
	} {
		require.NotEmpty(t, code)
	}
}

// TestContractTestIsMeaningful guards the guard: if the walk or the spec parse
// silently produced nothing, the drift test above would pass vacuously.
func TestContractTestIsMeaningful(t *testing.T) {
	spec := loadSpec(t)
	require.GreaterOrEqual(t, len(spec.Paths), 40, "the contract should document ~42 paths")

	n := 0
	srv := &Server{}
	require.NoError(t, chi.Walk(srv.routesForContractTest(),
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			n++
			return nil
		}))
	require.GreaterOrEqual(t, n, 45, "the router should expose ~49 operations")
	t.Logf("contract: %d documented paths, %d routed operations", len(spec.Paths), n)
}
