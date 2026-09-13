/**
 * Dashboard feed adapter: locates where the Stories row belongs. Tries a
 * handful of resilient selectors (test ids first, semantic containers as a
 * fallback) rather than one brittle class name — GitHub's dashboard markup
 * changes often, and this adapter should degrade to "no row" rather than
 * mounting somewhere destructive.
 */
const DASHBOARD_FEED_SELECTORS = [
  '[data-testid="dashboard-feed"]',
  '[data-testid="feed-container"]',
  ".js-dashboard-feed",
  "#dashboard .feed-container",
];

export function findDashboardFeed(root: ParentNode): Element | null {
  for (const selector of DASHBOARD_FEED_SELECTORS) {
    const match = root.querySelector(selector);
    if (match) return match;
  }
  return null;
}
