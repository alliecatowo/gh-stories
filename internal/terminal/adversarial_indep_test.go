package terminal_test

import (
	"fmt"
	"strings"

	"testing"

	"github.com/alliecatowo/gh-stories/internal/terminal"
)

func TestAdversarialSanitize(t *testing.T) {
	cases := map[string]string{
		"clear screen":     "\x1b[2Jgotcha",
		"set window title": "\x1b]0;pwned\x07hi",
		"OSC8 hyperlink":   "\x1b]8;;http://evil.example\x1b\\click me\x1b]8;;\x1b\\",
		"DCS payload":      "\x1bPq#0;2;0;0;0#0~~\x1b\\",
		"APC":              "\x1b_Gf=100,a=T;AAAA\x1b\\",
		"CR overwrite":     "safe text\rEVIL",
		"C1 CSI raw byte":  "\x9b31mred",
		"C1 OSC raw byte":  "\x9d0;title\x9c",
		"bidi override":    "annexe‮gnp.exe",
		"bidi isolate":     "a⁦b⁩c",
		"NUL + BEL + DEL":  "a\x00b\x07c\x7fd",
		"backspace":        "abc\b\b\bXYZ",
		"nested ESC":       "\x1b\x1b[31m",
		"kitty delete all": "\x1b_Ga=d\x1b\\",
		"tmux passthrough": "\x1bPtmux;\x1b\x1b[2J\x1b\\",
		"legit unicode":    "café 日本語 🐈 naïve — ok",
		"legit newline":    "line1\nline2",
	}
	bad := 0
	for name, in := range cases {
		out := terminal.Sanitize(in)
		problems := []string{}
		if strings.ContainsRune(out, 0x1b) {
			problems = append(problems, "ESC survived")
		}
		for _, r := range out {
			if r < 0x20 && r != '\n' && r != '\t' {
				problems = append(problems, fmt.Sprintf("C0 %#x survived", r))
			}
			if r >= 0x80 && r <= 0x9f {
				problems = append(problems, fmt.Sprintf("C1 %#x survived", r))
			}
			if (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
				problems = append(problems, "bidi override survived")
			}
			if r == 0x7f {
				problems = append(problems, "DEL survived")
			}
		}
		status := "OK  "
		if len(problems) > 0 {
			status = "FAIL"
			bad++
		}
		fmt.Printf("%s %-18s %-40q %s\n", status, name, out, strings.Join(problems, ","))
	}
	// single-line Sanitize must not emit newlines
	if strings.Contains(terminal.Sanitize("a\nb"), "\n") {
		fmt.Println("FAIL Sanitize (single-line) leaked a newline")
		bad++
	}
	if bad > 0 {
		t.Fatalf("%d hostile inputs were not neutralised", bad)
	}
}
