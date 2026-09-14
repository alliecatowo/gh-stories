/**
 * Client-side (popup/content) cache of authorized media as local `blob:`
 * URLs. The session token never reaches this context — every fetch goes
 * through the background over `runtime.sendMessage`, which returns raw
 * bytes; this module only ever turns bytes it already received into a
 * same-document object URL.
 */
import type { AuthorGroup } from "@gh-stories/contracts";
import { callBackground } from "../messaging/client.js";

function keyFor(storyId: string, variant: string): string {
  return `${storyId}:${variant}`;
}

/** Turns the base64 the background sent back into a real, playable Blob. */
function decodeMedia(base64: string, mime: string): Blob {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  return new Blob([bytes], { type: mime });
}

export class MediaUrlCache {
  private readonly urls = new Map<string, string>();
  private readonly inflight = new Map<string, Promise<string | null>>();

  /** Synchronous accessor for `StoryViewer`'s `mediaUrl` prop. Returns an
   * empty string on a cache miss — the component's own `onError` path
   * renders "Couldn't load this Story." until a prefetch fills the cache
   * and the caller re-renders. */
  get(storyId: string, variant: string): string {
    return this.urls.get(keyFor(storyId, variant)) ?? "";
  }

  async ensure(storyId: string, variant: string): Promise<string | null> {
    const key = keyFor(storyId, variant);
    const cached = this.urls.get(key);
    if (cached) return cached;
    const existing = this.inflight.get(key);
    if (existing) return existing;

    const promise = (async () => {
      const result = await callBackground({ type: "ghs:media/fetch", storyId, variant });
      if (!result.ok) return null;
      const url = URL.createObjectURL(decodeMedia(result.data.base64, result.data.mime));
      this.urls.set(key, url);
      return url;
    })();
    this.inflight.set(key, promise);
    try {
      return await promise;
    } finally {
      this.inflight.delete(key);
    }
  }

  /**
   * Warms ONLY the item the viewer is about to display.
   *
   * Deliberately not a bulk prefetch. The media authorization gateway records
   * a view when it delivers content-bearing bytes, so fetching a whole
   * author's sequence up front — or the next author's — would mark Stories as
   * viewed that the person never actually opened. Media is fetched when an
   * item is shown, and not before.
   */
  async warmCurrent(group: AuthorGroup, itemIndex: number): Promise<void> {
    const item = group.items?.[itemIndex];
    if (!item?.id) return;
    const displayable = (item.variants ?? []).find(
      (v) => v.kind === 'image' || v.kind === 'video' || v.kind === 'poster',
    );
    if (!displayable) return;
    await this.ensure(item.id, displayable.kind);
  }

  revokeAll(): void {
    for (const url of this.urls.values()) URL.revokeObjectURL(url);
    this.urls.clear();
  }
}

/** Separate small cache for arbitrary authorization-gateway paths (the
 * Inbox's thumbnail URLs), keyed by the raw path rather than story/variant. */
export class MediaPathCache {
  private readonly urls = new Map<string, string>();

  resolve(path: string): string {
    return this.urls.get(path) ?? "";
  }

  async prefetch(path: string): Promise<void> {
    if (this.urls.has(path)) return;
    const match = /\/media\/([^/]+)\/([^/?#]+)/.exec(path);
    const storyId = match?.[1];
    const variant = match?.[2];
    if (!storyId || !variant) return;
    const result = await callBackground({ type: "ghs:media/fetch", storyId, variant });
    if (!result.ok) return;
    this.urls.set(path, URL.createObjectURL(decodeMedia(result.data.base64, result.data.mime)));
  }

  revokeAll(): void {
    for (const url of this.urls.values()) URL.revokeObjectURL(url);
    this.urls.clear();
  }
}
