# Production deployment runbook (Cloud Run + Neon + R2)

This is the operator side of `docs/public-launch-plan.md`. Nothing here
contains a credential: every secret lives in Secret Manager and is referenced
by name from the manifests.

## 0. One-time GCP project setup (operator, console + gcloud)

Create a **new, dedicated** project for GitHub Stories. Do not reuse an
unrelated Firebase or experimental project.

```bash
export GHS_GCP_PROJECT=gh-stories-prod GHS_REGION=us-central1
gcloud projects create "$GHS_GCP_PROJECT" --name="GitHub Stories"
gcloud beta billing projects link "$GHS_GCP_PROJECT" \
  --billing-account=BILLING_ACCOUNT_ID
gcloud services enable run.googleapis.com artifactregistry.googleapis.com \
  secretmanager.googleapis.com cloudbuild.googleapis.com \
  cloudscheduler.googleapis.com --project="$GHS_GCP_PROJECT"
gcloud config set project "$GHS_GCP_PROJECT"
gcloud config set run/region "$GHS_REGION"
```

Budget alerts (they do not cap charges — the manifests' max-instances and
resource limits are the real cost controls):

```bash
gcloud billing budgets create --billing-account=BILLING_ACCOUNT_ID \
  --display-name="gh-stories monthly" \
  --budget-amount=50USD \
  --threshold-rule=percent=50 --threshold-rule=percent=90
```

Least-privilege service accounts (the manifests reference these names):

```bash
for sa in gh-stories-api gh-stories-worker gh-stories-scheduler gh-stories-deployer; do
  gcloud iam service-accounts create "$sa" --project="$GHS_GCP_PROJECT"
done
# API/worker read their own secrets only:
for sa in gh-stories-api gh-stories-worker; do
  for secret in ghs-production-database-url ghs-production-r2-access-key-id \
      ghs-production-r2-secret-access-key ghs-production-secret-key \
      ghs-production-github-client-id ghs-production-github-client-secret; do
    gcloud secrets add-iam-policy-binding "$secret" --project="$GHS_GCP_PROJECT" \
      --member="serviceAccount:${sa}@${GHS_GCP_PROJECT}.iam.gserviceaccount.com" \
      --role="roles/secretmanager.secretAccessor"
  done
done
# Scheduler may only start the worker job:
gcloud run jobs add-iam-policy-binding gh-stories-worker \
  --project="$GHS_GCP_PROJECT" --region="$GHS_REGION" \
  --member="serviceAccount:gh-stories-scheduler@${GHS_GCP_PROJECT}.iam.gserviceaccount.com" \
  --role="roles/run.developer"
```

## 1. State and media (operator, Neon + Cloudflare dashboards)

1. Create a Neon project and production branch; store its pooled Postgres
   URL as `ghs-production-database-url` in Secret Manager.
2. Create one **private** R2 bucket (e.g. `gh-stories-media`).
3. Create a narrowly scoped R2 token (this bucket, read/write only) and store
   its key id/secret as `ghs-production-r2-access-key-id` /
   `ghs-production-r2-secret-access-key`.
4. CORS on the bucket: only the production service origin and the extension
   origins that need direct uploads. Never make the bucket public.
5. Migrations run from the release artifact (`gh-stories-migrate` job, step 3
   below) — never from a developer laptop against the production database.

## 2. Secrets (operator, once per value)

```bash
store() { # store <secret-name> — reads the value from stdin
  local name="$1"
  gcloud secrets describe "$name" --project="$GHS_GCP_PROJECT" >/dev/null 2>&1 \
    || gcloud secrets create "$name" --project="$GHS_GCP_PROJECT" --replication-policy=automatic
  gcloud secrets versions add "$name" --project="$GHS_GCP_PROJECT" --data-file=-
}
openssl rand -base64 48 | store ghs-production-secret-key
printf '%s' "$POOLED_NEON_URL" | store ghs-production-database-url
# ... R2 keys, GitHub client id/secret likewise
```

## 3. Deploy (explicit operator run)

```bash
export GHS_GCP_PROJECT=gh-stories-prod GHS_REGION=us-central1 \
  GHS_SERVICE=gh-stories-api GHS_WORKER_JOB=gh-stories-worker \
  GHS_IMAGE_TAG=v1.2.3 SERVICE_HOST=stories.example.com \
  R2_ACCOUNT_ID=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
mise run deploy
```

`scripts/deploy.sh` renders the manifests with those values, then in order:

1. executes the `gh-stories-migrate` job and waits for success,
2. deploys the API service,
3. upserts the worker job and the two scheduler triggers,
4. verifies `/v1/health/ready` and an **authenticated** media round trip
   (metadata 200 + private `no-store`) using a token from
   `GHS_VERIFY_TOKEN`, plus an **anonymous** public check
   (metadata 200 + public `Cache-Control`).

## 4. Identity and URLs (operator)

1. Point the production domain at the Cloud Run service (HTTPS managed).
2. `GHS_PUBLIC_URL` is that HTTPS origin (baked into the manifests as
   `https://SERVICE_HOST`).
3. `GHS_ALLOWED_ORIGINS` is exact origins only; add extension origins
   intentionally, then rebuild the extension with the service origin in its
   host permissions.
4. Register a GitHub **OAuth App** (manual — GitHub offers no API for
   creating OAuth Apps) with callback
   `https://<service-host>/v1/auth/github/callback`, and store its client
   id/secret. Keep `GHS_TEST_IDENTITY_PROVIDER=false`, `GHS_ENV=production`.

## 5. Prove it before announcing it

Two real GitHub accounts, two separate client machines:

1. OAuth login, logout, revocation, account switching.
2. Public Story viewing in a logged-out browser (`/s/<id>`, API metadata,
   media bytes with `Cache-Control: public`).
3. Followers/mutuals/custom-list denial and allowance cases.
4. Image and video upload, processing, playback, expiry, deletion, R2 cleanup.
5. CLI and extension login against production.
6. Abuse controls, moderator action, reports, recovery after worker/API restart.

Only then does the service URL become the default in release artifacts.
