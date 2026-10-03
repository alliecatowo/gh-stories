package terminal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"

	"golang.org/x/term"
)

// Protocol identifies which terminal inline-image protocol (if any)
// gh-stories will use to render an image.
type Protocol string

const (
	// ProtocolKitty is the kitty terminal graphics protocol.
	ProtocolKitty Protocol = "kitty"
	// ProtocolITerm2 is iTerm2's inline image protocol.
	ProtocolITerm2 Protocol = "iterm"
	// ProtocolExternal means no inline graphics protocol is usable; render
	// an honest text placeholder instead.
	ProtocolExternal Protocol = "external"
)

// Capabilities describes what a specific terminal, on this particular run,
// appears to support. Every field is best-effort: a Detect call that could
// not complete a given probe (non-TTY, timeout, forced override, Windows
// build) leaves the corresponding field at its zero value rather than
// guessing.
type Capabilities struct {
	// Protocol is the inline-image protocol Detect selected.
	Protocol Protocol
	// CellWidthPx and CellHeightPx are the pixel size of one terminal cell,
	// or 0 if it could not be determined.
	CellWidthPx  int
	CellHeightPx int
	// ColsCells and RowsCells are the terminal's current size in cells.
	ColsCells int
	RowsCells int
	// TruecolorOK reports whether the terminal advertises 24-bit color
	// support (COLORTERM=truecolor/24bit).
	TruecolorOK bool
	// InTmux reports whether this process is running inside a tmux client.
	InTmux bool
	// InScreen reports whether this process is running inside GNU screen.
	InScreen bool
	// TmuxPassthroughOK reports whether, when InTmux is true, tmux's
	// `allow-passthrough` was verified to actually forward escape
	// sequences to the outer terminal. Kitty and iTerm2 renderers must not
	// emit protocol escapes inside tmux unless this is true.
	TmuxPassthroughOK bool
	// OverSSH reports whether this process appears to be running over an
	// SSH connection (SSH_CONNECTION or SSH_TTY set).
	OverSSH bool
	// Reason is a human-readable explanation of why Protocol was chosen,
	// suitable for `--renderer` debugging output or a verbose log line.
	Reason string
}

// reDA matches a Primary Device Attributes reply (CSI ? ... c). Virtually
// every terminal answers DA, which is what makes it useful as a fence: its
// arrival tells Detect that the terminal has finished responding to
// whatever was sent before it, including probes a non-supporting terminal
// would otherwise just silently ignore.
var reDA = regexp.MustCompile(`\x1b\[\?[0-9;]*c`)

// reCellPx matches a CSI 16 t reply: `CSI 6 ; height ; width t`, the cell
// size in pixels.
var reCellPx = regexp.MustCompile(`\x1b\[6;(\d+);(\d+)t`)

// reWinPx matches a CSI 14 t reply: `CSI 4 ; height ; width t`, the whole
// window size in pixels.
var reWinPx = regexp.MustCompile(`\x1b\[4;(\d+);(\d+)t`)

// probeResult holds everything Detect could parse out of the raw probe
// reply buffer.
type probeResult struct {
	kittyOK      bool
	cellW, cellH int
	winW, winH   int
	daSeen       bool
}

