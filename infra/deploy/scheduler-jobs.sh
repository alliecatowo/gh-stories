#!/usr/bin/env bash
# Cloud Scheduler trigger for the run-to-completion worker job.
#
# ONE schedule, every 6 hours (3 Cloud Scheduler jobs per billing account
# are free; each extra costs $0.10/month). The worker is lease-based and
# idempotent: each run does one cleanup pass (expiry, GC, retention purges,
# physical object deletion) and drains any queued media jobs, then exits.
# Uploads also start the job directly (the API calls jobs.run on finalize,
# GHS_WORKER_TRIGGER_URL), so this sweep is the backstop for a missed trigger
# and the cleanup pass. Cost: every run wakes the serverless Neon database and
# starts a container, so a 15-minute sweep kept both effectively always on.
# Every 6 hours lets them idle to zero; expired Stories are already hidden at
# read time, only physical object deletion lags (up to ~6h). Do NOT add a
# 2-minute trigger: it multiplies container starts (and Secret Manager reads).
#
# Uses the scheduler service account with run.developer on the job only, so
# no trigger credential lives in the repository. Safe to re-run: an existing
# schedule with the same name is left untouched.
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

create_or_keep "gh-stories-worker-sweep" "7 */6 * * *" '{}'
