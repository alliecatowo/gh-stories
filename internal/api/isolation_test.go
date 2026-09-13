package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestProductionIsolation is a release gate.
//
// A published artifact must contain no test authentication, no demo secret, no
// seeded account and no localhost service URL. The demo sign-in route is
// guarded by a build tag rather than a runtime flag precisely so that this can
// be asserted about the compiled package, not merely about configuration.
func TestProductionIsolation(t *testing.T) {
	require.False(t, TestIdentityRoutesCompiled(),
		"a normal build must not compile the demo sign-in route")
}

// TestNoLocalhostInNonTestSources catches a development service URL that
// escaped into shipped code.
func TestNoLocalhostInShippedSources(t *testing.T) {
	roots := []string{
		filepath.Join("..", "api"),
		filepath.Join("..", "store"),
		filepath.Join("..", "auth"),
		filepath.Join("..", "account"),
		filepath.Join("..", "version"),
	}
	var offenders []string
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			// Test files legitimately talk to a local service.
			if strings.HasSuffix(path, "_test.go") || strings.Contains(path, "testidp") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			for _, needle := range []string{"localhost:", "127.0.0.1"} {
				if strings.Contains(string(raw), needle) {
					offenders = append(offenders, path+" contains "+needle)
				}
			}
			return nil
		})
	}
	require.Empty(t, offenders,
		"shipped sources must not hard-code a development service address")
}
