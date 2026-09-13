# Acceptance matrix

Every gate from the brief, what proves it, and its actual current result.
`pass` means a real execution was observed. `blocked` means an external
dependency is missing and is recorded in [blockers.md](blockers.md). Skipped
and flaky results are reported as themselves, never as passes.

Current totals: **24 store-level integration tests** and **18 API-level tests**
green against real PostgreSQL 18.6, plus 35 terminal-renderer tests, 12 GitHub
client tests and 14 auth tests.

| Gate | Proof | Result |
|---|---|---|
| Bootstrap | Fresh clone, documented mise setup, clean database/storage, demo starts | **pass** — CI runs `doctor`/`bootstrap`/`check`/`test`/`build` from a fresh clone on every push |
| Identity | Real OAuth positive path; state/PKCE/callback mismatch and expired/replayed authorization rejected | **partial** — state replay, expiry, PKCE S256, renamed-login-in-place and Org/Bot refusal all pass against an HTTP stub of GitHub's documented API. The real github.com path is [blocked](blockers.md) on OAuth registration, which is blocked on hosting. |
| Cross-client post | A posts an image via CLI; eligible B sees it in the browser, and the reverse | **pass** — released binary ↔ released GHCR image: post, feed, reply, react, inbox. See [visual report](../evidence/visual-report.md) |
| Video | Validated and transcoded; browser plays canonical video; CLI shows a real poster and opens the authorized external viewer | **pass** — `TestVideoEndToEnd`: real transcode to H.264 MP4, a poster proven to be a real frame, 206 range responses |
| Read state | Opening B's eligible item records exactly one view; the other client converges; feed/status do not mark views | **pass** — `TestViewRecordedOnMediaDeliveryOnly`, `TestThumbnailDoesNotRecordAView` |
| Audiences | Each audience with A/B/C, imported vs native follows, hides, blocks, changed relationships | **pass** — `TestDefaultAudienceDirection`, `TestEveryAudience`, `TestImportedFollowGrantsAccess` |
| No private leakage | C cannot obtain ring/status, metadata, media, poster, range response, reply or viewer list by guessing IDs | **pass** — `TestNoPrivateLeakageByGuessingIDs`, `TestViewerListIsAuthorOnly`, `TestRingStatusIsNotAnOracle` |
| Expiry | Works just before expiry, fails at and after the boundary across feed, status, media, thumbnail and interactions | **pass** — `TestExpiryAcrossEverySurface`, `TestExpiryBoundaryIsExclusive` |
| Independent clocks | Posting another item does not extend the first item's expiry | **pass** — `TestIndependentClocks`, `TestPostingAgainDoesNotExtendAnEarlierItem` |
| Revocation | Delete, block, hide, narrowed audience or suspension stops later media requests on already-known routes | **pass** — `TestRevocationStopsKnownMediaRoutes` (delete, block, hide, narrowed audience, suspension, session revocation) |
| Cleanup | Originals, variants, abandoned uploads and expired assets removed on schedule, across retries and restarts | **pass** (store level) — `TestExpiredItemStaysDeadWithoutCleanup`, `TestAbandonedUploadsAreCollected`, `TestDeletionSchedulesPhysicalCleanup`, `TestLeaseRecoveryAfterWorkerDeath` |
| Upload abuse | Spoofed MIME, corrupt files, oversized input, excessive dimensions/duration, unauthorized finalize and replay cannot publish | **pass** — `TestHostileUploadsCannotPublish`, `TestHostileFilenameIsInert` |
| Idempotency | Interrupted upload/finalize/post/reply retries do not duplicate | **pass** — `TestFinalizeIsIdempotent`, `TestReplyIdempotency` |
| Inbox | Only participants see private replies; read state, disabled replies and blocks hold | **pass** — `TestInboxPermissions` |
| Terminal text | Malicious control sequences in captions and replies cannot trigger terminal actions | **pass** — 17 independently-written hostile inputs neutralised; ordinary Unicode preserved |
| Browser resilience | Theme change, SPA navigation, worker suspension, late avatars, auth change, API failure | **partial** — adapters and message validation unit-tested against fictional markup; no real-GitHub extension session run |
| Terminal lifecycle | Next/previous, resize, cancellation, non-TTY, keyring unavailable, unsupported renderer, clean exit | **pass** — next/previous, resize, small window, unsupported renderer, non-TTY and clean exit; stale-image defect found by inspection and fixed |
| Real terminal pixels | Actual media visible in each emulator claimed as visually verified | **pass** — kitty on a real X display, inspected. iTerm2 [blocked](blockers.md) on macOS screen-recording permission |
| No GitHub mutation | Ordinary GitHub links and actions preserved; no unintended GitHub writes | **pass** — the extension issues no writes to github.com; the CLI never reads the user's gh token; follow import is read-only |
| Production isolation | Release bundles contain no test auth, demo secrets, private fixtures or localhost configuration | **pass** — source gate plus a scan of the packaged binaries for the demo route and localhost defaults |
| Published install | Clean environment installs the published extension and runs the real binary | **pass** — clean `GH_CONFIG_DIR`: `gh extension install alliecatowo/gh-stories` → `gh stories version` → v0.1.0; `setup` sets the alias without clobbering; `gh story` forwards arguments; checksum verified |
| GHCR | Public image pulls anonymously and starts from documented configuration | **pass** — anonymous manifest fetch and `docker pull` succeed; multi-arch amd64+arm64; `api` and `worker` both run |
| Pages | Public URL, demo, media, direct links, install links and mobile layout work after deployment | **pass** — https://alliecatowo.github.io/gh-stories/ live; all six routes HTTP 200 on direct visit; 24 checks across four viewports |
| Live service | Real identities across clients on the deployed API/storage/worker; health, restart and cleanup verified | **BLOCKED** — no hosting credentials exist. See [blockers.md](blockers.md) #1. |
