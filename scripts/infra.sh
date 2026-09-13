#!/usr/bin/env bash
# infra.sh — up/down/status for the local PostgreSQL + MinIO compose stack.
# `up` blocks until both services report healthy before returning.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

COMPOSE_FILE="$GHS_ROOT/infra/compose/docker-compose.yml"
[ -f "$COMPOSE_FILE" ] || ghs_die "infra/compose/docker-compose.yml not found"

compose() {
  if docker compose version >/dev/null 2>&1; then
    docker compose -f "$COMPOSE_FILE" "$@"
  elif ghs_have docker-compose; then
    docker-compose -f "$COMPOSE_FILE" "$@"
  else
    ghs_die "neither 'docker compose' nor 'docker-compose' is available"
  fi
}

usage() { ghs_log "usage: $(basename "$0") {up|down|status}"; }

cmd="${1:-}"
case "$cmd" in
  up)
    ghs_have docker || ghs_die "docker is not installed"
    docker info >/dev/null 2>&1 || ghs_die "docker daemon is not reachable"

    ghs_step "starting postgres + minio"
    compose up -d

    ghs_step "waiting for healthchecks"
    services=(postgres minio)
    deadline=$((SECONDS + 120))
    for svc in "${services[@]}"; do
      while true; do
        cid="$(compose ps -q "$svc")"
        [ -n "$cid" ] || ghs_die "container for service '$svc' did not start"
        status="$(docker inspect --format '{{.State.Health.Status}}' "$cid" 2>/dev/null || echo unknown)"
        case "$status" in
          healthy) ghs_ok "$svc: healthy"; break ;;
          unhealthy) ghs_die "$svc: reported unhealthy — see 'docker compose -f $COMPOSE_FILE logs $svc'" ;;
          *)
            if [ "$SECONDS" -ge "$deadline" ]; then
              ghs_die "$svc: did not become healthy within 120s (last status: $status)"
            fi
            sleep 2
            ;;
        esac
      done
    done
    ghs_ok "postgres on localhost:55432, minio S3 on localhost:55900 (console :55902), bucket stories-media ready"
    ;;
  down)
    ghs_step "stopping local stack"
    compose down
    ghs_ok "stopped"
    ;;
  status)
    compose ps
    ;;
  *)
    usage
    exit 1
    ;;
esac
