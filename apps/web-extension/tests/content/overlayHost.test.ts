import { describe, expect, it, vi } from 'vitest';

vi.mock('../../src/messaging/client.js', () => ({ callBackground: vi.fn() }));
vi.mock('../../src/viewer/ViewerBridge.js', () => ({ ViewerBridge: () => null }));
vi.mock('../../src/composer/ComposerBridge.js', () => ({ ComposerBridge: () => null }));

import { OverlayHost } from '../../src/content/overlayHost.js';

describe('OverlayHost survives Turbo body replacement', () => {
  it('lives on <html> and re-attaches if it is ever detached', () => {
    document.body.innerHTML = '<main>page one</main>';
    const overlay = new OverlayHost();
    const host = () => document.querySelector('ghs-overlay-host') as HTMLElement | null;
    expect(host()).not.toBeNull();
    expect(host()!.parentElement).toBe(document.documentElement);

    // Turbo Drive swaps the whole <body>.
    const next = document.createElement('body');
    next.innerHTML = '<main>page two</main>';
    document.body.replaceWith(next);
    expect(host()).not.toBeNull();

    // Even if something removes the host, showing the overlay puts it back.
    host()!.remove();
    expect(host()).toBeNull();
    overlay.openComposer();
    expect(host()).not.toBeNull();
    expect(host()!.style.display).toBe('block');
    overlay.destroy();
  });
});
