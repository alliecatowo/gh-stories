# Implementation checklist

Resume from the first unchecked item.

## 1 — Foundation
- [x] Inspect environment, tools, credentials, deployment access
- [x] Pin tool versions and write the `mise` task interface
- [x] Local Docker Compose stack (PostgreSQL 18 + private MinIO bucket), started and healthy
- [x] Full initial migration, applied / rolled back / re-applied against real PostgreSQL
- [x] OpenAPI contract
- [x] Repository meta (license, README, CONTRIBUTING, SECURITY, CHANGELOG, issue templates, ignores)
- [ ] Public repository created and pushed

## 2 — Backend vertical slice
- [ ] Config loading with startup validation and production safety refusals
- [ ] Migration runner, pgx pool, injectable clock
- [ ] Identity boundary (GitHub OAuth + a test identity provider confined to local/test builds)
- [ ] Sessions, pending CLI authorizations
- [ ] Shared authorization service (audience, block, hide, suspension, deletion, expiry)
- [ ] Upload intent → private object → finalize → media job
- [ ] Media worker: signature validation, normalization, variants, atomic publish
- [ ] Media authorization gateway with range support and view recording
- [ ] Feed, status batch, read state

## 3 — Release and install skeleton
- [ ] Root `gh-stories` executable for `gh extension install .`
- [ ] Service container image
- [ ] CI validation workflow using the same mise tasks
- [ ] Pages build at the correct project subpath

## 4 — Clients against the slice
- [ ] CLI: API client, credential storage, login flow, feed TUI
- [ ] Terminal renderers: kitty, iTerm2, external fallback, capability detection
- [ ] Browser extension: adapters, rings, dashboard row, viewer, composer
- [ ] First real cross-client proof captured

## 5 — Complete the product
- [ ] Video end to end; composer editing; audiences; follows; replies; reactions; viewers; inbox; moderation; settings — in both clients

## 6 — Hardening
- [ ] Hostile media, deletion/revocation, cleanup, restart behaviour, extension resilience, terminal lifecycle

## 7 — Site and launch assets
- [ ] Pages site, interactive sample demo, docs, screenshots, launch film

## 8 — Gates
- [ ] Clean bootstrap, behavioural and visual gates, candidate package checks

## 9 — Publish
- [ ] Release, GHCR, Pages, service deployment

## 10 — Handoff
- [ ] Evidence, URLs, exact support claims, remaining blockers
