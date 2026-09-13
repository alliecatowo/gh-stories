#!/usr/bin/env bash
# doctor.sh — report required tools/versions, local stack ports, .env
# completeness (names only, never values), Docker, ffmpeg, browser test
# dependencies, terminal graphics capability, and credential availability.
#
# Exit status: non-zero only if something REQUIRED is missing or wrong.
# Everything else is a warning; the process exit code is the actionable
# signal, the printed report is for a human to read and act on.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

FAIL=0
WARN=0

require_fail() { ghs_err "$*"; FAIL=$((FAIL + 1)); }
soft_warn()    { ghs_warn "$*"; WARN=$((WARN + 1)); }

ghs_step "gh-stories doctor — $GHS_ROOT"

# --------------------------------------------------------------- pinned tools
mise_pin() { grep -E "^${1}[[:space:]]*=" "$GHS_ROOT/mise.toml" | sed -E 's/.*"([^"]+)".*/\1/' | head -1; }
PIN_GO="$(mise_pin go)"
PIN_NODE="$(mise_pin node)"
PIN_PNPM="$(mise_pin pnpm)"

ghs_step "tools"
if ghs_have mise; then
  ghs_ok "mise $(mise --version 2>/dev/null | head -1)"
else
  require_fail "mise is not installed — see https://mise.jdx.dev (this is the entry point for every task)"
fi

check_pinned_tool() {
  local name="$1" want="$2" cmd="$3"
  local got
  if ! ghs_have "$cmd" && ! ghs_mise which "$cmd" >/dev/null 2>&1; then
    require_fail "$name: not found (want $want). Run 'mise install'."
    return
  fi
  got="$(ghs_mise "$cmd" --version 2>/dev/null | head -1 || true)"
  if [ -z "$want" ]; then
    ghs_ok "$name: $got"
    return
  fi
  if printf '%s' "$got" | grep -qF "$want"; then
    ghs_ok "$name: $got (pinned $want)"
  else
    soft_warn "$name: got '$got', mise.toml pins $want — run 'mise install'"
  fi
}
check_pinned_tool "go"   "$PIN_GO"   go
check_pinned_tool "node" "$PIN_NODE" node
check_pinned_tool "pnpm" "$PIN_PNPM" pnpm

if ghs_have git; then
  ghs_ok "git $(git --version | awk '{print $3}')"
else
  require_fail "git is not installed"
fi

# ------------------------------------------------------------------- docker
ghs_step "container engine"
if ghs_have docker; then
  if docker info >/dev/null 2>&1; then
    ghs_ok "docker $(docker --version | awk '{print $3}' | tr -d ,) — daemon reachable"
  else
    require_fail "docker CLI is installed but the daemon is not reachable (is Docker Desktop / colima / podman machine running?)"
  fi
else
  require_fail "docker is not installed — see docs/prerequisites.md (infra:up, test, build all need a container engine)"
fi

# -------------------------------------------------------------------- ffmpeg
ghs_step "ffmpeg (worker media processing)"
if ghs_have ffmpeg; then
  ghs_ok "ffmpeg $(ffmpeg -version 2>/dev/null | head -1 | awk '{print $3}')"
else
  soft_warn "ffmpeg not found on PATH — the worker cannot process video locally (it always runs inside the container image, which bundles ffmpeg). See docs/prerequisites.md."
fi
if ghs_have ffprobe; then
  ghs_ok "ffprobe $(ffprobe -version 2>/dev/null | head -1 | awk '{print $3}')"
else
  soft_warn "ffprobe not found on PATH"
fi

# --------------------------------------------------------------------- ports
ghs_step "local stack ports"
check_port() {
  local port="$1" label="$2"
  if ghs_port_in_use "$port"; then
    local pids; pids="$(ghs_port_pids "$port" | tr '\n' ' ')"
    ghs_info "port $port ($label): in use by pid(s) $pids — fine if that's our own 'mise run infra:up'/'dev'/'demo'"
  else
    ghs_ok "port $port ($label): free"
  fi
}
check_port 55432 "postgres (local compose)"
check_port 55900 "minio S3 (local compose)"
check_port 55902 "minio console (local compose)"
check_port 8787  "api (GHS_HTTP_ADDR default)"

# ----------------------------------------------------------------------- env
ghs_step ".env"
ENV_FILE="$GHS_ROOT/.env"
ENV_EXAMPLE="$GHS_ROOT/.env.example"
if [ ! -f "$ENV_FILE" ]; then
  soft_warn ".env does not exist yet — run 'mise run bootstrap' to create it from .env.example"
