# AGENTS.md

## What this repo is

GitHub Stories: expiring (24h) photo/video stories attached to GitHub identities.
Go service (`apps/api`, `apps/worker`), Go CLI (`cli/gh-stories`), Astro site
(`apps/site`), browser extension (`apps/web-extension`), shared API contract
(`packages/contracts`).

## Commands (mise owns the workflow)

```bash
mise install          # pinned Go, Node, pnpm
mise run doctor       # what's present / missing / misconfigured
mise run bootstrap    # deps + local config (idempotent)
mise run demo         # full local product with sample data
mise run check        # REQUIRED before PRs: gofmt, vet, build, typecheck, contract sync, isolation
mise run test         # unit + real-service integration tests
mise run build        # production builds
```

pnpm scripts (`typecheck`, `build`, `lint`) run per-workspace; prefer the mise
wrappers above so versions and ordering stay pinned.

## Conventions

- Go 1.26.8, gofmt clean, `go vet` clean. Touch Go? Run `mise run check`.
- `packages/contracts/openapi.yaml` is the source of truth. Never hand-edit
  `packages/contracts/src/schema.ts` — regenerate it; `mise run check` fails on drift.
- No test auth, no localhost URLs in shipped sources (`TestProductionIsolation`
  enforces this). Local-only code lives behind the `ghs_testidp` build tag.
- Product scope is narrow (see CONTRIBUTING.md): ordinary social actions only.
  No org/bot posting, feeds, Highlights, analytics, AI-generated stories.
- Docs live in `docs/`; user-facing site content under `apps/site/src/pages/docs/`.

## Agent routing (this project's opencode setup)

| Agent | Tier / model / reasoning | Use for |
|---|---|---|
| `build` (primary) | Zen `muse-spark-1.3-contributor-free` | Orchestration, default conversation |
| `@swift` | low / `openai/gpt-5.6-luna` / minimal | Triage, small scoped edits, quick questions |
| `@forge` | mid / `openai/gpt-5.6-terra` / medium | Feature work, multi-file changes, debugging |
| `@auditor` | high / `openai/gpt-5.6-sol` / max, read-only | Security review, auth/privacy, hard bugs, second opinions |

Every agent falls back to `llmgateway/muse-spark-1.3-contributor` ($0.10/$0.20
per 1M, same weights as the paid tier) when OpenAI quota/rate limits hit —
`fallback_models` per agent in `opencode.jsonc`, global catch-all in
`.opencode/opencode-model-fallback.json`, executed by the
`@razroo/opencode-model-fallback` plugin (installed project-local via
`opencode plugin`; its registration lives in `.opencode/opencode.json`).
Verified end-to-end via `opencode serve`: with a bogus primary the plugin
detects `model_not_found`, replays on the fallback, and the session lands an
assistant reply from `muse-spark-1.3-contributor`
(see `~/.config/opencode/opencode-model-fallback.log`). Nothing else beats
that price/performance for a 1M-context tool-calling model, so there is no
ladder — one fallback for everything.

## First-time model setup

```bash
/connect   # connect: openai, llmgateway (DevPass) — Zen is free, no key needed
/models     # verify IDs, esp. llmgateway/muse-spark-1.3-contributor
```

If a model ID differs in your region/catalog, update the `fallback_models`
chains in `opencode.jsonc` and restart opencode.
