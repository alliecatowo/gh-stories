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
import type { RingStatus } from "@gh-stories/contracts";
import { callBackground } from "../src/messaging/client.js";
import { isLogin } from "../src/messaging/guards.js";
import { OverlayHost } from "../src/content/overlayHost.js";
import { mountStoriesRow, type StoriesRowMount } from "../src/content/storiesRow.js";
import { mountProfileAffordance, type ProfileAffordanceMount } from "../src/content/profileAffordance.js";

const MUTATION_DEBOUNCE_MS = 150;
const STATUS_DEBOUNCE_MS = 50;
/** The service accepts at most this many ids and this many logins per request. */
const STATUS_BATCH_LIMIT = 100;
const STATUS_RETRY_DELAYS_MS = [1_000, 5_000, 20_000];
/** Rings are re-read when the tab becomes visible again after this long. */
const STATUS_REFRESH_AFTER_MS = 60_000;

export default defineContentScript({
  matches: ["https://github.com/*"],
  // We build and inject our own CSS manually per Shadow DOM root (see
  // `src/mount/shadow.ts`); nothing should be auto-injected onto the page.
  cssInjectionMode: "manual",

  main(ctx) {
    const processedImgs = new WeakSet<HTMLImageElement>();
    // A strong Map (not a WeakMap) so removed avatars can be found and torn
    // down: each decoration owns a React root and a shadow host.
    const tracked = new Map<HTMLImageElement, { identity: AccountIdentity; decoration: RingDecoration }>();
    const statusAttempts = new WeakMap<HTMLImageElement, number>();
    let lastStatusRefresh = Date.now();

    const overlay = new OverlayHost(() => refreshStatuses());
    let dashboardRow: StoriesRowMount | null = null;
    let profileAffordance: ProfileAffordanceMount | null = null;
    let currentProfileLogin: string | null = null;

    let pendingIdentities: AccountIdentity[] = [];
    const retryTimers = new Set<number>();
    let statusTimer: number | undefined;

    function scheduleStatusFlush(): void {
      if (statusTimer !== undefined) return;
      statusTimer = window.setTimeout(() => {
        statusTimer = undefined;
        void flushStatusRequests();
      }, STATUS_DEBOUNCE_MS);
    }

    function chunk<T>(values: T[]): T[][] {
      const out: T[][] = [];
      for (let i = 0; i < values.length; i += STATUS_BATCH_LIMIT) out.push(values.slice(i, i + STATUS_BATCH_LIMIT));
      return out;
    }

    /** Tears down decorations whose avatar GitHub has removed from the page. */
    function sweepDetached(): void {
      for (const [img, record] of tracked) {
        if (img.isConnected) continue;
        record.decoration.destroy();
        tracked.delete(img);
        processedImgs.delete(img);
      }
    }

    async function flushStatusRequests(): Promise<void> {
      const batch = pendingIdentities.filter((identity) => tracked.has(identity.avatarImg));
      pendingIdentities = [];
      if (batch.length === 0) return;

      // Deduplicate: a busy thread repeats the same few people many times.
      const ids = new Set<number>();
      const logins = new Map<string, string>();
      for (const identity of batch) {
        if (identity.githubUserId !== undefined) ids.add(identity.githubUserId);
        else logins.set(identity.login.toLowerCase(), identity.login);
      }

      const calls = [
        ...chunk([...ids]).map((githubUserIds) => ({ githubUserIds, logins: [] as string[] })),
        ...chunk([...logins.values()]).map((chunked) => ({ githubUserIds: [] as number[], logins: chunked })),
      ];
      const results = await Promise.all(
        calls.map(async (call) => ({ call, result: await callBackground({ type: "ghs:ring/status", ...call }) })),
      );

      const byId = new Map<number, RingStatus>();
      const byLogin = new Map<string, RingStatus>();
      const answeredIds = new Set<number>();
      const answeredLogins = new Set<string>();
      for (const { call, result } of results) {
        if (!result.ok) continue;
        for (const id of call.githubUserIds) answeredIds.add(id);
        for (const login of call.logins) answeredLogins.add(login.toLowerCase());
        for (const entry of result.data.entries) {
          if (entry.github_user_id !== undefined) byId.set(entry.github_user_id, entry);
          if (entry.login) byLogin.set(entry.login.toLowerCase(), entry);
        }
      }

      // A failed or cancelled request is NOT "this person has no Story":
      // leave those avatars untouched and try again, instead of caching an
      // empty ring for the rest of the page's life.
      const retry: AccountIdentity[] = [];
      for (const identity of batch) {
        const record = tracked.get(identity.avatarImg);
        if (!record) continue;
        const entry =
          (identity.githubUserId !== undefined ? byId.get(identity.githubUserId) : undefined) ??
          byLogin.get(identity.login.toLowerCase());
        const answered =
          identity.githubUserId !== undefined
            ? answeredIds.has(identity.githubUserId)
            : answeredLogins.has(identity.login.toLowerCase());
        if (!entry && !answered) {
          retry.push(identity);
          continue;
        }
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
      if (retry.length > 0) scheduleRetry(retry);
    }

    function scheduleRetry(identities: AccountIdentity[]): void {
      const due: AccountIdentity[] = [];
      let delay = STATUS_RETRY_DELAYS_MS[0]!;
      for (const identity of identities) {
        const attempt = statusAttempts.get(identity.avatarImg) ?? 0;
        if (attempt >= STATUS_RETRY_DELAYS_MS.length) continue;
        statusAttempts.set(identity.avatarImg, attempt + 1);
        delay = STATUS_RETRY_DELAYS_MS[attempt]!;
        due.push(identity);
      }
      if (due.length === 0) return;
      retryTimers.add(
        window.setTimeout(() => {
          pendingIdentities.push(...due);
          scheduleStatusFlush();
        }, delay),
      );
    }

    /** Re-reads every visible ring (tab refocus, viewer closed) so seen/unseen
     * state and newly posted Stories show up without a page reload. */
    function refreshStatuses(): void {
      lastStatusRefresh = Date.now();
      for (const { identity } of tracked.values()) pendingIdentities.push(identity);
      if (pendingIdentities.length > 0) scheduleStatusFlush();
    }

    function decorateAvatars(root: ParentNode): void {
      const imgs = root.querySelectorAll<HTMLImageElement>(ALL_AVATAR_SURFACES_SELECTOR);
      for (const img of imgs) {
        if (processedImgs.has(img)) continue;
        processedImgs.add(img);

        const identity = extractIdentity(img);
        // Logins the background would reject (and so drop the whole batch)
        // are never sent.
        if (!identity || !isLogin(identity.login)) continue;

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
        sweepDetached();
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
        sweepDetached();
        overlay.attach();
        processSubtree(document.body);
      }, 0);
    }

    function handleVisibility(): void {
      if (document.visibilityState === "visible" && Date.now() - lastStatusRefresh > STATUS_REFRESH_AFTER_MS) {
        refreshStatuses();
      }
    }
    document.addEventListener("visibilitychange", handleVisibility);

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
      document.removeEventListener("visibilitychange", handleVisibility);
      for (const timer of retryTimers) window.clearTimeout(timer);
      for (const { decoration } of tracked.values()) decoration.destroy();
      tracked.clear();
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
