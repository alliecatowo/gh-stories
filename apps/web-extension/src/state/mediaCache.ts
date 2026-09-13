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
      const blob = new Blob([result.data.bytes], { type: result.data.mime });
      const url = URL.createObjectURL(blob);
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

  /** Prefetches every variant of every item in one author group. Used before
   * opening the viewer on a group so playback starts without a visible
   * fetch, and again on `onAdvanceGroup` for the next group. */
  async prefetchGroup(group: AuthorGroup): Promise<void> {
    const jobs: Array<Promise<string | null>> = [];
    for (const item of group.items) {
      if (!item.id) continue;
      for (const variant of item.variants ?? []) {
        jobs.push(this.ensure(item.id, variant.kind));
      }
    }
    await Promise.all(jobs);
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
    if (!match) return;
    const [, storyId, variant] = match;
    const result = await callBackground({ type: "ghs:media/fetch", storyId, variant });
    if (!result.ok) return;
    const blob = new Blob([result.data.bytes], { type: result.data.mime });
    this.urls.set(path, URL.createObjectURL(blob));
  }

  revokeAll(): void {
    for (const url of this.urls.values()) URL.revokeObjectURL(url);
    this.urls.clear();
  }
}
