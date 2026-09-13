/**
 * Mounts the restrained Stories row near the top of the dashboard feed:
 * own avatar with a post control first, then followed accounts, with
 * unseen/seen state and a useful empty state — all handled by `StoryRow`
 * itself. Renders nothing (rather than a broken row) when signed out or
 * when the service can't be reached, so GitHub's own dashboard is never
 * disturbed by a failure here.
 */
import { createRoot, type Root } from "react-dom/client";
import { StoryRow } from "@gh-stories/ui";
import { createShadowMount, type ShadowMount } from "../mount/shadow.js";
import { observeGithubTheme } from "../mount/theme.js";
import { callBackground } from "../messaging/client.js";
import type { OverlayHost } from "./overlayHost.js";

export interface StoriesRowMount {
  destroy(): void;
}

export function mountStoriesRow(insertBefore: Element, overlay: OverlayHost): StoriesRowMount {
  const mount: ShadowMount = createShadowMount({ tagName: "ghs-stories-row" });
  mount.host.style.cssText = "display: block; margin-bottom: 16px;";
  insertBefore.parentElement?.insertBefore(mount.host, insertBefore);
  const stopTheme = observeGithubTheme(mount.host);
  const root: Root = createRoot(mount.container);
  let cancelled = false;

  async function load(): Promise<void> {
    const [sessionResult, feedResult] = await Promise.all([
      callBackground({ type: "ghs:session/get" }),
      callBackground({ type: "ghs:feed/get" }),
    ]);
    if (cancelled) return;

    if (!sessionResult.ok || !sessionResult.data.signedIn || !feedResult.ok) {
      root.render(null);
      return;
    }

    const feed = feedResult.data;
    const account = sessionResult.data.account;
    const me = account
      ? { login: account.login, avatarUrl: account.avatarUrl, hasStory: (feed.me?.items.length ?? 0) > 0 }
      : null;

    root.render(
      <div className="ghs-root">
        <StoryRow
          me={me}
          groups={feed.groups}
          onOpenGroup={(login) => {
            const index = feed.groups.findIndex((group) => group.author.login === login);
            if (index >= 0) overlay.openViewerForGroups(feed.groups, index);
          }}
          onOpenOwn={() => {
            if (feed.me) overlay.openViewerForGroups([feed.me], 0);
          }}
          onCreate={() => overlay.openComposer()}
        />
      </div>,
    );
  }

  void load();

  return {
    destroy() {
      cancelled = true;
      stopTheme();
      root.unmount();
      mount.destroy();
    },
  };
}
