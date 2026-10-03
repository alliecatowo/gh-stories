package terminal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCapCacheRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TERM_PROGRAM", "ghostty")
	caps := &Capabilities{Protocol: ProtocolKitty, Reason: "test", CellWidthPx: 10, CellHeightPx: 20}
	storeCapCache(caps, probeResult{daSeen: true})
	hit := loadCapCache()
	require.NotNil(t, hit)
	require.Equal(t, ProtocolKitty, hit.Protocol)
	require.Equal(t, 10, hit.CellWidthPx)
}

func TestCapCacheRejectsTimeoutAndMismatch(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	caps := &Capabilities{Protocol: ProtocolKitty, Reason: "test"}
	storeCapCache(caps, probeResult{}) // no fence -> must not store
	require.Nil(t, loadCapCache())
	storeCapCache(caps, probeResult{daSeen: true})
	t.Setenv("TERM_PROGRAM", "other")
	require.Nil(t, loadCapCache(), "env change busts the cache")
}

func TestCapCacheSkipsExternal(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	storeCapCache(&Capabilities{Protocol: ProtocolExternal}, probeResult{daSeen: true})
	require.Nil(t, loadCapCache(), "external verdicts are re-probed, not cached")
}
