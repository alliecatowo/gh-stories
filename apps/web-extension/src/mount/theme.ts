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

/** Mirrors GitHub's theme attributes onto `host` immediately and keeps them
 * live. Returns a cleanup function that stops observing. */
export function observeGithubTheme(host: HTMLElement): () => void {
  applyTheme(host);
  const observer = new MutationObserver(() => applyTheme(host));
  observer.observe(document.documentElement, { attributes: true, attributeFilter: [...THEME_ATTRS] });
  return () => observer.disconnect();
}
