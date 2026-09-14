#!/usr/bin/env bash
# Cloud Scheduler triggers for the bounded worker job.
#
# The worker job is lease-based and idempotent, so the two schedules below
# simply execute the same job at different cadences:
#
#   gh-stories-worker-frequent — every 2 minutes. This is the immediate path
#     for newly finalized uploads: FinalizeUpload queues a media job, and the
#     next execution picks it up within ~2 minutes.
#   gh-stories-worker-sweep — every 15 minutes (offset by 7 minutes so the
#     two never fire together). Backstop for the cleanup sweep: expiry, GC,
#     retention purges and physical object deletion converge here even if a
#     frequent run is ever skipped.
#
# Both invoke the Cloud Run Job :run API with the deploy service account, so
# no trigger credential lives in the repository. Safe to re-run: existing
# schedules with the same name are left untouched.
#
# Required environment: GHS_GCP_PROJECT, GHS_REGION (default us-central1),
# GHS_WORKER_JOB (default gh-stories-worker).
set -euo pipefail

PROJECT="${GHS_GCP_PROJECT:?set GHS_GCP_PROJECT to the dedicated Stories project}"
REGION="${GHS_REGION:-us-central1}"
JOB="${GHS_WORKER_JOB:-gh-stories-worker}"
SA="${GHS_SCHEDULER_SA:-gh-stories-scheduler@${PROJECT}.iam.gserviceaccount.com}"

RUN_URI="https://${REGION}-run.googleapis.com/v2/projects/${PROJECT}/locations/${REGION}/jobs/${JOB}:run"

create_or_keep() {
  local name="$1" schedule="$2" body="$3"
  if gcloud scheduler jobs describe "$name" --project="$PROJECT" --location="$REGION" >/dev/null 2>&1; then
    echo "scheduler job $name already exists, leaving it untouched"
    return 0
  fi
  gcloud scheduler jobs create http "$name" \
    --project="$PROJECT" \
    --location="$REGION" \
    --schedule="$schedule" \
    --time-zone="Etc/UTC" \
    --uri="$RUN_URI" \
    --http-method=POST \
    --oauth-service-account-email="$SA" \
    --headers="Content-Type=application/json" \
    --message-body="$body" \
    --attempt-deadline=60s
  echo "created scheduler job $name ($schedule)"
}

create_or_keep "gh-stories-worker-frequent" "*/2 * * * *" '{}'
create_or_keep "gh-stories-worker-sweep" "7,22,37,52 * * * *" '{}'
