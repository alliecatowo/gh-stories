import { useEffect, useMemo, useRef, useState } from 'react';
import { StoryRow, StoryViewer } from '@gh-stories/ui';
import { VISIBILITY_LABELS, type AuthorGroup, type StoryItem, type Visibility } from '@gh-stories/contracts';
import { withBase } from '../lib/base';
import { makeSimulatedItem, sampleGroups, simulatedPostChoices, you } from '../lib/fixtures';
import './DemoIsland.css';

type Presentation = 'browser' | 'terminal';

const VISIBILITY_ORDER: Visibility[] = [
  'followers_of_author',
  'author_follows',
  'mutuals',
  'custom_list',
  'public',
];

function usePrefersReducedMotion(): boolean {
  const [reduced, setReduced] = useState(false);
  useEffect(() => {
    const mql = window.matchMedia('(prefers-reduced-motion: reduce)');
    setReduced(mql.matches);
    const onChange = () => setReduced(mql.matches);
    mql.addEventListener('change', onChange);
    return () => mql.removeEventListener('change', onChange);
  }, []);
  return reduced;
}

/** The interactive sample demo. Runs entirely in memory: no network calls,
 * no login, fictional content only. See apps/site/public/samples/README.md
 * for asset provenance. */
export default function DemoIsland() {
  const [groups] = useState<AuthorGroup[]>(() => sampleGroups());
  const [youItems, setYouItems] = useState<StoryItem[]>([]);
  const [presentation, setPresentation] = useState<Presentation>('browser');
  const [openLogin, setOpenLogin] = useState<string | null>(null);
  const [openItemIndex, setOpenItemIndex] = useState(0);
  const [postOpen, setPostOpen] = useState(false);
  const [postChoice, setPostChoice] = useState(0);
  const [postVisibility, setPostVisibility] = useState<Visibility>('public');
  const [postStatus, setPostStatus] = useState<'idle' | 'posting' | 'posted'>('idle');
  const [statusMessage, setStatusMessage] = useState('');
  const reducedMotion = usePrefersReducedMotion();
  const openerRef = useRef<HTMLDivElement>(null);

  const youGroup: AuthorGroup | null = youItems.length ? { author: you, items: youItems, has_unseen: true } : null;

  // "you" first, matching how the real dashboard row always puts your own
  // tile first — computed fresh every render so an in-progress viewer
  // never reads a stale index after a simulated post changes this order.
  const allGroups = useMemo(() => (youGroup ? [youGroup, ...groups] : groups), [youGroup, groups]);
  const resolvedGroupIndex = openLogin ? allGroups.findIndex((g) => g.author.login === openLogin) : -1;
  const viewerOpen = resolvedGroupIndex !== -1;

  const mediaById = useMemo(() => {
    const map = new Map<string, StoryItem>();
    for (const group of allGroups) for (const it of group.items) if (it.id) map.set(it.id, it);
    return map;
  }, [allGroups]);

  function mediaUrl(storyId: string, kind: string): string {
    const variant = mediaById.get(storyId)?.variants?.find((v) => v.kind === kind);
    return variant ? withBase(variant.url) : '';
  }

  function openGroup(login: string) {
    setOpenLogin(login);
    setOpenItemIndex(0);
  }

  function closeViewer() {
    setOpenLogin(null);
  }

  function submitSimulatedPost() {
    setPostStatus('posting');
    setStatusMessage('Posting (simulated)…');
    window.setTimeout(() => {
      const choice = simulatedPostChoices[postChoice];
      const newItem = makeSimulatedItem(choice, postVisibility);
      setYouItems((prev) => [newItem, ...prev]);
      setPostStatus('posted');
      const label = postVisibility ? VISIBILITY_LABELS[postVisibility] : '';
      setStatusMessage(`Posted to "${label}" (simulated). Nothing was uploaded anywhere.`);
    }, 700);
  }

  const me = { login: you.login, avatarUrl: you.avatar_url, hasStory: Boolean(youGroup) };

  const viewer = viewerOpen ? (
    <StoryViewer
      groups={allGroups}
      startGroupIndex={resolvedGroupIndex}
      startItemIndex={openItemIndex}
      mediaUrl={mediaUrl}
      onClose={closeViewer}
      reducedMotion={reducedMotion}
      returnFocusTo={openerRef}
    />
  ) : null;

  return (
    <div className="ghs-root demo-island">
      <div className="demo-toolbar">
        <div className="demo-toolbar__presentation" role="radiogroup" aria-label="Presentation">
          <button
            type="button"
            role="radio"
            aria-checked={presentation === 'browser'}
            className="demo-toolbar__tab"
            data-active={presentation === 'browser' || undefined}
            onClick={() => setPresentation('browser')}
          >
            Browser presentation
          </button>
          <button
            type="button"
            role="radio"
            aria-checked={presentation === 'terminal'}
            className="demo-toolbar__tab"
            data-active={presentation === 'terminal' || undefined}
            onClick={() => setPresentation('terminal')}
          >
            Terminal presentation
          </button>
        </div>
        <p className="demo-hint">
          Tap a ring, then use the left/right edges (or arrow keys) to move through the sequence,
          and space to pause.
        </p>
      </div>

      {presentation === 'terminal' ? (
        <p className="demo-note">
          This is the same viewer component wrapped in a terminal-styled frame, for side-by-side
          comparison only. It does not prove terminal rendering — the real <code>gh stories</code>{' '}
          draws pixels directly in your terminal via the Kitty graphics protocol or iTerm2 inline
          images. That is proved separately by captured runs, not by this browser simulation.
        </p>
      ) : null}

      <div ref={openerRef}>
        <StoryRow
          me={me}
          groups={groups}
          onOpenGroup={openGroup}
          onOpenOwn={youGroup ? () => openGroup('you') : undefined}
          onCreate={() => setPostOpen(true)}
        />
      </div>

      {presentation === 'terminal' ? (
        <div className="demo-terminal-frame">
          <div className="term-mock__topbar">
            <span className="term-mock__dots">
              <span></span>
              <span></span>
              <span></span>
            </span>
            <span className="term-mock__title">zsh — gh stories</span>
          </div>
          <div className="demo-terminal-frame__body">
            {viewer ?? (
              <p className="demo-terminal-frame__idle">
                <span className="term-mock__prompt">gh stories @&lt;login&gt;</span>
                <br />
                Click a ring above to load a Story here.
              </p>
            )}
          </div>
        </div>
      ) : (
        viewer
      )}

      {postOpen ? (
        <div className="post-panel" role="dialog" aria-modal="true" aria-label="Simulate posting a Story">
          <div className="post-panel__card">
            <button
              type="button"
              className="post-panel__close"
              aria-label="Close"
              onClick={() => {
                setPostOpen(false);
                setPostStatus('idle');
              }}
            >
              ✕
            </button>
            <h3>
              Post a sample Story <span className="post-panel__badge">Simulated</span>
            </h3>
            <p className="post-panel__note">
              Nothing is uploaded anywhere. This only adds a fictional item to the "you" row above,
              in this browser tab, for as long as the page stays open.
            </p>
            <fieldset className="post-panel__fieldset">
              <legend>Choose a sample photo</legend>
              {simulatedPostChoices.map((choice, idx) => (
                <label key={choice.media} className="post-panel__choice">
                  <input
                    type="radio"
                    name="demo-post-choice"
                    checked={postChoice === idx}
                    onChange={() => setPostChoice(idx)}
                  />
                  <img src={withBase(choice.media)} alt="" width={32} height={40} loading="lazy" />
                  {choice.label}
                </label>
              ))}
            </fieldset>
            <label className="post-panel__field">
              Who can see it
              <select value={postVisibility} onChange={(event) => setPostVisibility(event.target.value as Visibility)}>
                {VISIBILITY_ORDER.map((v) => (
                  <option key={v} value={v}>
                    {VISIBILITY_LABELS[v]}
                  </option>
                ))}
              </select>
            </label>
            <div className="post-panel__actions">
              <button
                type="button"
                className="btn btn--secondary"
                onClick={() => {
                  setPostOpen(false);
                  setPostStatus('idle');
                }}
              >
                Cancel
              </button>
              <button
                type="button"
                className="btn btn--primary"
                disabled={postStatus === 'posting'}
                onClick={submitSimulatedPost}
              >
                {postStatus === 'posting' ? 'Posting…' : 'Post (simulated)'}
              </button>
            </div>
            {postStatus === 'posted' ? (
              <p className="post-panel__success">
                {statusMessage}{' '}
                <button
                  type="button"
                  className="btn btn--small btn--secondary"
                  onClick={() => {
                    setPostOpen(false);
                    setPostStatus('idle');
                    openGroup('you');
                  }}
                >
                  View it
                </button>
              </p>
            ) : null}
          </div>
        </div>
      ) : null}

      <p className="ghs-visually-hidden" role="status" aria-live="polite">
        {statusMessage}
      </p>
    </div>
  );
}
