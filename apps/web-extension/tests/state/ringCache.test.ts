import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RingStatusRegistry, clearAllRingCache } from '../../src/state/ringCache.js';
import type { ApiClient } from '../../src/api/client.js';

function registry(ringStatus: ApiClient['ringStatus'], accountId: string | null = 'acct') {
  return new RingStatusRegistry({ ringStatus } as unknown as ApiClient, async () => accountId);
}

describe('RingStatusRegistry', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    clearAllRingCache();
  });
  afterEach(() => vi.useRealTimers());

  it('resolves entries and caches a confirmed "no Story" for people the service answered for', async () => {
    const ringStatus = vi.fn(async () => ({ entries: [{ github_user_id: 1, login: 'a', has_active: true }] }));
    const reg = registry(ringStatus as never);
    const first = reg.request(1, [1, 2], []);
    await vi.advanceTimersByTimeAsync(200);
    const entries = await first;
    expect(entries.map((e) => [e.github_user_id, e.has_active])).toEqual([
      [1, true],
      [2, false],
    ]);
  });

  it('rejects, instead of answering "no Story", when the lookup fails', async () => {
    const ringStatus = vi.fn(async () => {
      throw new Error('offline');
    });
    const reg = registry(ringStatus as never);
    const pending = reg.request(1, [7], ['someone']);
    const settled = pending.then(
      () => 'resolved',
      () => 'rejected',
    );
    await vi.advanceTimersByTimeAsync(200);
    expect(await settled).toBe('rejected');
  });

  it('rejects with a cancelled error when the tab navigates away mid-queue', async () => {
    const reg = registry(vi.fn() as never);
    const pending = reg.request(1, [7], []);
    const settled = pending.catch((error: { code?: string }) => error.code);
    reg.cancelTab(1);
    expect(await settled).toBe('cancelled');
  });

  it('answers empty (nothing to show) when signed out', async () => {
    const reg = registry(vi.fn() as never, null);
    const pending = reg.request(1, [7], []);
    await vi.advanceTimersByTimeAsync(200);
    expect(await pending).toEqual([]);
  });
});
