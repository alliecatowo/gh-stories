# Changelog

All notable changes to this project are documented here. This project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
