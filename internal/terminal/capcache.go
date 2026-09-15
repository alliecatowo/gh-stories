package terminal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// A slow terminal can take seconds to answer the capability probe (observed:
// Ghostty replying after the budget expired). Probing on every single CLI
// invocation would either hang the CLI or, with a bounded budget, re-pay a
// timeout on every run. So a successful probe result is cached on disk and
// reused: one slow run, then instant answers.
//
// The cache is keyed on everything that can change the answer — TERM,
// TERM_PROGRAM, LC_TERMINAL, COLORTERM, tmux/screen presence, SSH presence —
// and expires after capCacheTTL. An explicit --renderer override bypasses
// the cache entirely (the caller already decided), and only successful,
// fence-verified detections are stored: a timed-out probe is never cached,
// so a genuinely changed terminal is re-probed rather than stuck on stale
// "external".
const capCacheTTL = 7 * 24 * time.Hour

// capCache is the on-disk form. Geometry is cached too: cell pixels only
// change when the font or window DPI changes, which also busts the TTL soon
// enough for a cosmetic value.
type capCache struct {
	Protocol    Protocol  `json:"protocol"`
	Reason      string    `json:"reason"`
	CellW       int       `json:"cell_w_px"`
	CellH       int       `json:"cell_h_px"`
	StoredAt    time.Time `json:"stored_at"`
	Fingerprint string    `json:"fingerprint"`
}

// capFingerprint identifies the environment a probe result belongs to.
func capFingerprint() string {
	return "TERM=" + os.Getenv("TERM") +
		"|TERM_PROGRAM=" + os.Getenv("TERM_PROGRAM") +
		"|LC_TERMINAL=" + os.Getenv("LC_TERMINAL") +
		"|COLORTERM=" + os.Getenv("COLORTERM") +
		"|TMUX=" + tmuxPresence() +
		"|STY=" + os.Getenv("STY") +
		"|SSH=" + sshPresence()
}

func tmuxPresence() string {
	if os.Getenv("TMUX") != "" {
		return "1"
	}
	return "0"
}

func sshPresence() string {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return "1"
	}
	return "0"
}

// capCachePath locates the cache file next to the credential store:
// $XDG_CONFIG_HOME/gh-stories, or ~/.config/gh-stories. A cache that cannot
// be located is simply skipped — detection always works without it.
func capCachePath() (string, bool) {
	dir := ""
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		dir = filepath.Join(xdg, "gh-stories")
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		dir = filepath.Join(home, ".config", "gh-stories")
	}
	if dir == "" {
		return "", false
	}
	return filepath.Join(dir, "terminal-caps.json"), true
}

// loadCapCache returns a usable cached result, or nil.
func loadCapCache() *Capabilities {
	path, ok := capCachePath()
	if !ok {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c capCache
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil
	}
	if c.Protocol != ProtocolKitty && c.Protocol != ProtocolITerm2 {
		return nil // only graphics verdicts are worth caching
	}
	if c.Fingerprint != capFingerprint() {
		return nil
	}
	if time.Since(c.StoredAt) > capCacheTTL {
		return nil
	}
	caps := &Capabilities{Protocol: c.Protocol, Reason: c.Reason + " (cached)"}
	populateEnvHints(caps)
	caps.CellWidthPx, caps.CellHeightPx = c.CellW, c.CellH
	return caps
}

// storeCapCache persists a fence-verified detection. Failures are silent:
// caching is an optimization, never a requirement.
func storeCapCache(caps *Capabilities, pr probeResult) {
	if !pr.daSeen {
		return // never cache a timed-out probe
	}
	if caps.Protocol != ProtocolKitty && caps.Protocol != ProtocolITerm2 {
		return
	}
	path, ok := capCachePath()
	if !ok {
		return
	}
	raw, err := json.Marshal(capCache{
		Protocol: caps.Protocol, Reason: caps.Reason,
		CellW: caps.CellWidthPx, CellH: caps.CellHeightPx,
		StoredAt: time.Now(), Fingerprint: capFingerprint(),
	})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o600)
}
