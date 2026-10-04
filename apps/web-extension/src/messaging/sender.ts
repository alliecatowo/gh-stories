/**
 * Sender validation for `runtime.onMessage`. Every handler in the background
 * must call `isTrustedSender` before doing anything with a message — this is
 * the boundary that keeps a compromised github.com page (or any other
 * extension) from ever reaching the session token.
 */
import type { Browser } from "wxt/browser";

const GITHUB_ORIGIN = "https://github.com";

/**
 * A sender is trusted when:
 *  - it belongs to this extension (`sender.id === browser.runtime.id`), and
 *  - it is either an extension page (popup/options — no `sender.tab`, no
 *    `sender.url` outside our own extension origin), or a content script
 *    running in a tab whose URL origin is exactly `https://github.com`.
 *
 * A message with `sender.tab` set but a `sender.url` that is not exactly the
 * GitHub origin (subdomains, gist.github.com, raw content hosts, etc.) is
 * rejected — the content script is only ever registered for
 * `https://github.com/*`, but we do not trust that registration alone.
 */
export type SenderKind = "extension-page" | "content-script";

/**
 * Classifies a sender, or returns null when it must not be trusted.
 *
 * `ownOrigin` is `new URL(runtime.getURL("/")).origin`. On Chromium it is
 * `chrome-extension://<id>`, but on Firefox it is `moz-extension://<random
 * per-install uuid>`, which is not `runtime.id`; comparing against the id
 * rejects every popup/options message there. When omitted it falls back to
 * the Chromium form derived from the id.
 */
export function senderKind(
  sender: Browser.runtime.MessageSender | undefined,
  ownExtensionId: string,
  ownOrigin?: string,
): SenderKind | null {
  if (!sender) return null;
  if (sender.id !== ownExtensionId) return null;

  const url = parseURL(sender.url);
  const origin = ownOrigin ?? `chrome-extension://${ownExtensionId}`;

  // Our own extension pages (popup, options) are trusted wherever they run.
  // Must come BEFORE the tab check: `options_ui.open_in_tab` makes the options
  // page a real tab, so `sender.tab` is set.
  if (url && (url.protocol === "chrome-extension:" || url.protocol === "moz-extension:")) {
    // `URL.origin` is the string "null" for non-special schemes in some
    // runtimes, so build it by hand.
    return `${url.protocol}//${url.host}` === origin ? "extension-page" : null;
  }

  // Anything else claiming to be a page must be a github.com content script.
  if (sender.tab) {
    return url?.origin === GITHUB_ORIGIN ? "content-script" : null;
  }

  // No tab and no URL: an internal sender (background / popup).
  return sender.url ? null : "extension-page";
}

export function isTrustedSender(
  sender: Browser.runtime.MessageSender | undefined,
  ownExtensionId: string,
  ownOrigin?: string,
): boolean {
  return senderKind(sender, ownExtensionId, ownOrigin) !== null;
}

function parseURL(value: string | undefined): URL | null {
  if (!value) return null;
  try {
    return new URL(value);
  } catch {
    return null;
  }
}

/** Same trust rule, applied to `runtime.onConnect` ports (used for uploads). */
export function isTrustedPort(
  port: Browser.runtime.Port,
  ownExtensionId: string,
  ownOrigin?: string,
): boolean {
  return isTrustedSender(port.sender, ownExtensionId, ownOrigin);
}
