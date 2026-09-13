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
	// DefaultServiceURL is the live service release artifacts target by
	// default. Self-hosters override it at runtime, never by rebuilding.
	DefaultServiceURL = "https://ghstories.fly.dev"
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
