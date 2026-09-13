/**
 * Hand-written type guards for every `RuntimeRequest` variant. No `as`
 * casts: each guard narrows structurally so a malformed or hostile payload
 * (wrong shape, extra prototype pollution attempts, wrong field types) is
 * rejected rather than silently coerced.
 */
import type {
  AccountDeleteRequest,
  AudienceListCreateRequest,
  AudienceListDeleteRequest,
  AudienceListRemoveMemberRequest,
  FeedGetRequest,
  GetSessionRequest,
  GraphActionRequest,
  InboxGetRequest,
  InboxReadRequest,
  LoginPollRequest,
  LoginStartRequest,
  LogoutRequest,
  MediaFetchRequest,
  MineGetRequest,
  RingCancelRequest,
  RingStatusRequest,
  RuntimeRequest,
  SessionRevokeRequest,
  SetServiceUrlRequest,
  SettingsGetRequest,
  SettingsUpdateRequest,
  SignOutAllRequest,
  StoryDeleteRequest,
  StoryGetRequest,
  StoryReactRequest,
  StoryReplyRequest,
  StoryReportRequest,
  StoryUpdateRequest,
  StoryViewAckRequest,
  StoryViewersRequest,
  UserStoriesGetRequest,
} from "./types.js";

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((entry) => typeof entry === "string");
}

function isNumberArray(value: unknown): value is number[] {
  return Array.isArray(value) && value.every((entry) => typeof entry === "number" && Number.isFinite(entry));
}

const VISIBILITY_VALUES = new Set(["followers_of_author", "author_follows", "mutuals", "custom_list", "public"]);
const REPORT_REASONS = new Set(["spam", "harassment", "nudity", "violence", "self_harm", "illegal", "other"]);
const GRAPH_ACTIONS = new Set(["follow", "unfollow", "mute", "unmute", "block", "unblock", "hide", "unhide"]);

export function isGetSessionRequest(v: unknown): v is GetSessionRequest {
  return isRecord(v) && v.type === "ghs:session/get";
}
export function isLoginStartRequest(v: unknown): v is LoginStartRequest {
  return isRecord(v) && v.type === "ghs:session/login-start";
}
export function isLoginPollRequest(v: unknown): v is LoginPollRequest {
  return isRecord(v) && v.type === "ghs:session/login-poll";
}
export function isLogoutRequest(v: unknown): v is LogoutRequest {
  if (!isRecord(v) || v.type !== "ghs:session/logout") return false;
  return v.accountId === undefined || typeof v.accountId === "string";
}
export function isSetServiceUrlRequest(v: unknown): v is SetServiceUrlRequest {
  return isRecord(v) && v.type === "ghs:session/set-service-url" && typeof v.url === "string" && v.url.length > 0;
}

export function isRingStatusRequest(v: unknown): v is RingStatusRequest {
  if (!isRecord(v) || v.type !== "ghs:ring/status") return false;
  return (
    isNumberArray(v.githubUserIds) &&
    v.githubUserIds.length <= 100 &&
    isStringArray(v.logins) &&
    v.logins.length <= 100
  );
}

export function isRingCancelRequest(v: unknown): v is RingCancelRequest {
  return isRecord(v) && v.type === "ghs:ring/cancel";
}

export function isFeedGetRequest(v: unknown): v is FeedGetRequest {
  if (!isRecord(v) || v.type !== "ghs:feed/get") return false;
  return v.cursor === undefined || typeof v.cursor === "string";
}
export function isMineGetRequest(v: unknown): v is MineGetRequest {
  return isRecord(v) && v.type === "ghs:stories/mine";
}
export function isUserStoriesGetRequest(v: unknown): v is UserStoriesGetRequest {
  return isRecord(v) && v.type === "ghs:stories/user" && typeof v.login === "string" && v.login.length > 0;
}

