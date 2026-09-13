#!/usr/bin/env bash
# mise run capture — regenerate reviewable screenshots from the running product.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

MEDIA="$GHS_ROOT/docs/media"
EVIDENCE="$GHS_ROOT/evidence"
mkdir -p "$MEDIA" "$EVIDENCE/terminal" "$EVIDENCE/browser"

ghs_step "Terminal captures (real kitty, real X display, real pixels)"
bash "$GHS_ROOT/scripts/test-terminal.sh"

ghs_step "Browser captures"
if [ -d "$GHS_ROOT/tests/e2e/node_modules" ] || ghs_have npx; then
  bash "$GHS_ROOT/scripts/test-visual.sh" --capture || \
    ghs_warn "browser capture did not complete; see evidence/visual-report.md"
else
  ghs_warn "Playwright is not installed — run: mise run bootstrap"
fi

ghs_ok "captures written to docs/media/ and evidence/"
ghs_info "now OPEN them and look. A capture nobody inspected proves nothing."
