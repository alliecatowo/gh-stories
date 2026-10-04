/**
 * Draws one Story ring over a GitHub avatar WITHOUT touching GitHub's DOM.
 *
 * GitHub's avatar is almost always inside a profile link, and GitHub's own
 * (React-managed) markup must never be moved or restyled: reparenting its
 * nodes breaks its reconciliation and Turbo navigation. So:
 *  - The ring lives in a Shadow DOM host inserted as a SIBLING right after
 *    GitHub's link, `position:absolute` and `pointer-events:none`, then
 *    translated so it sits exactly over the avatar (measured, so it does not
 *    care which ancestor is the containing block). It takes no layout space,
 *    so nothing on the page shifts.
 *  - The ring itself renders through one shared React root (see
 *    `mount/ringLayer.tsx`), not a root per avatar.
 *  - The only interactive element added is one small activation badge, a
 *    sibling of (never inside) GitHub's link, with its own `aria-label`.
 */
import type { MouseEvent } from "react";
import { StoryRing, type StoryRingState } from "@gh-stories/ui";
import { createShadowMount } from "../mount/shadow.js";
import { observeGithubTheme } from "../mount/theme.js";
import { mountRingPortal } from "../mount/ringLayer.js";
import type { AccountIdentity } from "./identity.js";

export interface RingDecoration {
  update(state: StoryRingState, hasStory: boolean): void;
  /** Re-inserts the ring host if GitHub's re-render dropped it. */
  reattach(): void;
  destroy(): void;
}

export interface WrapAvatarOptions {
  size?: number;
  initialState: StoryRingState;
  hasStory: boolean;
  onActivate: () => void;
}

const OVERLAY_CSS = `
.ghs-ring-overlay-root { position: absolute; inset: 0; pointer-events: none; }
.ghs-ring-overlay-root .ghs-story-ring { pointer-events: none; }
.ghs-ring-overlay-root .ghs-story-ring__avatar { visibility: hidden; }
/* As an overlay the ring must be a true annulus: GitHub's real avatar sits
   underneath and has to show through the middle.
   StoryRing normally draws a gradient-FILLED circle and masks its centre with
   an opaque gap disc, which as an overlay would paint a solid circle straight
   over the person. Instead the gradient is confined to the border box and the
   padding box is masked out, leaving a ring with a genuinely transparent
   centre. */
.ghs-ring-overlay-root .ghs-story-ring__gap { background: transparent; }

.ghs-ring-overlay-root .ghs-story-ring__frame {
  background-color: transparent;
  background-image: none;
  border: 2.5px solid transparent;
  border-radius: 999px;
  /* Confine the gradient to the border box, then mask the padding box away.
     What survives is the border ring alone, so the avatar underneath is
     genuinely visible through the middle. */
  background-origin: border-box;
  -webkit-mask:
    linear-gradient(#000 0 0) padding-box,
    linear-gradient(#000 0 0);
  -webkit-mask-composite: xor;
  mask:
    linear-gradient(#000 0 0) padding-box,
    linear-gradient(#000 0 0);
  mask-composite: exclude;
}
.ghs-ring-overlay-root .ghs-story-ring__frame[data-state="unseen"] {
  background-image: linear-gradient(135deg, var(--ghs-ring-unseen-start, #e254a8), var(--ghs-ring-unseen-end, #8f6ae8));
}
.ghs-ring-overlay-root .ghs-story-ring__frame[data-state="seen"] {
  background-image: linear-gradient(var(--ghs-ring-seen, #8c959f) 0 0);
}
.ghs-ring-overlay-root .ghs-story-ring__frame[data-state="muted"] {
  background-image: linear-gradient(var(--ghs-ring-muted, #8c959f) 0 0);
  border-style: dashed;
}
.ghs-ring-overlay-root .ghs-story-ring__frame[data-state="none"] {
  background-image: none;
}

.ghs-ring-overlay-badge {
  position: absolute;
  right: -1px;
  bottom: -1px;
  width: 13px;
  height: 13px;
  border-radius: 999px;
  border: 2px solid var(--ghs-canvas, #fff);
  background: linear-gradient(135deg, var(--ghs-ring-unseen-start, #e254a8), var(--ghs-ring-unseen-end, #8f6ae8));
  padding: 0;
  cursor: pointer;
  pointer-events: auto;
}
.ghs-ring-overlay-badge[data-state="seen"] { background: var(--ghs-ring-seen, #d1d9e0); }
.ghs-ring-overlay-badge[data-state="muted"] { background: var(--ghs-ring-muted, #8c959f); }
`;

const RING_PAD = 4;
const FALLBACK_SIZE = 20;

/** Avatar diameter: explicit option, else the rendered box, else the
 * intrinsic `width` (0 while unloaded, hence `||`, not `??`), else a default. */
export function measureAvatarSize(img: HTMLImageElement, explicit?: number): number {
  if (explicit && explicit > 0) return explicit;
  const rendered = img.getBoundingClientRect().width;
  return Math.round(rendered || img.width || FALLBACK_SIZE) || FALLBACK_SIZE;
}

// ---- positioning: one rAF-batched pass, reads before writes ------------
interface Positioner {
  host: HTMLElement;
  img: HTMLImageElement;
  outer: () => number;
}
const positioners = new Set<Positioner>();
let frame = 0;
let listening = false;
let resizeObserver: ResizeObserver | null = null;
const observedBy = new Map<Element, number>();

