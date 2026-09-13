#!/usr/bin/env bash
# mise run test — unit and real-service integration tests.
#
# These deliberately run against a REAL PostgreSQL and a REAL MinIO: the
# authorization rules ARE SQL, and an in-memory substitute would test a
# different program.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

# Only start our own stack if nothing is already serving these ports. CI
# supplies PostgreSQL as a service container and MinIO as a step.
if ghs_tcp_open 127.0.0.1 55432 && ghs_tcp_open 127.0.0.1 55900; then
  ghs_info "using the PostgreSQL and object storage already listening on 55432/55900"
else
  ghs_info "starting the local stack for integration tests"
  bash "$GHS_ROOT/scripts/infra.sh" up
fi

ghs_step "Go tests"
ghs_mise go test ./... -count=1 "$@"

ghs_step "JavaScript tests"
ghs_mise pnpm -r --if-present test

ghs_ok "tests passed"
