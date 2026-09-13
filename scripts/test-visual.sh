#!/usr/bin/env bash
# mise run test:visual — reproducible visual capture and comparison.
#
# Snapshots are NEVER auto-updated. A diff fails the run; a human has to look
# at it and pass --update deliberately.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

UPDATE=0
CAPTURE_ONLY=0
for arg in "$@"; do
  case "$arg" in
    --update)  UPDATE=1 ;;
    --capture) CAPTURE_ONLY=1 ;;
  esac
done

if [ ! -d "$GHS_ROOT/tests/e2e/node_modules" ]; then
  ghs_die "Playwright is not installed — run: mise run bootstrap"
fi

EXT_OUT="$GHS_ROOT/apps/web-extension/.output/chrome-mv3"
[ -d "$EXT_OUT" ] || ghs_mise pnpm --filter @gh-stories/web-extension exec wxt build -b chrome >/dev/null
export GHS_EXTENSION_PATH="$EXT_OUT"
export GHS_SERVICE_URL="${GHS_SERVICE_URL:-http://localhost:8787}"

ARGS=(--grep @visual)
if [ "$CAPTURE_ONLY" -eq 1 ]; then ARGS+=(--update-snapshots); fi
if [ "$UPDATE" -eq 1 ]; then
  ghs_warn "updating visual snapshots — you are asserting you LOOKED at the differences"
  ARGS+=(--update-snapshots)
fi

ghs_step "Visual scenarios"
ghs_mise pnpm --filter @gh-stories/e2e exec playwright test "${ARGS[@]}"
ghs_ok "visual run complete — inspect evidence/browser/ before trusting it"
