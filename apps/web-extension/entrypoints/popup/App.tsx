/**
 * The independent toolbar entry.
 *
 * This must keep working even when the GitHub page integration breaks
 * entirely: it talks only to the background, never to a content script, and it
 * never assumes a github.com tab is open.
 */
import React, { useCallback, useEffect, useRef, useState } from 'react';
import type { AuthorGroup, Feed, Inbox as InboxData } from '@gh-stories/contracts';
import type { AccountSummary } from '../../src/messaging/types.js';
import { StoryRow, Inbox } from '@gh-stories/ui';
import { callBackground } from '../../src/messaging/client.js';
import { MediaPathCache } from '../../src/state/mediaCache.js';
import { ViewerBridge } from '../../src/viewer/ViewerBridge.js';

type Tab = 'stories' | 'post' | 'inbox' | 'settings';

export function App(): React.ReactElement {
  const [tab, setTab] = useState<Tab>('stories');
  const [me, setMe] = useState<AccountSummary | null>(null);
  const [signedIn, setSignedIn] = useState<boolean | null>(null);
  const [error, setError] = useState<string>('');
  const [unreadInbox, setUnreadInbox] = useState(0);

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
  }, [loadSession]);

  // The unread count comes from the inbox itself; the session summary is
  // deliberately cheap and does not make a network call for it.
  useEffect(() => {
    if (!signedIn) return;
    void callBackground({ type: 'ghs:inbox/get' }).then((res) => {
      if (res.ok) setUnreadInbox(res.data.unread ?? 0);
    });
  }, [signedIn]);

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
            {t === 'inbox' && unreadInbox > 0 ? ` (${unreadInbox})` : ''}
          </button>
        ))}
      </div>
      <div className="ghs-pop-body">
        {tab === 'stories' && <StoriesTab onCreate={() => setTab('post')} />}
        {tab === 'post' && <PostTab onPosted={loadSession} />}
        {tab === 'inbox' && <InboxTab onUnreadChange={setUnreadInbox} />}
        {tab === 'settings' && <SettingsTab me={me} onSignedOut={loadSession} />}
      </div>
    </>
  );
}

function SignIn({ onDone, error }: { onDone: () => void; error: string }): React.ReactElement {
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState(error);
  const [userCode, setUserCode] = useState('');
  const timer = useRef<number | undefined>(undefined);
  const mounted = useRef(true);

  // Poll for the result of a login the background is already driving. The
  // background owns the real polling; this only refreshes the popup's view,
  // so closing and reopening the popup keeps showing the pending state.
  const watch = useCallback(
    (intervalMs: number) => {
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(async () => {
        const p = await callBackground({ type: 'ghs:session/login-poll' });
        if (!mounted.current) return;
        if (p.ok && p.data.status === 'approved') {
          setBusy(false);
          onDone();
        } else if (p.ok && (p.data.status === 'denied' || p.data.status === 'expired')) {
          setBusy(false);
          setMessage('That sign-in was not completed. Try again.');
        } else {
          watch((p.ok ? (p.data.intervalSeconds ?? 2) : 2) * 1000);
        }
      }, intervalMs);
    },
    [onDone],
  );

  useEffect(() => {
    mounted.current = true;
    void callBackground({ type: 'ghs:session/login-poll' }).then((p) => {
      if (!mounted.current || !p.ok) return;
      if (p.data.status === 'pending') {
        setBusy(true);
        setUserCode(p.data.userCode ?? '');
        watch((p.data.intervalSeconds ?? 2) * 1000);
      } else if (p.data.status === 'approved') {
        onDone();
      }
    });
    return () => {
      mounted.current = false;
      window.clearTimeout(timer.current);
    };
  }, [watch, onDone]);

  const start = async () => {
    setBusy(true);
    setMessage('');
    const res = await callBackground({ type: 'ghs:session/login-start' });
    if (!res.ok) {
      setMessage(res.error.message);
      setBusy(false);
      return;
    }
    setUserCode(res.data.userCode ?? '');
    // The background opens the service's authorization page and receives the
    // Stories token there. GitHub credentials never touch this context.
    watch((res.data.intervalSeconds ?? 2) * 1000);
  };

  return (
    <div className="ghs-pop-empty">
      <p style={{ fontWeight: 600, color: 'var(--ghs-fg-default)' }}>GitHub Stories</p>
      <p>Sign in with GitHub to see Stories from people you follow.</p>
      {message ? <p className="ghs-pop-err">{message}</p> : null}
      <button className="ghs-pop-btn ghs-pop-btn--primary" onClick={start} disabled={busy}>
        {busy ? 'Waiting for approval…' : 'Continue with GitHub'}
      </button>
      {busy && userCode ? <p className="ghs-pop-muted">Confirm the code {userCode} in the tab that opened.</p> : null}
      <p className="ghs-pop-muted" style={{ marginTop: 18 }}>
        This never posts to GitHub and never changes who you follow there.
      </p>
    </div>
  );
}

