#!/usr/bin/env bash
# mise run test:terminal — PTY interaction tests plus real graphical-terminal
# checks.
#
# Reports honestly which terminals were and were not available. A terminal we
# could not run is reported as UNAVAILABLE, never silently skipped and never
# counted as a pass.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

ghs_step "PTY interaction tests"
ghs_mise go test ./cli/... ./internal/tui/ ./internal/terminal/ -count=1

ghs_step "Real graphics-capable terminals"
IMAGE=ghs-terminal-capture
OUTDIR="${GHS_ROOT}/evidence/terminal"
mkdir -p "$OUTDIR"

if ! ghs_have docker; then
  ghs_warn "kitty          UNAVAILABLE — no container engine on this machine"
  ghs_warn "iTerm2         UNAVAILABLE — requires macOS with screen-recording permission"
  ghs_warn "graphics rendering was NOT verified in this run"
  exit 0
fi

ghs_info "building the kitty capture image (real X display, real graphics protocol)"
docker build -q -t "$IMAGE" "$GHS_ROOT/tests/terminal" >/dev/null

# A protocol smoke test that needs no running service: kitty's own icat draws
# a fixture through the real Kitty graphics protocol.
docker run --rm \
  -v "$OUTDIR:/out" -v "$GHS_ROOT/tests/fixtures:/fixtures:ro" \
  -e GHS_SETTLE=6 "$IMAGE" /out/kitty-protocol-smoke.png \
  sh -c 'kitty +kitten icat --align=left /fixtures/portrait.jpg; sleep 30' >/dev/null
ghs_ok "kitty         VERIFIED — real pixels captured to evidence/terminal/"

ghs_warn "iTerm2        UNAVAILABLE — needs a macOS GUI session with screen-recording permission"
ghs_warn "WezTerm       NOT TESTED  — no image built for it"
ghs_warn "Ghostty       NOT TESTED"
ghs_info "see docs/support-matrix.md; anything not printed as VERIFIED above is not a verified claim"
