/**
 * Catch-all adapter for hovercard-bearing avatar links anywhere else on the
 * page (mention autocomplete results, reaction tooltips, reviewer lists,
 * etc.) that aren't covered by a more specific surface adapter.
 */
export const HOVERCARD_AVATAR_SELECTOR = 'a[data-hovercard-type="user"] img.avatar, img.avatar[data-hovercard-type="user"]';

export function findHovercardAvatars(root: ParentNode): HTMLImageElement[] {
  return Array.from(root.querySelectorAll<HTMLImageElement>(HOVERCARD_AVATAR_SELECTOR));
}
