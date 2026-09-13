/**
 * UI-local types that are not part of the generated API contract.
 */
import type { Visibility } from "@gh-stories/contracts";

/** One choice in the audience picker, with its plain-language meaning
 * already resolved so it can be shown before publication. */
export interface AudienceOption {
  visibility: Visibility;
  /** Present when visibility is "custom_list". */
  audienceListId?: string;
  label: string;
  description: string;
}

export interface VideoEditParams {
  startMs: number;
  endMs: number;
  muted: boolean;
}

/** What StoryComposer hands back to the host on submit. The host performs
 * the actual upload-intent + PUT + finalize sequence. */
export interface ComposerDraft {
  file: File | Blob;
  filename: string;
  caption: string;
  altText: string;
  visibility: Visibility;
  audienceListId?: string;
  allowReplies: boolean;
  allowReactions: boolean;
  videoEdit?: VideoEditParams;
}
