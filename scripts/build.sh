#!/usr/bin/env bash
# mise run build — production builds of every component.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

VERSION="${GHS_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
PKG="github.com/alliecatowo/gh-stories/internal/version"
LDFLAGS="-s -w -X ${PKG}.Version=${VERSION} -X ${PKG}.Commit=${COMMIT} -X ${PKG}.BuildDate=${DATE}"
if [ -n "${GHS_DEFAULT_SERVICE_URL:-}" ]; then
  LDFLAGS="$LDFLAGS -X ${PKG}.DefaultServiceURL=${GHS_DEFAULT_SERVICE_URL}"
fi

mkdir -p "$GHS_ROOT/dist"

ghs_step "Go binaries ($VERSION)"
# Release builds NEVER carry the ghs_testidp tag: the demo sign-in route must
# not be compiled into a published artifact.
ghs_mise go build -trimpath -ldflags "$LDFLAGS" -o dist/gh-stories-api ./apps/api
ghs_mise go build -trimpath -ldflags "$LDFLAGS" -o dist/gh-stories-worker ./apps/worker
ghs_mise go build -trimpath -ldflags "$LDFLAGS" -o dist/gh-stories ./cli/gh-stories
# The root executable is what `gh extension install .` runs. It is generated
# and git-ignored; release distribution uses the published binaries instead.
cp dist/gh-stories "$GHS_ROOT/gh-stories"
ghs_ok "api, worker, cli, and the root gh-stories executable"

ghs_step "Browser extension"
for browser in chrome firefox edge; do
  ghs_mise pnpm --filter @gh-stories/web-extension exec wxt build -b "$browser" >/dev/null
  ghs_ok "extension: $browser"
done

ghs_step "Website"
ghs_mise pnpm --filter @gh-stories/site run build >/dev/null
ghs_ok "apps/site/dist"

ghs_ok "build complete — $VERSION ($COMMIT)"
