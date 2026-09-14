/**
 * Wires `@gh-stories/ui`'s `StoryViewer` to the background over
 * `runtime.sendMessage`. Shared by the content script's page overlay and
 * the popup's Stories tab so both surfaces behave identically.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { StoryViewer } from "@gh-stories/ui";
import type { AuthorGroup } from "@gh-stories/contracts";
import { callBackground } from "../messaging/client.js";
import { MediaUrlCache } from "../state/mediaCache.js";

export interface ViewerBridgeProps {
  groups: AuthorGroup[];
  startGroupIndex: number;
  onClose: () => void;
  container?: Element | DocumentFragment;
}

export function ViewerBridge(props: ViewerBridgeProps): React.JSX.Element {
  const { groups, startGroupIndex, onClose, container } = props;
  const cacheRef = useRef<MediaUrlCache | null>(null);
  if (!cacheRef.current) cacheRef.current = new MediaUrlCache();
  const cache = cacheRef.current;

  // Bumped whenever a fetch lands, to re-render with the now-cached URL.
  const [, setRevision] = useState(0);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    const group = groups[startGroupIndex];
    if (group) {
      void cache.warmCurrent(group, 0).then(() => {
        if (mountedRef.current) setRevision((n) => n + 1);
      });
    }
    return () => {
      mountedRef.current = false;
      cache.revokeAll();
    };
    // Intentionally only on mount/unmount: `groups` is a fresh array from
    // the caller on every open, and re-running per render would thrash the
    // blob URL cache.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  /**
   * Resolves a media URL for the item currently on screen.
   *
   * `StoryViewer` asks synchronously, but media has to be fetched through the
   * background (the session token never reaches this context). So a miss
   * starts the fetch and re-renders when it lands, rather than leaving the
   * viewer stuck on "Couldn't load this Story" forever.
   *
   * Fetching here — lazily, for the item actually being displayed — is also
   * what keeps the product honest: the media gateway records a view when it
   * delivers bytes, so nothing is fetched ahead of being shown.
   */
  const resolveMedia = useCallback(
    (storyId: string, variant: string): string => {
      const cached = cache.get(storyId, variant);
      if (cached) return cached;
      void cache.ensure(storyId, variant).then((url) => {
        if (url && mountedRef.current) setRevision((n) => n + 1);
      });
      return "";
    },
    [cache],
  );

  return (
    <StoryViewer
      groups={groups}
      startGroupIndex={startGroupIndex}
      mediaUrl={resolveMedia}
      onClose={onClose}
      onAdvanceGroup={(login) => {
        const next = groups.find((group) => group.author.login === login);
        // Only warm the group the viewer has actually moved to, and only
        // its first item: fetching ahead would record views nobody made.
        if (next) {
          void cache.warmCurrent(next, 0).then(() => {
            if (mountedRef.current) setRevision((n) => n + 1);
          });
        }
      }}
      onViewed={(storyId) => {
        void callBackground({ type: "ghs:story/view", storyId });
      }}
      onReply={async (storyId, body) => {
        const result = await callBackground({ type: "ghs:story/reply", storyId, body });
        if (!result.ok) throw new Error(result.error.message);
      }}
      onReact={async (storyId, emoji) => {
        await callBackground({ type: "ghs:story/react", storyId, emoji });
      }}
      onOpenProfile={(login) => {
        window.open(`https://github.com/${login}`, "_blank", "noopener,noreferrer");
      }}
      onDelete={(storyId) => {
        void callBackground({ type: "ghs:story/delete", storyId }).then(() => onClose());
      }}
      onReport={(storyId) => {
        void callBackground({ type: "ghs:story/report", storyId, reason: "other" });
      }}
      container={container}
    />
  );
}
