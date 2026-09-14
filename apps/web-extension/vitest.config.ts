import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    // The adapters do real DOM work (instanceof HTMLAnchorElement, closest,
    // currentSrc), so they must run against a real DOM implementation rather
    // than a hand-rolled stub that would let a broken adapter pass.
    environment: 'jsdom',
    include: ['tests/**/*.test.ts'],
    globalSetup: ['./tests/global-setup.ts'],
  },
});
