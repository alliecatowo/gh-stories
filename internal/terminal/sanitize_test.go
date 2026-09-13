package terminal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitize_HostileInputs(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"clear-screen csi", "before\x1b[2Jafter"},
		{"set title osc", "before\x1b]0;pwned\x07after"},
		{"osc8 hyperlink", "\x1b]8;;http://evil\x1b\\click\x1b]8;;\x1b\\"},
		{"dcs sequence", "before\x1bPsome-dcs-body\x1b\\after"},
		{"apc sequence", "before\x1b_Gsome-apc-body\x1b\\after"},
		{"cr overwrite", "real-message\rFAKE MESSAGE"},
		{"rtl override", "safe‮evil.exe"},
		{"raw c1 csi byte", "before\x9bmiddle"},
		{"nul byte", "a\x00b"},
		{"lone esc at end", "trailing\x1b"},
		{"bell", "ping\x07pong"},
		{"backspace", "abc\x08def"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := Sanitize(tc.input)
			assert.NotContains(t, out, "\x1b", "ESC must never survive sanitization")
			for _, r := range out {
				assert.False(t, r >= 0x80 && r <= 0x9f, "C1 control character %U must not survive", r)
			}
			assert.NotContains(t, out, "\x1b[", "no CSI introducer may survive")
			assert.NotContains(t, out, "\x1b]", "no OSC introducer may survive")
			assert.Contains(t, out, "�", "removed control content should leave a visible replacement marker")
		})
	}
}

func TestSanitize_PreservesOrdinaryUnicode(t *testing.T) {
	in := "héllo 世界 🎉 café"
	out := Sanitize(in)
	assert.Equal(t, in, out, "ordinary unicode text must survive unchanged")
}

func TestSanitize_TabsBecomeSpaces(t *testing.T) {
	out := Sanitize("a\tb")
	assert.Equal(t, "a b", out)
}

func TestSanitize_NewlineReplacedByDefault(t *testing.T) {
	out := Sanitize("line1\nline2")
	assert.NotContains(t, out, "\n")
	assert.Contains(t, out, "�")
}

func TestSanitizeMultiline_PreservesNewlineButStripsOtherControls(t *testing.T) {
	out := SanitizeMultiline("line1\nline2\x1b[2Jline3\r\n")
	assert.True(t, strings.Contains(out, "line1\nline2"))
	assert.NotContains(t, out, "\x1b")
	assert.NotContains(t, out, "\r")
}

func TestSanitize_BidiOverrideCharacters(t *testing.T) {
	for _, r := range []rune{0x202A, 0x202B, 0x202C, 0x202D, 0x202E, 0x2066, 0x2067, 0x2068, 0x2069} {
		out := Sanitize("x" + string(r) + "y")
		assert.NotContains(t, out, string(r))
	}
}

func TestSanitizeTruncate_LimitsDisplayWidth(t *testing.T) {
	out := SanitizeTruncate("this is a fairly long caption that should be cut", 10)
	assert.LessOrEqual(t, len([]rune(out)), 10)
}

func TestSanitizeTruncate_StripsEscapesBeforeMeasuring(t *testing.T) {
	// An injected escape sequence must not be able to smuggle extra
	// "invisible" width past the limit or corrupt the truncation point.
	out := SanitizeTruncate("abc\x1b[31mdef\x1b[0mghi", 6)
	require.NotContains(t, out, "\x1b")
}

func TestSanitizeTruncate_ZeroOrNegativeWidth(t *testing.T) {
	assert.Equal(t, "", SanitizeTruncate("hello", 0))
	assert.Equal(t, "", SanitizeTruncate("hello", -5))
}
