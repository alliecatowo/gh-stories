#!/usr/bin/env bash
# dev.sh — start the local stack, migrate, then run the API, worker,
# extension dev server and site dev server concurrently with prefixed output.
# Ctrl-C (or any exit) stops every child: children share this script's
# process group (the default for plain `&` background jobs when job control
# is off, which is the case for a non-interactive script), and the cleanup
# trap sends TERM to the whole group.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
source "$GHS_ROOT/scripts/_stack.sh"
ghs_load_dotenv

trap ghs_stack_cleanup EXIT INT TERM
ghs_start_stack

ghs_ok "running (Ctrl-C to stop everything)"
ghs_ok "api:       ${GHS_PUBLIC_URL:-http://localhost:8787}"
ghs_ok "extension: see the [extension] log lines above for the dev server URL"
ghs_ok "site:      see the [site] log lines above for the dev server URL"
wait
