//go:build !windows

package terminal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/term"
)

// probeBudget bounds how long Detect will wait for a terminal to answer the
// capability probe. This runs on every `gh stories` invocation against a
// real TTY: a slow or non-responding terminal must never make the CLI feel
// hung, but cutting Ghostty off mid-reply would be worse than the wait —
// half a second worst-case, only on runs where nothing answers at all.
const probeBudget = 500 * time.Millisecond

// probePayload is the escape sequence Detect writes to discover terminal
// capabilities in one round trip:
//   - CSI 16 t (`\x1b[16t`) asks for the cell size in pixels directly.
//   - CSI 14 t (`\x1b[14t`) asks for the whole window size in pixels, used
//     to derive cell size (together with TIOCGWINSZ's cols/rows) when a
//     terminal answers 14 t but not 16 t.
//   - The kitty graphics query (`a=q`, a 1x1 throwaway payload) asks
//     whether the terminal implements the kitty graphics protocol without
//     displaying anything.
//   - Primary Device Attributes (`\x1b[c`) is the fence. Virtually every
//     terminal answers DA, which is what makes it useful here: its arrival
//     is how Detect knows the terminal is done responding to everything
//     that came before it, including the queries above that a
//     non-supporting terminal would otherwise just silently ignore
//     forever. Without this fence, a terminal that ignores the kitty query
//     would make Detect hang until the probe budget expires on every
//     single run.
var probePayload = []byte("\x1b[16t\x1b[14t\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[c")

// runProbe puts in into raw mode, writes the capability probe (wrapped in
// tmux passthrough when inTmux, since tmux would otherwise swallow the
// kitty graphics query before it ever reached the outer terminal), and
// reads whatever comes back within probeBudget (or ctx's deadline, if
// sooner).
//
// Terminal state is always restored, and any read deadline set on in is
// always cleared, before returning on every path — success, error, or
// timeout — so a failed or partial probe can never leave the terminal
// stuck in raw mode or stdin stuck with a stale deadline for the rest of
// the program's life.
func runProbe(ctx context.Context, in, out *os.File, inTmux bool) ([]byte, error) {
	oldState, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return nil, err
	}
	defer term.Restore(int(in.Fd()), oldState)
	defer in.SetReadDeadline(time.Time{})

	start := time.Now()
	payload := probePayload
	if inTmux {
		payload = tmuxPassthrough(payload)
	}
	if _, err := out.Write(payload); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(probeBudget)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	var buf bytes.Buffer
	chunk := make([]byte, 256)
	fenceAt := time.Time{}
	for time.Now().Before(deadline) {
		if err := in.SetReadDeadline(deadline); err != nil {
			// This fd does not support read deadlines (unusual for a real
			// TTY). Stop rather than risk a Read that could block forever.
			break
		}
		n, err := in.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
			if reDA.Match(buf.Bytes()) {
				// The fence replied: nothing more is coming for this probe.
				// Whatever bytes arrived after it (if any) were read into
				// chunk but never appended past this point, and any bytes
				// still sitting in the kernel's TTY input queue are left
				// there — never handed back to the caller as if they were
				// real keystrokes.
				fenceAt = time.Now()
				break
			}
		}
		if err != nil {
			break
		}
	}
	if os.Getenv("GHS_DEBUG_PROBE") != "" {
		elapsed := time.Since(start)
		if !fenceAt.IsZero() {
			elapsed = fenceAt.Sub(start)
		}
		fmt.Fprintf(os.Stderr, "ghs probe: fence after %v, %d byte(s)\n",
			elapsed.Round(time.Millisecond), buf.Len())
	}
	drainLateReplies(in)
	return buf.Bytes(), nil
}

// drainLateReplies discards probe replies that arrive after the budget (a
// slow terminal can answer DA seconds later). Without this, those bytes sit
// in the TTY input queue and the shell reprints them as garbage — and can
// even execute fragments of them — the moment this process exits.
func drainLateReplies(in *os.File) {
	_ = in.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	var chunk [256]byte
	for {
		n, err := in.Read(chunk[:])
		if n <= 0 || err != nil {
			break
		}
	}
}
