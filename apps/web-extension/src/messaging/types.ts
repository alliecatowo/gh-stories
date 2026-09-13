/**
 * Typed runtime messages exchanged between the background service worker
 * (the only holder of the session token) and the content script / popup /
 * options pages. Every request has a literal `type` discriminant. Add a new
 * hand-written type guard in `guards.ts` for every new variant — no message
 * is dispatched without one.
 */
import type {
  AuthorGroup,
  Emoji,
  Feed,
  Inbox,
  Me,
  RingStatus,
  Settings,
  SettingsUpdate,
  StoryItem,
  Visibility,
  ViewerList,
} from "@gh-stories/contracts";

/** Minimal account summary the UI needs; never includes the session token. */
export interface AccountSummary {
  accountId: string;
  login: string;
  avatarUrl: string;
  isModerator: boolean;
  unreadInbox: number;
}

export interface SessionState {
  signedIn: boolean;
  account: AccountSummary | null;
  serviceUrl: string;
}

/** A structured, serializable error surfaced by the background API client. */
export interface WireError {
  code: string;
  message: string;
  field?: string;
  retryAfterSeconds?: number;
}

export type Ok<T> = { ok: true; data: T };
export type Err = { ok: false; error: WireError };
export type Result<T> = Ok<T> | Err;

// ---------------------------------------------------------------- requests

export interface GetSessionRequest {
  type: "ghs:session/get";
}
export interface LoginStartRequest {
  type: "ghs:session/login-start";
}
/** Polled by the popup (on a short interval, only while a sign-in UI is
 * open) to drive the pending-login approval loop responsively. A durable
 * `alarms`-based backstop in the background also advances it at a coarser
 * interval so login still completes if the popup is closed. */
export interface LoginPollRequest {
  type: "ghs:session/login-poll";
}
export interface LoginPollResponseData {
  status: "idle" | "pending" | "approved" | "denied" | "expired";
  verificationUrl?: string;
  userCode?: string;
}
export interface LogoutRequest {
  type: "ghs:session/logout";
  accountId?: string;
}
export interface SetServiceUrlRequest {
  type: "ghs:session/set-service-url";
  url: string;
}

export interface RingStatusRequest {
  type: "ghs:ring/status";
  githubUserIds: number[];
  logins: string[];
}
export interface RingStatusResponseData {
  entries: RingStatus[];
}
/** Sent by the content script right before it starts decorating a freshly
 * navigated page, so any in-flight batch for the previous page is aborted
 * instead of racing the new scan. */
export interface RingCancelRequest {
  type: "ghs:ring/cancel";
}

export interface FeedGetRequest {
  type: "ghs:feed/get";
  cursor?: string;
}
export interface MineGetRequest {
  type: "ghs:stories/mine";
}
export interface UserStoriesGetRequest {
  type: "ghs:stories/user";
  login: string;
}

export interface StoryGetRequest {
  type: "ghs:story/get";
  storyId: string;
}
export interface StoryUpdateRequest {
  type: "ghs:story/update";
  storyId: string;
  update: {
    caption?: string;
    altText?: string;
    visibility?: Visibility;
    audienceListId?: string;
    allowReplies?: boolean;
    allowReactions?: boolean;
  };
}
export interface StoryDeleteRequest {
  type: "ghs:story/delete";
  storyId: string;
}
export interface StoryViewersRequest {
  type: "ghs:story/viewers";
  storyId: string;
}
export interface StoryViewAckRequest {
  type: "ghs:story/view";
  storyId: string;
}
export interface StoryReactRequest {
  type: "ghs:story/react";
  storyId: string;
  emoji: Emoji | null;
}
export interface StoryReplyRequest {
  type: "ghs:story/reply";
  storyId: string;
  body: string;
}
export interface StoryReportRequest {
  type: "ghs:story/report";
  storyId: string;
  reason: "spam" | "harassment" | "nudity" | "violence" | "self_harm" | "illegal" | "other";
  details?: string;
}

export interface InboxGetRequest {
  type: "ghs:inbox/get";
  cursor?: string;
}
export interface InboxReadRequest {
  type: "ghs:inbox/read";
  eventIds?: string[];
  all?: boolean;
}

export interface SettingsGetRequest {
  type: "ghs:settings/get";
}
export interface SettingsUpdateRequest {
  type: "ghs:settings/update";
  update: SettingsUpdate;
}
export interface AudienceListCreateRequest {
  type: "ghs:audience/create";
  name: string;
}
export interface AudienceListDeleteRequest {
  type: "ghs:audience/delete";
  listId: string;
}
export interface AudienceListRemoveMemberRequest {
  type: "ghs:audience/remove-member";
  listId: string;
  githubUserId: number;
}

export interface GraphActionRequest {
  type: "ghs:graph/action";
  action: "follow" | "unfollow" | "mute" | "unmute" | "block" | "unblock" | "hide" | "unhide";
  login: string;
}

export interface SessionRevokeRequest {
  type: "ghs:settings/revoke-session";
  sessionId: string;
}
export interface SignOutAllRequest {
  type: "ghs:settings/sign-out-all";
}
export interface AccountDeleteRequest {
  type: "ghs:settings/delete-account";
}