else
  ghs_ok ".env exists"
  if [ -f "$ENV_EXAMPLE" ]; then
    missing=()
    while IFS='=' read -r key _; do
      [[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
      grep -q "^${key}=" "$ENV_FILE" 2>/dev/null || missing+=("$key")
    done < <(grep -E '^[A-Za-z_][A-Za-z0-9_]*=' "$ENV_EXAMPLE")
    if [ "${#missing[@]}" -eq 0 ]; then
      ghs_ok "all variable names from .env.example are present in .env (values not inspected)"
    else
      soft_warn "variables present in .env.example but missing from .env (names only): ${missing[*]}"
    fi
  fi
  # Credential AVAILABILITY, never values.
  ghs_load_dotenv "$ENV_FILE"
  if [ -n "${GHS_GITHUB_CLIENT_ID:-}" ] && [ -n "${GHS_GITHUB_CLIENT_SECRET:-}" ]; then
    ghs_ok "GitHub OAuth credentials: present (real GitHub login available)"
  else
    soft_warn "GitHub OAuth credentials: absent — real GitHub login unavailable; local dev falls back to the test identity provider if GHS_TEST_IDENTITY_PROVIDER=true (see docs/runbook.md)"
  fi
  if [ -n "${GHS_SECRET_KEY:-}" ]; then
    if printf '%s' "$GHS_SECRET_KEY" | grep -qi "change-me"; then
      soft_warn "GHS_SECRET_KEY is still the .env.example placeholder — fine for local, 'mise run bootstrap' generates a real one for a fresh .env"
    else
      ghs_ok "GHS_SECRET_KEY: present (custom, not the placeholder)"
    fi
  else
    soft_warn "GHS_SECRET_KEY: absent"
  fi
fi

# --------------------------------------------------------- browser test deps
ghs_step "browser test dependencies (Playwright)"
if [ -d "$GHS_ROOT/tests/e2e" ]; then
  if ghs_have pnpm && [ -d "$GHS_ROOT/node_modules" ]; then
    if ghs_mise pnpm --dir "$GHS_ROOT/tests/e2e" exec playwright --version >/dev/null 2>&1; then
      ver="$(ghs_mise pnpm --dir "$GHS_ROOT/tests/e2e" exec playwright --version 2>/dev/null || true)"
      ghs_ok "playwright CLI: $ver"
      if ghs_mise pnpm --dir "$GHS_ROOT/tests/e2e" exec playwright install --dry-run chromium firefox >/dev/null 2>&1; then
        ghs_ok "chromium/firefox browsers: installed (or dry-run reports nothing pending)"
      else
        soft_warn "chromium/firefox browsers: not confirmed installed — run 'mise run bootstrap'"
      fi
    else
      soft_warn "playwright CLI not resolvable yet — run 'mise run bootstrap' (pnpm install) first"
    fi
  else
    soft_warn "node_modules not installed yet — run 'mise run bootstrap'"
  fi
else
  soft_warn "tests/e2e does not exist yet — 'mise run test:e2e' will fail until it is created"
fi

# ------------------------------------------------------------- terminal caps
ghs_step "terminal graphics capability"
if [ -t 1 ]; then
  TERM_PROGRAM_L="${TERM_PROGRAM:-unknown}"
  ghs_info "TERM=${TERM:-unset} TERM_PROGRAM=${TERM_PROGRAM_L} COLORTERM=${COLORTERM:-unset}"
  case "$TERM_PROGRAM_L" in
    iTerm.app) ghs_ok "iTerm2 detected — supports inline images (iTerm2 image protocol)" ;;
    WezTerm) ghs_ok "WezTerm detected — supports the Kitty graphics protocol" ;;
    ghostty) ghs_ok "Ghostty detected — supports the Kitty graphics protocol" ;;
    kitty) ghs_ok "kitty detected — supports the Kitty graphics protocol" ;;
    Apple_Terminal) soft_warn "Terminal.app detected — no inline image protocol; the TUI falls back to a lower-fidelity renderer" ;;
    vscode) soft_warn "VS Code integrated terminal detected — inline image support varies" ;;
    *)
      if [ -n "${KITTY_WINDOW_ID:-}" ]; then
        ghs_ok "kitty (KITTY_WINDOW_ID set) — supports the Kitty graphics protocol"
      else
        soft_warn "unrecognized terminal ($TERM_PROGRAM_L) — graphics protocol support unknown; see docs/support-matrix.md"
      fi
      ;;
  esac
  if [ -n "${TMUX:-}" ]; then
    soft_warn "running inside tmux — inline graphics protocols are often passed through incorrectly; see docs/support-matrix.md"
  fi
  if [ -n "${SSH_TTY:-}" ] || [ -n "${SSH_CONNECTION:-}" ]; then
    soft_warn "running over SSH — local terminal capability detection is unreliable here"
  fi
else
  soft_warn "stdout is not a TTY (running non-interactively) — terminal graphics capability cannot be probed"
fi

# ------------------------------------------------------------------- summary
ghs_step "summary"
if [ "$FAIL" -gt 0 ]; then
  ghs_err "$FAIL required check(s) failed, $WARN warning(s)"
  exit 1
fi
ghs_ok "all required checks passed ($WARN warning(s))"
