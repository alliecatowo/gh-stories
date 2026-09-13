#!/usr/bin/env bash
# seed.sh — idempotently seed deterministic sample accounts (alice, maya, sam,
# ravi) with varied audiences, follow relationships and read states, using
# real fixture media through the real HTTP API (upload -> finalize -> view).
#
# Refuses unless GHS_ENV is local or test. If no API is already listening at
# GHS_PUBLIC_URL, seed.sh builds and runs a short-lived one itself (tagged
# ghs_testidp, with the test identity provider enabled) and tears it down on
# exit — so `mise run seed` works standalone, not just from inside demo.sh.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
ghs_load_dotenv
ghs_require_env local test

BASE="${GHS_PUBLIC_URL:-http://localhost:8787}"
BASE="${BASE%/}"
V1="$BASE/v1"
FIXTURES="$GHS_ROOT/tests/fixtures"
BIN_DIR="$GHS_ROOT/.bin"
mkdir -p "$BIN_DIR"

MANAGED_API_PID=""
MANAGED_WORKER_PID=""
cleanup() {
  [ -n "$MANAGED_WORKER_PID" ] && ghs_killpg "$MANAGED_WORKER_PID"
  [ -n "$MANAGED_API_PID" ] && ghs_killpg "$MANAGED_API_PID"
}
trap cleanup EXIT INT TERM

api_reachable() { curl -fsS -o /dev/null "$V1/health/ready" 2>/dev/null; }

ensure_api() {
  if api_reachable; then
    ghs_info "using the API already running at $BASE"
    return
  fi

  ghs_step "no API reachable at $BASE — starting a temporary seed instance"
  bash "$GHS_ROOT/scripts/infra.sh" up
  bash "$GHS_ROOT/scripts/db.sh" migrate

  ghs_info "building apps/api with -tags ghs_testidp (seed-only; never used for release artifacts)"
  if ! ghs_mise go build -tags ghs_testidp -o "$BIN_DIR/api-testidp" ./apps/api 2>/tmp/ghs-seed-api-build.log; then
    cat /tmp/ghs-seed-api-build.log >&2
    ghs_die "apps/api does not build yet — cannot seed until the API component exists and builds"
  fi

  GHS_ENV=test GHS_TEST_IDENTITY_PROVIDER=true GHS_PUBLIC_URL="$BASE" \
    "$BIN_DIR/api-testidp" >/tmp/ghs-seed-api.log 2>&1 &
  MANAGED_API_PID=$!

  if [ -d "$GHS_ROOT/apps/worker" ] && [ -n "$(find "$GHS_ROOT/apps/worker" -name '*.go' 2>/dev/null)" ]; then
    ghs_info "building apps/worker"
    if ghs_mise go build -o "$BIN_DIR/worker" ./apps/worker 2>/tmp/ghs-seed-worker-build.log; then
      GHS_ENV=test "$BIN_DIR/worker" >/tmp/ghs-seed-worker.log 2>&1 &
      MANAGED_WORKER_PID=$!
      ghs_info "worker started (pid $MANAGED_WORKER_PID) — seeded media will actually finish processing"
    else
      ghs_warn "apps/worker does not build yet (see /tmp/ghs-seed-worker-build.log) — seeded Stories will stay queued/processing until a worker exists"
    fi
  else
    ghs_warn "apps/worker does not exist yet — seeded Stories will stay queued/processing until a worker exists"
  fi

  ghs_info "waiting for $V1/health/ready"
  if ! ghs_wait_http "$V1/health/ready" 60; then
    [ -f /tmp/ghs-seed-api.log ] && tail -50 /tmp/ghs-seed-api.log >&2
    ghs_die "API did not become ready within 60s"
  fi
  ghs_ok "temporary API ready at $BASE"
}

ensure_api

# Confirm the test identity provider is actually usable against whatever API
# we ended up with (self-started, or already-running from `mise run demo`).
probe_status="$(curl -sS -o /tmp/ghs-seed-probe.json -w '%{http_code}' -X POST "$V1/auth/test/login" \
  -H 'content-type: application/json' -d '{"login":"probe"}' || true)"
if [ "$probe_status" != "200" ]; then
  ghs_die "POST $V1/auth/test/login returned HTTP $probe_status, not 200 (body: $(cat /tmp/ghs-seed-probe.json 2>/dev/null)).
The running API must be built with -tags ghs_testidp AND started with GHS_TEST_IDENTITY_PROVIDER=true and GHS_ENV=local|test.
If this API instance is managed by 'mise run dev'/'mise run demo' elsewhere, restart it with those settings."
fi

declare -A TOKEN

login_as() {
  local login="$1"
  local resp status
  status="$(curl -sS -o /tmp/ghs-seed-login.json -w '%{http_code}' -X POST "$V1/auth/test/login" \
    -H 'content-type: application/json' -d "{\"login\":\"${login}\"}")"
  [ "$status" = "200" ] || ghs_die "test login for '$login' failed: HTTP $status ($(cat /tmp/ghs-seed-login.json))"
  TOKEN[$login]="$(ghs_json_get token < /tmp/ghs-seed-login.json)"
  [ -n "${TOKEN[$login]}" ] || ghs_die "test login for '$login' returned no token"
  ghs_ok "signed in as $login"
}

