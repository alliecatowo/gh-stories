# Blocker log

External gates this project cannot pass on its own, what each blocks, and the
smallest concrete action that resolves it. Anything unresolved here is stated
as unresolved in the README, on the website, and in the release notes.

| # | Blocked | Impact | Smallest resolving action | Status |
|---|---|---|---|---|
| 1 | **No hosting provider credentials** | There is no public GitHub Stories service. Every server-side behaviour is implemented, tested against real PostgreSQL/MinIO/ffmpeg, and shipped as a runnable multi-arch image — but nothing is deployed, so there is no live cross-account proof on the public internet. | Provide credentials for any one host that can run a container plus PostgreSQL plus an S3-compatible bucket (Fly.io, Railway, Render, a DigitalOcean droplet, or any Docker host). Then: `docker run ghcr.io/alliecatowo/gh-stories migrate`, run `api` and `worker`, and set the repository variable `GHS_DEFAULT_SERVICE_URL` so release binaries target it. `infra/deploy/` has ready configuration. | **Open** |
| 2 | **No GitHub OAuth app registered** | Real GitHub sign-in cannot be exercised. The flow is implemented (state, PKCE S256, server-side exchange, identity revalidation) and tested against an HTTP stub of GitHub's documented API, but not against `github.com` itself. It also cannot be registered until #1 gives a real callback origin. | After #1, register an OAuth app at https://github.com/settings/developers with callback `<service>/v1/auth/github/callback`, then set `GHS_GITHUB_CLIENT_ID` / `GHS_GITHUB_CLIENT_SECRET` on the service. | **Open** (depends on #1) |
| 3 | **Chrome Web Store / AMO accounts** | The extension is a manual install only. No signed, persistent installation exists. | Provide a Chrome Web Store developer account and a Firefox AMO account. The listing copy, icons, screenshots and permission justification are prepared; the packaged archives are published on every release. | **Open** |
| 4 | **macOS screen-recording permission** | iTerm2 rendering could not be captured on this machine: `screencapture` returns "could not create image from display". The iTerm2 renderer is implemented and its wire format is unit-tested, but no iTerm2 pixels were captured, so it is listed as **not visually verified**. | Grant Screen Recording to the terminal running the capture, then run `mise run test:terminal` on a macOS GUI session. | **Open** |
| 5 | **Firefox extension harness** | Firefox builds and is packaged, but was not loaded into a real Firefox profile — Playwright's extension support covers persistent Chromium contexts, and a separate `web-ext` run was not performed. Rendering the shared UI in Playwright Firefox would not prove a Firefox *extension* installs and runs, so that claim is not made. | Run `web-ext run` against `apps/web-extension/.output/firefox-mv2` on a machine with Firefox installed, and capture it. | **Open** |

## What is NOT blocked

Everything that does not require one of the above is done and verified locally:
the API, worker, media pipeline, both clients, the authorization rules, expiry,
the container image, the website, and the release pipeline.
