#!/usr/bin/env bash
# mise run site:preview — serve the production static output at the real
# project subpath, so links behave exactly as they will on GitHub Pages.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

PORT="${GHS_SITE_PORT:-4321}"
BASE="/gh-stories"
DIST="$GHS_ROOT/apps/site/dist"

[ -d "$DIST" ] || { ghs_info "building the site first"; ghs_mise pnpm --filter @gh-stories/site run build; }

SERVE_ROOT="$(mktemp -d)"
trap 'rm -rf "$SERVE_ROOT"' EXIT
mkdir -p "$SERVE_ROOT$BASE"
cp -R "$DIST"/. "$SERVE_ROOT$BASE/"

ghs_ok "serving apps/site/dist at http://localhost:${PORT}${BASE}/ (all interfaces, e.g. tailnet)"
ghs_info "a link that only works without the ${BASE} prefix is a release blocker"
cd "$SERVE_ROOT"
# Explicit all-interfaces bind so the preview is reachable over tailnet too.
ghs_mise python3 -m http.server --bind 0.0.0.0 "$PORT" 2>/dev/null || python3 -m http.server --bind 0.0.0.0 "$PORT"