function StoriesTab({ onCreate }: { onCreate: () => void }): React.ReactElement {
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
    setOpen({ groups: all, index });
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
        onCreate={onCreate}
      />
      {groups.length === 0 && !feed.me?.items?.length ? (
        <p className="ghs-pop-empty">
          No Stories right now.
          <br />
          People you follow will show up here.
        </p>
      ) : null}
      {open ? (
        <ViewerBridge groups={open.groups} startGroupIndex={open.index} onClose={() => setOpen(null)} />
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

function InboxTab({ onUnreadChange }: { onUnreadChange: (count: number) => void }): React.ReactElement {
  const [inbox, setInbox] = useState<InboxData | null>(null);
  const [error, setError] = useState('');
  const [, setThumbRevision] = useState(0);
  const thumbsRef = useRef<MediaPathCache | null>(null);
  if (!thumbsRef.current) thumbsRef.current = new MediaPathCache();
  const thumbs = thumbsRef.current;

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const res = await callBackground({ type: 'ghs:inbox/get' });
      if (cancelled) return;
      if (!res.ok) {
        setError(res.error.message);
        return;
      }
      setInbox(res.data);
      // The thumbnail path is behind the authorization gateway, so it is
      // fetched through the background and shown as a blob URL.
      for (const entry of res.data.entries ?? []) {
        if (entry.story_expired || !entry.story_thumb_url) continue;
        void thumbs.prefetch(entry.story_thumb_url).then(() => {
          if (!cancelled) setThumbRevision((n) => n + 1);
        });
      }
    })();
    return () => {
      cancelled = true;
      thumbs.revokeAll();
    };
  }, [thumbs]);

  const unread = inbox?.unread;
  useEffect(() => {
    if (unread !== undefined) onUnreadChange(unread);
  }, [unread, onUnreadChange]);

  const markRead = async (id: string) => {
    const res = await callBackground({ type: 'ghs:inbox/read', eventIds: [id] });
    if (!res.ok) return;
    setInbox((prev) => {
      if (!prev) return prev;
      let cleared = false;
      const entries = (prev.entries ?? []).map((entry) => {
        if (entry.id !== id || entry.read_at) return entry;
        cleared = true;
        return { ...entry, read_at: new Date().toISOString() };
      });
      return { ...prev, entries, unread: cleared ? Math.max(0, (prev.unread ?? 1) - 1) : prev.unread };
    });
  };

  if (error) return <p className="ghs-pop-err">{error}</p>;
  if (!inbox) return <p className="ghs-pop-muted">Loading…</p>;
  if (!inbox.entries?.length) return <p className="ghs-pop-empty">Nothing here yet.</p>;
  // No onReply: the service forbids replying to a reply on your own Story and
  // has no thread-reply endpoint yet, so the Inbox is read-only.
  return (
    <Inbox
      entries={inbox.entries}
      resolveThumbUrl={(path) => thumbs.resolve(path)}
      onOpenEntry={(id) => void markRead(id)}
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
