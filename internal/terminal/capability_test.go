package terminal

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetect_NonTTY_ReturnsExternalQuickly(t *testing.T) {
	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	defer inR.Close()
	defer inW.Close()
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	defer outR.Close()
	defer outW.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	caps, err := Detect(ctx, inR, outW, "")
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, ProtocolExternal, caps.Protocol)
	assert.NotEmpty(t, caps.Reason)
	assert.Less(t, elapsed, 500*time.Millisecond, "Detect must not hang or run a probe against a non-TTY")
}

func TestDetect_NilFiles_ReturnsExternal(t *testing.T) {
	caps, err := Detect(context.Background(), nil, nil, "")
	require.NoError(t, err)
	assert.Equal(t, ProtocolExternal, caps.Protocol)
	assert.NotEmpty(t, caps.Reason)
}

func TestDetect_OverrideWinsImmediatelyEvenOnNonTTY(t *testing.T) {
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	defer outR.Close()
	defer outW.Close()

	for _, override := range []Protocol{ProtocolKitty, ProtocolITerm2, ProtocolExternal} {
		caps, err := Detect(context.Background(), nil, outW, override)
		require.NoError(t, err)
		assert.Equal(t, override, caps.Protocol)
		assert.NotEmpty(t, caps.Reason)
	}
}

func TestDetect_PopulatesEnvHintsEvenOnNonTTYPath(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	t.Setenv("STY", "12345.pts-0.host")
	t.Setenv("SSH_CONNECTION", "1.2.3.4 1 5.6.7.8 22")
	t.Setenv("COLORTERM", "truecolor")

	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	defer outR.Close()
	defer outW.Close()

	caps, err := Detect(context.Background(), nil, outW, "")
	require.NoError(t, err)
	assert.True(t, caps.InTmux)
	assert.True(t, caps.InScreen)
	assert.True(t, caps.OverSSH)
	assert.True(t, caps.TruecolorOK)
}
