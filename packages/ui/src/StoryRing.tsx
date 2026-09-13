import { type CSSProperties } from "react";
import "./StoryRing.css";

export type StoryRingState = "unseen" | "seen" | "muted" | "none";

export interface StoryRingProps {
  src: string;
  alt: string;
  /** Avatar diameter in px. The ring is drawn around this without changing
   * the overall footprint, so layouts never shift when a ring appears. */
  size?: number;
  state: StoryRingState;
  hasStory: boolean;
  onActivate?: () => void;
  /** Full accessible label, e.g. "maya has an unseen Story". */
  label: string;
}

const RING_WIDTH = 2;
const RING_GAP = 2;

const STATE_TEXT: Record<StoryRingState, string> = {
  unseen: "Unseen Story",
  seen: "Story seen",
  muted: "Story muted",
  none: "No Story",
};

/** Wraps an avatar with the product's one distinctive ring accent. Renders a
 * `<button>` only when it is truly activatable, so consumers can safely nest
 * this inside their own interactive avatar links elsewhere on the page. */
export function StoryRing(props: StoryRingProps): React.JSX.Element {
  const { src, alt, size = 40, state, hasStory, onActivate, label } = props;
  const outer = size + 2 * (RING_WIDTH + RING_GAP);
  const interactive = Boolean(onActivate) && hasStory;
  const outerStyle: CSSProperties = { width: outer, height: outer };

  const inner = (
    <>
      <span className="ghs-story-ring__frame" data-state={state}>
        <span className="ghs-story-ring__gap">
          {/* The composite control's own aria-label carries the accessible
              name (including read state); `alt` here is only the browser's
              broken-image fallback text. */}
          <img className="ghs-story-ring__avatar" src={src} alt={alt} width={size} height={size} />
        </span>
      </span>
      <span className="ghs-visually-hidden">{STATE_TEXT[state]}</span>
    </>
  );

  if (interactive) {
    return (
      <button
        type="button"
        className="ghs-story-ring"
        style={outerStyle}
        aria-label={label}
        onClick={onActivate}
      >
        {inner}
      </button>
    );
  }

  return (
    <span className="ghs-story-ring" style={outerStyle} aria-label={label} role="img">
      {inner}
    </span>
  );
}
