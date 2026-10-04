/**
 * Ring-status batching for the content script's avatar scans. Scoped per
 * tab so a Turbo/SPA navigation can cancel exactly its own in-flight work
 * without disturbing other tabs. Debounces bursts of small requests from a
 * `MutationObserver` into bounded (<=100 ids, <=100 logins) upstream calls,
 * and caches both positive and negative results per signed-in account so
 * switching accounts never leaks the previous account's cached rings.
 */
import type { RingStatus } from "@gh-stories/contracts";
import type { ApiClient } from "../api/client.js";
import { ApiClientError } from "../api/errors.js";

const DEBOUNCE_MS = 120;
const MAX_IDS_PER_BATCH = 100;
const MAX_LOGINS_PER_BATCH = 100;
const POSITIVE_TTL_MS = 30_000;
const NEGATIVE_TTL_MS = 5 * 60_000;

interface CacheEntry {
  status: RingStatus;
  expiresAt: number;
}

/** `accountId` scoping is baked into every cache key so a stale entry from
 * a previous signed-in account can never be served after switching. */
const cache = new Map<string, CacheEntry>();

function idKey(accountId: string, id: number): string {
  return `${accountId}::u:${id}`;
}
function loginKey(accountId: string, login: string): string {
  return `${accountId}::l:${login.toLowerCase()}`;
}

function readCache(key: string): RingStatus | undefined {
  const entry = cache.get(key);
  if (!entry) return undefined;
  if (entry.expiresAt < Date.now()) {
    cache.delete(key);
    return undefined;
  }
  return entry.status;
}

function writeCache(accountId: string, status: RingStatus): void {
  const ttl = status.has_active ? POSITIVE_TTL_MS : NEGATIVE_TTL_MS;
  const entry: CacheEntry = { status, expiresAt: Date.now() + ttl };
  cache.set(idKey(accountId, status.github_user_id), entry);
  if (status.login) cache.set(loginKey(accountId, status.login), entry);
}

/** Call on logout, account switch away, or revocation, so nothing from that
 * account's private graph can be read back afterward. */
export function purgeAccountRingCache(accountId: string): void {
  const prefix = `${accountId}::`;
  for (const key of cache.keys()) {
    if (key.startsWith(prefix)) cache.delete(key);
  }
}

export function clearAllRingCache(): void {
  cache.clear();
}

interface Waiter {
  ids: number[];
  logins: string[];
  resolve: (entries: RingStatus[]) => void;
  /** Rejected (rather than resolved with a short list) when the lookup was
   * cancelled or failed, so callers can tell "no Story" from "don't know". */
  reject: (error: unknown) => void;
}

class TabRingBatcher {
  private queueIds = new Set<number>();
  private queueLogins = new Set<string>();
  private waiters: Waiter[] = [];
  private timer: ReturnType<typeof setTimeout> | null = null;
  private controller: AbortController | null = null;

  constructor(
    private readonly apiClient: ApiClient,
    private readonly getActiveAccountId: () => Promise<string | null>,
  ) {}

  enqueue(ids: number[], logins: string[]): Promise<RingStatus[]> {
    return new Promise((resolve, reject) => {
      for (const id of ids) this.queueIds.add(id);
      for (const login of logins) this.queueLogins.add(login);
      this.waiters.push({ ids, logins, resolve, reject });
      if (this.queueIds.size >= MAX_IDS_PER_BATCH || this.queueLogins.size >= MAX_LOGINS_PER_BATCH) {
        this.flushSoon(0);
      } else {
        this.flushSoon(DEBOUNCE_MS);
      }
    });
  }

  /** Aborts any in-flight upstream call and drops queued-but-unsent work.
   * Waiters are rejected with a `cancelled` error (not resolved empty, which
   * would read as "nobody has a Story"), so a navigating page's pending scan
   * settles and knows to ask again. */
  cancel(): void {
    this.controller?.abort();
    this.controller = null;
    if (this.timer !== null) {
      clearTimeout(this.timer);
      this.timer = null;
    }
    this.queueIds.clear();
    this.queueLogins.clear();
    const waiters = this.waiters.splice(0);
    for (const waiter of waiters) {
      waiter.reject(new ApiClientError({ code: "cancelled", message: "Request cancelled." }));
    }
  }

