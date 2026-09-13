#!/usr/bin/env bash
# db.sh migrate|reset
#
# migrate: applies pending migrations via `apps/api -migrate` when that
#          package builds; falls back to running the .sql files in order with
#          psql inside the running postgres container when it does not (e.g.
#          apps/api isn't buildable yet).
# reset:   drops and recreates the LOCAL database only. Refuses unless
#          GHS_ENV is local/test AND the database host is loopback.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
ghs_load_dotenv

DB_URL="${GHS_DATABASE_URL:-postgres://stories:stories_local_dev@localhost:55432/stories?sslmode=disable}"
BIN_DIR="$GHS_ROOT/.bin"

parse_url_part() {
  # $1: part (user|pass|host|port|db)
  python3 - "$DB_URL" "$1" <<'PY' 2>/dev/null || true
import sys
from urllib.parse import urlsplit
u = urlsplit(sys.argv[1])
part = sys.argv[2]
out = {
    "user": u.username or "",
    "pass": u.password or "",
    "host": u.hostname or "",
    "port": str(u.port or 5432),
    "db": (u.path or "/").lstrip("/"),
}[part]
print(out)
PY
}

migrate_via_api_binary() {
  ghs_step "migrate: building apps/api"
  mkdir -p "$BIN_DIR"
  if ! ghs_mise go build -o "$BIN_DIR/api" ./apps/api 2>/tmp/ghs-db-build.log; then
    ghs_warn "apps/api does not build yet (see /tmp/ghs-db-build.log) — falling back to psql"
    return 1
  fi
  ghs_step "migrate: running apps/api -migrate"
  GHS_DATABASE_URL="$DB_URL" "$BIN_DIR/api" -migrate
  return 0
}

migrate_via_psql() {
  local host port user db pass
  host="$(parse_url_part host)"; port="$(parse_url_part port)"
  user="$(parse_url_part user)"; db="$(parse_url_part db)"
  pass="$(parse_url_part pass)"
  if [ -z "$host" ]; then
    ghs_die "could not parse GHS_DATABASE_URL and python3 is unavailable to help — cannot fall back to psql"
  fi

  local dir="$GHS_ROOT/infra/migrations"
  [ -d "$dir" ] || ghs_die "infra/migrations does not exist"
  ghs_step "migrate: applying infra/migrations/*.up.sql via psql (fallback path)"

  local cid
  cid="$(docker compose -f "$GHS_ROOT/infra/compose/docker-compose.yml" ps -q postgres 2>/dev/null || true)"

  psql_run() {
    local file="$1"
    if [ -n "$cid" ]; then
      docker exec -i -e PGPASSWORD="$pass" "$cid" \
        psql -v ON_ERROR_STOP=1 -U "$user" -d "$db" < "$file"
    else
      PGPASSWORD="$pass" psql -v ON_ERROR_STOP=1 -h "$host" -p "$port" -U "$user" -d "$db" < "$file"
    fi
  }

  psql_run <(cat <<'SQL'
CREATE TABLE IF NOT EXISTS schema_migrations (
  version    INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  checksum   TEXT NOT NULL,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
SQL
  )

  for up in "$dir"/*.up.sql; do
    [ -e "$up" ] || continue
    local base version
    base="$(basename "$up" .up.sql)"
    version="${base%%_*}"
    already="$(
      if [ -n "$cid" ]; then
        docker exec -e PGPASSWORD="$pass" "$cid" psql -tA -U "$user" -d "$db" \
          -c "SELECT 1 FROM schema_migrations WHERE version=${version}"
      else
        PGPASSWORD="$pass" psql -tA -h "$host" -p "$port" -U "$user" -d "$db" \
          -c "SELECT 1 FROM schema_migrations WHERE version=${version}"
      fi
    )"
    if [ "$already" = "1" ]; then
      ghs_info "already applied: $base"
      continue
    fi
    ghs_info "applying: $base"
    psql_run "$up"
    checksum="$(shasum -a 256 "$up" | awk '{print $1}')"
    if [ -n "$cid" ]; then
      docker exec -e PGPASSWORD="$pass" "$cid" psql -U "$user" -d "$db" \
        -c "INSERT INTO schema_migrations (version, name, checksum) VALUES (${version}, '${base#*_}', '${checksum}')"
    else
      PGPASSWORD="$pass" psql -h "$host" -p "$port" -U "$user" -d "$db" \
        -c "INSERT INTO schema_migrations (version, name, checksum) VALUES (${version}, '${base#*_}', '${checksum}')"
    fi
  done
  ghs_ok "migrations applied via psql fallback"
}

cmd_migrate() {
  if migrate_via_api_binary; then
    ghs_ok "migrations applied via apps/api -migrate"
    return
  fi
  migrate_via_psql
}

cmd_reset() {
  ghs_require_env local test
  ghs_require_local_db "$DB_URL"

  local host port user db pass
  host="$(parse_url_part host)"; port="$(parse_url_part port)"
  user="$(parse_url_part user)"; db="$(parse_url_part db)"
  pass="$(parse_url_part pass)"
  [ -n "$db" ] || ghs_die "could not determine database name from GHS_DATABASE_URL"

  ghs_step "reset: dropping and recreating '$db' on $host:$port (GHS_ENV=${GHS_ENV:-local})"
  local cid
  cid="$(docker compose -f "$GHS_ROOT/infra/compose/docker-compose.yml" ps -q postgres 2>/dev/null || true)"
  admin_psql() {
    if [ -n "$cid" ]; then
      docker exec -e PGPASSWORD="$pass" "$cid" psql -v ON_ERROR_STOP=1 -U "$user" -d postgres -c "$1"
    else
      PGPASSWORD="$pass" psql -v ON_ERROR_STOP=1 -h "$host" -p "$port" -U "$user" -d postgres -c "$1"
    fi
  }
  admin_psql "DROP DATABASE IF EXISTS ${db} WITH (FORCE);"
  admin_psql "CREATE DATABASE ${db} OWNER ${user};"
  ghs_ok "recreated '$db'"

  cmd_migrate
}

case "${1:-}" in
  migrate) cmd_migrate ;;
  reset)   cmd_reset ;;
  *) ghs_log "usage: $(basename "$0") {migrate|reset}"; exit 1 ;;
esac
