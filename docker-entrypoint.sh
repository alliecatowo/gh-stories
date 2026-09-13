#!/bin/sh
# One image, two roles. `api` also accepts -migrate for a one-shot migration.
set -eu
role="${1:-api}"
shift 2>/dev/null || true
case "$role" in
  api)     exec /usr/local/bin/gh-stories-api "$@" ;;
  worker)  exec /usr/local/bin/gh-stories-worker "$@" ;;
  migrate) exec /usr/local/bin/gh-stories-api -migrate ;;
  *)
    echo "usage: docker run ghcr.io/alliecatowo/gh-stories {api|worker|migrate}" >&2
    exit 2
    ;;
esac
