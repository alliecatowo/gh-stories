//go:build windows

package terminal

import (
	"context"
	"errors"
	"os"
)

// runProbe never runs an interactive probe on Windows. golang.org/x/term's
// raw-mode support and the escape-sequence replies this package parses are
// designed and tested against unix ptys; ConPTY's behavior for these
// specific queries (kitty graphics query, CSI 14 t/16 t) has not been
// verified here. Rather than guess and risk leaving a Windows console in a
// bad state, Detect always falls back to the honest External renderer on
// Windows by treating this as a probe failure.
func runProbe(_ context.Context, _, _ *os.File, _ bool) ([]byte, error) {
	return nil, errors.New("interactive terminal graphics probing is not supported on windows")
}
