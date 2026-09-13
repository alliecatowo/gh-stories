/**
 * MV3 service worker — the ONLY holder of the Stories session token.
 *
 * This extension never mutates github.com: it issues no write requests to
 * repository contents, issues, PRs, Actions, follows or notifications. Every
 * network call this file makes targets the configured Stories service
 * origin only (read via `getServiceOrigin()`), never github.com.
 */
import { defineBackground } from "wxt/utils/define-background";
import { browser } from "wxt/browser";
import type { Browser } from "wxt/browser";
import { ApiClient } from "../src/api/client.js";
import { ApiClientError } from "../src/api/errors.js";
import { runUploadFlow } from "../src/api/upload.js";
import {
  clearAllAccounts,
  getActiveAccount,
  getActiveAccountId,
  getAccounts,
  getServiceOrigin,
  getToken,
  removeAccount,
  setServiceOrigin,
} from "../src/state/session.js";
import { pollLoginOnce, startLogin } from "../src/state/login.js";
import { RingStatusRegistry, clearAllRingCache, purgeAccountRingCache } from "../src/state/ringCache.js";
import { isTrustedPort, isTrustedSender } from "../src/messaging/sender.js";
import { isRuntimeRequest, isUploadStartMessage, isUploadCancelMessage } from "../src/messaging/guards.js";
import { PORT_UPLOAD } from "../src/messaging/types.js";
import type { RuntimeRequest, RuntimeResponse, SessionState, WireError } from "../src/messaging/types.js";

const ALARM_LOGIN_POLL = "ghs:poll-login";

function toWireError(error: unknown): WireError {
  if (error instanceof ApiClientError) return error.toWireError();
  return { code: "internal_error", message: "Something went wrong. Please try again." };
}

