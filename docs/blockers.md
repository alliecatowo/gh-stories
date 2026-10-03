# Blocker log

External gates this project cannot pass on its own, what each blocks, and the
smallest concrete action that resolves it. Anything unresolved here is stated
as unresolved in the README, on the website, and in the release notes.

| # | Blocked | Impact | Smallest resolving action | Status |
|---|---|---|---|---|
| 1 | **Hosted service not verified end to end** | A Cloud Run deployment exists (`/v1/health/live` answers 200; it was last deployed at v0.4.1, behind the v0.5.3 release) and release binaries default to it. There is still no live cross-account proof with real identities across clients, so the hosted service is described as experimental. | Redeploy the current release with the manual Deploy workflow (`workflow_dispatch`, see `infra/deploy/README.md`), then run a two-account round trip against it. | **Open** (hosting exists; verification pending) |
| 2 | **No GitHub OAuth app registered** | Real GitHub sign-in cannot be exercised. The flow is implemented (state, PKCE S256, server-side exchange, identity revalidation) and tested against an HTTP stub of GitHub's documented API, but not against `github.com` itself. It needs the real callback origin from the hosted service (#1). | After #1, register an OAuth app at https://github.com/settings/developers with callback `<service>/v1/auth/github/callback`, then set `GHS_GITHUB_CLIENT_ID` / `GHS_GITHUB_CLIENT_SECRET` on the service. | **Open** (depends on #1) |
| 3 | **Chrome Web Store / AMO accounts** | The extension is a manual install only. No signed, persistent installation exists. | Provide a Chrome Web Store developer account and a Firefox AMO account. The listing copy, icons, screenshots and permission justification are prepared; the packaged archives are published on every release. | **Open** |
| 4 | **macOS screen-recording permission** | iTerm2 rendering could not be captured on this machine: `screencapture` returns "could not create image from display". The iTerm2 renderer is implemented and its wire format is unit-tested, but no iTerm2 pixels were captured, so it is listed as **not visually verified**. | Grant Screen Recording to the terminal running the capture, then run `mise run test:terminal` on a macOS GUI session. | **Open** |
| 5 | **Firefox extension harness** | Firefox builds and is packaged, but was not loaded into a real Firefox profile — Playwright's extension support covers persistent Chromium contexts, and a separate `web-ext` run was not performed. Rendering the shared UI in Playwright Firefox would not prove a Firefox *extension* installs and runs, so that claim is not made. | Run `web-ext run` against `apps/web-extension/.output/firefox-mv2` on a machine with Firefox installed, and capture it. | **Open** |

## What is NOT blocked

Everything that does not require one of the above is done and verified locally:
the API, worker, media pipeline, both clients, the authorization rules, expiry,
the container image, the website, and the release pipeline.
