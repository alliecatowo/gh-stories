/**
 * Mounts React into an isolated Shadow DOM root so GitHub's page CSS and
 * ours can never collide. Every content-script injection (ring overlays,
 * the Stories row, the viewer/composer opened from the page) gets its own
 * shadow host built through this module.
 */
// `?inline` returns the stylesheet as a string instead of injecting it into
// the host document — exactly what a manually-managed shadow root needs.
// eslint-disable-next-line import/no-unresolved
import stylesText from "@gh-stories/ui/styles.css?inline";

export interface ShadowMount {
  /** The light-DOM element GitHub's layout actually sees. Give it a fixed
   * footprint before appending so surrounding layout never jumps. */
  host: HTMLElement;
  shadow: ShadowRoot;
  /** React root target, already inside the shadow tree with styles loaded. */
  container: HTMLDivElement;
  destroy(): void;
}

export interface ShadowMountOptions {
  tagName?: string;
  mode?: "open" | "closed";
  /** Extra CSS appended after the shared stylesheet, e.g. host-specific
   * layout rules that don't belong in the shared package. */
  extraCss?: string;
}

export function createShadowMount(options: ShadowMountOptions = {}): ShadowMount {
  const host = document.createElement(options.tagName ?? "ghs-root-host");
  // Reset any inherited page styles on the host element itself; the shadow
  // boundary already isolates its contents, but the host element is still
  // light DOM and can inherit `display`, `font`, etc. from GitHub's CSS.
  host.style.all = "initial";
  host.style.display = "inline-block";

  const shadow = host.attachShadow({ mode: options.mode ?? "open" });
  const style = document.createElement("style");
  style.textContent = options.extraCss ? `${stylesText}\n${options.extraCss}` : stylesText;
  shadow.appendChild(style);

  const container = document.createElement("div");
  container.className = "ghs-shadow-container";
  shadow.appendChild(container);

  return {
    host,
    shadow,
    container,
    destroy: () => host.remove(),
  };
}
