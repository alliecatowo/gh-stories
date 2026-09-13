/**
 * Every internal link and asset reference on this site must be produced
 * through `withBase`, never written as a bare `/path`. The site is served
 * from https://alliecatowo.github.io/gh-stories/, so a bare root-relative
 * path resolves against the domain root and 404s in production while
 * working fine in local dev — the exact bug the build-verification step
 * greps for.
 */
const rawBase = import.meta.env.BASE_URL || '/';
export const BASE = rawBase.endsWith('/') ? rawBase.slice(0, -1) : rawBase;

/** Prefixes a root-relative path with the configured base path. */
export function withBase(path: string): string {
  const normalized = path.startsWith('/') ? path : `/${path}`;
  return `${BASE}${normalized}`;
}

/** Absolute canonical URL for a root-relative path, for metadata that
 * requires a full URL (canonical link, Open Graph, Twitter card, JSON-LD). */
export function absoluteUrl(path: string): string {
  const site = 'https://alliecatowo.github.io';
  return `${site}${withBase(path)}`;
}
