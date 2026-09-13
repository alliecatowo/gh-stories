// Package version carries build metadata stamped in at link time.
package version

import "runtime/debug"

var (
	// Version is the release tag, e.g. "v0.1.0". Overridden with -ldflags.
	Version = "dev"
	// Commit is the full git SHA the binary was built from.
	Commit = "unknown"
	// BuildDate is an RFC3339 timestamp.
	BuildDate = "unknown"
	// DefaultServiceURL is the service release artifacts target by default.
	//
	// It is deliberately EMPTY in a build that was not given one at link time.
	// Shipping a hard-coded URL for a service that is not actually running
	// would send every new user's login attempt at a host that does not exist,
	// which is worse than saying plainly that none is configured. A release
	// that has a live service sets this with
	// -X .../internal/version.DefaultServiceURL=https://…
	//
	// Self-hosters override it at runtime with GHS_SERVICE_URL or --service,
	// never by rebuilding.
	DefaultServiceURL = ""
)

// Short returns a compact "v0.1.0 (abc1234)" style string.
func Short() string {
	c := Commit
	if len(c) > 7 {
		c = c[:7]
	}
	return Version + " (" + c + ")"
}

func init() {
	if Commit != "unknown" {
		return
	}
	// Fall back to VCS stamping for `go install`-style builds.
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			Commit = s.Value
		}
		if s.Key == "vcs.time" && s.Value != "" {
			BuildDate = s.Value
		}
	}
}
