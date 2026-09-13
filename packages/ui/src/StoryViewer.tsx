import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  REACTIONS,
  VISIBILITY_LABELS,
  type AuthorGroup,
  type Emoji,
  type MediaVariant,
  type StoryItem,
} from "@gh-stories/contracts";
import "./StoryViewer.css";

export interface StoryViewerProps {
  groups: AuthorGroup[];
  startGroupIndex: number;
  startItemIndex?: number;
  /** Resolves an authorized gateway URL for a story's media variant. */
  mediaUrl: (storyId: string, variant: string) => string;
  onClose: () => void;
  onAdvanceGroup?: (login: string) => void;
  onViewed?: (storyId: string) => void;
  onReply?: (storyId: string, body: string) => Promise<void>;
  onReact?: (storyId: string, emoji: Emoji | null) => Promise<void>;
  onOpenViewers?: (storyId: string) => void;
  onOpenProfile?: (login: string) => void;
  onReport?: (storyId: string) => void;
  onDelete?: (storyId: string) => void;
  serverTimeOffsetMs?: number;
  reducedMotion?: boolean;
  /** Focus returns here on close. */
  returnFocusTo?: React.RefObject<HTMLElement | null>;
  /** Portal target, for hosts mounting inside a Shadow DOM. */
  container?: Element | DocumentFragment;
}

const IMAGE_DURATION_MS = 5000;
const TAP_MAX_DIST = 10;
const TAP_MAX_MS = 250;

function findVariant(item: StoryItem, kind: MediaVariant["kind"]): MediaVariant | undefined {
  return item.variants?.find((variant) => variant.kind === kind);
}

function relativeTime(iso: string | undefined, nowMs: number): string {
  if (!iso) return "";
  const diffSec = Math.max(0, Math.floor((nowMs - new Date(iso).getTime()) / 1000));
  if (diffSec < 60) return "now";
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m`;
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h`;
  return `${Math.floor(diffSec / 86400)}d`;
}

interface PointerTrack {
  x: number;
  y: number;
  t: number;
  held: boolean;
}