/** Fetches one authorized media byte range through the background so the
 * session token never reaches a page or extension-page document. Returns
 * transferable bytes; the caller builds its own local blob: URL. */
export interface MediaFetchRequest {
  type: "ghs:media/fetch";
  storyId: string;
  variant: string;
}
export interface MediaFetchResponseData {
  mime: string;
  bytes: ArrayBuffer;
}

export type RuntimeRequest =
  | GetSessionRequest
  | LoginStartRequest
  | LoginPollRequest
  | LogoutRequest
  | SetServiceUrlRequest
  | RingStatusRequest
  | RingCancelRequest
  | FeedGetRequest
  | MineGetRequest
  | UserStoriesGetRequest
  | StoryGetRequest
  | StoryUpdateRequest
  | StoryDeleteRequest
  | StoryViewersRequest
  | StoryViewAckRequest
  | StoryReactRequest
  | StoryReplyRequest
  | StoryReportRequest
  | InboxGetRequest
  | InboxReadRequest
  | SettingsGetRequest
  | SettingsUpdateRequest
  | AudienceListCreateRequest
  | AudienceListDeleteRequest
  | AudienceListRemoveMemberRequest
  | GraphActionRequest
  | SessionRevokeRequest
  | SignOutAllRequest
  | AccountDeleteRequest
  | MediaFetchRequest;

/** Maps each request's `type` to its success payload. Used only for
 * call-site ergonomics in `messaging/client.ts`; not enforced at runtime. */
export interface RuntimeResponseDataMap {
  "ghs:session/get": SessionState;
  "ghs:session/login-start": LoginPollResponseData;
  "ghs:session/login-poll": LoginPollResponseData;
  "ghs:session/logout": { signedOut: boolean };
  "ghs:session/set-service-url": { url: string };
  "ghs:ring/status": RingStatusResponseData;
  "ghs:ring/cancel": { cancelled: true };
  "ghs:feed/get": Feed;
  "ghs:stories/mine": AuthorGroup;
  "ghs:stories/user": AuthorGroup;
  "ghs:story/get": StoryItem;
  "ghs:story/update": StoryItem;
  "ghs:story/delete": { deleted: true };
  "ghs:story/viewers": ViewerList;
  "ghs:story/view": { acknowledged: true };
  "ghs:story/react": { storyId: string; emoji: Emoji | null };
  "ghs:story/reply": { sent: true };
  "ghs:story/report": { filed: true };
  "ghs:inbox/get": Inbox;
  "ghs:inbox/read": { marked: true };
  "ghs:settings/get": Settings;
  "ghs:settings/update": Settings;
  "ghs:audience/create": Settings;
  "ghs:audience/delete": Settings;
  "ghs:audience/remove-member": Settings;
  "ghs:graph/action": { login: string; action: string };
  "ghs:settings/revoke-session": { revoked: true };
  "ghs:settings/sign-out-all": { signedOut: true };
  "ghs:settings/delete-account": { deleted: true };
  "ghs:media/fetch": MediaFetchResponseData;
}

export type RuntimeResponse<T extends RuntimeRequest["type"] = RuntimeRequest["type"]> = Result<
  RuntimeResponseDataMap[T]
>;

/** The one-shot `Me` snapshot the background caches for the popup badge. */
export type MeSnapshot = Me;

// ------------------------------------------------------------- upload port

/** Upload progress happens over a long-lived `runtime.connect` port named
 * `PORT_UPLOAD`, because a single request/response cannot stream progress or
 * be cancelled mid-flight. */
export const PORT_UPLOAD = "ghs:upload";

export interface UploadStartMessage {
  type: "start";
  idempotencyKey: string;
  mime: string;
  byteSize: number;
  filename: string;
  caption: string;
  altText: string;
  visibility: Visibility;
  audienceListId?: string;
  allowReplies: boolean;
  allowReactions: boolean;
  /** Raw file bytes. Transferred once; never persisted by the background
   * beyond the lifetime of this upload. */
  bytes: ArrayBuffer;
}
export interface UploadCancelMessage {
  type: "cancel";
}
export type UploadClientMessage = UploadStartMessage | UploadCancelMessage;

export type UploadServerMessage =
  | { type: "progress"; phase: "uploading"; loaded: number; total: number }
  | { type: "phase"; phase: "processing" }
  | { type: "done"; story: StoryItem }
  | { type: "error"; error: WireError }
  | { type: "cancelled" };

export function isUploadStartMessage(value: unknown): value is UploadStartMessage {
  if (typeof value !== "object" || value === null) return false;
  const v = value as Record<string, unknown>;
  return (
    v.type === "start" &&
    typeof v.idempotencyKey === "string" &&
    typeof v.mime === "string" &&
    typeof v.byteSize === "number" &&
    typeof v.filename === "string" &&
    typeof v.caption === "string" &&
    typeof v.altText === "string" &&
    typeof v.visibility === "string" &&
    typeof v.allowReplies === "boolean" &&
    typeof v.allowReactions === "boolean" &&
    v.bytes instanceof ArrayBuffer
  );
}
export function isUploadCancelMessage(value: unknown): value is UploadCancelMessage {
  return typeof value === "object" && value !== null && (value as Record<string, unknown>).type === "cancel";
}
