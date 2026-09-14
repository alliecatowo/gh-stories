#!/usr/bin/env bash
# mise run deploy — ordered production deployment for GitHub Stories.
#
#   build tagged image (release artifact) -> migrate ONCE -> deploy API ->
#   upsert worker job + schedules -> verify health + media round trip
#
# Nothing is built from a laptop: GHS_IMAGE_TAG names the GHCR image the
# release workflow published (ghcr.io/alliecatowo/gh-stories:<tag>).
# Migrations run from the gh-stories-migrate Cloud Run Job, exactly once,
# before the API rolls. No credentials live in this repository; every secret
# is referenced by name from Secret Manager (see infra/deploy/README.md).
#
# Required environment:
#   GHS_GCP_PROJECT  dedicated GCP project (e.g. gh-stories-prod)
#   SERVICE_HOST     production host, no scheme (e.g. stories.example.com)
#   GHS_IMAGE_TAG    release tag (e.g. v1.2.3)
# Optional:
#   GHS_REGION (us-central1), GHS_SERVICE (gh-stories-api),
#   GHS_WORKER_JOB (gh-stories-worker), R2_ACCOUNT_ID,
#   GHS_VERIFY_TOKEN (a production session token for the auth round trip)
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

PROJECT="${GHS_GCP_PROJECT:?set GHS_GCP_PROJECT}"
HOST="${SERVICE_HOST:?set SERVICE_HOST}"
TAG="${GHS_IMAGE_TAG:?set GHS_IMAGE_TAG}"
REGION="${GHS_REGION:-us-central1}"
SERVICE="${GHS_SERVICE:-gh-stories-api}"
WORKER_JOB="${GHS_WORKER_JOB:-gh-stories-worker}"
R2_ACCOUNT="${R2_ACCOUNT_ID:-R2_ACCOUNT_ID}"
IMAGE="ghcr.io/alliecatowo/gh-stories:${TAG}"
BASE="https://${HOST}"
DEPLOY_DIR="$GHS_ROOT/infra/deploy"
RENDERED="$(mktemp -d)"
trap 'rm -rf "$RENDERED"' EXIT

ghs_step "Rendering manifests for $IMAGE -> $BASE"
# NOTE: only *.yaml manifests are rendered. Shell scripts read their
# configuration from the environment at runtime; running sed over them would
# rewrite their own variable references (GHS_GCP_PROJECT, ...).
for f in api-service.yaml worker-job.yaml migrate-job.yaml; do
  sed -e "s|ghcr.io/alliecatowo/gh-stories:TAG|${IMAGE}|g" \
      -e "s|SERVICE_HOST|${HOST}|g" \
      -e "s|R2_ACCOUNT_ID|${R2_ACCOUNT}|g" \
      -e "s|GHS_GCP_PROJECT|${PROJECT}|g" \
      "$DEPLOY_DIR/$f" > "$RENDERED/$f"
  if grep -q "ghcr.io/alliecatowo/gh-stories:TAG\|SERVICE_HOST\|R2_ACCOUNT_ID" "$RENDERED/$f"; then
    ghs_die "unrendered placeholder left in $f"
  fi
done
ghs_ok "manifests rendered"

ghs_step "Migrations, exactly once (gh-stories-migrate job)"
if ! gcloud run jobs describe "gh-stories-migrate" --project="$PROJECT" --region="$REGION" >/dev/null 2>&1; then
  # Created bare, then replaced below with the fully rendered manifest so the
  # first deploy gets the exact same env, secrets and service account as
  # every later one.
  gcloud run jobs create "gh-stories-migrate" --project="$PROJECT" --region="$REGION" --image="$IMAGE" \
    --args="migrate" --task-timeout=300 --max-retries=0 >/dev/null
fi
gcloud run jobs replace "$RENDERED/migrate-job.yaml" --project="$PROJECT" --region="$REGION" >/dev/null
EXEC="$(gcloud run jobs execute "gh-stories-migrate" --project="$PROJECT" \
  --region="$REGION" --wait --format='value(metadata.name)')"
ghs_info "migration execution: $EXEC"
STATUS="$(gcloud run jobs executions describe "$EXEC" --project="$PROJECT" \
  --region="$REGION" --format='value(status.conditions[0].type)')"
[ "$STATUS" = "Completed" ] || ghs_die "migrations did not complete (status: $STATUS)"
ghs_ok "migrations applied"

ghs_step "Deploying API service $SERVICE"
gcloud run services replace "$RENDERED/api-service.yaml" \
  --project="$PROJECT" --region="$REGION"
# Public launch: the anonymous surface (/s/<id>, public metadata/media) must
# be reachable without a Google identity. Authorization stays in the app.
gcloud run services add-iam-policy-binding "$SERVICE" \
  --project="$PROJECT" --region="$REGION" \
  --member="allUsers" --role="roles/run.invoker" >/dev/null
ghs_ok "API deployed (public invoker)"

ghs_step "Upserting worker job + schedules"
if ! gcloud run jobs describe "$WORKER_JOB" --project="$PROJECT" --region="$REGION" >/dev/null 2>&1; then
  gcloud run jobs create "$WORKER_JOB" --project="$PROJECT" --region="$REGION" --image="$IMAGE" \
    --args="worker,-once,-max-jobs=50,-max-duration=8m" --task-timeout=600 --max-retries=2 >/dev/null
fi
gcloud run jobs replace "$RENDERED/worker-job.yaml" --project="$PROJECT" --region="$REGION" >/dev/null
GHS_GCP_PROJECT="$PROJECT" GHS_REGION="$REGION" GHS_WORKER_JOB="$WORKER_JOB" \
  bash "$DEPLOY_DIR/scheduler-jobs.sh"
ghs_ok "worker job + schedules up to date"

ghs_step "Verifying $BASE"
ghs_wait_http "$BASE/v1/health/ready" 90 || ghs_die "readiness never became ready"
READY="$(curl -fsS "$BASE/v1/health/ready")"
echo "$READY" | grep -q '"status":"ok"' || ghs_die "readiness is not ok: $READY"
ghs_ok "health: ready"

# Anonymous public surface: exercised fully only when a public Story exists;
# the check below asserts the gateway answers 404 (not 401/500) for an
# unknown id, proving anonymous routing reaches the predicate.
CODE="$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/v1/stories/00000000-0000-0000-0000-000000000000")"
[ "$CODE" = "404" ] || ghs_die "anonymous story route unexpected: HTTP $CODE"
ghs_ok "anonymous public route answers 404 for unknown ids (predicate reachable)"

if [ -n "${GHS_VERIFY_TOKEN:-}" ]; then
  CODE="$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $GHS_VERIFY_TOKEN" "$BASE/v1/me")"
  [ "$CODE" = "200" ] || ghs_die "authenticated /v1/me unexpected: HTTP $CODE"
  ghs_ok "authenticated round trip works"
else
  ghs_warn "GHS_VERIFY_TOKEN unset: skipping the authenticated round trip (prove it manually per the README)"
fi

ghs_ok "deployed $IMAGE to $BASE"
