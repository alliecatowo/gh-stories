import { describe, expect, it } from "vitest";
import { createElement } from "react";
import { act } from "react";
import { mountRingPortal, ringLayerStats } from "../../src/mount/ringLayer.js";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("ring layer", () => {
  it("renders every ring through ONE shared root and tears it down with the last ring", async () => {
    const targets = [0, 1, 2].map(() => document.body.appendChild(document.createElement("div")));
    const portals = [] as ReturnType<typeof mountRingPortal>[];
    await act(async () => {
      targets.forEach((t, i) => portals.push(mountRingPortal(t, createElement("b", null, `ring ${i}`))));
    });
    expect(ringLayerStats()).toEqual({ rings: 3, hasRoot: true });
    expect(targets.map((t) => t.textContent)).toEqual(["ring 0", "ring 1", "ring 2"]);

    await act(async () => portals[1]!.update(createElement("b", null, "changed")));
    expect(targets[1]!.textContent).toBe("changed");

    await act(async () => portals[1]!.remove());
    expect(targets[1]!.textContent).toBe("");
    expect(ringLayerStats()).toEqual({ rings: 2, hasRoot: true });

    await act(async () => {
      portals[0]!.remove();
      portals[2]!.remove();
    });
    expect(ringLayerStats()).toEqual({ rings: 0, hasRoot: false });
  });
});
