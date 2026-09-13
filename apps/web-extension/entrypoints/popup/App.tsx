/**
 * The independent toolbar entry.
 *
 * This must keep working even when the GitHub page integration breaks
 * entirely: it talks only to the background, never to a content script, and it
 * never assumes a github.com tab is open.
 */
import React, { useCallback, useEffect, useState } from 'react';
import type { AuthorGroup, Feed, Inbox as InboxData } from '@gh-stories/contracts';
import type { AccountSummary } from '../../src/messaging/types.js';
import { StoryRow, StoryViewer, Inbox } from '@gh-stories/ui';
import { callBackground } from '../../src/messaging/client.js';
import { MediaUrlCache } from '../../src/state/mediaCache.js';

type Tab = 'stories' | 'post' | 'inbox' | 'settings';

const cache = new MediaUrlCache();

export function App(): React.ReactElement {
  const [tab, setTab] = useState<Tab>('stories');
  const [me, setMe] = useState<AccountSummary | null>(null);
  const [signedIn, setSignedIn] = useState<boolean | null>(null);
  const [error, setError] = useState<string>('');

  const loadSession = useCallback(async () => {
    const res = await callBackground({ type: 'ghs:session/get' });
    if (!res.ok) {
      setError(res.error.message);
      setSignedIn(false);
      return;
    }
    setSignedIn(res.data.signedIn);
    setMe(res.data.account);
  }, []);

  useEffect(() => {
    void loadSession();
    return () => cache.revokeAll();
  }, [loadSession]);

  if (signedIn === null) {
    return <div className="ghs-pop-empty">Loading…</div>;
  }
  if (!signedIn) {
    return <SignIn onDone={loadSession} error={error} />;
  }

  return (
    <>
      <div className="ghs-pop-tabs" role="tablist" aria-label="GitHub Stories">
        {(['stories', 'post', 'inbox', 'settings'] as Tab[]).map((t) => (
          <button
            key={t}
            role="tab"
            aria-selected={tab === t}
            className="ghs-pop-tab"
            onClick={() => setTab(t)}
          >
            {t === 'stories' ? 'Stories' : t === 'post' ? 'Post' : t === 'inbox' ? 'Inbox' : 'Settings'}
            {t === 'inbox' && me?.unreadInbox ? ` (${me.unreadInbox})` : ''}
          </button>
        ))}
      </div>
      <div className="ghs-pop-body">
        {tab === 'stories' && <StoriesTab />}
        {tab === 'post' && <PostTab onPosted={loadSession} />}
        {tab === 'inbox' && <InboxTab />}
        {tab === 'settings' && <SettingsTab me={me} onSignedOut={loadSession} />}
      </div>
    </>
  );
}

function SignIn({ onDone, error }: { onDone: () => void; error: string }): React.ReactElement {
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState(error);

  const start = async () => {
    setBusy(true);
    setMessage('');
    const res = await callBackground({ type: 'ghs:session/login-start' });
    if (!res.ok) {
      setMessage(res.error.message);
      setBusy(false);
      return;
    }
    // The background opens the service's authorization page and receives the
    // Stories token there. GitHub credentials never touch this context.
    const poll = window.setInterval(async () => {
      const p = await callBackground({ type: 'ghs:session/login-poll' });
      if (p.ok && p.data.status === 'approved') {
        window.clearInterval(poll);
        setBusy(false);
        onDone();
      } else if (p.ok && (p.data.status === 'denied' || p.data.status === 'expired')) {
        window.clearInterval(poll);
        setBusy(false);
        setMessage('That sign-in was not completed. Try again.');
      }
    }, 2000);
  };

  return (
    <div className="ghs-pop-empty">
      <p style={{ fontWeight: 600, color: 'var(--ghs-fg-default)' }}>GitHub Stories</p>
      <p>Sign in with GitHub to see Stories from people you follow.</p>
      {message ? <p className="ghs-pop-err">{message}</p> : null}
      <button className="ghs-pop-btn ghs-pop-btn--primary" onClick={start} disabled={busy}>
        {busy ? 'Waiting for approval…' : 'Continue with GitHub'}
      </button>
      <p className="ghs-pop-muted" style={{ marginTop: 18 }}>
        This never posts to GitHub and never changes who you follow there.
      </p>
    </div>
  );
}

