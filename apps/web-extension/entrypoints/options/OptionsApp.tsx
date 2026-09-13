/**
 * Full settings page: audiences, privacy, sessions, the service URL for
 * self-hosters, and account deletion. All mutations go through the background.
 */
import React, { useCallback, useEffect, useState } from 'react';
import type { Settings, SettingsUpdate } from '@gh-stories/contracts';
import { SettingsPanel } from '@gh-stories/ui';
import { callBackground } from '../../src/messaging/client.js';

export function OptionsApp(): React.ReactElement {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [serviceUrl, setServiceUrl] = useState('');
  const [error, setError] = useState('');
  const [saved, setSaved] = useState('');

  const reload = useCallback(async () => {
    const res = await callBackground({ type: 'ghs:settings/get' });
    if (!res.ok) {
      setError(res.error.message);
      return;
    }
    setSettings(res.data);
    const sess = await callBackground({ type: 'ghs:session/get' });
    if (sess.ok) setServiceUrl(sess.data.serviceUrl ?? '');
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const act = async (fn: () => Promise<unknown>) => {
    await fn();
    await reload();
  };

  /** Maps a numeric GitHub id back to a login, which is how the graph
   *  endpoints address people. Ids are authoritative; logins are renameable,
   *  so we always resolve from the freshest settings payload we have. */
  const loginFor = (githubUserId: number): string | null => {
    const all = [
      ...(settings?.hidden_from ?? []),
      ...(settings?.muted ?? []),
      ...(settings?.blocked ?? []),
    ];
    return all.find((u) => u.github_user_id === githubUserId)?.login ?? null;
  };

  const graph = async (
    action: 'unhide' | 'unmute' | 'unblock',
    githubUserId: number,
  ): Promise<void> => {
    const login = loginFor(githubUserId);
    if (!login) return;
    await act(() => callBackground({ type: 'ghs:graph/action', action, login }));
  };

  return (
    <main
      style={{
        maxWidth: 720,
        margin: '0 auto',
        padding: '32px 20px 80px',
        font: '14px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Inter, sans-serif',
        color: 'var(--ghs-fg-default)',
        background: 'var(--ghs-canvas)',
      }}
    >
      <h1 style={{ fontSize: 22, marginTop: 0 }}>GitHub Stories</h1>
      {error ? <p style={{ color: '#cf222e' }}>{error}</p> : null}

      {settings ? (
        <SettingsPanel
          settings={settings}
          onUpdateDefaults={async (update: SettingsUpdate) =>
            act(() => callBackground({ type: 'ghs:settings/update', update }))
          }
          onCreateAudienceList={async (name) =>
            act(() => callBackground({ type: 'ghs:audience/create', name }))
          }
          onDeleteAudienceList={async (id) =>
            act(() => callBackground({ type: 'ghs:audience/delete', listId: id }))
          }
          onRemoveAudienceListMember={async (listId, githubUserId) =>
            act(() =>
              callBackground({ type: 'ghs:audience/remove-member', listId, githubUserId }),
            )
          }
          onUnhide={(githubUserId) => graph('unhide', githubUserId)}
          onUnmute={(githubUserId) => graph('unmute', githubUserId)}
          onUnblock={(githubUserId) => graph('unblock', githubUserId)}
          onRevokeSession={async (sessionId) =>
            act(() => callBackground({ type: 'ghs:settings/revoke-session', sessionId }))
          }
          onDeleteAccount={async () =>
            act(() => callBackground({ type: 'ghs:settings/delete-account' }))
          }
        />
      ) : (
        <p>Loading…</p>
      )}

      <h2 style={{ fontSize: 16, marginTop: 36 }}>Service</h2>
      <p style={{ color: 'var(--ghs-fg-muted)' }}>
        Point the extension at your own deployment. Leave this alone unless you self-host.
      </p>
      <div style={{ display: 'flex', gap: 8 }}>
        <input
          value={serviceUrl}
          onChange={(e) => setServiceUrl(e.target.value)}
          spellCheck={false}
          style={{
            flex: 1,
            padding: '8px 10px',
            borderRadius: 8,
            border: '1px solid var(--ghs-border)',
            background: 'var(--ghs-canvas)',
            color: 'var(--ghs-fg-default)',
            font: 'inherit',
          }}
        />
        <button
          onClick={async () => {
            const res = await callBackground({
              type: 'ghs:session/set-service-url',
              url: serviceUrl.trim(),
            });
            setSaved(res.ok ? 'Saved. Sign in again to use it.' : '');
            if (!res.ok) setError(res.error.message);
          }}
          style={{
            padding: '8px 16px',
            borderRadius: 8,
            border: '1px solid var(--ghs-border)',
            background: 'var(--ghs-canvas-subtle)',
            color: 'var(--ghs-fg-default)',
            font: 'inherit',
            cursor: 'pointer',
          }}
        >
          Save
        </button>
      </div>
      {saved ? <p style={{ color: 'var(--ghs-fg-muted)' }}>{saved}</p> : null}

      <h2 style={{ fontSize: 16, marginTop: 36 }}>Sessions</h2>
      <button
        onClick={() => act(() => callBackground({ type: 'ghs:settings/sign-out-all' }))}
        style={{
          padding: '8px 16px',
          borderRadius: 8,
          border: '1px solid var(--ghs-border)',
          background: 'var(--ghs-canvas)',
          color: 'var(--ghs-fg-default)',
          font: 'inherit',
          cursor: 'pointer',
        }}
      >
        Sign out of all sessions
      </button>

      <p style={{ marginTop: 40, fontSize: 12, color: 'var(--ghs-fg-muted)' }}>
        An independent project. Not affiliated with GitHub.
      </p>
    </main>
  );
}
