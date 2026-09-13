/**
 * Typed fetch client for the GitHub Stories API. This module is imported
 * ONLY by the background service worker — it is the sole place that ever
 * attaches `Authorization: Bearer <token>` to a request. Every other surface
 * (content script, popup, options) reaches the API by asking the background
 * over `runtime.sendMessage`.
 *
 * This extension never mutates github.com. It talks exclusively to the
 * Stories service's own origin(s); it issues no write requests to GitHub.
 */
import type {
  AudienceList,
  AuthorGroup,
  Emoji,
  Feed,
  Inbox,
  Me,
  Reaction,
  Reply,
  RingStatus,
  Settings,
  SettingsUpdate,
  SessionInfo,
  StoryItem,
  StoryUpdate,
  UploadIntent,
  Visibility,
  ViewerList,
} from "@gh-stories/contracts";
import { ApiClientError } from "./errors.js";

const DEFAULT_TIMEOUT_MS = 15_000;

export interface ApiClientDeps {
  /** Reads the current session token from storage on every call — the
   * background never relies on an in-memory copy, since MV3 workers can be
   * killed and restarted between messages. */
  getToken(): Promise<string | null>;
  /** Base origin of the Stories service, e.g. "https://stories.example" or
   * a self-hoster's own deployment. Never includes a trailing slash. */
  getServiceOrigin(): Promise<string>;
}

interface RequestOptions {
  method?: string;
  path: string;
  query?: Record<string, string | number | boolean | undefined>;
  body?: unknown;
  idempotencyKey?: string;
  timeoutMs?: number;
  signal?: AbortSignal;
  /** When true, omits the Authorization header (only the two unauthenticated
   * `/health/*` endpoints and `/auth/*` bootstrap calls need this). */
  anonymous?: boolean;
  /** Parse the response as raw bytes instead of JSON (media fetch). */
  binary?: boolean;
}

interface BinaryResult {
  mime: string;
  bytes: ArrayBuffer;
}

export class ApiClient {
  constructor(private readonly deps: ApiClientDeps) {}

  private async request<T>(options: RequestOptions): Promise<T> {
    const origin = await this.deps.getServiceOrigin();
    const url = new URL(`${origin}/v1${options.path}`);
    if (options.query) {
      for (const [key, value] of Object.entries(options.query)) {
        if (value !== undefined) url.searchParams.set(key, String(value));
      }
    }

    const headers: Record<string, string> = {};
    if (options.body !== undefined) headers["content-type"] = "application/json";
    if (options.idempotencyKey) headers["idempotency-key"] = options.idempotencyKey;
    if (!options.anonymous) {
      const token = await this.deps.getToken();
      if (!token) throw ApiClientError.unauthenticated();
      headers.authorization = `Bearer ${token}`;
    }

    const controller = new AbortController();
    const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
    const timeout = setTimeout(() => controller.abort(), timeoutMs);
    // Compose the caller's abort signal (e.g. tab navigation) with our timeout.
    const externalAbort = options.signal;
    const onExternalAbort = () => controller.abort();
    externalAbort?.addEventListener("abort", onExternalAbort);

    let response: Response;
    try {
      response = await fetch(url.toString(), {
        method: options.method ?? "GET",
        headers,
        body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
        signal: controller.signal,
        credentials: "omit",
      });
    } catch (error) {
      if (controller.signal.aborted) {
        throw externalAbort?.aborted ? new ApiClientError({ code: "cancelled", message: "Request cancelled." }) : ApiClientError.timeout();
      }
      throw ApiClientError.network(error instanceof Error ? error.message : undefined);
    } finally {
      clearTimeout(timeout);
      externalAbort?.removeEventListener("abort", onExternalAbort);
    }

    if (response.status === 204) {
      return undefined as T;
    }

    if (!response.ok) {
      throw await this.toApiError(response);
    }

    if (options.binary) {
      const mime = response.headers.get("content-type") ?? "application/octet-stream";
      const bytes = await response.arrayBuffer();
      const result: BinaryResult = { mime, bytes };
      return result as unknown as T;
    }

    try {
      return (await response.json()) as T;
    } catch {
      throw ApiClientError.malformed();
    }
  }

