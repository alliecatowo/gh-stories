# Acceptance matrix

Every gate from the brief, what proves it, and its actual current result.
`pass` means a real execution was observed. `blocked` means an external
dependency is missing and is recorded in [blockers.md](blockers.md). Skipped
and flaky results are reported as themselves, never as passes.

| Gate | Proof | Result |
|---|---|---|
| Bootstrap | Fresh clone, documented mise setup, clean database/storage, demo starts | pending |
| Identity | Real OAuth positive path; state/PKCE/callback mismatch and expired/replayed authorization rejected | pending |
| Cross-client post | A posts an image via CLI; eligible B sees it in the browser, and the reverse | pending |
| Video | Validated and transcoded; browser plays canonical video; CLI shows a real poster and opens the authorized external viewer | pending |
| Read state | Opening B's eligible item records exactly one view; the other client converges; feed/status do not mark views | pending |
| Audiences | Each audience with A/B/C, imported vs native follows, hides, blocks, changed relationships | pending |
| No private leakage | C cannot obtain ring/status, metadata, media, poster, range response, reply or viewer list by guessing IDs | pending |
| Expiry | Works just before expiry, fails at and after the boundary across feed, status, media, thumbnail and interactions | pending |
| Independent clocks | Posting another item does not extend the first item's expiry | pending |
| Revocation | Delete, block, hide, narrowed audience or suspension stops later media requests on already-known routes | pending |
| Cleanup | Originals, variants, abandoned uploads and expired assets removed on schedule, across retries and restarts | pending |
| Upload abuse | Spoofed MIME, corrupt files, oversized input, excessive dimensions/duration, unauthorized finalize and replay cannot publish | pending |
| Idempotency | Interrupted upload/finalize/post/reply retries do not duplicate | pending |
| Inbox | Only participants see private replies; read state, disabled replies and blocks hold | pending |
| Terminal text | Malicious control sequences in captions and replies cannot trigger terminal actions | pending |
| Browser resilience | Theme change, SPA navigation, worker suspension, late avatars, auth change, API failure | pending |
| Terminal lifecycle | Next/previous, resize, cancellation, non-TTY, keyring unavailable, unsupported renderer, clean exit | pending |
| Real terminal pixels | Actual media visible in each emulator claimed as visually verified | pending |
| No GitHub mutation | Ordinary GitHub links and actions preserved; no unintended GitHub writes | pending |
| Production isolation | Release bundles contain no test auth, demo secrets, private fixtures or localhost configuration | pending |
| Published install | Clean environment installs the published extension and runs the real binary | pending |
| GHCR | Public image pulls anonymously and starts from documented configuration | pending |
| Pages | Public URL, demo, media, direct links, install links and mobile layout work after deployment | pending |
| Live service | Real identities across clients on the deployed API/storage/worker; health, restart and cleanup verified | pending |
