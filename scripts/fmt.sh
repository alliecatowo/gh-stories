#!/usr/bin/env bash
# mise run fmt — format sources in place and regenerate the contract bindings.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

ghs_step "gofmt"
ghs_mise gofmt -w ./apps ./cli ./internal ./infra ./tests
ghs_ok "Go formatted"

ghs_step "contract bindings"
ghs_mise pnpm exec openapi-typescript packages/contracts/openapi.yaml \
  -o packages/contracts/src/schema.ts
ghs_ok "regenerated packages/contracts/src/schema.ts"

ghs_step "prettier"
ghs_mise pnpm exec prettier --write \
  "packages/**/*.{ts,tsx,css,md}" \
  "apps/site/src/**/*.{ts,tsx,astro,css}" \
  "apps/web-extension/{src,entrypoints,tests}/**/*.{ts,tsx,css}" \
  --log-level warn || ghs_warn "prettier reported problems"
ghs_ok "formatted"
