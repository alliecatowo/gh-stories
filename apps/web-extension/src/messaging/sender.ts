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
export function isTrustedSender(
  sender: Browser.runtime.MessageSender | undefined,
  ownExtensionId: string,
): boolean {
  if (!sender) return false;
  if (sender.id !== ownExtensionId) return false;

  if (sender.tab) {
    if (!sender.url) return false;
    let origin: string;
    try {
      origin = new URL(sender.url).origin;
    } catch {
      return false;
    }
    return origin === GITHUB_ORIGIN;
  }

  // No `sender.tab` means this came from an extension page (popup, options,
  // or the background itself never receives its own messages this way).
  // Extension-page senders report `sender.url` as a chrome-extension:// /
  // moz-extension:// URL; confirm it belongs to us when present.
  if (sender.url) {
    try {
      const url = new URL(sender.url);
      if (url.protocol !== "chrome-extension:" && url.protocol !== "moz-extension:") return false;
      if (url.host !== ownExtensionId) return false;
    } catch {
      return false;
    }
  }
  return true;
}

/** Same trust rule, applied to `runtime.onConnect` ports (used for uploads). */
export function isTrustedPort(port: Browser.runtime.Port, ownExtensionId: string): boolean {
  return isTrustedSender(port.sender, ownExtensionId);
}
