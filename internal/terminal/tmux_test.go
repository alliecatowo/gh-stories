package terminal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTmuxPassthrough_WrapsAndDoublesEscapes(t *testing.T) {
	payload := []byte("\x1b_Ga=T,f=32\x1b\\")
	got := tmuxPassthrough(payload)

	assert.True(t, len(got) > len(payload), "wrapped payload must be larger than the input")
	assert.Equal(t, []byte("\x1bPtmux;"), got[:7], "must be bracketed by ESC P tmux;")
	assert.Equal(t, []byte("\x1b\\"), got[len(got)-2:], "must end with ST (ESC \\)")

	// Every ESC byte inside the original payload must appear doubled in the
	// wrapped body (excluding the wrapper's own leading ESC P and trailing
	// ESC \, which are the envelope, not doubled payload content).
	body := got[7 : len(got)-2]
	wantEscCount := 0
	for _, b := range payload {
		if b == 0x1b {
			wantEscCount++
		}
	}
	gotEscCount := 0
	for _, b := range body {
		if b == 0x1b {
			gotEscCount++
		}
	}
	assert.Equal(t, wantEscCount*2, gotEscCount, "every inner ESC must be doubled")
}

func TestTmuxPassthrough_ExactBytesForKnownInput(t *testing.T) {
	got := tmuxPassthrough([]byte("A\x1bB"))
	want := []byte("\x1bPtmux;A\x1b\x1bB\x1b\\")
	assert.Equal(t, want, got)
}

func TestTmuxPassthrough_NoEscapesInPayload(t *testing.T) {
	got := tmuxPassthrough([]byte("plain text"))
	assert.Equal(t, []byte("\x1bPtmux;plain text\x1b\\"), got)
}
