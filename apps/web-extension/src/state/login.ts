/**
 * Pending-login flow (background only): the extension asks the service for
 * a client-mediated authorization, opens the service's own approval page in
 * a tab (never a GitHub URL — GitHub credentials never reach this
 * extension), then polls until the user approves or the code expires. The
 * token is received exactly once, from the poll response body — never via
 * `postMessage`, never via a third-party cookie on github.com.
 */
import { browser } from "wxt/browser";
import type { PendingLogin as ContractPendingLogin, PendingLoginPoll } from "@gh-stories/contracts";
import { ApiClientError } from "../api/errors.js";
import { getPendingLogin, setPendingLogin, upsertAccountSession, type PendingLogin } from "./session.js";
import type { LoginPollResponseData } from "../messaging/types.js";

function labelForThisClient(): string {
  const platform = typeof navigator !== "undefined" ? navigator.userAgent : "browser";
  const family = /firefox/i.test(platform) ? "Firefox" : /edg\//i.test(platform) ? "Edge" : "Chrome";
  return `GitHub Stories — ${family} extension`;
}

async function createPendingLogin(origin: string): Promise<PendingLogin> {
  const response = await fetch(`${origin}/v1/auth/cli/pending`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ client_kind: "browser_extension", client_label: labelForThisClient() }),
  });
  if (!response.ok) {
    throw new ApiClientError({ code: `http_${response.status}`, message: "Could not start sign-in." });
  }
  const body = (await response.json()) as ContractPendingLogin;
  return {
    pendingLoginId: body.pending_login_id,
    pollingSecret: body.polling_secret,
    verificationUrl: body.verification_url,
    userCode: body.user_code,
    expiresAt: body.expires_at,
    intervalSeconds: body.interval_seconds,
  };
}

export async function startLogin(origin: string): Promise<LoginPollResponseData> {
  const pending = await createPendingLogin(origin);
  await setPendingLogin(pending);
  await browser.tabs.create({ url: pending.verificationUrl, active: true });
  return pendingResult(pending);
}

const MIN_INTERVAL_S = 2;
const MAX_INTERVAL_S = 30;

function pendingResult(pending: PendingLogin): LoginPollResponseData {
  const seconds = Math.min(MAX_INTERVAL_S, Math.max(MIN_INTERVAL_S, pending.intervalSeconds || MIN_INTERVAL_S));
  return {
    status: "pending",
    verificationUrl: pending.verificationUrl,
    userCode: pending.userCode,
    intervalSeconds: seconds,
  };
}

let inFlightPoll: Promise<LoginPollResponseData> | null = null;

/** Polls once. Safe to call repeatedly and concurrently (the popup, the
 * background loop and the alarm backstop all call it): overlapping calls
 * share one in-flight request, so an approved login can never be consumed
 * twice or register the account twice. */
export function pollLoginOnce(origin: string): Promise<LoginPollResponseData> {
  if (!inFlightPoll) {
    inFlightPoll = pollLoginOnceUnlocked(origin).finally(() => {
      inFlightPoll = null;
    });
  }
  return inFlightPoll;
}

let loopRunning = false;

/** Background-owned poll loop: keeps polling at the interval the service asked
 * for until the login resolves, whether or not the popup is still open. Only
 * one loop runs at a time. If the service worker is evicted, the alarm
 * backstop and the next startup resume it. */
export async function runLoginLoop(
  origin: string,
  sleep: (ms: number) => Promise<void> = (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
): Promise<LoginPollResponseData> {
  if (loopRunning) return pollLoginOnce(origin);
  loopRunning = true;
  try {
    for (;;) {
      const result = await pollLoginOnce(origin).catch((): LoginPollResponseData => ({ status: "pending" }));
      if (result.status !== "pending") return result;
      await sleep((result.intervalSeconds ?? MIN_INTERVAL_S) * 1000);
    }
  } finally {
    loopRunning = false;
  }
}

async function pollLoginOnceUnlocked(origin: string): Promise<LoginPollResponseData> {
  const pending = await getPendingLogin();
  if (!pending) return { status: "idle" };

  if (new Date(pending.expiresAt).getTime() < Date.now()) {
    await setPendingLogin(null);
    return { status: "expired" };
  }

  const response = await fetch(`${origin}/v1/auth/cli/poll`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ pending_login_id: pending.pendingLoginId, polling_secret: pending.pollingSecret }),
  });
  if (!response.ok) {
    if (response.status === 404) {
      await setPendingLogin(null);
      return { status: "expired" };
    }
    // Transient failure (rate limited, network blip): keep the pending
    // login around so the next poll can retry.
    return pendingResult(pending);
  }

  const body = (await response.json()) as PendingLoginPoll;
  if (body.status === "pending") {
    return pendingResult(pending);
  }
  if (body.status === "denied" || body.status === "expired") {
    await setPendingLogin(null);
    return { status: body.status };
  }

  // approved
  await setPendingLogin(null);
  if (body.token && body.user) {
    await upsertAccountSession({
      accountId: String(body.user.github_user_id),
      login: body.user.login,
      avatarUrl: body.user.avatar_url,
      isModerator: false,
      token: body.token,
    });
  }
  return { status: "approved" };
}