  private flushSoon(delayMs: number): void {
    if (this.timer !== null) {
      clearTimeout(this.timer);
    }
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.flush();
    }, delayMs);
  }

  private async flush(): Promise<void> {
    if (this.waiters.length === 0) return;
    const accountId = await this.getActiveAccountId();
    const waiters = this.waiters.splice(0);
    const idsToFetch: number[] = [];
    const loginsToFetch: string[] = [];
    const results = new Map<string, RingStatus>();
    let failure: unknown;

    if (!accountId) {
      // Signed out: no authorized ring data exists to show.
      for (const waiter of waiters) waiter.resolve([]);
      this.queueIds.clear();
      this.queueLogins.clear();
      return;
    }

    for (const id of this.queueIds) {
      const cached = readCache(idKey(accountId, id));
      if (cached) results.set(idKey(accountId, id), cached);
      else idsToFetch.push(id);
    }
    for (const login of this.queueLogins) {
      const cached = readCache(loginKey(accountId, login));
      if (cached) results.set(loginKey(accountId, login), cached);
      else loginsToFetch.push(login);
    }
    this.queueIds.clear();
    this.queueLogins.clear();

    if (idsToFetch.length > 0 || loginsToFetch.length > 0) {
      const boundedIds = idsToFetch.slice(0, MAX_IDS_PER_BATCH);
      const boundedLogins = loginsToFetch.slice(0, MAX_LOGINS_PER_BATCH);
      this.controller = new AbortController();
      try {
        const response = await this.apiClient.ringStatus(boundedIds, boundedLogins, this.controller.signal);
        for (const entry of response.entries) {
          writeCache(accountId, entry);
          results.set(idKey(accountId, entry.github_user_id), entry);
          if (entry.login) results.set(loginKey(accountId, entry.login), entry);
        }
        // Anything requested but absent from the response is authoritatively
        // "no story" — cache that too so it isn't re-queried immediately.
        for (const id of boundedIds) {
          const key = idKey(accountId, id);
          if (!results.has(key)) {
            const negative: RingStatus = { github_user_id: id, has_active: false };
            writeCache(accountId, negative);
            results.set(key, negative);
          }
        }
      } catch (error) {
        // Network/timeout/cancellation: leave unresolved entries out of the
        // result map. Waiters that needed them are rejected below, so the
        // page retries instead of caching "no Story" for people it never
        // actually heard about.
        failure = error;
      } finally {
        this.controller = null;
      }

      // Anything beyond the bounded slice goes back on the queue for the
      // next debounce cycle instead of being dropped.
      for (const id of idsToFetch.slice(MAX_IDS_PER_BATCH)) this.queueIds.add(id);
      for (const login of loginsToFetch.slice(MAX_LOGINS_PER_BATCH)) this.queueLogins.add(login);
      if (this.queueIds.size > 0 || this.queueLogins.size > 0) this.flushSoon(0);
    }

    for (const waiter of waiters) {
      const entries: RingStatus[] = [];
      let missing = false;
      for (const id of waiter.ids) {
        const found = results.get(idKey(accountId, id));
        if (found) entries.push(found);
        else if (failure !== undefined) missing = true;
      }
      for (const login of waiter.logins) {
        const found = results.get(loginKey(accountId, login));
        if (found && !entries.includes(found)) entries.push(found);
        else if (!found && failure !== undefined) missing = true;
      }
      if (missing) waiter.reject(failure);
      else waiter.resolve(entries);
    }
  }
}

export class RingStatusRegistry {
  private readonly tabs = new Map<number, TabRingBatcher>();

  constructor(
    private readonly apiClient: ApiClient,
    private readonly getActiveAccountId: () => Promise<string | null>,
  ) {}

  request(tabId: number, ids: number[], logins: string[]): Promise<RingStatus[]> {
    return this.forTab(tabId).enqueue(ids, logins);
  }

  cancelTab(tabId: number): void {
    this.tabs.get(tabId)?.cancel();
  }

  dropTab(tabId: number): void {
    this.tabs.get(tabId)?.cancel();
    this.tabs.delete(tabId);
  }

  private forTab(tabId: number): TabRingBatcher {
    let batcher = this.tabs.get(tabId);
    if (!batcher) {
      batcher = new TabRingBatcher(this.apiClient, this.getActiveAccountId);
      this.tabs.set(tabId, batcher);
    }
    return batcher;
  }
}
