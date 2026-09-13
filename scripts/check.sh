#!/usr/bin/env bash
# mise run check — formatting, lint, types, contract and static analysis.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

FAILED=0
fail() { ghs_err "$*"; FAILED=1; }

ghs_step "Go formatting"
UNFORMATTED="$(ghs_mise gofmt -l ./apps ./cli ./internal ./infra ./tests 2>/dev/null || true)"
if [ -n "$UNFORMATTED" ]; then
  fail "these files are not gofmt'd:"; printf '  %s\n' $UNFORMATTED >&2
else
  ghs_ok "gofmt clean"
fi

ghs_step "go vet"
if ghs_mise go vet ./... ; then ghs_ok "go vet clean"; else fail "go vet reported problems"; fi

ghs_step "Go build (including the demo-only build tag)"
if ghs_mise go build ./... && ghs_mise go build -tags ghs_testidp ./... ; then
  ghs_ok "both build configurations compile"
else
  fail "build failed"
fi

ghs_step "TypeScript types"
if ghs_mise pnpm -r --if-present typecheck; then ghs_ok "typecheck clean"; else fail "typecheck failed"; fi

ghs_step "API contract is in sync"
# Regenerate the TypeScript bindings and fail if they differ from what is
# committed: a hand-edited schema.ts would silently decouple the clients from
# the contract the server is tested against.
GEN_TMP="$(mktemp -d)"
trap 'rm -rf "$GEN_TMP"' EXIT
ghs_mise pnpm exec openapi-typescript packages/contracts/openapi.yaml -o "$GEN_TMP/schema.ts" >/dev/null 2>&1
if diff -q "$GEN_TMP/schema.ts" packages/contracts/src/schema.ts >/dev/null 2>&1; then
  ghs_ok "generated contract bindings match the committed file"
else
  fail "packages/contracts/src/schema.ts is stale — run: mise run fmt (or pnpm --filter @gh-stories/contracts generate)"
fi

ghs_step "Router matches the published contract"
if ghs_mise go test ./internal/api/ -run 'TestRouterMatchesOpenAPI|TestContractTestIsMeaningful' -count=1; then
  ghs_ok "no route drift"
else
  fail "routes and openapi.yaml disagree"
fi

ghs_step "Production isolation"
if ghs_mise go test ./internal/api/ -run 'TestProductionIsolation|TestNoLocalhostInShippedSources' -count=1; then
  ghs_ok "no test auth or localhost configuration in shipped sources"
else
  fail "production isolation check failed"
fi

ghs_step "Vulnerability scan"
if ghs_mise go run golang.org/x/vuln/cmd/govulncheck@latest ./... 2>/dev/null; then
  ghs_ok "govulncheck found nothing actionable"
else
  ghs_warn "govulncheck could not run or reported findings (see above)"
fi

if [ "$FAILED" -ne 0 ]; then ghs_die "check failed"; fi
ghs_ok "all checks passed"
