/**
 * GitHub page integration. This file never mutates GitHub's own state — it
 * only reads the DOM to place decorations, and every write it triggers
 * (follow, react, reply, post) goes to the Stories service through the
 * background, never to github.com. Every surface below is wrapped so a
 * failure in one (a selector that stops matching, a rejected fetch) can
 * never disable the others or the toolbar popup.
 */
import { defineContentScript } from "wxt/utils/define-content-script";
import {
  ALL_AVATAR_SURFACES_SELECTOR,
  extractIdentity,
  findDashboardFeed,
  findProfilePage,
  wrapAvatarWithRing,
  type AccountIdentity,
  type RingDecoration,
} from "../src/adapters/index.js";
import { callBackground } from "../src/messaging/client.js";
import { OverlayHost } from "../src/content/overlayHost.js";
import { mountStoriesRow, type StoriesRowMount } from "../src/content/storiesRow.js";
import { mountProfileAffordance, type ProfileAffordanceMount } from "../src/content/profileAffordance.js";

const MUTATION_DEBOUNCE_MS = 150;
const STATUS_DEBOUNCE_MS = 50;

export default defineContentScript({
  matches: ["https://github.com/*"],
  // We build and inject our own CSS manually per Shadow DOM root (see
  // `src/mount/shadow.ts`); nothing should be auto-injected onto the page.
  cssInjectionMode: "manual",

  main(ctx) {
    const processedImgs = new WeakSet<HTMLImageElement>();
    const tracked = new WeakMap<HTMLImageElement, { identity: AccountIdentity; decoration: RingDecoration }>();

    const overlay = new OverlayHost();
    let dashboardRow: StoriesRowMount | null = null;
    let profileAffordance: ProfileAffordanceMount | null = null;
    let currentProfileLogin: string | null = null;

    let pendingIdentities: AccountIdentity[] = [];
    let statusTimer: number | undefined;

    function scheduleStatusFlush(): void {
      if (statusTimer !== undefined) return;
      statusTimer = window.setTimeout(() => {
        statusTimer = undefined;
        void flushStatusRequests();
      }, STATUS_DEBOUNCE_MS);
    }

    async function flushStatusRequests(): Promise<void> {
      const batch = pendingIdentities;
      pendingIdentities = [];
      if (batch.length === 0) return;

      const ids: number[] = [];
      const logins: string[] = [];
      for (const identity of batch) {
        if (identity.githubUserId !== undefined) ids.push(identity.githubUserId);
        else logins.push(identity.login);
      }

      const result = await callBackground({ type: "ghs:ring/status", githubUserIds: ids, logins });
      if (!result.ok) return;

      for (const identity of batch) {
        const entry = result.data.entries.find(
          (candidate) =>
            (identity.githubUserId !== undefined && candidate.github_user_id === identity.githubUserId) ||
            (candidate.login && candidate.login.toLowerCase() === identity.login.toLowerCase()),
        );
        const record = tracked.get(identity.avatarImg);
        if (!record) continue;
        if (!entry || !entry.has_active) {
          record.decoration.update("none", false);
        } else if (entry.muted) {
          record.decoration.update("muted", true);
        } else if (entry.has_unseen) {
          record.decoration.update("unseen", true);
        } else {
          record.decoration.update("seen", true);
        }
      }
    }

    function decorateAvatars(root: ParentNode): void {
      const imgs = root.querySelectorAll<HTMLImageElement>(ALL_AVATAR_SURFACES_SELECTOR);
      for (const img of imgs) {
        if (processedImgs.has(img)) continue;
        processedImgs.add(img);

        const identity = extractIdentity(img);
        if (!identity) continue;

        const decoration = wrapAvatarWithRing(identity, {
          initialState: "none",
          hasStory: false,
          onActivate: () => void overlay.openViewerForLogin(identity.login),
        });
        tracked.set(img, { identity, decoration });
        pendingIdentities.push(identity);
      }
      if (pendingIdentities.length > 0) scheduleStatusFlush();
    }

    function decorateDashboard(root: ParentNode): void {
      if (dashboardRow) return;
      const feed = findDashboardFeed(root);
      if (feed) dashboardRow = mountStoriesRow(feed, overlay);
    }

    function decorateProfile(): void {
      const info = findProfilePage(document);
      if (!info) {
        if (profileAffordance) {
          profileAffordance.destroy();
          profileAffordance = null;
          currentProfileLogin = null;
        }
        return;
      }
      if (profileAffordance && currentProfileLogin === info.login) return;
      profileAffordance?.destroy();
      currentProfileLogin = info.login;
      profileAffordance = mountProfileAffordance(info.container, info.login, overlay);
    }

    /** Every surface is isolated in its own try/catch: a thrown error while
     * decorating comment avatars must never stop the dashboard row (or vice
     * versa), and neither can ever disable the toolbar popup, which talks
     * to the background directly. */
    function processSubtree(root: ParentNode): void {
      try {
        decorateDashboard(root);
      } catch {
        /* GitHub keeps working; this surface just sits out this pass. */
      }
      try {
        decorateProfile();
      } catch {
        /* likewise */
      }
      try {
        decorateAvatars(root);
      } catch {
        /* likewise */
      }
    }

    // ------------------------------------------------------ mutation watch
    const mutatedRoots = new Set<ParentNode>();
    let mutationTimer: number | undefined;

    function scheduleMutationFlush(): void {
      if (mutationTimer !== undefined) return;
      mutationTimer = window.setTimeout(() => {
        mutationTimer = undefined;
        const roots = Array.from(mutatedRoots);
        mutatedRoots.clear();
        for (const root of roots) processSubtree(root);
      }, MUTATION_DEBOUNCE_MS);
    }

    const observer = new MutationObserver((mutations) => {
      for (const mutation of mutations) {
        for (const node of mutation.addedNodes) {
          if (node instanceof Element) mutatedRoots.add(node);
        }
      }
      if (mutatedRoots.size > 0) scheduleMutationFlush();
    });
    observer.observe(document.documentElement, { childList: true, subtree: true });

    // --------------------------------------------------------- navigation
    let navigationTimer: number | undefined;
    function handleNavigation(): void {
      if (navigationTimer !== undefined) return;
      navigationTimer = window.setTimeout(() => {
        navigationTimer = undefined;
        void callBackground({ type: "ghs:ring/cancel" });
        dashboardRow?.destroy();
        dashboardRow = null;
        processSubtree(document.body);
      }, 0);
    }

    document.addEventListener("turbo:load", handleNavigation);
    document.addEventListener("turbo:render", handleNavigation);
    document.addEventListener("pjax:end", handleNavigation);
    window.addEventListener("popstate", handleNavigation);

    const originalPushState = history.pushState.bind(history);
    const originalReplaceState = history.replaceState.bind(history);
    history.pushState = function patchedPushState(...args: Parameters<History["pushState"]>) {
      const result = originalPushState(...args);
      handleNavigation();
      return result;
    };
    history.replaceState = function patchedReplaceState(...args: Parameters<History["replaceState"]>) {
      const result = originalReplaceState(...args);
      handleNavigation();
      return result;
    };

    // ------------------------------------------------------------ startup
    processSubtree(document.body);

    // ----------------------------------------------------------- teardown
    ctx.onInvalidated(() => {
      observer.disconnect();
      if (mutationTimer !== undefined) window.clearTimeout(mutationTimer);
      if (statusTimer !== undefined) window.clearTimeout(statusTimer);
      if (navigationTimer !== undefined) window.clearTimeout(navigationTimer);
      document.removeEventListener("turbo:load", handleNavigation);
      document.removeEventListener("turbo:render", handleNavigation);
      document.removeEventListener("pjax:end", handleNavigation);
      window.removeEventListener("popstate", handleNavigation);
      history.pushState = originalPushState;
      history.replaceState = originalReplaceState;
      dashboardRow?.destroy();
      profileAffordance?.destroy();
      overlay.destroy();
    });
  },
});
