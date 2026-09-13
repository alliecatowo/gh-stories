/**
 * Plain comment avatar adapter — comment authors outside the timeline
 * layout (e.g. review comment threads, gist/wiki-style comments).
 */
export const COMMENT_AVATAR_SELECTOR = '.comment-avatar img.avatar, [data-testid="comment-avatar"] img.avatar';

export function findCommentAvatars(root: ParentNode): HTMLImageElement[] {
  return Array.from(root.querySelectorAll<HTMLImageElement>(COMMENT_AVATAR_SELECTOR));
}