api() {
  # api METHOD PATH LOGIN [DATA]
  local method="$1" path="$2" login="$3" data="${4:-}"
  local args=(-sS -X "$method" "$V1$path" -H "authorization: Bearer ${TOKEN[$login]}" -H 'content-type: application/json')
  [ -n "$data" ] && args+=(-d "$data")
  curl "${args[@]}"
}

ghs_step "accounts"
for u in alice maya sam ravi; do login_as "$u"; done

ghs_step "follow graph (idempotent: PUT is a set-membership operation)"
# alice <-> maya mutual; sam -> alice one-way; alice -> ravi one-way (ravi has
# a follower but follows no one) — gives every Visibility rule a true case.
api PUT /following/maya alice >/dev/null
api PUT /following/alice maya >/dev/null
api PUT /following/alice sam  >/dev/null
api PUT /following/ravi alice >/dev/null
ghs_ok "alice<->maya mutual, sam->alice, alice->ravi"

ghs_step "audience list (ravi's custom_list, containing alice)"
list_resp="$(api GET /audience-lists ravi)"
LIST_ID="$(printf '%s' "$list_resp" | ghs_json_get 'lists.0.id')"
if [ -z "$LIST_ID" ]; then
  LIST_ID="$(api POST /audience-lists ravi '{"name":"seed-close-friends","logins":["alice"]}' | ghs_json_get id)"
  ghs_ok "created audience list seed-close-friends ($LIST_ID)"
else
  ghs_ok "reusing existing audience list ($LIST_ID)"
fi

SEED_TAG="seed-$(date -u +%Y-%m-%d)"

story_exists() {
  local login="$1" caption_tag="$2"
  api GET /stories/mine "$login" | python3 -c "
import json,sys
try:
  d=json.load(sys.stdin)
except Exception:
  sys.exit(1)
items = d.get('items') or []
sys.exit(0 if any('$caption_tag' in (i.get('caption') or '') for i in items) else 1)
" 2>/dev/null
}

post_story() {
  local login="$1" file="$2" mime="$3" visibility="$4" extra="$5" caption="$6"
  local path="$FIXTURES/$file"
  [ -f "$path" ] || { ghs_warn "fixture $file not found — skipping $login's story"; return; }
  if story_exists "$login" "$SEED_TAG"; then
    ghs_info "$login already has a seeded story for $SEED_TAG — skipping (idempotent)"
    return
  fi

  local bytes sha
  bytes="$(wc -c < "$path" | tr -d ' ')"
  sha="$(shasum -a 256 "$path" | awk '{print $1}')"

  local intent
  intent="$(api POST /uploads "$login" "{\"mime\":\"${mime}\",\"byte_size\":${bytes},\"caption\":\"${caption} (${SEED_TAG})\",\"visibility\":\"${visibility}\"${extra}}")"
  local upload_id url
  upload_id="$(printf '%s' "$intent" | ghs_json_get upload_id)"
  url="$(printf '%s' "$intent" | ghs_json_get url)"
  if [ -z "$upload_id" ] || [ -z "$url" ]; then
    ghs_warn "$login: /uploads did not return an upload_id/url (response: $intent) — skipping"
    return
  fi

  if ! curl -sS -X PUT "$url" -H "content-type: ${mime}" --data-binary "@${path}" -o /dev/null; then
    ghs_warn "$login: PUT to the authorized upload URL failed — skipping finalize"
    return
  fi

  api POST "/uploads/${upload_id}/finalize" "$login" "{\"byte_size\":${bytes},\"checksum_sha256\":\"${sha}\"}" >/dev/null
  ghs_ok "$login: posted '$caption' ($visibility, $file)"
}

ghs_step "sample Stories (real fixture media, varied audiences)"
post_story alice landscape.png image/png    public              "" "cat on a keyboard"
post_story maya  portrait.jpg  image/jpeg   followers_of_author "" "vertical selfie"
post_story sam   sample.mp4    video/mp4    mutuals             "" "short clip"
post_story ravi  square.webp   image/webp   custom_list          ",\"audience_list_id\":\"${LIST_ID}\"" "for my close friends only"

ghs_step "read states"
# maya (mutual follower of alice) views alice's story -> read.
# sam (follows alice) does not view -> unread.
# ravi's story is only visible to alice (custom list) and is left unread.
alice_group="$(api GET /users/alice/stories maya)"
alice_story_id="$(printf '%s' "$alice_group" | ghs_json_get 'items.0.id')"
if [ -n "$alice_story_id" ]; then
  if api GET "/media/${alice_story_id}/thumb" maya >/dev/null 2>&1; then
    api POST "/stories/${alice_story_id}/view" maya >/dev/null 2>&1 || true
    ghs_ok "maya viewed alice's story (thumb fetched + acknowledged)"
  else
    ghs_warn "could not fetch alice's story media yet (worker may still be processing, or not running) — read state left as-is"
  fi
else
  ghs_warn "alice's seeded story is not visible to maya yet (still processing?) — skipping view"
fi

ghs_step "done"
ghs_ok "seeded alice, maya, sam, ravi against $BASE"
