/**
 * Wires `@gh-stories/ui`'s `StoryComposer` to the background's upload port
 * protocol: `POST /uploads` -> PUT bytes -> `POST /uploads/{id}/finalize`
 * -> poll until `published`/`failed`, all inside `runUploadFlow`. Shared by
 * the content script's page overlay and the popup's Post tab.
 *
 * The idempotency key is created once per mount and reused for every retry
 * of the same draft (`StoryComposer`'s own "Retry" button calls `onSubmit`
 * again with the same in-progress draft state), so a retry after a dropped
 * connection can never create a duplicate Story.
 */
import { useEffect, useRef, useState } from "react";
import { StoryComposer, type StoryComposerDefaults } from "@gh-stories/ui";
import type { AudienceOption, ComposerDraft } from "@gh-stories/ui";
import type { Settings } from "@gh-stories/contracts";
import { VISIBILITY_LABELS } from "@gh-stories/contracts";
import { browser } from "wxt/browser";
import { callBackground } from "../messaging/client.js";
import { PORT_UPLOAD, type UploadServerMessage, type UploadStartMessage } from "../messaging/types.js";

function audiencesFromSettings(settings: Settings | null): AudienceOption[] {
  const base: AudienceOption[] = (["followers_of_author", "author_follows", "mutuals", "public"] as const).map(
    (visibility) => ({ visibility, label: VISIBILITY_LABELS[visibility], description: VISIBILITY_LABELS[visibility] }),
  );
  const listOptions: AudienceOption[] = (settings?.audience_lists ?? []).flatMap((list) =>
    list.id
      ? [
          {
            visibility: "custom_list" as const,
            audienceListId: list.id,
            label: list.name ?? "Custom list",
            description: `${list.member_count ?? list.members?.length ?? 0} people`,
          },
        ]
      : [],
  );
  return [...base, ...listOptions];
}

export interface ComposerBridgeProps {
  onClose: () => void;
  container?: Element | DocumentFragment;
}

export function ComposerBridge(props: ComposerBridgeProps): React.JSX.Element | null {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [loaded, setLoaded] = useState(false);
  const idempotencyKeyRef = useRef<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void callBackground({ type: "ghs:settings/get" }).then((result) => {
      if (cancelled) return;
      if (result.ok) setSettings(result.data);
      setLoaded(true);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  if (!loaded) return null;

  const defaults: StoryComposerDefaults = {
    visibility: settings?.default_visibility ?? "followers_of_author",
    audienceListId: settings?.default_audience_list_id,
    allowReplies: settings?.default_allow_replies ?? true,
    allowReactions: settings?.default_allow_reactions ?? true,
  };

  async function submit(draft: ComposerDraft): Promise<void> {
    const bytes = await draft.file.arrayBuffer();
    if (!idempotencyKeyRef.current) idempotencyKeyRef.current = crypto.randomUUID();
    const start: UploadStartMessage = {
      type: "start",
      idempotencyKey: idempotencyKeyRef.current,
      mime: draft.file.type || "application/octet-stream",
      byteSize: bytes.byteLength,
      filename: draft.filename,
      caption: draft.caption,
      altText: draft.altText,
      visibility: draft.visibility,
      audienceListId: draft.audienceListId,
      allowReplies: draft.allowReplies,
      allowReactions: draft.allowReactions,
      bytes,
    };

    await new Promise<void>((resolve, reject) => {
      const port = browser.runtime.connect({ name: PORT_UPLOAD });
      port.onMessage.addListener((raw: unknown) => {
        const msg = raw as UploadServerMessage;
        if (msg.type === "done") {
          idempotencyKeyRef.current = null;
          port.disconnect();
          resolve();
        } else if (msg.type === "error") {
          port.disconnect();
          reject(new Error(msg.error.message));
        } else if (msg.type === "cancelled") {
          port.disconnect();
          reject(new Error("Upload cancelled."));
        }
        // "progress" / "phase" ticks: StoryComposer owns its own internal
        // ramp/processing UI and has no external progress prop to feed.
      });
      port.postMessage(start);
    });
  }

  return (
    <StoryComposer
      onSubmit={submit}
      audiences={audiencesFromSettings(settings)}
      defaults={defaults}
      onCancel={() => {
        idempotencyKeyRef.current = null;
        props.onClose();
      }}
      container={props.container}
    />
  );
}
