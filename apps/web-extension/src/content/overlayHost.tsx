/**
 * One full-page Shadow DOM overlay, lazily shown/hidden, that hosts either
 * the Story viewer or the composer on top of the current GitHub page. A
 * single instance is reused for the lifetime of the content script so
 * repeated opens don't accumulate shadow roots.
 */
import { createRoot, type Root } from "react-dom/client";
import type { AuthorGroup } from "@gh-stories/contracts";
import { createShadowMount, type ShadowMount } from "../mount/shadow.js";
import { observeGithubTheme } from "../mount/theme.js";
import { callBackground } from "../messaging/client.js";
import { ViewerBridge } from "../viewer/ViewerBridge.js";
import { ComposerBridge } from "../composer/ComposerBridge.js";

type Mode = { kind: "closed" } | { kind: "viewer"; groups: AuthorGroup[]; startGroupIndex: number } | { kind: "composer" };

export class OverlayHost {
  private readonly mount: ShadowMount;
  private readonly root: Root;
  private readonly stopTheme: () => void;
  private mode: Mode = { kind: "closed" };

  constructor(private readonly onClosed?: () => void) {
    this.mount = createShadowMount({ tagName: "ghs-overlay-host" });
    this.mount.host.style.cssText = "position: fixed; inset: 0; z-index: 2147483647; display: none;";
    this.attach();
    this.stopTheme = observeGithubTheme(this.mount.host);
    this.root = createRoot(this.mount.container);
  }

  async openViewerForLogin(login: string): Promise<void> {
    const result = await callBackground({ type: "ghs:stories/user", login });
    if (!result.ok || result.data.items.length === 0) return;
    this.openViewerForGroups([result.data], 0);
  }

  openViewerForGroups(groups: AuthorGroup[], startGroupIndex: number): void {
    this.mode = { kind: "viewer", groups, startGroupIndex };
    this.show();
  }

  openComposer(): void {
    this.mode = { kind: "composer" };
    this.show();
  }

  close(): void {
    this.mode = { kind: "closed" };
    this.mount.host.style.display = "none";
    this.root.render(null);
    this.onClosed?.();
  }

  destroy(): void {
    this.stopTheme();
    this.root.unmount();
    this.mount.destroy();
  }

  /** Turbo Drive swaps the whole <body>, which would take a host appended to
   * it along; <html> survives. Re-run before every show in case anything
   * detached it anyway. */
  attach(): void {
    if (this.mount.host.isConnected) return;
    (document.documentElement ?? document.body).appendChild(this.mount.host);
  }

  private show(): void {
    this.attach();
    this.mount.host.style.display = "block";
    this.render();
  }

  private render(): void {
    if (this.mode.kind === "viewer") {
      this.root.render(
        <ViewerBridge
          groups={this.mode.groups}
          startGroupIndex={this.mode.startGroupIndex}
          onClose={() => this.close()}
          container={this.mount.container}
        />,
      );
    } else if (this.mode.kind === "composer") {
      this.root.render(<ComposerBridge onClose={() => this.close()} container={this.mount.container} />);
    } else {
      this.root.render(null);
    }
  }
}
