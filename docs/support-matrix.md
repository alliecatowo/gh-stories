# Support matrix

Three separate claims, kept separate on purpose:

- **Built** — the artifact is compiled/packaged for this target.
- **Exercised** — the software was actually run on it and behaved.
- **Visually verified** — someone looked at real captured pixels from it.

A cell is only filled in when that specific thing happened. Empty means *not
done*, not *assumed fine*.

## CLI — operating systems

| OS / arch | Built | Exercised | Visually verified |
|---|---|---|---|
| macOS arm64 | ✅ | ✅ full flow: login, post, feed, reply, react, inbox, viewers | — (no screen-recording permission; see [blockers #4](blockers.md)) |
| macOS amd64 | ✅ cross-compiled | — | — |
| Linux arm64 | ✅ | ✅ ran the real binary in a container against a real service | ✅ kitty, real X display |
| Linux amd64 | ✅ cross-compiled | — | — |
| Windows amd64 | ✅ cross-compiled | — | — |

Cross-compilation is **not** runtime validation. The three rows without an
"Exercised" mark were compiled and nothing more.

## CLI — terminals

| Terminal | Renderer | Built | Exercised | Visually verified |
|---|---|---|---|---|
| kitty | Kitty graphics protocol | ✅ | ✅ | ✅ — the TUI, an inline image, **inline video**, small-window layout and the honest fallback were all captured as real pixels |
| iTerm2 | iTerm2 inline images | ✅ | — | — (see [blockers #4](blockers.md)) |
| WezTerm | decided by probe | ✅ | — | — |
| Ghostty | Kitty graphics protocol | ✅ | — | — |
| Terminal.app | external fallback | ✅ | — | — |
| GNOME Terminal | external fallback | ✅ | — | — |
| Windows Terminal | external fallback | ✅ | — | — |
| tmux | passthrough, verified at runtime | ✅ | — | — |
| SSH | protocol bytes, not local paths | ✅ | — | — |

The external fallback **was** visually verified (forced with
`--renderer=external`): it shows dimensions, byte size, the author's
description, the reason it cannot draw inline, and how to open the media —
never a claim that a picture is on screen.

## Inline video

`gh stories` can play a Story's video **inline, as moving pixels**, using the
Kitty graphics protocol's frame animation. It is not universal:

| Requirement | Why |
|---|---|
| kitty (or another terminal implementing kitty graphics animation) | iTerm2's inline-image protocol has no frame concept |
| `ffmpeg` on the machine running the CLI | frames are decoded locally |
| **not** over SSH | frames are handed to the terminal as local files, which a remote terminal cannot read |

Where any of those is missing, the CLI shows the real poster frame and an
external-open action, and says which. It never shows a still and calls it
video. `--video=auto` (default) decides; `--video=inline` asks for it and
reports why if refused; `--video=poster` never animates.

Verified: kitty 0.43.1 on a real X display, ~120,000 pixels changing between
captures 0.35 s apart across five consecutive intervals. Captures in
`evidence/terminal/inline-video-frame-{a,b}.png`.

## Browser extension

| Browser | Built | Exercised | Visually verified |
|---|---|---|---|
| Chromium (MV3) | ✅ | ✅ **loaded into a real Chromium** — rings on GitHub avatars, viewer opened from a ring with real media, posting a Story end to end, dashboard row, popup, colour-mode following, API-outage resilience | ✅ |
| Edge (MV3) | ✅ | ✅ built manifest validated (permissions, hosts, icons, CSP) | — |
| Firefox (MV2) | ✅ | ✅ built manifest validated, including the gecko add-on id and MV2 background/browser_action shape | — (see [blockers #5](blockers.md)) |
| Safari | — | — | — (explicitly out of scope) |

The Chromium row is the only one where the extension was actually **run**.
Edge and Firefox are built and their packaged manifests are validated, but
neither was loaded into its browser, so neither is claimed as exercised in a
browser.

WXT produces MV2 for Firefox by default, and that is what is packaged. The
Chromium and Edge packages are MV3.

## Website

| Viewport | Exercised | Visually verified |
|---|---|---|
| 360 × 780 | ✅ | ✅ |
| Pixel 7 | ✅ | ✅ |
| Desktop 1280 | ✅ | ✅ |
| Wide 1920 | ✅ | ✅ |

All four run the same suite: every documented route resolves at the
`/gh-stories` project subpath on a direct visit, no console errors, no failed
asset requests, no claim of store availability, and the demo makes zero
external requests. Mobile **Safari** was not tested — the mobile project is
Chromium-based.