/** Near-full-screen Story viewer: the main event. */
export function StoryViewer(props: StoryViewerProps): React.JSX.Element {
  const {
    groups,
    mediaUrl,
    onClose,
    onAdvanceGroup,
    onViewed,
    onReply,
    onReact,
    onOpenViewers,
    onOpenProfile,
    onReport,
    onDelete,
    serverTimeOffsetMs = 0,
    reducedMotion = false,
    returnFocusTo,
    container,
  } = props;

  const [groupIndex, setGroupIndex] = useState(props.startGroupIndex);
  const [itemIndex, setItemIndex] = useState(props.startItemIndex ?? 0);
  const [manualPause, setManualPause] = useState(reducedMotion);
  const [replyFocused, setReplyFocused] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [docHidden, setDocHidden] = useState(() => document.hidden);
  const [muted, setMuted] = useState(true);
  const [needsPlayTap, setNeedsPlayTap] = useState(false);
  const [mediaStatus, setMediaStatus] = useState<"loading" | "ready" | "error">("loading");
  const [expiredIds, setExpiredIds] = useState<ReadonlySet<string>>(new Set());
  const [descriptionOpen, setDescriptionOpen] = useState(false);
  const [reactionOverride, setReactionOverride] = useState<Record<string, Emoji | null>>({});
  const [replyDraft, setReplyDraft] = useState("");
  const [replySending, setReplySending] = useState(false);
  const [progress, setProgress] = useState(0);

  const frameRef = useRef<HTMLDivElement | null>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const pointerRef = useRef<PointerTrack | null>(null);
  const holdTimerRef = useRef<number | undefined>(undefined);
  const elapsedRef = useRef(0);
  const viewedFiredRef = useRef<Set<string>>(new Set());

  const group: AuthorGroup | undefined = groups[groupIndex];
  const item: StoryItem | undefined = group?.items[itemIndex];
  const autoAdvanceEnabled = !reducedMotion;

  // Restore focus to the opener on unmount.
  useEffect(() => {
    return () => {
      returnFocusTo?.current?.focus();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    function onVisibility() {
      setDocHidden(document.hidden);
    }
    document.addEventListener("visibilitychange", onVisibility);
    return () => document.removeEventListener("visibilitychange", onVisibility);
  }, []);

  // Reset per-item ephemeral state when the displayed item changes.
  useEffect(() => {
    setMediaStatus("loading");
    setNeedsPlayTap(false);
    setDescriptionOpen(false);
    setProgress(0);
    elapsedRef.current = 0;
  }, [item?.id]);

  // Client-side expiry: fires even while paused, and clears the media.
  useEffect(() => {
    if (!item?.id || !item.expires_at) return;
    const id = item.id;
    const msLeft = new Date(item.expires_at).getTime() - (Date.now() + serverTimeOffsetMs);
    if (msLeft <= 0) {
      setExpiredIds((prev) => (prev.has(id) ? prev : new Set(prev).add(id)));
      return;
    }
    const timer = window.setTimeout(() => {
      setExpiredIds((prev) => (prev.has(id) ? prev : new Set(prev).add(id)));
    }, msLeft);
    return () => window.clearTimeout(timer);
  }, [item?.id, item?.expires_at, serverTimeOffsetMs]);

  const isExpired = Boolean(item?.id && expiredIds.has(item.id));
  const isDeleted = item?.state === "deleted" || item?.state === "removed";
  const effectivePaused =
    manualPause || replyFocused || menuOpen || docHidden || isExpired || isDeleted || !item;

  function goNext() {
    setGroupIndex((currentGroupIndex) => {
      const currentGroup = groups[currentGroupIndex];
      if (!currentGroup) return currentGroupIndex;
      const nextInGroup = itemIndex + 1;
      if (nextInGroup < currentGroup.items.length) {
        setItemIndex(nextInGroup);
        return currentGroupIndex;
      }
      const nextGroupIndex = currentGroupIndex + 1;
      const nextGroup = groups[nextGroupIndex];
      if (!nextGroup) {
        close();
        return currentGroupIndex;
      }
      setItemIndex(0);
      onAdvanceGroup?.(nextGroup.author.login);
      return nextGroupIndex;
    });
  }

  function goPrev() {
    setGroupIndex((currentGroupIndex) => {
      if (itemIndex > 0) {
        setItemIndex(itemIndex - 1);
        return currentGroupIndex;
      }
      const prevGroupIndex = currentGroupIndex - 1;
      const prevGroup = groups[prevGroupIndex];
      if (!prevGroup) return currentGroupIndex;
      setItemIndex(Math.max(0, prevGroup.items.length - 1));
      return prevGroupIndex;
    });
  }

  function close() {
    onClose();
  }

  // Image auto-advance timer (rAF-based so pause/resume preserves elapsed time).
  useEffect(() => {
    if (!item || item.media_kind !== "image") return;
    if (!autoAdvanceEnabled || mediaStatus !== "ready" || effectivePaused) return;
    let raf = 0;
    const startPerf = performance.now();
    const startElapsed = elapsedRef.current;
    function frame(now: number) {
      const elapsed = startElapsed + (now - startPerf);
      elapsedRef.current = elapsed;
      if (elapsed >= IMAGE_DURATION_MS) {
        setProgress(1);
        goNext();
        return;
      }
      setProgress(elapsed / IMAGE_DURATION_MS);
      raf = requestAnimationFrame(frame);
    }
    raf = requestAnimationFrame(frame);
    return () => cancelAnimationFrame(raf);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [item?.id, item?.media_kind, mediaStatus, effectivePaused, autoAdvanceEnabled]);

  // Video play/pause follows the same pause conditions.
  useEffect(() => {
    const video = videoRef.current;
    if (!video || item?.media_kind !== "video") return;
    if (effectivePaused) {
      video.pause();
      return;
    }
    const playPromise = video.play();
    if (playPromise) {
      playPromise.catch(() => setNeedsPlayTap(true));
    }
  }, [effectivePaused, item?.id, item?.media_kind, mediaStatus]);

  // Keyboard controls. Ignored (except Escape) while a text field has focus.
  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      const active = document.activeElement;
      const typing = active instanceof HTMLInputElement || active instanceof HTMLTextAreaElement;
      if (event.key === "Escape") {
        event.preventDefault();
        close();
        return;
      }
      if (typing) return;
      if (event.key === "ArrowRight") {
        event.preventDefault();
        goNext();
      } else if (event.key === "ArrowLeft") {
        event.preventDefault();
        goPrev();
      } else if (event.key === " ") {
        event.preventDefault();
        setManualPause((paused) => !paused);
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  });

  function onPointerDown(event: React.PointerEvent<HTMLDivElement>) {
    if (event.target instanceof HTMLElement && event.target.closest("button, input, textarea, a")) {
      return;
    }
    pointerRef.current = { x: event.clientX, y: event.clientY, t: performance.now(), held: false };
    holdTimerRef.current = window.setTimeout(() => {
      const track = pointerRef.current;
      if (!track) return;
      track.held = true;
      setManualPause(true);
    }, TAP_MAX_MS);
  }

  function onPointerUp(event: React.PointerEvent<HTMLDivElement>) {
    window.clearTimeout(holdTimerRef.current);
    const track = pointerRef.current;
    pointerRef.current = null;
    if (!track) return;
    const dist = Math.hypot(event.clientX - track.x, event.clientY - track.y);
    const elapsed = performance.now() - track.t;
    if (track.held) {
      setManualPause(false);
      return;
    }
    if (dist > TAP_MAX_DIST || elapsed > TAP_MAX_MS) return;
    const rect = event.currentTarget.getBoundingClientRect();
    const relX = (event.clientX - rect.left) / rect.width;
    if (relX < 1 / 3) goPrev();
    else goNext();
  }

  function onPointerCancel() {
    window.clearTimeout(holdTimerRef.current);
    if (pointerRef.current?.held) setManualPause(false);
    pointerRef.current = null;
  }

  async function submitReply() {
    if (!item?.id || !onReply || replyDraft.trim() === "") return;
    setReplySending(true);
    try {
      await onReply(item.id, replyDraft.trim());
      setReplyDraft("");
    } finally {
      setReplySending(false);
    }
  }

  async function toggleReaction(emoji: Emoji) {
    if (!item?.id || !onReact) return;
    const current = reactionOverride[item.id] ?? item.my_reaction ?? null;
    const next = current === emoji ? null : emoji;
    setReactionOverride((prev) => ({ ...prev, [item.id as string]: next }));
    await onReact(item.id, next);
  }

  const nowMs = Date.now() + serverTimeOffsetMs;

  const content = (
    <div className="ghs-root ghs-viewer" role="dialog" aria-modal="true" aria-label="Story viewer">
      <div className="ghs-viewer__backdrop" />
      <div
        className="ghs-viewer__frame"
        ref={frameRef}
        onPointerDown={onPointerDown}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerCancel}
      >
        {!group || groups.length === 0 ? (
          <EmptyState onClose={close} message="No Stories to show." />
        ) : group.items.length === 0 ? (
          <EmptyState onClose={close} message="This person has no active Stories." />
        ) : !item ? (
          <EmptyState onClose={close} message="No Stories to show." />
        ) : (
          <>
            <div className="ghs-viewer__progress" role="presentation">
              {group.items.map((it, idx) => {
                const fill = idx < itemIndex ? 1 : idx === itemIndex ? (reducedMotion ? 1 : progress) : 0;
                return (
                  <span className="ghs-viewer__segment" key={it.id}>
                    <span
                      className="ghs-viewer__segment-fill"
                      style={{ width: `${Math.round(fill * 100)}%` }}
                    />
                  </span>
                );
              })}
            </div>

            <header className="ghs-viewer__header">
              <img className="ghs-viewer__avatar" src={group.author.avatar_url} alt="" width={32} height={32} />
              <div className="ghs-viewer__identity">
                <button type="button" className="ghs-viewer__login" onClick={() => onOpenProfile?.(group.author.login)}>
                  {group.author.login}
                </button>
                <span className="ghs-viewer__meta">
                  {relativeTime(item.published_at, nowMs)}
                  {item.audience_label || item.visibility ? " · " : ""}
                  {item.audience_label ?? (item.visibility ? VISIBILITY_LABELS[item.visibility] : "")}
                </span>
              </div>
              {reducedMotion && !isExpired && !isDeleted ? (
                <button
                  type="button"
                  className="ghs-viewer__playpause"
                  aria-label={manualPause ? "Play" : "Pause"}
                  onClick={() => setManualPause((p) => !p)}
                >
                  {manualPause ? "▶" : "❚❚"}
                </button>
              ) : null}
              <button
                type="button"
                className="ghs-viewer__menu-button"
                aria-haspopup="menu"
                aria-expanded={menuOpen}
                aria-label="Story menu"
                onClick={() => setMenuOpen((open) => !open)}
              >
                ⋯
              </button>
              {menuOpen ? (
                <div className="ghs-viewer__menu" role="menu">
                  {item.is_owner && onOpenViewers ? (
                    <button role="menuitem" type="button" onClick={() => item.id && onOpenViewers(item.id)}>
                      Viewers{typeof item.viewer_count === "number" ? ` (${item.viewer_count})` : ""}
                    </button>
                  ) : null}
                  <button role="menuitem" type="button" onClick={() => onOpenProfile?.(group.author.login)}>
                    Open profile
                  </button>
                  {!item.is_owner && onReport ? (
                    <button role="menuitem" type="button" onClick={() => item.id && onReport(item.id)}>
                      Report
                    </button>
                  ) : null}
                  {item.is_owner && onDelete ? (
                    <button role="menuitem" type="button" onClick={() => item.id && onDelete(item.id)}>
                      Delete
                    </button>
                  ) : null}
                </div>
              ) : null}
            </header>

            <div className="ghs-viewer__stage">
              {isExpired ? (
                <StagePlaceholder text="This Story expired." />
              ) : isDeleted ? (
                <StagePlaceholder text="This Story was deleted." />
              ) : item.state === "failed" ? (
                <StagePlaceholder text={item.failure_message ?? "This Story failed to process."} />
              ) : (
                <MediaStage
                  item={item}
                  mediaUrl={mediaUrl}
                  status={mediaStatus}
                  onStatus={setMediaStatus}
                  onViewed={() => {
                    if (item.id && !viewedFiredRef.current.has(item.id)) {
                      viewedFiredRef.current.add(item.id);
                      onViewed?.(item.id);
                    }
                  }}
                  videoRef={videoRef}
                  muted={muted}
                  onToggleMuted={() => setMuted((m) => !m)}
                  needsPlayTap={needsPlayTap}
                  onManualPlay={() => {
                    setNeedsPlayTap(false);
                    videoRef.current?.play().catch(() => setNeedsPlayTap(true));
                  }}
                  onEnded={() => autoAdvanceEnabled && goNext()}
                />
              )}
              {item.alt_text ? (
                <div className="ghs-viewer__description">
                  <button type="button" onClick={() => setDescriptionOpen((open) => !open)} aria-expanded={descriptionOpen}>
                    Description
                  </button>
                  {descriptionOpen ? <p>{item.alt_text}</p> : null}
                </div>
              ) : null}
            </div>

            <footer className="ghs-viewer__footer">
              {!onReply ? (
                <p className="ghs-viewer__signed-out">Sign in to reply and react.</p>
              ) : (
                <form
                  className="ghs-viewer__reply"
                  onSubmit={(event) => {
                    event.preventDefault();
                    void submitReply();
                  }}
                >
                  <label className="ghs-visually-hidden" htmlFor="ghs-viewer-reply">
                    Reply to {group.author.login}
                  </label>
                  <input
                    id="ghs-viewer-reply"
                    type="text"
                    value={replyDraft}
                    disabled={item.allow_replies === false || replySending}
                    placeholder={item.allow_replies === false ? "Replies are off" : `Reply to ${group.author.login}…`}
                    onFocus={() => setReplyFocused(true)}
                    onBlur={() => setReplyFocused(false)}
                    onChange={(event) => setReplyDraft(event.target.value)}
                  />
                  <button type="submit" disabled={item.allow_replies === false || replySending || replyDraft.trim() === ""}>
                    Send
                  </button>
                </form>
              )}

              {onReact && item.allow_reactions !== false ? (
                <div className="ghs-viewer__reactions" role="group" aria-label="React">
                  {REACTIONS.map((emoji) => {
                    const active = (item.id ? reactionOverride[item.id] : undefined) ?? item.my_reaction;
                    return (
                      <button
                        key={emoji}
                        type="button"
                        className="ghs-viewer__reaction"
                        aria-pressed={active === emoji}
                        data-active={active === emoji || undefined}
                        onClick={() => void toggleReaction(emoji)}
                      >
                        {emoji}
                      </button>
                    );
                  })}
                </div>
              ) : null}

              <button type="button" className="ghs-viewer__close" onClick={close} aria-label="Close">
                ✕
              </button>
            </footer>
          </>
        )}
      </div>
    </div>
  );

  return container ? createPortal(content, container) : content;
}