  private async toApiError(response: Response): Promise<ApiClientError> {
    if (response.status === 401) {
      return ApiClientError.unauthenticated();
    }
    try {
      const body = (await response.json()) as { error?: { code?: string; message?: string; field?: string; retry_after_seconds?: number } };
      if (body?.error?.code && body.error.message) {
        return new ApiClientError({
          code: body.error.code,
          message: body.error.message,
          field: body.error.field,
          retryAfterSeconds: body.error.retry_after_seconds,
        });
      }
    } catch {
      // fall through to generic mapping
    }
    return new ApiClientError({ code: `http_${response.status}`, message: `Request failed (${response.status}).` });
  }

  // ------------------------------------------------------------------ auth
  me(signal?: AbortSignal): Promise<Me> {
    return this.request({ path: "/me", signal });
  }
  logout(signal?: AbortSignal): Promise<void> {
    return this.request({ method: "POST", path: "/auth/logout", signal });
  }
  listSessions(signal?: AbortSignal): Promise<{ sessions: SessionInfo[] }> {
    return this.request({ path: "/auth/sessions", signal });
  }
  revokeSession(sessionId: string, signal?: AbortSignal): Promise<void> {
    return this.request({ method: "DELETE", path: `/auth/sessions/${encodeURIComponent(sessionId)}`, signal });
  }

  // ------------------------------------------------------------------ feed
  feed(cursor: string | undefined, signal?: AbortSignal): Promise<Feed> {
    return this.request({ path: "/feed", query: { cursor }, signal });
  }
  ringStatus(
    githubUserIds: number[],
    logins: string[],
    signal?: AbortSignal,
  ): Promise<{ entries: RingStatus[] }> {
    return this.request({
      method: "POST",
      path: "/stories/status",
      body: { github_user_ids: githubUserIds, logins },
      signal,
    });
  }
  mine(signal?: AbortSignal): Promise<AuthorGroup> {
    return this.request({ path: "/stories/mine", signal });
  }
  userStories(login: string, signal?: AbortSignal): Promise<AuthorGroup> {
    return this.request({ path: `/users/${encodeURIComponent(login)}/stories`, signal });
  }

  // --------------------------------------------------------------- uploads
  createUpload(
    input: { mime: string; byteSize: number; filename: string; caption: string; altText: string; visibility: Visibility; audienceListId?: string; allowReplies: boolean; allowReactions: boolean },
    idempotencyKey: string,
    signal?: AbortSignal,
  ): Promise<UploadIntent> {
    return this.request({
      method: "POST",
      path: "/uploads",
      idempotencyKey,
      body: {
        mime: input.mime,
        byte_size: input.byteSize,
        filename: input.filename,
        caption: input.caption,
        alt_text: input.altText,
        visibility: input.visibility,
        audience_list_id: input.audienceListId,
        allow_replies: input.allowReplies,
        allow_reactions: input.allowReactions,
      },
      signal,
    });
  }
  finalizeUpload(
    uploadId: string,
    byteSize: number,
    checksumSha256: string,
    idempotencyKey: string,
    signal?: AbortSignal,
  ): Promise<StoryItem> {
    return this.request({
      method: "POST",
      path: `/uploads/${encodeURIComponent(uploadId)}/finalize`,
      idempotencyKey,
      body: { byte_size: byteSize, checksum_sha256: checksumSha256 },
      signal,
    });
  }

  // ---------------------------------------------------------------- story
  getStory(storyId: string, signal?: AbortSignal): Promise<StoryItem> {
    return this.request({ path: `/stories/${encodeURIComponent(storyId)}`, signal });
  }
  updateStory(storyId: string, update: StoryUpdate, signal?: AbortSignal): Promise<StoryItem> {
    return this.request({ method: "PATCH", path: `/stories/${encodeURIComponent(storyId)}`, body: update, signal });
  }
  deleteStory(storyId: string, signal?: AbortSignal): Promise<void> {
    return this.request({ method: "DELETE", path: `/stories/${encodeURIComponent(storyId)}`, signal });
  }
  viewers(storyId: string, signal?: AbortSignal): Promise<ViewerList> {
    return this.request({ path: `/stories/${encodeURIComponent(storyId)}/viewers`, signal });
  }
  acknowledgeView(storyId: string, signal?: AbortSignal): Promise<void> {
    return this.request({ method: "POST", path: `/stories/${encodeURIComponent(storyId)}/view`, signal });
  }
  setReaction(storyId: string, emoji: Emoji, idempotencyKey: string, signal?: AbortSignal): Promise<Reaction> {
    return this.request({ method: "PUT", path: `/stories/${encodeURIComponent(storyId)}/reaction`, body: { emoji }, idempotencyKey, signal });
  }
  clearReaction(storyId: string, signal?: AbortSignal): Promise<void> {
    return this.request({ method: "DELETE", path: `/stories/${encodeURIComponent(storyId)}/reaction`, signal });
  }
  reply(storyId: string, body: string, idempotencyKey: string, signal?: AbortSignal): Promise<Reply> {
    return this.request({ method: "POST", path: `/stories/${encodeURIComponent(storyId)}/replies`, body: { body }, idempotencyKey, signal });
  }
  report(
    input: { subjectKind: "story" | "user"; storyId?: string; login?: string; reason: string; details?: string },
    signal?: AbortSignal,
  ): Promise<void> {
    return this.request({
      method: "POST",
      path: "/reports",
      body: { subject_kind: input.subjectKind, story_id: input.storyId, login: input.login, reason: input.reason, details: input.details },
      signal,
    });
  }

