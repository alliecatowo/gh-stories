# Visual verification report

Everything below was **run and then looked at**. A capture nobody inspected is
not evidence, so each item says what was seen and what was wrong with it.

- **Commits under test:** `92d2aa1` (release `v0.1.0`) and `9d83fa6` (release `v0.2.0`, inline video)
- **Published artifacts exercised:** `gh-stories_v0.1.0_linux-arm64` from the
  GitHub Release (SHA-256 verified against `checksums.txt`) and
  `ghcr.io/alliecatowo/gh-stories:v0.1.0` pulled anonymously from GHCR.
- **Host:** macOS 15 (Darwin 25.4.0), Apple Silicon.
- **Real terminal:** kitty on Debian bookworm, Xvfb `:99` at 1280×800×24, Mesa
  llvmpipe. Captures are the X display's actual framebuffer (`import -window
  root`), not a rendering of escape bytes.
- **Real browser:** Chromium 153 via Playwright 1.63.

## Why a container for the terminal

"The picture actually appeared" cannot be established by printing escape
sequences, by a mocked renderer, or by an asciinema recording. It needs a real
terminal emulator drawing to a real display, and a capture of those pixels.
macOS `screencapture` is unavailable to this process ("could not create image
from display" — no Screen Recording permission), so iTerm2 could not be
captured here. kitty on a virtual X display gives genuine pixels from a genuine
implementation of the Kitty graphics protocol, and that is what was used.

## Terminal scenarios

| Scenario | Capture | What I saw | Result |
|---|---|---|---|
| Kitty graphics protocol works at all | `terminal/kitty-protocol-smoke.png` | A fixture image filling the terminal, drawn by `kitty +kitten icat`. Confirms the harness itself renders real graphics. | ✅ |
| **The product, published artifacts** | `terminal/published-v0.1.0-kitty.png` | The released binary against the released image: a concert photo drawn inline, three progress segments (first complete, second in progress, third pending), `● alice · now · Public — anyone signed in`, the caption, and the key hints. | ✅ |
| Story sequence, dev build | `terminal/terminal-story-sequence.png` | A portrait cat photo, centred, aspect preserved, not cropped. | ✅ |
| Next-item cleanup | same capture, after a fix | **Defect found.** The first capture showed the *previous* image still on screen above the current one. The clear escape was emitted from an async command that could land after the replacement was drawn. Fixed by emitting the delete in the same write as the new placement, and by skipping re-transmission when the picture and geometry are unchanged. Re-captured: one image. | ✅ after fix |
| Small window (54×18) | `terminal/terminal-small-window.png` | Image scaled down proportionally, layout intact, key hints truncated with `…`. | ✅ |
| **Inline video** | `terminal/inline-video-frame-{a,b}.png` | A Story's video playing as moving pixels in kitty: ~120,000 pixels changed between captures 0.35 s apart across five consecutive intervals, and the zoom has visibly advanced between the two saved frames. | ✅ |
| Unsupported terminal fallback | `terminal/terminal-fallback.png` | `[image] · 1080×1920 · 0.3 MB`, the author's accessibility description, the explicit reason (`renderer forced via override`), and `Press Enter to open it in your browser.` It never implies a picture is on screen. | ✅ |
| Viewer ends after the last item | observed during capture | The viewer exits cleanly when a sequence finishes. Initially looked like a blank-capture bug; it is correct behaviour, and the capture was simply taken after the sequence ended. | ✅ |

### What inline video cost to get right

Four things had to be discovered by driving a real terminal, because the
obvious reading of the kitty specification does not work:

1. The image must be addressed by **number** (`I=`), not id (`i=`). With `i=`,
   kitty replied `r=2` to every appended frame — each one overwriting the last.
2. Frame pixels must arrive **atomically as a file** (`t=f`). Chunked inline
   transmission reproduced the same overwrite. This is also exactly why inline
   video cannot work over SSH.
3. `q=2` suppresses **error** replies as well as OK, which hid both of the
   above. `GHS_DEBUG_ANIMATION=1` now turns errors back on.
4. The escapes must reach the terminal directly rather than through Bubble
   Tea's line-diffing renderer, and the poster frame placed while the video was
   decoding has to be cleared first — otherwise it sits on top and a playing
   video looks like a still.

The working sequence was recovered by capturing what kitty's own `icat` emits
for an animated GIF, and is now pinned by tests.

Not captured, and therefore **not claimed**: iTerm2, WezTerm, Ghostty,
Terminal.app, Windows Terminal, tmux passthrough, SSH. See
[docs/support-matrix.md](../docs/support-matrix.md) and
[docs/blockers.md](../docs/blockers.md).

## Browser scenarios (website)

24 checks across four viewports — 360×780, Pixel 7, 1280, and 1920 — in
`browser/`:

| Check | Result |
|---|---|
| Landing page states the product and carries the independence disclaimer | ✅ |
| Every documented route resolves **at the `/gh-stories` subpath on a direct visit** | ✅ |
| No console errors, no failed asset requests | ✅ |
| No claim of store availability; any store mention is a disclaimer | ✅ |
| The sample demo makes **zero** external requests | ✅ |
| Dark mode renders | ✅ |

Three defects found by looking at the captures:

1. The hero terminal showed **`gh stories view otterframes`** — a command that
   does not exist. Now `gh stories @otterframes`.
2. Two words ran into a link with no space.
3. The install command overflowed its block at 360px.

And one case where the **test** was wrong rather than the product: a naive
substring ban on "chrome web store" flagged the page for honestly saying it is
*not* in the store. The check now forbids the claim, not the words.

## Cross-client, with published artifacts only

Released `linux-arm64` binary ↔ `ghcr.io/alliecatowo/gh-stories:v0.1.0`, against
real PostgreSQL 18.6 and real MinIO:

1. `gh stories post cat.jpg --caption "…" --audience public` → `uploaded.
   processing…` → `Posted. It disappears Mon 14:34.`
2. A second account's feed shows the item.
3. The second account replies and reacts from the terminal.
4. The author's inbox shows both, unread count 2.
5. `gh stories viewers` reports **no views** — correct, because listing a feed
   never delivers media, and views are recorded on delivery.

One real deployment finding: the presigned upload URL is built from
`GHS_S3_ENDPOINT`, so that endpoint must be reachable **by clients**, not only
by the service. Pointing it at a service-only hostname makes uploads fail at the
PUT. This is now called out in the runbook.

## Browser extension, loaded into a real Chromium

A persistent Chromium context with the built MV3 extension loaded, driving
`https://github.com/*` URLs whose content comes from a fictional, hand-written
fixture. The URL is real, so the content script matches exactly as it would in
the wild; no real page, account or private content is involved.

