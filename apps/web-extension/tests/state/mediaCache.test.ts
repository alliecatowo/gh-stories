import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const callBackground = vi.fn();
vi.mock('../../src/messaging/client.js', () => ({ callBackground: (...args: unknown[]) => callBackground(...args) }));

import { MediaUrlCache } from '../../src/state/mediaCache.js';

describe('MediaUrlCache negative cache', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    callBackground.mockReset();
  });
  afterEach(() => vi.useRealTimers());

  it('does not refetch a failed item on every call, then retries after the cooldown', async () => {
    callBackground.mockResolvedValue({ ok: false, error: { code: 'network_error', message: 'x' } });
    const cache = new MediaUrlCache();
    expect(await cache.ensure('s', 'image')).toBeNull();
    expect(await cache.ensure('s', 'image')).toBeNull();
    expect(await cache.ensure('s', 'image')).toBeNull();
    expect(callBackground).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(16_000);
    await cache.ensure('s', 'image');
    expect(callBackground).toHaveBeenCalledTimes(2);
  });

  it('shares one in-flight request between concurrent callers', async () => {
    URL.createObjectURL = vi.fn(() => 'blob:x');
    callBackground.mockResolvedValue({ ok: true, data: { mime: 'image/png', base64: 'AAAA' } });
    const cache = new MediaUrlCache();
    const [a, b] = await Promise.all([cache.ensure('s', 'image'), cache.ensure('s', 'image')]);
    expect(a).toBe('blob:x');
    expect(b).toBe('blob:x');
    expect(callBackground).toHaveBeenCalledTimes(1);
    expect(cache.get('s', 'image')).toBe('blob:x');
  });
});
