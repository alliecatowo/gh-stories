package terminal

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// replacementChar marks where untrusted input contained something we
// refused to print verbatim. We replace rather than delete so the caller
// (and anyone debugging garbled output) can see that something was there
// and removed, instead of text silently going missing.
const replacementChar = '�'

// maxEscapeScan bounds how far Sanitize will scan looking for the
// terminator of an apparent escape or control-string sequence (CSI, OSC,
// DCS, APC, PM, SOS). Untrusted input might contain an ESC or C1 introducer
// with no terminator at all (truncated, malicious, or simply corrupt); this
// cap guarantees Sanitize never performs an unbounded scan hunting for one.
const maxEscapeScan = 4096

// Sanitize makes arbitrary untrusted text (captions, usernames, filenames,
// server error messages, API replies, ...) safe to print in a terminal on a
// single line.
//
// It neutralises ESC (0x1b) and every escape/control-string sequence it
// introduces (CSI, OSC — including the OSC 8 hyperlink sequence, DCS, APC,
// PM, SOS), all C0 and C1 control characters (including BEL, backspace,
// carriage return, vertical tab, form feed, and newline), DEL, and
// bidirectional text-override characters (U+202A-U+202E, U+2066-U+2069).
// Each neutralised run is replaced with a single U+FFFD rather than being
// silently dropped. Tabs become a single space. Ordinary printable Unicode,
// including multi-byte scripts and emoji, passes through unchanged.
//
// Sequences that this package itself needs to emit to actually drive a
// terminal (kitty/iTerm2 graphics escapes, cursor movement for iTerm2's
// Clear, tmux passthrough wrapping) are constructed directly as byte
// slices elsewhere in this package and must never be built by concatenating
// Sanitize's output — Sanitize's entire job is to guarantee its output
// contains no bytes a terminal would act on.
func Sanitize(s string) string {
	return sanitize(s, false)
}

// SanitizeMultiline is like Sanitize but preserves caller-intended line
// breaks: a bare LF (0x0A) is passed through as a real newline instead of
// being replaced. Every other control character and escape sequence is
// still neutralised exactly as in Sanitize, including CR (so a Windows-style
// CRLF becomes a single LF, not a line-overwrite CR followed by a newline).
func SanitizeMultiline(s string) string {
	return sanitize(s, true)
}

// SanitizeTruncate sanitizes s and then truncates it to fit within maxWidth
// terminal cells, as measured by github.com/charmbracelet/x/ansi (which
// accounts for wide CJK/emoji glyphs). Because the sanitized text is
// guaranteed free of escape sequences, the width measurement is a plain,
// reliable cell count with no risk of an escape sequence being mistaken for
// visible width or, worse, being split by truncation.
func SanitizeTruncate(s string, maxWidth int) string {
	clean := Sanitize(s)
	if maxWidth <= 0 {
		return ""
	}
	if ansi.StringWidth(clean) <= maxWidth {
		return clean
	}
	return ansi.Truncate(clean, maxWidth, "…")
}

func sanitize(s string, allowNewline bool) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	n := len(s)
	for i < n {
		c := s[i]
		switch {
		case c == 0x1b: // ESC: possibly the start of CSI/OSC/DCS/APC/PM/SOS.
			b.WriteRune(replacementChar)
			i += skipEscape(s, i)
		case c == '\t':
			b.WriteByte(' ')
			i++
		case c == '\n' && allowNewline:
			b.WriteByte('\n')
			i++
		case c < 0x20 || c == 0x7f:
			// Every other C0 control character (NUL, BEL, BS, LF-when-not-
			// allowed, VT, FF, CR, ...) plus DEL.
			b.WriteRune(replacementChar)
			i++
		case c >= 0x80 && c <= 0x9f:
			// A raw C1 control byte. In valid UTF-8 this only appears as the
			// lead byte of a 2-byte encoding of U+0080-U+009F, which is
			// itself a C1 control character being represented in text —
			// still not something we print. Some C1 codes (0x90 DCS, 0x98
			// SOS, 0x9b CSI, 0x9c ST, 0x9d OSC, 0x9e PM, 0x9f APC) are
			// themselves control-string introducers, so treat them exactly
			// like their ESC-prefixed two-byte equivalents.
			b.WriteRune(replacementChar)
			i += skipC1(s, i)
		case c < 0x80:
			b.WriteByte(c)
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size <= 1 {
				// Invalid UTF-8 byte on its own; drop it behind a single
				// replacement rune rather than emitting invalid text.
				b.WriteRune(replacementChar)
				i++
				continue
			}
			if isBidiOverride(r) {
				b.WriteRune(replacementChar)
			} else {
				b.WriteRune(r)
			}
			i += size
		}
	}
	return b.String()
}

