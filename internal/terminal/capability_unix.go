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
// Reads are bounded by a timer, never by socket-style read deadlines: Go
// read deadlines do not work on terminal ptys on macOS ("file type does not
// support deadline"), so a probe built on them never waits at all and
// reports every Mac terminal — Ghostty included — as unresponsive. A
// blocked reader goroutine is abandoned on timeout instead; the CLI is
// short-lived, and one wedged goroutine per process is the documented cost
// of a bounded read on an fd the runtime cannot poll.
//
// Terminal state is always restored before returning on every path —
// success, error, or timeout — so a failed or partial probe can never leave
// the terminal stuck in raw mode for the rest of the program's life.
func runProbe(ctx context.Context, in, out *os.File, inTmux bool) ([]byte, error) {
	oldState, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return nil, err
	}
	defer term.Restore(int(in.Fd()), oldState)

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

	// One reader goroutine feeds chunks until the fence matches or the
	// budget runs out. It is abandoned, never joined, on timeout (see the
	// package comment above for why that is safe here).
	type chunk struct {
		b   []byte
		err error
	}
	ch := make(chan chunk, 32)
	go func() {
		defer close(ch)
		tmp := make([]byte, 256)
		for {
			n, err := in.Read(tmp)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, tmp[:n])
				ch <- chunk{b: cp}
			}
			if err != nil {
				ch <- chunk{err: err}
				return
			}
		}
	}()

	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	var buf bytes.Buffer
	fenceAt := time.Time{}
done:
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				break done
			}
			if len(c.b) > 0 {
				buf.Write(c.b)
				if reDA.Match(buf.Bytes()) {
					// The fence replied: nothing more is coming for this
					// probe. Bytes already pulled off the fd but arriving
					// after the fence stay out of the caller's way; the
					// drain below handles anything still queued.
					fenceAt = time.Now()
					break done
				}
			}
			if c.err != nil {
				break done
			}
		case <-timer.C:
			break done
		case <-ctx.Done():
			break done
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
//
// The drain is timer-bounded like the probe itself and never touches read
// deadlines, for the same macOS-ptys reason documented above.
func drainLateReplies(in *os.File) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		var tmp [256]byte
		for {
			n, err := in.Read(tmp[:])
			if n <= 0 || err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
	}
}
