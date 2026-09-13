package terminal

import "bytes"

// tmuxPassthrough wraps payload in tmux's DCS passthrough envelope so escape
// sequences meant for the *outer* terminal survive being sent to a tmux
// client instead of being interpreted (or silently dropped) by tmux itself.
//
// tmux's DCS parser treats an unescaped ESC inside the passthrough body as
// the end of the DCS string, so every literal ESC byte in payload must be
// doubled (0x1b -> 0x1b 0x1b) for tmux to reassemble the original bytes and
// forward them on. Skipping this doubling is the single most common cause of
// corrupted graphics inside tmux: the sequence gets truncated at the first
// inner ESC and tmux (or the outer terminal) is left parsing a fragment.
//
// Callers must only invoke this after confirming Capabilities.TmuxPassthroughOK
// — wrapping a sequence does not by itself guarantee tmux has
// `allow-passthrough` enabled; an unverified wrap sent into a tmux session
// without passthrough enabled is simply dropped (or printed as garbage) by
// tmux, which is why this package never does that (see kitty.go, iterm.go).
func tmuxPassthrough(payload []byte) []byte {
	escaped := bytes.ReplaceAll(payload, []byte{0x1b}, []byte{0x1b, 0x1b})
	out := make([]byte, 0, len(escaped)+8)
	out = append(out, 0x1b, 'P')
	out = append(out, "tmux;"...)
	out = append(out, escaped...)
	out = append(out, 0x1b, '\\')
	return out
}
