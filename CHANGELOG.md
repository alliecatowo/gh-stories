# Changelog

All notable changes to this project are documented here. This project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.4.0]

### Added
- Public launch: a live `public` Story is world-readable without a session.
  Its canonical `/s/<id>` viewer, `GET /v1/stories/{id}` metadata and
  `GET /v1/media/{id}/{variant}` bytes serve anonymous callers; anonymous
  delivery increments an aggregate counter only (no named view, no IP
  history). Authenticated media keeps `private, no-store` and named views.
- Cloud Run production deployment: `infra/deploy/` manifests (API service
  with max-instances/resource cost controls, bounded `worker -once` Job,
  one-shot migrate Job), Cloud Scheduler triggers, `mise run deploy`, and a
  tag-triggered deploy workflow (migrate once, deploy API, verify health and
  media round trip).
- Bounded worker mode (`worker -once -max-jobs N -max-duration D`,
  `GHS_WORKER_ONCE/MAX_JOBS/MAX_DURATION`) for Cloud Run Jobs.
- Follow import can be re-run from settings (opt-in re-authorization that
  reuses no stored upstream token).
- Local servers bind all interfaces, so dev and preview are reachable over
  tailnet.

## [0.3.0]

### Fixed
- The browser extension now actually works in a browser. Loading it into a real
  Chromium surfaced six defects that no unit test could reach: a missing
  `alarms` permission that stopped the background from ever starting; an opaque
  ring overlay that hid GitHub's avatars entirely; an overlay host that
  swallowed clicks meant for profile links; a ring drawn as a filled circle
  rather than a ring; media and uploads transferred as ArrayBuffers over
  messaging channels that serialise with JSON, so nothing ever loaded and
  uploads stalled at 0%; and a trust rule that rejected the extension's own
  settings page because it opens in a tab.

### Added
- Browser-extension scenarios running against a real Chromium with the MV3
  extension loaded, covering rings, the viewer, posting, the dashboard row, the
  toolbar popup, colour-mode following and API-outage resilience.
- Packaged-manifest validation for all three browser targets.

## [0.2.0]

### Added
- **Inline video in the terminal.** `gh stories` plays a Story's video as
  moving pixels using the Kitty graphics protocol's frame animation, rather
  than only showing a poster frame. Requires a terminal with kitty graphics
  animation and `ffmpeg` locally, and is unavailable over SSH because frames
  are handed to the terminal as local files. `--video=auto|inline|poster`
  selects the behaviour; anywhere it cannot animate, the CLI shows the real
  poster frame and says why.

## [0.1.0]

Initial release.

### Added
- Real GitHub OAuth with PKCE (S256), server-side code exchange, and revocable
  Stories sessions. Account switching and logout in both clients.
- Service-mediated CLI authorization that works over SSH, with a user
  verification code, bounded single-use polling, and `--no-browser`.
- Opt-out import of GitHub follows at first login; independent Stories follows
  afterwards, with `github_import` / `stories_native` provenance, unfollow
  tombstones, mute, block, hide rules and username lookup.
- Image and video Stories with captions and accessibility descriptions,
  per-item 24-hour expiry, and deletion.
- Five audiences — People I follow, My followers, Mutuals, Custom list, Public
  — evaluated server side against the current graph on every request.
- Browser extension for Chromium, Firefox and Edge: dashboard row, avatar rings
  across GitHub surfaces, viewer, composer, inbox, settings, and an independent
  toolbar fallback.
- `gh stories` CLI extension with a Bubble Tea TUI, real inline image rendering
  via the Kitty graphics protocol and iTerm2 inline images, an honest external
  fallback, posting, replies, reactions, viewers, inbox, and social/settings
  commands.
- Private object storage behind an authenticated media authorization gateway,
  a media worker with leases and retries, and scheduled physical cleanup.
- Reports, blocks, owner deletion, moderator removal, account suspension, rate
  limits, and a minimal authenticated moderation queue.