function EmptyState(props: { message: string; onClose: () => void }): React.JSX.Element {
  return (
    <div className="ghs-viewer__empty">
      <p>{props.message}</p>
      <button type="button" onClick={props.onClose}>
        Close
      </button>
    </div>
  );
}

function StagePlaceholder(props: { text: string }): React.JSX.Element {
  return (
    <div className="ghs-viewer__placeholder">
      <p>{props.text}</p>
    </div>
  );
}

interface MediaStageProps {
  item: StoryItem;
  mediaUrl: (storyId: string, variant: string) => string;
  status: "loading" | "ready" | "error";
  onStatus: (status: "loading" | "ready" | "error") => void;
  onViewed: () => void;
  videoRef: React.RefObject<HTMLVideoElement | null>;
  muted: boolean;
  onToggleMuted: () => void;
  needsPlayTap: boolean;
  onManualPlay: () => void;
  onEnded: () => void;
}

function MediaStage(props: MediaStageProps): React.JSX.Element {
  const { item, mediaUrl, status, onStatus, onViewed, videoRef, muted, onToggleMuted, needsPlayTap, onManualPlay, onEnded } = props;
  const isVideo = item.media_kind === "video";
  const primary = isVideo ? findVariant(item, "video") : findVariant(item, "image");
  const poster = findVariant(item, "poster");
  const backdropVariant = poster ?? primary;
  const src = primary && item.id ? mediaUrl(item.id, primary.kind) : undefined;
  const backdropSrc = backdropVariant && item.id ? mediaUrl(item.id, backdropVariant.kind) : undefined;

  if (!src) {
    return <StagePlaceholder text="Couldn't load this Story." />;
  }

  return (
    <div className="ghs-viewer__media">
      {backdropSrc ? (
        <div
          className="ghs-viewer__media-backdrop"
          style={{ backgroundImage: `url("${backdropSrc.replace(/"/g, "%22")}")` }}
        />
      ) : null}
      {status === "loading" ? <div className="ghs-viewer__spinner" aria-hidden="true" /> : null}
      {status === "error" ? <StagePlaceholder text="Couldn't load this Story." /> : null}
      {isVideo ? (
        <video
          ref={videoRef}
          className="ghs-viewer__media-content"
          src={src}
          poster={backdropSrc}
          muted={muted}
          playsInline
          aria-label={item.alt_text}
          style={{ visibility: status === "error" ? "hidden" : "visible" }}
          onLoadedData={() => {
            onStatus("ready");
            onViewed();
          }}
          onError={() => onStatus("error")}
          onEnded={onEnded}
        />
      ) : (
        // eslint-disable-next-line jsx-a11y/alt-text
        <img
          className="ghs-viewer__media-content"
          src={src}
          alt={item.alt_text ?? ""}
          style={{ visibility: status === "error" ? "hidden" : "visible" }}
          onLoad={() => {
            onStatus("ready");
            onViewed();
          }}
          onError={() => onStatus("error")}
        />
      )}
      {isVideo && status === "ready" ? (
        <button type="button" className="ghs-viewer__sound" onClick={onToggleMuted} aria-label={muted ? "Unmute" : "Mute"}>
          {muted ? "🔇" : "🔊"}
        </button>
      ) : null}
      {isVideo && needsPlayTap ? (
        <button type="button" className="ghs-viewer__play-tap" onClick={onManualPlay} aria-label="Play video">
          ▶
        </button>
      ) : null}
    </div>
  );
}
