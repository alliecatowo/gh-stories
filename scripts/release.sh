#!/usr/bin/env bash
# mise run release -- vX.Y.Z
#
# Validates the version, refuses to overwrite an existing release, requires a
# clean tree and passing gates, then tags and pushes so CI publishes.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

VERSION="${1:-}"
shift || true
ASSUME_YES=0
for arg in "$@"; do [ "$arg" = "--yes" ] && ASSUME_YES=1; done

[ -n "$VERSION" ] || ghs_die "usage: mise run release -- vX.Y.Z [--yes]"
echo "$VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' \
  || ghs_die "version must look like v0.1.0, got: $VERSION"

ghs_step "Preflight"

[ -z "$(git status --porcelain)" ] || ghs_die "the working tree is dirty; commit or stash first"
ghs_ok "working tree is clean"

if git rev-parse "$VERSION" >/dev/null 2>&1; then
  ghs_die "tag $VERSION already exists — releases are never overwritten"
fi
if ghs_have gh && gh release view "$VERSION" >/dev/null 2>&1; then
  ghs_die "a release for $VERSION already exists — releases are never overwritten"
fi
ghs_ok "$VERSION does not already exist"

BRANCH="$(git rev-parse --abbrev-ref HEAD)"
[ "$BRANCH" = "main" ] || ghs_warn "releasing from '$BRANCH', not main"

ghs_step "Gates"
GHS_VERSION="$VERSION" bash "$GHS_ROOT/scripts/verify.sh" || ghs_die "gates failed; not releasing"

ghs_step "What will happen"
cat >&2 <<PLAN

  1. tag ${VERSION} at $(git rev-parse --short HEAD)
  2. push the tag to origin
  3. CI (.github/workflows/release.yml) then:
       - cross-compiles the CLI for 5 platforms with gh-recognised names
       - builds the browser extension archives
       - publishes the GitHub Release with checksums and provenance
       - pushes ghcr.io/alliecatowo/gh-stories:${VERSION} (amd64 + arm64)

  Nothing is published from this machine.

PLAN

if [ "$ASSUME_YES" -ne 1 ]; then
  if [ -t 0 ]; then
    printf 'Proceed? [y/N] ' >&2
    read -r answer
    case "$answer" in y|Y) ;; *) ghs_die "cancelled" ;; esac
  else
    ghs_die "not a terminal; re-run with --yes to proceed noninteractively"
  fi
fi

git tag -a "$VERSION" -m "GitHub Stories $VERSION"
git push origin "$VERSION"
ghs_ok "pushed $VERSION — watch: gh run watch"
