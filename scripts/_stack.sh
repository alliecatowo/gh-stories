#!/usr/bin/env bash
# _stack.sh — shared "start everything locally" logic for dev.sh and demo.sh.
# Not a mise task itself; source it after scripts/lib.sh.
if [ -n "${GHS_STACK_SH_LOADED:-}" ]; then return 0 2>/dev/null || exit 0; fi
GHS_STACK_SH_LOADED=1

BIN_DIR="$GHS_ROOT/.bin"
mkdir -p "$BIN_DIR"
PIDS=()

ghs_stack_cleanup() {
  trap - EXIT INT TERM
  ghs_step "stopping dev processes"
  kill -TERM -$$ 2>/dev/null || true
  wait 2>/dev/null || true
}

run_prefixed() {
  local name="$1" color="$2"; shift 2
  ( "$@" 2>&1 | while IFS= read -r line; do
      printf '%s[%s]%s %s\n' "$color" "$name" "$C_RESET" "$line"
    done ) &
  PIDS+=("$!")
}

run_pnpm_dev_in() {
  local name="$1" color="$2" dir="$3"
  if [ ! -f "$dir/package.json" ]; then
    ghs_warn "$dir/package.json does not exist yet — not starting $name"
    return
  fi
  if command -v mise >/dev/null 2>&1; then
    run_prefixed "$name" "$color" bash -c "cd '$dir' && exec mise exec -- pnpm dev"
  else
    run_prefixed "$name" "$color" bash -c "cd '$dir' && exec pnpm dev"
  fi
}

# Builds and starts infra, api, worker, extension dev, site dev. Leaves
# global PIDS populated. Does not wait. Caller installs cleanup trap and
# calls `wait` when ready to block.
ghs_start_stack() {
  ghs_step "infra"
  bash "$GHS_ROOT/scripts/infra.sh" up

  ghs_step "migrate"
  bash "$GHS_ROOT/scripts/db.sh" migrate

  ghs_step "building api + worker"
  ghs_info "api build includes the local-only test identity provider (-tags ghs_testidp), gated at runtime by GHS_TEST_IDENTITY_PROVIDER — never used for release builds"
  API_BUILD_OK=1
  ghs_mise go build -tags ghs_testidp -o "$BIN_DIR/api" ./apps/api 2>/tmp/ghs-dev-api-build.log || API_BUILD_OK=0

  WORKER_BUILD_OK=1
  if [ -n "$(find "$GHS_ROOT/apps/worker" -name '*.go' 2>/dev/null)" ]; then
    ghs_mise go build -o "$BIN_DIR/worker" ./apps/worker 2>/tmp/ghs-dev-worker-build.log || WORKER_BUILD_OK=0
  else
    WORKER_BUILD_OK=0
  fi

  ghs_step "starting processes"
  if [ "$API_BUILD_OK" -eq 1 ]; then
    run_prefixed api "$C_GREEN" "$BIN_DIR/api"
  else
    ghs_warn "apps/api does not build yet (see /tmp/ghs-dev-api-build.log) — not starting it"
  fi

  if [ "$WORKER_BUILD_OK" -eq 1 ]; then
    run_prefixed worker "$C_YELLOW" "$BIN_DIR/worker"
  elif [ -f /tmp/ghs-dev-worker-build.log ]; then
    ghs_warn "apps/worker does not build yet (see /tmp/ghs-dev-worker-build.log) — not starting it"
  else
    ghs_warn "apps/worker does not exist yet — not starting it (media will queue but never finish processing)"
  fi

  run_pnpm_dev_in extension "$C_BLUE" "$GHS_ROOT/apps/web-extension"
  run_pnpm_dev_in site "$C_DIM" "$GHS_ROOT/apps/site"

  if [ "${#PIDS[@]}" -eq 0 ]; then
    ghs_die "nothing to run: no component builds/exists yet"
  fi

  if [ "$API_BUILD_OK" -eq 1 ]; then
    ghs_info "waiting for the API to become ready"
    ghs_load_dotenv
    if ! ghs_wait_http "${GHS_PUBLIC_URL:-http://localhost:8787}/v1/health/ready" 60; then
      ghs_warn "API did not report ready within 60s — continuing anyway, check the [api] log lines above"
    fi
  fi
}
