/**
 * Issue / pull request / discussion timeline avatar adapter — the avatars
 * on `TimelineItem`-style entries (commits, reviews, status changes,
 * top-level comments rendered as timeline events).
 */
export const TIMELINE_AVATAR_SELECTOR = ".TimelineItem-avatar img.avatar, .timeline-comment-avatar img.avatar";

export function findTimelineAvatars(root: ParentNode): HTMLImageElement[] {
  return Array.from(root.querySelectorAll<HTMLImageElement>(TIMELINE_AVATAR_SELECTOR));
}