| Scenario | Capture | Result |
|---|---|---|
| Story rings on GitHub avatars | `browser/extension-pr-rings.png` | ✅ ring around the avatar, avatar visible through the centre, activation badge; bots/apps undecorated |
| Viewer opened from a ring | `browser/extension-viewer-open.png` | ✅ Story plays over the dimmed PR, with real media |
| Closing returns to the page | `browser/extension-after-close.png` | ✅ GitHub intact, links unchanged |
| Posting from the browser | `browser/extension-composer{,-published}.png` | ✅ preview → plain-language audience → uploading → processing → "Story posted.", confirmed on the service |
| Dashboard Stories row | `browser/extension-dashboard-row.png` | ✅ |
| Toolbar popup with no GitHub tab open | `browser/extension-popup.png` | ✅ |
| API outage | — | ✅ GitHub stays completely usable |

Six defects came out of actually running it, none of which unit tests could
have found:

1. **The background never started.** It used `browser.alarms` without the
   `alarms` permission, so the service worker threw during init and never
   registered its message listener. The popup hung on "Loading…" forever.
2. **People disappeared from the page.** The ring's inner disc is opaque by
   design; as an overlay it painted a solid circle over GitHub's real avatar.
3. **Clicks were swallowed.** The overlay host covered the avatar with default
   pointer events, intercepting every click and modified-click meant for
   GitHub's profile link.
4. **The ring was a filled circle, not a ring.** Fixed by confining the
   gradient to the border box and masking the padding box away.
5. **Media never loaded.** `runtime.sendMessage` serialises with JSON, not
   structured clone, so an ArrayBuffer of media bytes arrived as `{}` and every
   Blob was silently garbage. Uploads stalled at 0% for the same reason over a
   port. Both now travel as chunked base64.
6. **The settings page could not talk to its own background.**
   `options_ui.open_in_tab` gives it a `sender.tab`, and the trust rule then
   demanded a github.com origin and rejected it.

Numbers 2, 3 and 4 were found by opening the screenshot and looking at it, not
by a failing assertion. Two of my own tests were also wrong rather than the
product: the popup check matched a word the signed-OUT screen contains, and the
composer check matched "published" inside the instructional copy. Both now
assert something that can only be true if the feature works.

## Not done

- **Firefox and Edge were not loaded into their browsers.** Both are built and
  their packaged manifests are validated (permissions, host access, icons, CSP,
  the gecko add-on id, the MV2 background/browser_action shape), but neither
  was run. They are not claimed as exercised in a browser.
- No live-service capture: nothing is deployed.
