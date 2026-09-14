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

  const url = parseURL(sender.url);

  // Our own extension pages (popup, options) are trusted wherever they run.
  //
  // This check must come BEFORE the tab check: `options_ui.open_in_tab` makes
  // the options page a real tab, so `sender.tab` is set and a tab-first rule
  // would demand a github.com origin and reject the extension's own settings
  // page — which is exactly what happened.
  if (url && (url.protocol === "chrome-extension:" || url.protocol === "moz-extension:")) {
    return url.host === ownExtensionId;
  }

  // Anything else claiming to be a page must be a github.com content script.
  // The content script is only registered for https://github.com/*, but that
  // registration alone is not trusted.
  if (sender.tab) {
    return url?.origin === GITHUB_ORIGIN;
  }

  // No tab and no URL: an internal sender. Anything with a URL that reached
  // here is neither one of our pages nor a GitHub content script.
  return !sender.url;
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
export function isTrustedPort(port: Browser.runtime.Port, ownExtensionId: string): boolean {
  return isTrustedSender(port.sender, ownExtensionId);
}