export default defineBackground(() => {
  const apiClient = new ApiClient({ getToken, getServiceOrigin });
  const ringRegistry = new RingStatusRegistry(apiClient, getActiveAccountId);

  async function currentSessionState(): Promise<SessionState> {
    const [account, origin] = await Promise.all([getActiveAccount(), getServiceOrigin()]);
    if (!account) return { signedIn: false, account: null, serviceUrl: origin };
    return {
      signedIn: true,
      account: {
        accountId: account.accountId,
        login: account.login,
        avatarUrl: account.avatarUrl,
        isModerator: account.isModerator,
        unreadInbox: 0,
      },
      serviceUrl: origin,
    };
  }

  async function logoutAccount(accountId: string | null): Promise<void> {
    const targetId = accountId ?? (await getActiveAccountId());
    if (!targetId) return;
    try {
      await apiClient.logout();
    } catch {
      // Revoke server-side on a best-effort basis; the local token is
      // dropped regardless so the extension always reflects "signed out"
      // even if the network call fails.
    }
    await removeAccount(targetId);
    purgeAccountRingCache(targetId);
  }

  // ---------------------------------------------------------- alarm backstop
  function ensureLoginAlarm(): void {
    browser.alarms.create(ALARM_LOGIN_POLL, { periodInMinutes: 1 });
  }
  browser.alarms.onAlarm.addListener((alarm: Browser.alarms.Alarm) => {
    if (alarm.name !== ALARM_LOGIN_POLL) return;
    void (async () => {
      const origin = await getServiceOrigin();
      const result = await pollLoginOnce(origin).catch((): null => null);
      if (!result || result.status !== "pending") {
        await browser.alarms.clear(ALARM_LOGIN_POLL);
      }
    })();
  });

  // -------------------------------------------------------------- messaging
  async function handleRequest(
    message: RuntimeRequest,
    sender: Browser.runtime.MessageSender,
  ): Promise<RuntimeResponse> {
    try {
      switch (message.type) {
        case "ghs:session/get": {
          return { ok: true, data: await currentSessionState() };
        }
        case "ghs:session/login-start": {
          const origin = await getServiceOrigin();
          const data = await startLogin(origin);
          ensureLoginAlarm();
          return { ok: true, data };
        }
        case "ghs:session/login-poll": {
          const origin = await getServiceOrigin();
          const data = await pollLoginOnce(origin);
          return { ok: true, data };
        }
        case "ghs:session/logout": {
          await logoutAccount(message.accountId ?? null);
          return { ok: true, data: { signedOut: true } };
        }
        case "ghs:session/set-service-url": {
          await setServiceOrigin(message.url);
          clearAllRingCache();
          return { ok: true, data: { url: await getServiceOrigin() } };
        }

        case "ghs:ring/status": {
          if (!sender.tab?.id) {
            return { ok: false, error: { code: "invalid_sender", message: "Ring status requires a page context." } };
          }
          const entries = await ringRegistry.request(sender.tab.id, message.githubUserIds, message.logins);
          return { ok: true, data: { entries } };
        }
        case "ghs:ring/cancel": {
          if (sender.tab?.id) ringRegistry.cancelTab(sender.tab.id);
          return { ok: true, data: { cancelled: true } };
        }

        case "ghs:feed/get":
          return { ok: true, data: await apiClient.feed(message.cursor) };
        case "ghs:stories/mine":
          return { ok: true, data: await apiClient.mine() };
        case "ghs:stories/user":
          return { ok: true, data: await apiClient.userStories(message.login) };

        case "ghs:story/get":
          return { ok: true, data: await apiClient.getStory(message.storyId) };
        case "ghs:story/update":
          return {
            ok: true,
            data: await apiClient.updateStory(message.storyId, {
              caption: message.update.caption,
              alt_text: message.update.altText,
              visibility: message.update.visibility,
              audience_list_id: message.update.audienceListId,
              allow_replies: message.update.allowReplies,
              allow_reactions: message.update.allowReactions,
            }),
          };
        case "ghs:story/delete":
          await apiClient.deleteStory(message.storyId);
          return { ok: true, data: { deleted: true } };
        case "ghs:story/viewers":
          return { ok: true, data: await apiClient.viewers(message.storyId) };
        case "ghs:story/view":
          await apiClient.acknowledgeView(message.storyId);
          return { ok: true, data: { acknowledged: true } };
        case "ghs:story/react": {
          const idempotencyKey = `${message.storyId}:${message.emoji ?? "clear"}`;
          if (message.emoji) {
            await apiClient.setReaction(message.storyId, message.emoji, idempotencyKey);
          } else {
            await apiClient.clearReaction(message.storyId);
          }
          return { ok: true, data: { storyId: message.storyId, emoji: message.emoji } };
        }
        case "ghs:story/reply": {
          const idempotencyKey = `reply:${message.storyId}:${message.body}`;
          await apiClient.reply(message.storyId, message.body, idempotencyKey);
          return { ok: true, data: { sent: true } };
        }
        case "ghs:story/report":
          await apiClient.report({ subjectKind: "story", storyId: message.storyId, reason: message.reason, details: message.details });
          return { ok: true, data: { filed: true } };

        case "ghs:inbox/get":
          return { ok: true, data: await apiClient.inbox(message.cursor) };
        case "ghs:inbox/read":
          await apiClient.markInboxRead({ eventIds: message.eventIds, all: message.all });
          return { ok: true, data: { marked: true } };

        case "ghs:settings/get":
          return { ok: true, data: await apiClient.getSettings() };
        case "ghs:settings/update":
          return { ok: true, data: await apiClient.updateSettings(message.update) };
        case "ghs:audience/create": {
          await apiClient.createAudienceList(message.name);
          return { ok: true, data: await apiClient.getSettings() };
        }
        case "ghs:audience/delete": {
          await apiClient.deleteAudienceList(message.listId);
          return { ok: true, data: await apiClient.getSettings() };
        }
        case "ghs:audience/remove-member": {
          const settings = await apiClient.getSettings();
          const list = settings.audience_lists?.find((candidate) => candidate.id === message.listId);
          const remaining = (list?.members ?? [])
            .filter((member) => member.github_user_id !== message.githubUserId)
            .map((member) => member.login);
          await apiClient.patchAudienceList(message.listId, { logins: remaining });
          return { ok: true, data: await apiClient.getSettings() };
        }

        case "ghs:graph/action": {
          const byAction: Record<typeof message.action, () => Promise<void>> = {
            follow: () => apiClient.follow(message.login),
            unfollow: () => apiClient.unfollow(message.login),
            mute: () => apiClient.mute(message.login),
            unmute: () => apiClient.unmute(message.login),
            block: () => apiClient.block(message.login),
            unblock: () => apiClient.unblock(message.login),
            hide: () => apiClient.hide(message.login),
            unhide: () => apiClient.unhide(message.login),
          };
          await byAction[message.action]();
          return { ok: true, data: { login: message.login, action: message.action } };
        }

        case "ghs:settings/revoke-session":
          await apiClient.revokeSession(message.sessionId);
          return { ok: true, data: { revoked: true } };
        case "ghs:settings/sign-out-all": {
          const accounts = await getAccounts();
          for (const account of Object.values(accounts)) {
            purgeAccountRingCache(account.accountId);
          }
          await apiClient.logout().catch(() => undefined);
          await clearAllAccounts();
          clearAllRingCache();
          return { ok: true, data: { signedOut: true } };
        }
        case "ghs:settings/delete-account": {
          const activeId = await getActiveAccountId();
          await apiClient.deleteAccount();
          if (activeId) {
            await removeAccount(activeId);
            purgeAccountRingCache(activeId);
          }
          return { ok: true, data: { deleted: true } };
        }

        case "ghs:media/fetch": {
          const result = await apiClient.media(message.storyId, message.variant);
          return { ok: true, data: result };
        }

        default: {
          const exhaustive: never = message;
          return { ok: false, error: { code: "unknown_message", message: `Unhandled message: ${String((exhaustive as { type?: string })?.type)}` } };
        }
      }
    } catch (error) {
      return { ok: false, error: toWireError(error) };
    }
  }

  browser.runtime.onMessage.addListener(
    (message: unknown, sender: Browser.runtime.MessageSender, sendResponse: (response: RuntimeResponse) => void) => {
      if (!isTrustedSender(sender, browser.runtime.id)) return undefined;
      if (!isRuntimeRequest(message)) return undefined;
      void handleRequest(message, sender).then(sendResponse);
      return true; // keep the message channel open for the async response
    },
  );

  browser.runtime.onConnect.addListener((port: Browser.runtime.Port) => {
    if (port.name !== PORT_UPLOAD) return;
    if (!isTrustedPort(port, browser.runtime.id)) {
      port.disconnect();
      return;
    }
    const onMessage = (raw: unknown) => {
      if (isUploadStartMessage(raw)) {
        void runUploadFlow(port, raw, apiClient);
      } else if (isUploadCancelMessage(raw)) {
        // Cancellation is handled by the listener `runUploadFlow` itself
        // installs on the same port; nothing further to do here.
      }
    };
    port.onMessage.addListener(onMessage);
    port.onDisconnect.addListener(() => port.onMessage.removeListener(onMessage));
  });

  browser.tabs.onRemoved.addListener((tabId: number) => ringRegistry.dropTab(tabId));
});