  // ---------------------------------------------------------------- media
  media(storyId: string, variant: string, signal?: AbortSignal): Promise<BinaryResult> {
    return this.request({ path: `/media/${encodeURIComponent(storyId)}/${encodeURIComponent(variant)}`, binary: true, signal, timeoutMs: 30_000 });
  }

  // ---------------------------------------------------------------- inbox
  inbox(cursor: string | undefined, signal?: AbortSignal): Promise<Inbox> {
    return this.request({ path: "/inbox", query: { cursor }, signal });
  }
  markInboxRead(input: { eventIds?: string[]; all?: boolean }, signal?: AbortSignal): Promise<void> {
    return this.request({ method: "POST", path: "/inbox/read", body: { event_ids: input.eventIds, all: input.all }, signal });
  }

  // ---------------------------------------------------------------- graph
  follow(login: string, signal?: AbortSignal): Promise<void> {
    return this.request({ method: "PUT", path: `/following/${encodeURIComponent(login)}`, signal });
  }
  unfollow(login: string, signal?: AbortSignal): Promise<void> {
    return this.request({ method: "DELETE", path: `/following/${encodeURIComponent(login)}`, signal });
  }
  mute(login: string, signal?: AbortSignal): Promise<void> {
    return this.request({ method: "PUT", path: `/mutes/${encodeURIComponent(login)}`, signal });
  }
  unmute(login: string, signal?: AbortSignal): Promise<void> {
    return this.request({ method: "DELETE", path: `/mutes/${encodeURIComponent(login)}`, signal });
  }
  block(login: string, signal?: AbortSignal): Promise<void> {
    return this.request({ method: "PUT", path: `/blocks/${encodeURIComponent(login)}`, signal });
  }
  unblock(login: string, signal?: AbortSignal): Promise<void> {
    return this.request({ method: "DELETE", path: `/blocks/${encodeURIComponent(login)}`, signal });
  }
  hide(login: string, signal?: AbortSignal): Promise<void> {
    return this.request({ method: "PUT", path: `/hides/${encodeURIComponent(login)}`, signal });
  }
  unhide(login: string, signal?: AbortSignal): Promise<void> {
    return this.request({ method: "DELETE", path: `/hides/${encodeURIComponent(login)}`, signal });
  }

  // ------------------------------------------------------------- settings
  getSettings(signal?: AbortSignal): Promise<Settings> {
    return this.request({ path: "/settings", signal });
  }
  updateSettings(update: SettingsUpdate, signal?: AbortSignal): Promise<Settings> {
    return this.request({ method: "PATCH", path: "/settings", body: update, signal });
  }
  createAudienceList(name: string, signal?: AbortSignal): Promise<AudienceList> {
    return this.request({ method: "POST", path: "/audience-lists", body: { name }, signal });
  }
  deleteAudienceList(listId: string, signal?: AbortSignal): Promise<void> {
    return this.request({ method: "DELETE", path: `/audience-lists/${encodeURIComponent(listId)}`, signal });
  }
  patchAudienceList(listId: string, input: { name?: string; logins?: string[] }, signal?: AbortSignal): Promise<AudienceList> {
    return this.request({ method: "PATCH", path: `/audience-lists/${encodeURIComponent(listId)}`, body: input, signal });
  }
  deleteAccount(signal?: AbortSignal): Promise<void> {
    return this.request({ method: "DELETE", path: "/settings/account", signal });
  }
}
