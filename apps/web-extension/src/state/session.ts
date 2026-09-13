/**
 * Background-only session storage. The Stories session token lives ONLY
 * here, in `browser.storage.local` — never `storage.sync` (which syncs
 * across devices), never held only in memory (MV3 workers are killed
 * aggressively; every read goes back to storage). Content scripts and
 * extension pages never see this module; they reach it only through
 * `runtime.sendMessage`.
 */
import { browser } from "wxt/browser";

export const DEFAULT_SERVICE_ORIGIN = "https://stories.example";

export interface StoredAccount {
  accountId: string;
  login: string;
  avatarUrl: string;
  isModerator: boolean;
  /** The Stories session token for this account. Never leaves the
   * background; never included in any message to a content script or
   * extension page. */
  token: string;
}

export interface PendingLogin {
  pendingLoginId: string;
  pollingSecret: string;
  verificationUrl: string;
  userCode: string;
  expiresAt: string;
  intervalSeconds: number;
}

const KEY_SERVICE_ORIGIN = "ghs:serviceOrigin";
const KEY_ACTIVE_ACCOUNT_ID = "ghs:activeAccountId";
const KEY_ACCOUNTS = "ghs:accounts";
const KEY_PENDING_LOGIN = "ghs:pendingLogin";

type AccountMap = Record<string, StoredAccount>;

async function getLocal<T>(key: string, fallback: T): Promise<T> {
  const stored = await browser.storage.local.get(key);
  const value = stored[key];
  return value === undefined ? fallback : (value as T);
}

async function setLocal(key: string, value: unknown): Promise<void> {
  await browser.storage.local.set({ [key]: value });
}

export async function getServiceOrigin(): Promise<string> {
  return getLocal(KEY_SERVICE_ORIGIN, DEFAULT_SERVICE_ORIGIN);
}

/** Changing the service origin points the extension at a different backend
 * entirely — any cached tokens belong to the old one, so every account is
 * signed out and every cache is implicitly stale. */
export async function setServiceOrigin(origin: string): Promise<void> {
  const normalized = origin.replace(/\/+$/, "");
  await setLocal(KEY_SERVICE_ORIGIN, normalized);
  await clearAllAccounts();
}

export async function getAccounts(): Promise<AccountMap> {
  return getLocal<AccountMap>(KEY_ACCOUNTS, {});
}

export async function getActiveAccountId(): Promise<string | null> {
  return getLocal<string | null>(KEY_ACTIVE_ACCOUNT_ID, null);
}

export async function getActiveAccount(): Promise<StoredAccount | null> {
  const [accounts, activeId] = await Promise.all([getAccounts(), getActiveAccountId()]);
  if (!activeId) return null;
  return accounts[activeId] ?? null;
}

/** Read fresh from storage on every call — never cache this in a module
 * variable, since the service worker can be evicted and restarted between
 * messages, and switching accounts must take effect immediately. */
export async function getToken(): Promise<string | null> {
  const account = await getActiveAccount();
  return account?.token ?? null;
}

export async function upsertAccountSession(account: StoredAccount): Promise<void> {
  const accounts = await getAccounts();
  accounts[account.accountId] = account;
  await setLocal(KEY_ACCOUNTS, accounts);
  await setLocal(KEY_ACTIVE_ACCOUNT_ID, account.accountId);
}

export async function switchAccount(accountId: string): Promise<boolean> {
  const accounts = await getAccounts();
  if (!accounts[accountId]) return false;
  await setLocal(KEY_ACTIVE_ACCOUNT_ID, accountId);
  return true;
}

/** Removes one account's stored token and, if it was active, promotes
 * another remaining account (or signs fully out). Always purges any
 * account-scoped caches for the removed account via the caller. */
export async function removeAccount(accountId: string): Promise<void> {
  const accounts = await getAccounts();
  delete accounts[accountId];
  await setLocal(KEY_ACCOUNTS, accounts);
  const activeId = await getActiveAccountId();
  if (activeId === accountId) {
    const remaining = Object.keys(accounts);
    await setLocal(KEY_ACTIVE_ACCOUNT_ID, remaining[0] ?? null);
  }
}

export async function clearAllAccounts(): Promise<void> {
  await setLocal(KEY_ACCOUNTS, {});
  await setLocal(KEY_ACTIVE_ACCOUNT_ID, null);
}

export async function getPendingLogin(): Promise<PendingLogin | null> {
  return getLocal<PendingLogin | null>(KEY_PENDING_LOGIN, null);
}
export async function setPendingLogin(pending: PendingLogin | null): Promise<void> {
  await setLocal(KEY_PENDING_LOGIN, pending);
}
