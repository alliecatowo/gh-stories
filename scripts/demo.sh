#!/usr/bin/env bash
# demo.sh — like dev.sh, but seeds deterministic sample accounts first and
# prints a clear summary of URLs and sample accounts at the end.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
source "$GHS_ROOT/scripts/_stack.sh"
ghs_load_dotenv

trap ghs_stack_cleanup EXIT INT TERM
ghs_start_stack

ghs_step "seeding sample accounts"
if [ "${API_BUILD_OK:-0}" -eq 1 ]; then
  GHS_ENV="${GHS_ENV:-local}" GHS_TEST_IDENTITY_PROVIDER=true \
    bash "$GHS_ROOT/scripts/seed.sh" || ghs_warn "seeding did not complete cleanly — the demo is still usable, but sample content may be incomplete"
else
  ghs_warn "apps/api did not build — skipping seed"
fi

PUBLIC_URL="${GHS_PUBLIC_URL:-http://localhost:8787}"
ghs_step "demo ready"
cat >&2 <<EOF

${C_BOLD}GitHub Stories — local demo${C_RESET}

  API              ${PUBLIC_URL}
  Health           ${PUBLIC_URL}/v1/health/ready
  MinIO console    http://localhost:55902  (storiesminio / storiesminio_local_dev)
  Extension        see the [extension] log lines above for its dev server URL
  Site             see the [site] log lines above for its dev server URL

  Sample accounts (sign in with the test identity provider, no GitHub OAuth
  needed): ${C_BOLD}alice, maya, sam, ravi${C_RESET}
    - alice <-> maya follow each other (mutuals)
    - sam follows alice (one-way)
    - alice follows ravi (one-way)
    - ravi has a custom audience list containing alice
    - each account has one seeded Story with a different visibility rule

  Ctrl-C stops everything.
EOF
wait
