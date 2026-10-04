import { createElement } from 'react';
import { createRoot } from 'react-dom/client';
import { act } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const callBackground = vi.fn();
vi.mock('../../src/messaging/client.js', () => ({ callBackground: (...args: unknown[]) => callBackground(...args) }));

import { ViewerBridge } from '../../src/viewer/ViewerBridge.js';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const A = '11111111-1111-4111-8111-111111111111';
const B = '22222222-2222-4222-8222-222222222222';

function item(id: string) {
  return {
    id,
    author: { login: 'octocat', github_user_id: 1 },
    state: 'published',
    media_kind: 'image',
    variants: [{ kind: 'image', mime: 'image/png' }],
    allow_replies: true,
    allow_reactions: true,
  };
}

const fetched = () =>
  callBackground.mock.calls
    .map((call) => call[0] as { type: string; storyId?: string })
    .filter((message) => message.type === 'ghs:media/fetch')
    .map((message) => message.storyId);

describe('ViewerBridge media loading', () => {
  beforeEach(() => {
    callBackground.mockReset();
    callBackground.mockImplementation(async (message: { type: string }) =>
      message.type === 'ghs:media/fetch'
        ? { ok: true, data: { mime: 'image/png', base64: 'AAAA' } }
        : { ok: true, data: {} },
    );
    URL.createObjectURL = vi.fn(() => 'blob:fake');
    URL.revokeObjectURL = vi.fn();
  });
  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('fetches the next item when the viewer advances inside a group', async () => {
    const group = { author: { login: 'octocat', github_user_id: 1 }, items: [item(A), item(B)] };
    const host = document.createElement('div');
    document.body.appendChild(host);
    const root = createRoot(host);

    await act(async () => {
      root.render(createElement(ViewerBridge, { groups: [group] as never, startGroupIndex: 0, onClose: () => undefined }));
    });
    expect(fetched()).toContain(A);
    expect(fetched()).not.toContain(B);

    await act(async () => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true, cancelable: true }));
    });
    expect(fetched()).toContain(B);

    await act(async () => root.unmount());
  });

  it('does not refetch a broken item in a loop', async () => {
    callBackground.mockImplementation(async (message: { type: string }) =>
      message.type === 'ghs:media/fetch' ? { ok: false, error: { code: 'x', message: 'x' } } : { ok: true, data: {} },
    );
    const group = { author: { login: 'octocat', github_user_id: 1 }, items: [item(A)] };
    const host = document.createElement('div');
    document.body.appendChild(host);
    const root = createRoot(host);
    await act(async () => {
      root.render(createElement(ViewerBridge, { groups: [group] as never, startGroupIndex: 0, onClose: () => undefined }));
    });
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });
    expect(fetched().length).toBe(1);
    await act(async () => root.unmount());
  });
});