// Detect determines which inline-image protocol gh-stories should use for
// out, and gathers the terminal geometry needed to size images for it.
//
// override forces the result when it is ProtocolKitty, ProtocolITerm2, or
// ProtocolExternal (this is what backs the `--renderer=` flag): Detect
// returns immediately with only cheap, non-interactive information filled
// in (environment hints, and ioctl window size) and performs no
// interactive terminal query, since the caller has already made the
// decision — running an interactive probe afterward would only add latency
// for no benefit. Any other value of override (including "auto" and "")
// triggers full detection.
//
// Full detection never blocks for more than the probe budget (a few
// hundred milliseconds): a slow or non-responding terminal falls back to
// ProtocolExternal rather than hanging the CLI.
func Detect(ctx context.Context, in *os.File, out *os.File, override Protocol) (Capabilities, error) {
	caps := Capabilities{}
	populateEnvHints(&caps)

	if out != nil {
		if w, h, err := term.GetSize(int(out.Fd())); err == nil {
			caps.ColsCells, caps.RowsCells = w, h
		}
	}

	switch override {
	case ProtocolKitty, ProtocolITerm2, ProtocolExternal:
		caps.Protocol = override
		caps.Reason = "renderer forced via override (--renderer=" + string(override) + ")"
		return caps, nil
	}

	if out == nil || !term.IsTerminal(int(out.Fd())) {
		caps.Protocol = ProtocolExternal
		caps.Reason = "stdout is not a terminal"
		return caps, nil
	}
	if in == nil || !term.IsTerminal(int(in.Fd())) {
		caps.Protocol = ProtocolExternal
		caps.Reason = "stdin is not a terminal, cannot query graphics support"
		return caps, nil
	}

	// A cached graphics verdict from this exact environment skips the probe
	// entirely: repeat runs answer instantly instead of re-paying up to the
	// full probe budget. Cols/rows above already came from a fresh ioctl.
	if hit := loadCapCache(); hit != nil {
		hit.ColsCells, hit.RowsCells = caps.ColsCells, caps.RowsCells
		return *hit, nil
	}

	buf, err := runProbe(ctx, in, out, caps.InTmux)
	if err != nil {
		// A probe failure (raw mode unavailable, write error, ...) is not
		// fatal: fall back to the always-safe external renderer instead of
		// failing the whole CLI over a cosmetic capability check.
		caps.Protocol = ProtocolExternal
		caps.Reason = "capability probe failed: " + err.Error()
		return caps, nil
	}

	pr := parseProbe(buf)
	if os.Getenv("GHS_DEBUG_PROBE") != "" {
		debugProbe(buf, pr)
	}
	resolveCellGeometry(&caps, pr)
	decideProtocol(&caps, pr)
	storeCapCache(&caps, pr)
	return caps, nil
}

// debugProbe dumps the raw probe reply for terminal-compatibility debugging.
// Set GHS_DEBUG_PROBE=1 and run `gh stories doctor`: the hex dump shows
// exactly what the terminal sent back (or that nothing arrived at all),
// which is the ground truth for "couldn't detect in time" reports.
func debugProbe(buf []byte, pr probeResult) {
	fmt.Fprintf(os.Stderr, "ghs probe: %d byte(s) in %q (tmux=%v screen=%v ssh=%v)\n",
		len(buf), os.Getenv("TERM_PROGRAM"),
		os.Getenv("TMUX") != "", os.Getenv("STY") != "",
		os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "")
	fmt.Fprintf(os.Stderr, "ghs probe: raw=%q\n", buf)
	fmt.Fprintf(os.Stderr, "ghs probe: parsed kittyOK=%v daSeen=%v cell=%dx%d win=%dx%d\n",
		pr.kittyOK, pr.daSeen, pr.cellW, pr.cellH, pr.winW, pr.winH)
}

func populateEnvHints(caps *Capabilities) {
	caps.InTmux = os.Getenv("TMUX") != ""
	caps.InScreen = os.Getenv("STY") != ""
	caps.OverSSH = os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != ""
	if ct := os.Getenv("COLORTERM"); ct == "truecolor" || ct == "24bit" {
		caps.TruecolorOK = true
	}
}

func parseProbe(buf []byte) probeResult {
	var pr probeResult
	pr.kittyOK = bytes.Contains(buf, []byte("_Gi=31;OK"))
	pr.daSeen = reDA.Match(buf)
	if m := reCellPx.FindSubmatch(buf); m != nil {
		pr.cellH, _ = strconv.Atoi(string(m[1]))
		pr.cellW, _ = strconv.Atoi(string(m[2]))
	}
	if m := reWinPx.FindSubmatch(buf); m != nil {
		pr.winH, _ = strconv.Atoi(string(m[1]))
		pr.winW, _ = strconv.Atoi(string(m[2]))
	}
	return pr
}