export function isStoryGetRequest(v: unknown): v is StoryGetRequest {
  return isRecord(v) && v.type === "ghs:story/get" && typeof v.storyId === "string" && v.storyId.length > 0;
}
export function isStoryUpdateRequest(v: unknown): v is StoryUpdateRequest {
  if (!isRecord(v) || v.type !== "ghs:story/update") return false;
  if (typeof v.storyId !== "string" || v.storyId.length === 0) return false;
  if (!isRecord(v.update)) return false;
  const u = v.update;
  if (u.caption !== undefined && typeof u.caption !== "string") return false;
  if (u.altText !== undefined && typeof u.altText !== "string") return false;
  if (u.visibility !== undefined && (typeof u.visibility !== "string" || !VISIBILITY_VALUES.has(u.visibility))) {
    return false;
  }
  if (u.audienceListId !== undefined && typeof u.audienceListId !== "string") return false;
  if (u.allowReplies !== undefined && typeof u.allowReplies !== "boolean") return false;
  if (u.allowReactions !== undefined && typeof u.allowReactions !== "boolean") return false;
  return true;
}
export function isStoryDeleteRequest(v: unknown): v is StoryDeleteRequest {
  return isRecord(v) && v.type === "ghs:story/delete" && typeof v.storyId === "string" && v.storyId.length > 0;
}
export function isStoryViewersRequest(v: unknown): v is StoryViewersRequest {
  return isRecord(v) && v.type === "ghs:story/viewers" && typeof v.storyId === "string" && v.storyId.length > 0;
}
export function isStoryViewAckRequest(v: unknown): v is StoryViewAckRequest {
  return isRecord(v) && v.type === "ghs:story/view" && typeof v.storyId === "string" && v.storyId.length > 0;
}
export function isStoryReactRequest(v: unknown): v is StoryReactRequest {
  if (!isRecord(v) || v.type !== "ghs:story/react") return false;
  if (typeof v.storyId !== "string" || v.storyId.length === 0) return false;
  return v.emoji === null || typeof v.emoji === "string";
}
export function isStoryReplyRequest(v: unknown): v is StoryReplyRequest {
  return (
    isRecord(v) &&
    v.type === "ghs:story/reply" &&
    typeof v.storyId === "string" &&
    v.storyId.length > 0 &&
    typeof v.body === "string" &&
    v.body.trim().length > 0 &&
    v.body.length <= 500
  );
}
export function isStoryReportRequest(v: unknown): v is StoryReportRequest {
  if (!isRecord(v) || v.type !== "ghs:story/report") return false;
  if (typeof v.storyId !== "string" || v.storyId.length === 0) return false;
  if (typeof v.reason !== "string" || !REPORT_REASONS.has(v.reason)) return false;
  return v.details === undefined || typeof v.details === "string";
}

export function isInboxGetRequest(v: unknown): v is InboxGetRequest {
  if (!isRecord(v) || v.type !== "ghs:inbox/get") return false;
  return v.cursor === undefined || typeof v.cursor === "string";
}
export function isInboxReadRequest(v: unknown): v is InboxReadRequest {
  if (!isRecord(v) || v.type !== "ghs:inbox/read") return false;
  if (v.eventIds !== undefined && !isStringArray(v.eventIds)) return false;
  if (v.all !== undefined && typeof v.all !== "boolean") return false;
  return true;
}