// skipEscape returns the number of bytes, starting at s[i] (which must be
// ESC), that make up one escape sequence: either a full CSI/OSC/DCS/APC/PM/
// SOS sequence (consumed up to and including its terminator, bounded by
// maxEscapeScan), a generic two-byte escape (ESC followed by any other
// byte, e.g. ESC c / RIS), or, if ESC is the very last byte of the input, a
// lone ESC.
func skipEscape(s string, i int) int {
	n := len(s)
	if i+1 >= n {
		return 1
	}
	switch s[i+1] {
	case '[', ']', 'P', '_', '^', 'X':
		return escapeSeqEnd(s, i+2, s[i+1]) - i
	default:
		return 2
	}
}

// skipC1 returns the number of bytes, starting at s[i] (a raw C1 control
// byte), that make up its sequence. Only the control-string introducers
// (CSI/OSC/DCS/APC/PM/SOS) have a body to consume; every other C1 byte is
// neutralised on its own.
func skipC1(s string, i int) int {
	var introducer byte
	switch s[i] {
	case 0x9b:
		introducer = '['
	case 0x9d:
		introducer = ']'
	case 0x90:
		introducer = 'P'
	case 0x9f:
		introducer = '_'
	case 0x9e:
		introducer = '^'
	case 0x98:
		introducer = 'X'
	default:
		return 1
	}
	return escapeSeqEnd(s, i+1, introducer) - i
}

// escapeSeqEnd scans the body of a control-string sequence whose introducer
// (one of '[' ']' 'P' '_' '^' 'X', meaning CSI/OSC/DCS/APC/PM/SOS
// respectively) has already been consumed, and returns the index of the
// first byte after the sequence's terminator.
//
// CSI sequences end at their first "final byte" (0x40-0x7E). The string-type
// sequences (OSC/DCS/APC/PM/SOS) end at ST (ESC \, or the single-byte C1
// form 0x9c); OSC additionally accepts a bare BEL as a terminator, which is
// what most real-world OSC 8 hyperlinks and window-title sequences use.
//
// Scanning is capped at maxEscapeScan bytes from i so a sequence that never
// terminates (truncated input, or an adversarial attempt to make Sanitize
// scan forever) cannot force an unbounded scan; if no terminator is found
// within the cap, the whole scanned span is treated as consumed.
func escapeSeqEnd(s string, i int, introducer byte) int {
	n := len(s)
	limit := n
	if limit > i+maxEscapeScan {
		limit = i + maxEscapeScan
	}
	switch introducer {
	case '[':
		j := i
		for j < limit {
			c := s[j]
			if c >= 0x40 && c <= 0x7e {
				return j + 1
			}
			j++
		}
		return limit
	default: // ']', 'P', '_', '^', 'X'
		j := i
		for j < limit {
			c := s[j]
			if c == 0x07 && introducer == ']' {
				return j + 1
			}
			if c == 0x1b && j+1 < limit && s[j+1] == '\\' {
				return j + 2
			}
			if c == 0x9c {
				return j + 1
			}
			j++
		}
		return limit
	}
}

// isBidiOverride reports whether r is one of the Unicode bidirectional
// text-control characters that can be used to visually disguise text (e.g.
// making a malicious filename display as if it had a different extension).
func isBidiOverride(r rune) bool {
	switch {
	case r >= 0x202a && r <= 0x202e:
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	default:
		return false
	}
}
