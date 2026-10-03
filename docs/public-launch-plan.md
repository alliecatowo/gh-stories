# Public launch plan

## Outcome

Run GitHub Stories as a public service: anyone can view a public Story URL
without a Stories account; a GitHub account is needed only to publish,
interact, follow, or see a non-public Story.

The first deployment uses the existing Go service rather than a Firebase or
Workers rewrite:

```text
Browser extension / gh stories / public web viewer
                     |
                     v
             Cloud Run API service
                     |
          +----------+----------+
          |                     |
          v                     v
    Neon Postgres        Cloudflare R2 (private media)
          |                     |
          +----------+----------+
                     |
                     v
        Cloud Run media/cleanup worker job
```

The Pages site remains static. GitHub OAuth is the identity provider and must
be registered against the production service URL.

## Product decisions to lock before implementation

1. `public` is a first-class Story visibility. Its canonical `/s/<id>` viewer
   is readable without a session and can be shared outside GitHub Stories.
2. `followers`, `mutuals`, `custom_list`, and `private` remain session-gated.
   The server, never an object-storage URL alone, decides access.
3. A public Story's media may be served through a cacheable public-media
   endpoint; private media always gets a short-lived signed URL after an
   authorization check.
4. GitHub OAuth is for identity, profile data, and an opt-in one-time follow
   import. It does not grant GitHub users access to private Stories merely by
   existing on GitHub.
5. Viewer tracking for public Stories needs a privacy decision: either count
   anonymous views only, or do not retain anonymous view records. Do not store
   unnecessary IP-address history.
6. Video launch policy: retain the current ffmpeg normalization pipeline, but
   explicitly cap uploads and monitor processing cost. Do not promise a
   permanently-free video product.

## Deployment phases

### 0. Establish a dedicated GCP project

Create a new project only for GitHub Stories, attach the selected open billing
account, and enable only the APIs actually needed for Cloud Run, Artifact
Registry, Secret Manager, and Cloud Build. Do not reuse an unrelated Firebase
or experimental project.

Before deploying, create budget alerts and service quotas. Alerts do not cap
charges, so use Cloud Run maximum instances, request/CPU/memory limits, R2
usage monitoring, upload-size limits, and explicit per-service quotas as the
real cost controls.

### 1. Provision state and media

1. Create a Neon project and production branch; store its pooled Postgres URL
   in Secret Manager.
2. Create one private R2 bucket for original and processed media.
3. Create a narrowly scoped R2 token used only by the deployed API/worker.
4. Configure CORS only for the public service and extension origins needed by
   direct uploads; do not make the bucket itself public.
5. Apply migrations from the release artifact, not from a developer laptop.

Neon Object Storage may be evaluated in a disposable environment because it is
S3-compatible, but it is beta and is not the primary production media store
for this launch plan.

### 2. Adapt the repository for Cloud Run

The repository currently has no GCP deployment manifests and its worker is a
long-running process. Add:

1. A Cloud Run API service definition using the existing container image.
2. A bounded worker execution mode suitable for a Cloud Run Job. It should
   claim a finite batch of media/cleanup jobs, exit cleanly, and be safe to run
   more than once.
3. A trigger/schedule for worker jobs, with an immediate path for newly
   finalized uploads and a periodic cleanup sweep.
4. Infrastructure configuration (Terraform or narrowly scoped gcloud scripts)
   with no credentials committed to the repository.
5. Deployment CI that builds a tagged image, runs migrations once, deploys the
   API, then verifies health and an authenticated media round trip.

### 3. Configure public identity and URLs

1. Choose and configure a production domain and HTTPS.
2. Set `GHS_PUBLIC_URL` to that HTTPS origin.
3. Set exact `GHS_ALLOWED_ORIGINS`; include extension origins intentionally.
4. Generate a production `GHS_SECRET_KEY` and store all secrets in Secret
   Manager.
5. Register a GitHub OAuth App with callback:
   `https://<service-host>/v1/auth/github/callback`.
6. Set `GHS_GITHUB_CLIENT_ID` and `GHS_GITHUB_CLIENT_SECRET` only in deployed
   secrets. Keep `GHS_TEST_IDENTITY_PROVIDER=false` and `GHS_ENV=production`.
7. Rebuild the extension with the service origin in its host permissions;
   published builds cannot safely widen permissions at runtime.

### 4. Prove the product before announcing it

Use two real GitHub accounts and two separate client machines to verify:

1. OAuth login, logout, revocation, and account switching.
2. Public Story viewing in a logged-out browser.
3. Followers/mutuals/custom-list denial and allowance cases.
4. Image and video upload, processing, playback, expiry, deletion, and R2
   cleanup.
5. CLI and extension login against the same production service.
6. Abuse controls, moderator action, reports, and recovery after a worker/API
   restart.

Only after this proof should the service URL become the default in release
artifacts and public documentation.

## Current account audit (read-only, 2026-09-14)

Authenticated GCP account: `allisonemilycoleman@gmail.com`.

There are 24 active projects visible. Eight are attached to an open billing
account:

- `Firebase Payment`: `legalease-420`, `minireview-8v4e1`,
  `studio-3236238100-57452`, `portfolio-c1306`
- `Main`: `gen-lang-client-0789401272`, `shrike-publishing`,
  `gen-lang-client-0211877407`, `gen-lang-client-0721429006`

Several unrelated projects have Cloud Run, Cloud SQL, Storage, Firebase, or
other billable APIs enabled. Enabled APIs alone do not prove charges. A
resource/billing export audit is still needed before any cleanup action.

Do not disable services, detach billing, delete buckets, or close projects
until the operator approves a per-project list that includes the resource,
its owner/purpose, current usage, and recovery impact.

## Operator authentication status

Google Cloud authentication is working. Neon and Wrangler authentication were
completed in the interactive terminal, but this headless runner cannot read
macOS Keychain-backed credentials and Wrangler did not retain its credential
in the runner's context. Before the provisioning phase, verify locally:

```bash
neon projects list
wrangler whoami
gcloud auth list
```

If needed, use a Neon API key stored in the macOS Keychain and rerun
`wrangler login --device` without `--use-keyring`.