function StoriesTab(): React.ReactElement {
  const [feed, setFeed] = useState<Feed | null>(null);
  const [open, setOpen] = useState<{ groups: AuthorGroup[]; index: number } | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    void (async () => {
      const res = await callBackground({ type: 'ghs:feed/get' });
      if (!res.ok) {
        setError(res.error.message);
        return;
      }
      setFeed(res.data);
    })();
  }, []);

  if (error) return <p className="ghs-pop-err">{error}</p>;
  if (!feed) return <p className="ghs-pop-muted">Loading…</p>;

  const groups = feed.groups ?? [];
  const openAt = (login: string) => {
    const all = feed.me?.items?.length ? [feed.me, ...groups] : groups;
    const index = all.findIndex((g) => g.author.login === login);
    if (index < 0) return;
    void cache.warmCurrent(all[index]!, 0).then(() => setOpen({ groups: all, index }));
  };

  return (
    <>
      <StoryRow
        me={
          feed.me
            ? {
                login: feed.me.author.login,
                avatarUrl: feed.me.author.avatar_url,
                hasStory: (feed.me.items?.length ?? 0) > 0,
              }
            : null
        }
        groups={groups}
        onOpenGroup={openAt}
        onOpenOwn={() => feed.me && openAt(feed.me.author.login)}
        onCreate={() => {
          /* the Post tab owns composing */
        }}
      />
      {groups.length === 0 && !feed.me?.items?.length ? (
        <p className="ghs-pop-empty">
          No Stories right now.
          <br />
          People you follow will show up here.
        </p>
      ) : null}
      {open ? (
        <StoryViewer
          groups={open.groups}
          startGroupIndex={open.index}
          mediaUrl={(storyId, variant) => cache.get(storyId, variant)}
          onClose={() => setOpen(null)}
          onViewed={(storyId) => void callBackground({ type: 'ghs:story/view', storyId })}
          onReply={async (storyId, body) => {
            await callBackground({ type: 'ghs:story/reply', storyId, body });
          }}
          onReact={async (storyId, emoji) => {
            await callBackground({ type: 'ghs:story/react', storyId, emoji });
          }}
        />
      ) : null}
    </>
  );
}

function PostTab({ onPosted }: { onPosted: () => void }): React.ReactElement {
  return (
    <div>
      <p className="ghs-pop-muted">
        Pick a photo or video. It disappears 24 hours after it is published.
      </p>
      <ComposerLoader onPosted={onPosted} />
    </div>
  );
}

// Loaded lazily so opening the popup on the Stories tab does not pay for the
// composer's canvas editing code.
const LazyComposer = React.lazy(async () => {
  const mod = await import('../../src/composer/ComposerBridge.js');
  return { default: mod.ComposerBridge };
});

function ComposerLoader({ onPosted }: { onPosted: () => void }): React.ReactElement {
  return (
    <React.Suspense fallback={<p className="ghs-pop-muted">Loading…</p>}>
      <LazyComposer onClose={onPosted} />
    </React.Suspense>
  );
}

function InboxTab(): React.ReactElement {
  const [inbox, setInbox] = useState<InboxData | null>(null);
  useEffect(() => {
    void (async () => {
      const res = await callBackground({ type: 'ghs:inbox/get' });
      if (res.ok) {
        setInbox(res.data);
        void callBackground({ type: 'ghs:inbox/read', all: true });
      }
    })();
  }, []);
  if (!inbox) return <p className="ghs-pop-muted">Loading…</p>;
  if (!inbox.entries?.length) return <p className="ghs-pop-empty">Nothing here yet.</p>;
  return (
    <Inbox
      entries={inbox.entries}
      resolveThumbUrl={(path) => path}
      onOpenEntry={() => undefined}
      onReply={async (storyId, body) => {
        await callBackground({ type: 'ghs:story/reply', storyId, body });
      }}
    />
  );
}

function SettingsTab({
  me,
  onSignedOut,
}: {
  me: AccountSummary | null;
  onSignedOut: () => void;
}): React.ReactElement {
  return (
    <div>
      {me ? (
        <div className="ghs-pop-row">
          <img src={me.avatarUrl} alt="" width={28} height={28} style={{ borderRadius: '50%' }} />
          <span>
            Signed in as <strong>{me.login}</strong>
          </span>
        </div>
      ) : null}
      <p className="ghs-pop-muted" style={{ marginTop: 12 }}>
        Stories disappear 24 hours after they are published.
      </p>
      <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
        <button
          className="ghs-pop-btn"
          onClick={() => void browserRuntimeOpenOptions()}
        >
          All settings
        </button>
        <button
          className="ghs-pop-btn"
          onClick={async () => {
            await callBackground({ type: 'ghs:session/logout' });
            onSignedOut();
          }}
        >
          Sign out
        </button>
      </div>
    </div>
  );
}

async function browserRuntimeOpenOptions(): Promise<void> {
  const { browser } = await import('wxt/browser');
  await browser.runtime.openOptionsPage();
}