function layoutAll(): void {
  frame = 0;
  const reads: Array<{ p: Positioner; dx: number; dy: number }> = [];
  for (const p of positioners) {
    if (!p.host.isConnected || !p.img.isConnected) continue;
    // Measure with the transform cleared so the host's static position is the
    // reference point, whatever the containing block is.
    p.host.style.transform = "none";
  }
  for (const p of positioners) {
    if (!p.host.isConnected || !p.img.isConnected) continue;
    const img = p.img.getBoundingClientRect();
    const host = p.host.getBoundingClientRect();
    reads.push({
      p,
      dx: img.left + img.width / 2 - p.outer() / 2 - host.left,
      dy: img.top + img.height / 2 - p.outer() / 2 - host.top,
    });
  }
  for (const { p, dx, dy } of reads) {
    p.host.style.transform = `translate(${Math.round(dx * 100) / 100}px, ${Math.round(dy * 100) / 100}px)`;
    p.host.style.visibility = p.img.getBoundingClientRect().width === 0 ? "hidden" : "";
  }
}

/** Re-measures every ring on the next frame (call after layout may have moved). */
export function scheduleRingLayout(): void {
  if (frame || positioners.size === 0) return;
  frame = requestAnimationFrame(layoutAll);
}

function observe(el: Element): void {
  observedBy.set(el, (observedBy.get(el) ?? 0) + 1);
  resizeObserver?.observe(el);
}
function unobserve(el: Element): void {
  const n = (observedBy.get(el) ?? 1) - 1;
  if (n > 0) {
    observedBy.set(el, n);
    return;
  }
  observedBy.delete(el);
  resizeObserver?.unobserve(el);
}

function startListening(): void {
  if (listening) return;
  listening = true;
  window.addEventListener("resize", scheduleRingLayout);
  if (typeof ResizeObserver !== "undefined") {
    resizeObserver = new ResizeObserver(scheduleRingLayout);
    for (const el of observedBy.keys()) resizeObserver.observe(el);
    // Layout shifts of the whole page (content above growing) move every ring.
    resizeObserver.observe(document.documentElement);
  }
}
function stopListening(): void {
  if (!listening) return;
  listening = false;
  window.removeEventListener("resize", scheduleRingLayout);
  resizeObserver?.disconnect();
  resizeObserver = null;
  if (frame) cancelAnimationFrame(frame);
  frame = 0;
}

export function wrapAvatarWithRing(identity: AccountIdentity, options: WrapAvatarOptions): RingDecoration {
  const { anchor, avatarImg } = identity;
  const size = measureAvatarSize(avatarImg, options.size);
  const outer = size + 2 * RING_PAD;

  const mount = createShadowMount({ tagName: "ghs-ring-overlay", extraCss: OVERLAY_CSS });
  // pointer-events:none on the HOST itself, not just on the root inside the
  // shadow: the host covers the whole avatar, so with default pointer events
  // it silently swallows every click, modified-click and context-menu meant
  // for GitHub's profile link. The badge re-enables pointer events for itself.
  mount.host.style.cssText = `position:absolute; pointer-events:none; width:${outer}px; height:${outer}px; margin:0; padding:0; border:0; z-index:1;`;
  mount.host.dataset.ghsLogin = identity.login;
  anchor.insertAdjacentElement("afterend", mount.host);
  const stopTheme = observeGithubTheme(mount.host);

  const positioner: Positioner = { host: mount.host, img: avatarImg, outer: () => outer };
  positioners.add(positioner);
  startListening();
  observe(avatarImg);
  scheduleRingLayout();

  let state = options.initialState;
  let hasStory = options.hasStory;

  function handleBadgeClick(event: MouseEvent<HTMLButtonElement>) {
    event.preventDefault();
    event.stopPropagation();
    options.onActivate();
  }

  function view() {
    return (
      <div className="ghs-root ghs-ring-overlay-root">
        <StoryRing
          src={identity.avatarUrl}
          alt=""
          size={size}
          state={state}
          hasStory={hasStory}
          label={
            hasStory
              ? state === "unseen"
                ? `${identity.login} has an unseen Story`
                : state === "muted"
                  ? `${identity.login}'s Story, muted`
                  : `${identity.login} has a Story`
              : `${identity.login}, no active Story`
          }
        />
        {hasStory ? (
          <button
            type="button"
            className="ghs-ring-overlay-badge"
            data-state={state}
            aria-label={`Open ${identity.login}'s Story`}
            onClick={handleBadgeClick}
          />
        ) : null}
      </div>
    );
  }

  const portal = mountRingPortal(mount.container, view());
  let destroyed = false;

  return {
    update(nextState, nextHasStory) {
      if (destroyed) return;
      state = nextState;
      hasStory = nextHasStory;
      portal.update(view());
    },
    reattach() {
      if (destroyed || mount.host.isConnected || !anchor.isConnected) return;
      anchor.insertAdjacentElement("afterend", mount.host);
      scheduleRingLayout();
    },
    destroy() {
      if (destroyed) return;
      destroyed = true;
      stopTheme();
      portal.remove();
      positioners.delete(positioner);
      unobserve(avatarImg);
      if (positioners.size === 0) stopListening();
      mount.destroy();
    },
  };
}