// resolveCellGeometry fills in CellWidthPx/CellHeightPx, preferring the
// direct CSI 16 t reply. Some terminals don't answer 16 t but do answer
// 14 t (whole window size in pixels); when that's all we have, and we also
// know the cell grid size from TIOCGWINSZ, cell size is derived by
// division.
func resolveCellGeometry(caps *Capabilities, pr probeResult) {
	switch {
	case pr.cellW > 0 && pr.cellH > 0:
		caps.CellWidthPx, caps.CellHeightPx = pr.cellW, pr.cellH
	case pr.winW > 0 && pr.winH > 0 && caps.ColsCells > 0 && caps.RowsCells > 0:
		caps.CellWidthPx = pr.winW / caps.ColsCells
		caps.CellHeightPx = pr.winH / caps.RowsCells
	}
}

func decideProtocol(caps *Capabilities, pr probeResult) {
	if caps.InTmux {
		// Whether tmux actually forwarded our probe to the outer terminal is
		// exactly what the DA fence tells us: if allow-passthrough is off,
		// tmux drops the wrapped DCS passthrough sequence instead of
		// forwarding it, so nothing at all comes back within the probe
		// budget and daSeen stays false.
		caps.TmuxPassthroughOK = pr.daSeen
		if !caps.TmuxPassthroughOK {
			caps.Protocol = ProtocolExternal
			caps.Reason = "tmux detected but graphics passthrough is not enabled (run: tmux set -g allow-passthrough on)"
			return
		}
		if pr.kittyOK {
			caps.Protocol = ProtocolKitty
			caps.Reason = "kitty graphics protocol confirmed through verified tmux passthrough"
			return
		}
		if isITermLikeEnv() {
			caps.Protocol = ProtocolITerm2
			caps.Reason = "iTerm2 inline image protocol assumed from TERM_PROGRAM/LC_TERMINAL, through verified tmux passthrough"
			return
		}
		caps.Protocol = ProtocolExternal
		caps.Reason = "tmux passthrough verified but neither kitty graphics nor a known iTerm2-protocol terminal was detected"
		return
	}

	if !pr.daSeen {
		caps.Protocol = ProtocolExternal
		caps.Reason = "terminal did not answer the capability probe in time"
		return
	}
	if pr.kittyOK {
		caps.Protocol = ProtocolKitty
		caps.Reason = "kitty graphics protocol query succeeded"
		return
	}
	if os.Getenv("TERM_PROGRAM") == "ghostty" {
		// Ghostty documents native kitty-graphics support, and the DA fence
		// above proves this exact channel round-trips escape queries — so a
		// missing kitty OK here means Ghostty ignored that one query, not
		// that graphics are unavailable. An unrecognized kitty escape is
		// ignored by terminals, not rendered as garbage, so trusting the
		// vendor here fails safe. (Ghostty is deliberately NOT an iTerm2-
		// protocol signal: it does not implement OSC 1337.)
		caps.Protocol = ProtocolKitty
		caps.Reason = "Ghostty implements the kitty graphics protocol; probe channel verified live via device-attributes fence"
		return
	}
	if isITermLikeEnv() {
		caps.Protocol = ProtocolITerm2
		caps.Reason = "kitty graphics unsupported; falling back to iTerm2 inline images based on TERM_PROGRAM/LC_TERMINAL"
		return
	}
	caps.Protocol = ProtocolExternal
	caps.Reason = "no supported inline image protocol detected"
}

// isITermLikeEnv reports whether environment variables suggest a terminal
// that implements iTerm2's inline image protocol. There is no capability
// query for OSC 1337 the way there is for kitty graphics, so — same as
// every other tool in this space (e.g. chafa, timg) — this has to fall
// back to env hints.
//
// WezTerm is included here deliberately: per its own documentation it
// implements the iTerm2 protocol (and only a subset of kitty's), but we
// still only reach this function after the live kitty probe already came
// back negative, so this never overrides an actual working kitty
// connection — it only fills in the case the probe correctly reported.
//
// TERM_PROGRAM=="ghostty" is deliberately *not* treated as an iTerm2-
// protocol signal: Ghostty implements the kitty graphics protocol
// natively, so a real Ghostty session should already have matched
// pr.kittyOK above. Assuming iTerm2-protocol support for it here would be
// wrong (Ghostty does not implement OSC 1337) and would only mask a kitty
// probe that failed for some other reason (e.g. nested through something
// that ate the reply).
func isITermLikeEnv() bool {
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm":
		return true
	}
	return os.Getenv("LC_TERMINAL") == "iTerm2"
}