export function isSettingsGetRequest(v: unknown): v is SettingsGetRequest {
  return isRecord(v) && v.type === "ghs:settings/get";
}
export function isSettingsUpdateRequest(v: unknown): v is SettingsUpdateRequest {
  if (!isRecord(v) || v.type !== "ghs:settings/update") return false;
  if (!isRecord(v.update)) return false;
  const u = v.update;
  if (u.default_visibility !== undefined && (typeof u.default_visibility !== "string" || !VISIBILITY_VALUES.has(u.default_visibility))) {
    return false;
  }
  if (u.default_audience_list_id !== undefined && typeof u.default_audience_list_id !== "string") return false;
  if (u.default_allow_replies !== undefined && typeof u.default_allow_replies !== "boolean") return false;
  if (u.default_allow_reactions !== undefined && typeof u.default_allow_reactions !== "boolean") return false;
  return true;
}
export function isAudienceListCreateRequest(v: unknown): v is AudienceListCreateRequest {
  return isRecord(v) && v.type === "ghs:audience/create" && typeof v.name === "string" && v.name.trim().length > 0;
}
export function isAudienceListDeleteRequest(v: unknown): v is AudienceListDeleteRequest {
  return isRecord(v) && v.type === "ghs:audience/delete" && typeof v.listId === "string" && v.listId.length > 0;
}
export function isAudienceListRemoveMemberRequest(v: unknown): v is AudienceListRemoveMemberRequest {
  return (
    isRecord(v) &&
    v.type === "ghs:audience/remove-member" &&
    typeof v.listId === "string" &&
    v.listId.length > 0 &&
    typeof v.githubUserId === "number" &&
    Number.isFinite(v.githubUserId)
  );
}

export function isGraphActionRequest(v: unknown): v is GraphActionRequest {
  if (!isRecord(v) || v.type !== "ghs:graph/action") return false;
  if (typeof v.action !== "string" || !GRAPH_ACTIONS.has(v.action)) return false;
  return typeof v.login === "string" && v.login.length > 0;
}

export function isSessionRevokeRequest(v: unknown): v is SessionRevokeRequest {
  return (
    isRecord(v) && v.type === "ghs:settings/revoke-session" && typeof v.sessionId === "string" && v.sessionId.length > 0
  );
}
export function isSignOutAllRequest(v: unknown): v is SignOutAllRequest {
  return isRecord(v) && v.type === "ghs:settings/sign-out-all";
}
export function isAccountDeleteRequest(v: unknown): v is AccountDeleteRequest {
  return isRecord(v) && v.type === "ghs:settings/delete-account";
}

export function isMediaFetchRequest(v: unknown): v is MediaFetchRequest {
  return (
    isRecord(v) &&
    v.type === "ghs:media/fetch" &&
    typeof v.storyId === "string" &&
    v.storyId.length > 0 &&
    typeof v.variant === "string" &&
    v.variant.length > 0
  );
}

const ALL_GUARDS: Array<(v: unknown) => v is RuntimeRequest> = [
  isGetSessionRequest,
  isLoginStartRequest,
  isLoginPollRequest,
  isLogoutRequest,
  isSetServiceUrlRequest,
  isRingStatusRequest,
  isRingCancelRequest,
  isFeedGetRequest,
  isMineGetRequest,
  isUserStoriesGetRequest,
  isStoryGetRequest,
  isStoryUpdateRequest,
  isStoryDeleteRequest,
  isStoryViewersRequest,
  isStoryViewAckRequest,
  isStoryReactRequest,
  isStoryReplyRequest,
  isStoryReportRequest,
  isInboxGetRequest,
  isInboxReadRequest,
  isSettingsGetRequest,
  isSettingsUpdateRequest,
  isAudienceListCreateRequest,
  isAudienceListDeleteRequest,
  isAudienceListRemoveMemberRequest,
  isGraphActionRequest,
  isSessionRevokeRequest,
  isSignOutAllRequest,
  isAccountDeleteRequest,
  isMediaFetchRequest,
];

/** Validates that a value is a well-formed `RuntimeRequest` of some known
 * variant. This does not validate the sender — see `sender.ts`. */
export function isRuntimeRequest(value: unknown): value is RuntimeRequest {
  if (!isRecord(value) || typeof value.type !== "string") return false;
  return ALL_GUARDS.some((guard) => guard(value));
}

// The upload message guards live alongside their types; re-exported here so
// every runtime message guard has one import site.
export { isUploadStartMessage, isUploadCancelMessage } from "./types.js";
