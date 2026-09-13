export { extractIdentity, collectIdentities, type AccountIdentity } from "./identity.js";
export { findDashboardFeed } from "./dashboard.js";
export { findProfilePage, type ProfilePageInfo } from "./profile.js";
export { TIMELINE_AVATAR_SELECTOR, findTimelineAvatars } from "./timeline.js";
export { COMMENT_AVATAR_SELECTOR, findCommentAvatars } from "./comments.js";
export { HOVERCARD_AVATAR_SELECTOR, findHovercardAvatars } from "./hovercard.js";
export { wrapAvatarWithRing, type RingDecoration } from "./wrapAvatar.js";

import { TIMELINE_AVATAR_SELECTOR } from "./timeline.js";
import { COMMENT_AVATAR_SELECTOR } from "./comments.js";
import { HOVERCARD_AVATAR_SELECTOR } from "./hovercard.js";

/** Union of every "wrap this avatar with a ring" surface. Comment and
 * timeline containers frequently nest (a review comment is also inside a
 * `.TimelineItem`), so callers should dedupe matched `<img>` elements (e.g.
 * with a `WeakSet`) rather than assume these selectors are disjoint. */
export const ALL_AVATAR_SURFACES_SELECTOR = [
  TIMELINE_AVATAR_SELECTOR,
  COMMENT_AVATAR_SELECTOR,
  HOVERCARD_AVATAR_SELECTOR,
].join(", ");
