#!/usr/bin/env bash
# mise run package — release binaries, extension archives, checksums, metadata.
#
# GitHub CLI extension binary naming: `gh extension install OWNER/gh-NAME`
# looks for a release asset named `gh-NAME_<tag>_<os>-<arch>` (with `.exe` on
# Windows) and downloads that raw executable. It does NOT unpack archives, so
# the CLI must be published as bare executables using exactly that pattern.
# See https://cli.github.com/manual/gh_extension and the precompiled-extension
# documentation.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

VERSION="${GHS_VERSION:-$(git describe --tags --always 2>/dev/null || echo v0.0.0-dev)}"
COMMIT="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
PKG="github.com/alliecatowo/gh-stories/internal/version"
DIST="$GHS_ROOT/dist"

LDFLAGS="-s -w -X ${PKG}.Version=${VERSION} -X ${PKG}.Commit=${COMMIT} -X ${PKG}.BuildDate=${DATE}"
if [ -n "${GHS_DEFAULT_SERVICE_URL:-}" ]; then
  LDFLAGS="$LDFLAGS -X ${PKG}.DefaultServiceURL=${GHS_DEFAULT_SERVICE_URL}"
fi

rm -rf "$DIST"; mkdir -p "$DIST"

ghs_step "CLI binaries ($VERSION)"
# os/arch pairs, and the suffix `gh` expects for each.
build_one() {
  local goos="$1" goarch="$2" suffix="$3" ext="${4:-}"
  local out="$DIST/gh-stories_${VERSION}_${suffix}${ext}"
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
    ghs_mise go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cli/gh-stories
  ghs_ok "$(basename "$out")"
}
build_one linux  amd64 linux-amd64
build_one linux  arm64 linux-arm64
build_one darwin amd64 darwin-amd64
build_one darwin arm64 darwin-arm64
build_one windows amd64 windows-amd64 .exe

ghs_step "Browser extension archives"
for browser in chrome firefox edge; do
  ghs_mise pnpm --filter @gh-stories/web-extension exec wxt zip -b "$browser" >/dev/null
done
find "$GHS_ROOT/apps/web-extension/.output" -maxdepth 1 -name '*.zip' -print0 |
  while IFS= read -r -d '' zip; do
    base="$(basename "$zip")"
    cp "$zip" "$DIST/gh-stories-extension-${VERSION}-${base#*-}"
  done
ls "$DIST" | grep -c '\.zip$' >/dev/null && ghs_ok "extension archives"

ghs_step "Checksums"
( cd "$DIST" && shasum -a 256 * > checksums.txt 2>/dev/null || sha256sum * > checksums.txt )
ghs_ok "checksums.txt"

ghs_step "Metadata"
cat > "$DIST/metadata.json" <<JSON
{
  "version": "${VERSION}",
  "commit": "${COMMIT}",
  "built_at": "${DATE}",
  "default_service_url": "${GHS_DEFAULT_SERVICE_URL:-unset (binary default)}",
  "artifacts": [
$(cd "$DIST" && ls | grep -v metadata.json | sed 's/^/    "/;s/$/",/' | sed '$ s/,$//')
  ],
  "notes": "CLI binaries use the raw-executable naming that gh extension install expects. Extension archives are manual-install ZIPs, not store listings."
}
JSON
ghs_ok "metadata.json"

ghs_step "Production isolation of the packaged binaries"
# A published binary must not contain the demo sign-in route or a localhost
# service default. This checks the actual bytes, not the source.
BAD=0
for bin in "$DIST"/gh-stories_*; do
  case "$bin" in *.exe) ;; esac
  if strings "$bin" 2>/dev/null | grep -q '/v1/auth/test/login'; then
    ghs_err "$(basename "$bin") contains the demo sign-in route"; BAD=1
  fi
  if strings "$bin" 2>/dev/null | grep -qE 'DefaultServiceURL.*localhost|http://localhost:8787'; then
    ghs_err "$(basename "$bin") defaults to a localhost service"; BAD=1
  fi
done
[ "$BAD" -eq 0 ] || ghs_die "packaged artifacts failed the production isolation gate"
ghs_ok "no demo auth or localhost default in any packaged binary"

ghs_ok "packaged $VERSION into dist/"
ls -la "$DIST"
