package tui

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/alliecatowo/gh-stories/internal/terminal"
)

// Run starts the viewer and blocks until the user exits.
//
// Terminal state — cursor, input mode, alternate screen, mouse reporting and
// any placed graphics — is restored on EVERY exit path, including a panic, an
// error, and Ctrl-C. A Story viewer that leaves the terminal broken is worse
// than one that does not run.
func Run(ctx context.Context, m *Model) (err error) {
	defer func() {
		// Belt and braces: even if Bubble Tea's own restore fails, put the
		// terminal back into a sane state and remove any lingering graphic.
		var b []byte
		if m.renderer != nil && m.lastDrawn != 0 {
			var sb clearBuf
			_ = m.renderer.Clear(&sb, m.lastDrawn)
			b = sb.b
		}
		b = append(b, []byte("\x1b[?25h\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l")...)
		_, _ = os.Stdout.Write(b)
		if r := recover(); r != nil {
			err = fmt.Errorf("viewer crashed: %v", r)
		}
	}()

	p := tea.NewProgram(m,
		tea.WithContext(ctx),
		tea.WithAltScreen(),
		// Mouse reporting is deliberately off: it would capture scroll and
		// selection, and the viewer is fully keyboard-driven.
	)
	_, err = p.Run()
	return err
}

type clearBuf struct{ b []byte }

func (c *clearBuf) Write(p []byte) (int, error) { c.b = append(c.b, p...); return len(p), nil }

// Capabilities re-exports the detected terminal capabilities for callers that
// want to report them (for example `gh stories doctor`).
func Capabilities(m *Model) terminal.Capabilities { return m.caps }
