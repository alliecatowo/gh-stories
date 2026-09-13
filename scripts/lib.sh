#!/usr/bin/env bash
# Shared helpers for scripts/*.sh. Source this; do not execute it directly.
#
#   source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
#
# Provides: repo root resolution, NO_COLOR-respecting log helpers, a
# `mise exec --` wrapper so pinned Go/Node/pnpm versions are always used, and
# small process/port utilities shared by infra.sh, dev.sh and demo.sh.

if [ -n "${GHS_LIB_SH_LOADED:-}" ]; then return 0 2>/dev/null || exit 0; fi
GHS_LIB_SH_LOADED=1

# ------------------------------------------------------------------- repo root
ghs_repo_root() {
  git rev-parse --show-toplevel 2>/dev/null || {
    echo "fatal: not inside a git checkout of gh-stories" >&2
    exit 1
  }
}
GHS_ROOT="$(ghs_repo_root)"
cd "$GHS_ROOT"

# ------------------------------------------------------------------------ tty
if [ -n "${NO_COLOR:-}" ] || [ ! -t 1 ]; then
  C_RESET="" C_BOLD="" C_RED="" C_GREEN="" C_YELLOW="" C_BLUE="" C_DIM=""
else
  C_RESET=$'\033[0m' C_BOLD=$'\033[1m' C_RED=$'\033[31m' C_GREEN=$'\033[32m'
  C_YELLOW=$'\033[33m' C_BLUE=$'\033[34m' C_DIM=$'\033[2m'
fi

ghs_log()  { printf '%s\n' "$*" >&2; }
ghs_info() { printf '%s[info]%s %s\n' "$C_BLUE" "$C_RESET" "$*" >&2; }
ghs_ok()   { printf '%s[ ok ]%s %s\n' "$C_GREEN" "$C_RESET" "$*" >&2; }
ghs_warn() { printf '%s[warn]%s %s\n' "$C_YELLOW" "$C_RESET" "$*" >&2; }
ghs_err()  { printf '%s[fail]%s %s\n' "$C_RED" "$C_RESET" "$*" >&2; }
ghs_step() { printf '\n%s%s==>%s %s%s\n' "$C_BOLD" "$C_BLUE" "$C_RESET" "$C_BOLD" "$*$C_RESET" >&2; }

ghs_die() { ghs_err "$*"; exit 1; }

# --------------------------------------------------------------------- mise
# Run a command with mise's pinned tool versions on PATH. Falls back to a
# plain invocation (with a warning) if mise itself is not installed, so a
# missing `mise` doesn't make every script unusable for diagnosis purposes.
ghs_mise() {
  if command -v mise >/dev/null 2>&1; then
    mise exec -- "$@"
  else
    ghs_warn "mise not found on PATH; running '$*' with whatever is installed globally"
    "$@"
  fi
}

ghs_have() { command -v "$1" >/dev/null 2>&1; }

# ------------------------------------------------------------------- env file
# Load .env into the current process environment (does not overwrite
# already-exported variables), if present.
ghs_load_dotenv() {
  local f="${1:-$GHS_ROOT/.env}"
  [ -f "$f" ] || return 0
  set -a
  # shellcheck disable=SC1090
  source "$f"
  set +a
}

# ------------------------------------------------------------------ env guard
# Refuse unless GHS_ENV is one of the given values. Used by db.sh reset and
# seed.sh, which must never run against a real deployment.
ghs_require_env() {
  local current="${GHS_ENV:-local}"
  local ok=0
  for allowed in "$@"; do
    [ "$current" = "$allowed" ] && ok=1
  done
  if [ "$ok" -ne 1 ]; then
    ghs_die "GHS_ENV=$current, but this operation is only allowed when GHS_ENV is one of: $* (refusing)"
  fi
}

# Refuse unless a database URL host is loopback. Defense in depth alongside
# ghs_require_env: even a mis-set GHS_ENV=local pointed at a real database
# host must not be reset.
ghs_require_local_db() {
  local url="$1"
  local host
  host="$(printf '%s' "$url" | sed -E 's#^[a-zA-Z0-9+]+://[^@]*@?([^:/]+).*#\1#')"
  case "$host" in
    localhost|127.0.0.1|::1) ;;
    *) ghs_die "refusing: GHS_DATABASE_URL host '$host' is not localhost/127.0.0.1 (reset/seed only ever touch a local database)" ;;
  esac
}

# --------------------------------------------------------------------- ports
# Print PIDs (possibly empty) listening on a TCP port.
ghs_port_pids() {
  lsof -nP -iTCP:"$1" -sTCP:LISTEN -t 2>/dev/null || true
}

ghs_port_in_use() {
  [ -n "$(ghs_port_pids "$1")" ]
}

# --------------------------------------------------------------- process tree
# Kill a background job's entire process group. Used by dev.sh/demo.sh so
# Ctrl-C reliably stops API, worker and every dev server, not just the shell
# that spawned them.
ghs_killpg() {
  local pid="$1"
  [ -n "$pid" ] || return 0
  kill -TERM "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
}

# ----------------------------------------------------------------------- json
# Minimal JSON field extraction without requiring jq. Reads JSON from stdin;
# $1 is a dotted path of object keys (e.g. "user.login"). Only handles the
# shallow shapes our own API returns (strings/numbers/booleans at the leaf).
ghs_json_get() {
  python3 - "$1" <<'PY'
import json, sys
path = sys.argv[1].split(".")
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(1)
for p in path:
    if isinstance(data, list):
        data = data[int(p)]
    else:
        data = data.get(p) if isinstance(data, dict) else None
    if data is None:
        print("")
        sys.exit(0)
print(data if not isinstance(data, (dict, list)) else json.dumps(data))
PY
}

# Wait for an HTTP URL to return 200 within a timeout (seconds).
ghs_wait_http() {
  local url="$1" timeout="${2:-60}"
  local deadline=$((SECONDS + timeout))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if curl -fsS -o /dev/null "$url" 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}
