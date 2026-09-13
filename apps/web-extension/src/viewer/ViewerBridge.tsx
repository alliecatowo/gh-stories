/**
 * Wires `@gh-stories/ui`'s `StoryViewer` to the background over
 * `runtime.sendMessage`. Shared by the content script's page overlay and
 * the popup's Stories tab so both surfaces behave identically.
 */
import { useEffect, useRef } from "react";
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

  useEffect(() => {
    const group = groups[startGroupIndex];
    if (group) void cache.prefetchGroup(group);
    return () => cache.revokeAll();
    // Intentionally only on mount/unmount: `groups` is a fresh array from
    // the caller on every open, and re-running per render would thrash the
    // blob URL cache.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <StoryViewer
      groups={groups}
      startGroupIndex={startGroupIndex}
      mediaUrl={(storyId, variant) => cache.get(storyId, variant)}
      onClose={onClose}
      onAdvanceGroup={(login) => {
        const next = groups.find((group) => group.author.login === login);
        if (next) void cache.prefetchGroup(next);
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
