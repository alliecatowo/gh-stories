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
  return { status: "pending", verificationUrl: pending.verificationUrl, userCode: pending.userCode };
}

/** Polls once. Safe to call repeatedly (from the popup on a short interval,
 * and from the durable alarm backstop) — it always reflects current
 * storage state and never double-registers an account. */
export async function pollLoginOnce(origin: string): Promise<LoginPollResponseData> {
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
    return { status: "pending", verificationUrl: pending.verificationUrl, userCode: pending.userCode };
  }

  const body = (await response.json()) as PendingLoginPoll;
  if (body.status === "pending") {
    return { status: "pending", verificationUrl: pending.verificationUrl, userCode: pending.userCode };
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
