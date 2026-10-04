/**
 * Propagates GitHub's color mode onto a shadow host. `@gh-stories/ui`'s
 * theme.css already matches `:host-context([data-color-mode="dark"])`,
 * which resolves against the *light-DOM* ancestor chain (GitHub already
 * sets `data-color-mode` on `<html>`), so this is a belt-and-suspenders
 * mirror for hosts that portal outside the shadow tree or want a stable,
 * explicit signal without depending on `:host-context` support.
 */
const THEME_ATTRS = ["data-color-mode", "data-dark-theme", "data-light-theme"] as const;

function applyTheme(host: HTMLElement): void {
  const docEl = document.documentElement;
  for (const attr of THEME_ATTRS) {
    const value = docEl.getAttribute(attr);
    if (value) host.setAttribute(attr, value);
    else host.removeAttribute(attr);
  }
}

// One MutationObserver on <html> serves every host. A per-host observer was
// strongly held by the document, so every wrapped avatar leaked an observer
// (and its closed-over host) for the life of the page, and each theme change
// fanned out to all of them.
const hosts = new Set<HTMLElement>();
let sharedObserver: MutationObserver | null = null;

function ensureObserver(): void {
  if (sharedObserver) return;
  sharedObserver = new MutationObserver(() => {
    for (const host of hosts) applyTheme(host);
  });
  sharedObserver.observe(document.documentElement, { attributes: true, attributeFilter: [...THEME_ATTRS] });
}

/** Mirrors GitHub's theme attributes onto `host` immediately and keeps them
 * live. Returns a cleanup function that stops tracking the host; the shared
 * observer disconnects once the last host is gone. */
export function observeGithubTheme(host: HTMLElement): () => void {
  applyTheme(host);
  hosts.add(host);
  ensureObserver();
  return () => {
    hosts.delete(host);
    if (hosts.size === 0 && sharedObserver) {
      sharedObserver.disconnect();
      sharedObserver = null;
    }
  };
}

/** Test hook: number of hosts currently tracked. */
export function trackedThemeHostCount(): number {
  return hosts.size;
}
