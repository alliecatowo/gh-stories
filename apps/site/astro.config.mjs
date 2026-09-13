import { defineConfig } from 'astro/config';
import react from '@astrojs/react';

// Served from https://alliecatowo.github.io/gh-stories/ — every internal
// link and asset reference must go through `import.meta.env.BASE_URL`
// (see src/lib/base.ts) rather than a bare root-relative path.
export default defineConfig({
  site: 'https://alliecatowo.github.io',
  base: '/gh-stories',
  trailingSlash: 'always',
  integrations: [react()],
  build: {
    format: 'directory',
  },
});
