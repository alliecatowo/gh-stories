/**
 * Wraps one GitHub avatar with a Story ring, without ever nesting a new
 * interactive element inside GitHub's own `<a>`. GitHub's avatar is almost
 * always already inside a profile link; putting a `<button>` inside that
 * link would be invalid HTML and would break the link's own click target.
 *
 * Strategy:
 *  - Reserve a fixed-size `slot` matching the ring's full footprint
 *    (avatar size + ring width/gap) and reparent GitHub's original
 *    `<a><img></a>` into it, completely untouched — same listeners, same
 *    `data-hovercard-*` attributes, same href, same keyboard focus order.
 *  - Render `StoryRing` as a purely decorative overlay on top
 *    (`pointer-events: none`, and its own inner `<img>` hidden so GitHub's
 *    real avatar shows through the ring's hollow center) — every click
 *    still reaches GitHub's link normally, including modified clicks.
 *  - Add one small activation badge as a SIBLING of GitHub's anchor (never
 *    inside it) with its own `aria-label`, which is the only additional
 *    interactive element this module introduces.
 */
import type { MouseEvent } from "react";
import { createRoot, type Root } from "react-dom/client";
import { StoryRing, type StoryRingState } from "@gh-stories/ui";
import { createShadowMount } from "../mount/shadow.js";
import { observeGithubTheme } from "../mount/theme.js";
import type { AccountIdentity } from "./identity.js";

export interface RingDecoration {
  update(state: StoryRingState, hasStory: boolean): void;
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
.ghs-ring-overlay-badge {
  position: absolute;
  right: -1px;
  bottom: -1px;
  width: 13px;
  height: 13px;
  border-radius: 999px;
  border: 2px solid var(--ghs-canvas, #fff);
  background: linear-gradient(135deg, var(--ghs-ring-unseen-start, #e5486f), var(--ghs-ring-unseen-end, #f0883e));
  padding: 0;
  cursor: pointer;
  pointer-events: auto;
}
.ghs-ring-overlay-badge[data-state="seen"] { background: var(--ghs-ring-seen, #d1d9e0); }
.ghs-ring-overlay-badge[data-state="muted"] { background: var(--ghs-ring-muted, #8c959f); }
`;

export function wrapAvatarWithRing(identity: AccountIdentity, options: WrapAvatarOptions): RingDecoration {
  const { anchor } = identity;
  const size = options.size ?? identity.avatarImg.width ?? 20;
  const outer = size + 8;

  const slot = document.createElement("span");
  slot.className = "ghs-avatar-slot";
  slot.style.cssText = `position:relative; display:inline-block; width:${outer}px; height:${outer}px; line-height:0; vertical-align:middle;`;

  const originalAnchorPosition = anchor.style.position;
  anchor.replaceWith(slot);
  slot.appendChild(anchor);
  anchor.style.position = "absolute";
  anchor.style.inset = "0";
  anchor.style.display = "flex";
  anchor.style.alignItems = "center";
  anchor.style.justifyContent = "center";

  const mount = createShadowMount({ tagName: "ghs-ring-overlay", extraCss: OVERLAY_CSS });
  mount.host.style.cssText = "position:absolute; inset:0;";
  slot.appendChild(mount.host);
  const stopTheme = observeGithubTheme(mount.host);

  const root: Root = createRoot(mount.container);
  let state = options.initialState;
  let hasStory = options.hasStory;

  function handleBadgeClick(event: MouseEvent<HTMLButtonElement>) {
    event.preventDefault();
    event.stopPropagation();
    options.onActivate();
  }

  function render() {
    root.render(
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
      </div>,
    );
  }
  render();

  return {
    update(nextState, nextHasStory) {
      state = nextState;
      hasStory = nextHasStory;
      render();
    },
    destroy() {
      stopTheme();
      root.unmount();
      mount.destroy();
      anchor.style.position = originalAnchorPosition;
      anchor.style.inset = "";
      anchor.style.display = "";
      anchor.style.alignItems = "";
      anchor.style.justifyContent = "";
      slot.replaceWith(anchor);
    },
  };
}
