import { describe, expect, it } from 'vitest';
import { observeGithubTheme, trackedThemeHostCount } from '../../src/mount/theme.js';

describe('observeGithubTheme', () => {
  it('tracks hosts through one shared observer and releases them on cleanup', async () => {
    const hosts = Array.from({ length: 50 }, () => document.createElement('div'));
    const stops = hosts.map((host) => observeGithubTheme(host));
    expect(trackedThemeHostCount()).toBe(50);

    document.documentElement.setAttribute('data-color-mode', 'dark');
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(hosts.every((host) => host.getAttribute('data-color-mode') === 'dark')).toBe(true);

    for (const stop of stops) stop();
    expect(trackedThemeHostCount()).toBe(0);

    // Released hosts no longer follow the page.
    document.documentElement.setAttribute('data-color-mode', 'light');
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(hosts[0]!.getAttribute('data-color-mode')).toBe('dark');
    document.documentElement.removeAttribute('data-color-mode');
  });
});
