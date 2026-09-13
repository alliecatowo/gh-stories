#!/usr/bin/env bash
# mise run verify — the local release gates, with an honest summary.
#
# PASS / FAIL / SKIPPED / BLOCKED are reported as themselves. A skipped gate is
# never counted as a pass, and the script exits non-zero if anything FAILED.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

declare -a NAMES=() RESULTS=() NOTES=()
record() { NAMES+=("$1"); RESULTS+=("$2"); NOTES+=("${3:-}"); }

gate() { # name, description, command...
  local name="$1"; shift
  local note="$1"; shift
  ghs_step "$name"
  if "$@"; then record "$name" PASS "$note"; else record "$name" FAIL "$note"; fi
}

blocked() { record "$1" BLOCKED "$2"; ghs_warn "$1 — BLOCKED: $2"; }
skipped() { record "$1" SKIPPED "$2"; ghs_warn "$1 — SKIPPED: $2"; }

# ---------------------------------------------------------------- local gates
if ghs_port_in_use 55432 && ghs_port_in_use 55900; then
  gate "Static checks" "format, vet, types, contract drift, isolation" \
    bash "$GHS_ROOT/scripts/check.sh"
  gate "Behavioural tests" "real PostgreSQL, real MinIO, real ffmpeg" \
    bash "$GHS_ROOT/scripts/test.sh"
else
  skipped "Static checks" "local stack not running (mise run infra:up)"
  skipped "Behavioural tests" "local stack not running (mise run infra:up)"
fi

gate "Production builds" "api, worker, cli, extension, site" \
  bash "$GHS_ROOT/scripts/build.sh"

gate "Release packaging" "cross-compiled binaries, archives, checksums, artifact isolation scan" \
  bash "$GHS_ROOT/scripts/package.sh"

if ghs_have docker; then
  gate "Service image" "builds, runs as non-root, both roles resolve" bash -c '
    docker build -q -t ghs-verify-image . >/dev/null &&
    docker run --rm ghs-verify-image api -version >/dev/null &&
    docker run --rm ghs-verify-image worker -version >/dev/null'
  gate "Real terminal pixels" "kitty on a real X display" \
    bash "$GHS_ROOT/scripts/test-terminal.sh"
else
  blocked "Service image" "no container engine available"
  blocked "Real terminal pixels" "no container engine available"
fi

# --------------------------------------------------------------- live gates
if [ -n "${GHS_LIVE_SERVICE_URL:-}" ]; then
  gate "Live service health" "deployed API answers /v1/health/ready" \
    bash -c "curl -fsS '${GHS_LIVE_SERVICE_URL}/v1/health/ready' >/dev/null"
else
  blocked "Live service" "GHS_LIVE_SERVICE_URL is not set — no deployment to verify"
fi

# ------------------------------------------------------------------- summary
echo
printf '%s%-28s %-9s %s%s\n' "$C_BOLD" "GATE" "RESULT" "NOTE" "$C_RESET"
FAILURES=0
for i in "${!NAMES[@]}"; do
  case "${RESULTS[$i]}" in
    PASS)    colour="$C_GREEN" ;;
    FAIL)    colour="$C_RED"; FAILURES=$((FAILURES+1)) ;;
    BLOCKED) colour="$C_YELLOW" ;;
    *)       colour="$C_DIM" ;;
  esac
  printf '%-28s %s%-9s%s %s\n' "${NAMES[$i]}" "$colour" "${RESULTS[$i]}" "$C_RESET" "${NOTES[$i]}"
done
echo

if [ "$FAILURES" -gt 0 ]; then
  ghs_die "$FAILURES gate(s) FAILED"
fi
ghs_ok "no gate failed (skipped and blocked gates are listed above and are not passes)"
