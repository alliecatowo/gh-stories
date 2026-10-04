import { afterEach, describe, expect, it } from "vitest";
import { act } from "react";
import { extractIdentity } from "../../src/adapters/identity.js";
import { measureAvatarSize, wrapAvatarWithRing } from "../../src/adapters/wrapAvatar.js";
import { ringLayerStats } from "../../src/mount/ringLayer.js";
import { trackedThemeHostCount } from "../../src/mount/theme.js";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function avatar(login: string, id: number, width = 32) {
  document.body.innerHTML = `<div id="row"><a href="/${login}" id="a"><img class="avatar" src="https://avatars.githubusercontent.com/u/${id}?s=64" width="${width}" height="${width}"></a><span>after</span></div>`;
  const img = document.querySelector("img")!;
  return extractIdentity(img as HTMLImageElement)!;
}

afterEach(() => {
  document.body.innerHTML = "";
});

describe("wrapAvatarWithRing", () => {
  it("never moves or restyles GitHub's own nodes", async () => {
    const identity = avatar("octocat", 1);
    const row = document.getElementById("row")!;
    const before = row.innerHTML;
    let deco!: ReturnType<typeof wrapAvatarWithRing>;
    await act(async () => {
      deco = wrapAvatarWithRing(identity, { initialState: "none", hasStory: false, onActivate: () => {} });
    });
    const anchor = document.getElementById("a")!;
    expect(anchor.parentElement).toBe(row);
    expect(anchor.getAttribute("style")).toBeNull();
    expect(anchor.nextElementSibling?.tagName).toBe("GHS-RING-OVERLAY");

    await act(async () => deco.destroy());
    expect(row.innerHTML).toBe(before);
  });

  it("releases the shared root and theme tracking when the last ring is destroyed", async () => {
    const a = avatar("a", 1);
    let d1!: ReturnType<typeof wrapAvatarWithRing>;
    await act(async () => {
      d1 = wrapAvatarWithRing(a, { initialState: "none", hasStory: false, onActivate: () => {} });
    });
    expect(ringLayerStats().hasRoot).toBe(true);
    expect(trackedThemeHostCount()).toBeGreaterThan(0);
    await act(async () => d1.destroy());
    await act(async () => d1.destroy()); // idempotent
    expect(ringLayerStats()).toEqual({ rings: 0, hasRoot: false });
    expect(trackedThemeHostCount()).toBe(0);
  });

  it("reattaches a host that GitHub's re-render dropped", async () => {
    const identity = avatar("octocat", 1);
    let deco!: ReturnType<typeof wrapAvatarWithRing>;
    await act(async () => {
      deco = wrapAvatarWithRing(identity, { initialState: "none", hasStory: false, onActivate: () => {} });
    });
    document.querySelector("ghs-ring-overlay")!.remove();
    deco.reattach();
    expect(document.querySelectorAll("ghs-ring-overlay")).toHaveLength(1);
    await act(async () => deco.destroy());
  });
});

describe("measureAvatarSize", () => {
  it("falls back when the width is 0 (unloaded image) instead of treating 0 as a size", () => {
    const img = document.createElement("img");
    expect(measureAvatarSize(img)).toBe(20);
    expect(measureAvatarSize(img, 48)).toBe(48);
  });
});
