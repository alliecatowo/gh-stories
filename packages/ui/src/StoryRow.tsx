import { useRef } from "react";
import type { AuthorGroup } from "@gh-stories/contracts";
import { StoryRing, type StoryRingState } from "./StoryRing.js";
import "./StoryRow.css";

export interface StoryRowOwn {
  login: string;
  avatarUrl: string;
  /** Whether the signed-in user currently has an active (unexpired) Story. */
  hasStory: boolean;
}

export interface StoryRowProps {
  /** The signed-in user's own tile. `null` when signed out. */
  me: StoryRowOwn | null;
  /** Followed accounts with at least one active Story, in display order. */
  groups: AuthorGroup[];
  onOpenGroup: (login: string) => void;
  /** Opens the signed-in user's own Story ring, when they have one. */
  onOpenOwn?: () => void;
  /** Opens the composer to post a new Story. */
  onCreate: () => void;
  ringSize?: number;
}

function ringStateFor(group: AuthorGroup): StoryRingState {
  if (group.items.length === 0) return "none";
  if (group.muted) return "muted";
  return group.has_unseen ? "unseen" : "seen";
}

/** The dashboard row: own avatar + post control first, then followed
 * accounts with active Stories. Horizontally scrollable, arrow-key
 * navigable, with a genuinely useful empty state. */
export function StoryRow(props: StoryRowProps): React.JSX.Element {
  const { me, groups, onOpenGroup, onOpenOwn, onCreate, ringSize = 56 } = props;
  const listRef = useRef<HTMLDivElement | null>(null);

  function handleKeyDown(event: React.KeyboardEvent<HTMLDivElement>) {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    const root = listRef.current;
    if (!root) return;
    const focusable = Array.from(root.querySelectorAll<HTMLButtonElement>("button"));
    const currentIndex = focusable.indexOf(document.activeElement as HTMLButtonElement);
    if (currentIndex === -1) return;
    event.preventDefault();
    const delta = event.key === "ArrowRight" ? 1 : -1;
    const next = focusable[Math.min(Math.max(currentIndex + delta, 0), focusable.length - 1)];
    next?.focus();
    next?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }

  const isEmpty = groups.length === 0;

  return (
    <div className="ghs-root ghs-story-row">
      <div className="ghs-story-row__track" ref={listRef} role="list" onKeyDown={handleKeyDown}>
        {me ? (
          <div className="ghs-story-row__item" role="listitem" data-ghs-row-item>
            <div className="ghs-story-row__own">
              <StoryRing
                src={me.avatarUrl}
                alt={`${me.login}'s avatar`}
                size={ringSize}
                state={me.hasStory ? "seen" : "none"}
                hasStory={me.hasStory}
                onActivate={me.hasStory ? onOpenOwn : undefined}
                label={me.hasStory ? `Your Story` : `${me.login}, no active Story`}
              />
              <button
                type="button"
                className="ghs-story-row__create"
                onClick={onCreate}
                aria-label="Post a new Story"
              >
                +
              </button>
            </div>
            <span className="ghs-story-row__caption">You</span>
          </div>
        ) : null}

        {groups.map((group) => {
          const state = ringStateFor(group);
          return (
            <div
              className="ghs-story-row__item"
              role="listitem"
              key={group.author.login}
              data-ghs-row-item
            >
              <StoryRing
                src={group.author.avatar_url}
                alt={`${group.author.login}'s avatar`}
                size={ringSize}
                state={state}
                hasStory={group.items.length > 0}
                onActivate={() => onOpenGroup(group.author.login)}
                label={
                  state === "unseen"
                    ? `${group.author.login} has an unseen Story`
                    : state === "muted"
                      ? `${group.author.login}'s Story, muted`
                      : `${group.author.login} has a Story`
                }
              />
              <span className="ghs-story-row__caption">{group.author.login}</span>
            </div>
          );
        })}

        {isEmpty ? (
          <div className="ghs-story-row__empty">
            <p>No Stories right now. People you follow will show up here.</p>
            <button type="button" className="ghs-story-row__empty-cta" onClick={onCreate}>
              Post a Story
            </button>
          </div>
        ) : null}
      </div>
    </div>
  );
}
