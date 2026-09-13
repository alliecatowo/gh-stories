#!/usr/bin/env bash
# mise run test:e2e — browser extension and cross-client behavioural scenarios.
#
# An MV3 extension cannot be loaded by a plain headless browser: Playwright
# requires a PERSISTENT Chromium context with --load-extension. Testing the
# shared React UI in a normal page would not exercise extension permissions,
# background messaging, GitHub injection or the OAuth handoff, so it is not a
# substitute and this script does not pretend otherwise.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

EXT_OUT="$GHS_ROOT/apps/web-extension/.output/chrome-mv3"
if [ ! -d "$EXT_OUT" ]; then
  ghs_info "building the extension first"
  ghs_mise pnpm --filter @gh-stories/web-extension exec wxt build -b chrome >/dev/null
fi
[ -f "$EXT_OUT/manifest.json" ] || ghs_die "no built extension at $EXT_OUT"

if [ ! -d "$GHS_ROOT/tests/e2e/node_modules" ]; then
  ghs_die "Playwright is not installed — run: mise run bootstrap"
fi

export GHS_EXTENSION_PATH="$EXT_OUT"
export GHS_SERVICE_URL="${GHS_SERVICE_URL:-http://localhost:8787}"

ghs_step "Playwright (persistent Chromium context with the built extension)"
ghs_mise pnpm --filter @gh-stories/e2e exec playwright test "$@"
