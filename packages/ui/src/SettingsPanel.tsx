import { useState } from "react";
import {
  VISIBILITY_LABELS,
  type PublicUser,
  type Settings,
  type SettingsUpdate,
  type Visibility,
} from "@gh-stories/contracts";
import "./SettingsPanel.css";

export interface SettingsPanelProps {
  settings: Settings;
  onUpdateDefaults: (update: SettingsUpdate) => Promise<void>;
  onCreateAudienceList: (name: string) => Promise<void>;
  onDeleteAudienceList: (id: string) => Promise<void>;
  onRemoveAudienceListMember: (listId: string, githubUserId: number) => Promise<void>;
  onUnhide: (githubUserId: number) => Promise<void>;
  onUnmute: (githubUserId: number) => Promise<void>;
  onUnblock: (githubUserId: number) => Promise<void>;
  onRevokeSession: (sessionId: string) => Promise<void>;
  onDeleteAccount: () => Promise<void>;
}

const VISIBILITY_OPTIONS: Visibility[] = [
  "followers_of_author",
  "author_follows",
  "mutuals",
  "custom_list",
  "public",
];

function PersonRow(props: { user: PublicUser; actionLabel: string; onAction: () => void }) {
  return (
    <li className="ghs-settings__person">
      <img src={props.user.avatar_url} alt="" width={24} height={24} />
      <span>{props.user.login}</span>
      <button type="button" onClick={props.onAction}>
        {props.actionLabel}
      </button>
    </li>
  );
}

/** Pure-presentation settings surface. Every mutation goes through a prop
 * callback — this component never calls the API directly. */
export function SettingsPanel(props: SettingsPanelProps): React.JSX.Element {
  const { settings } = props;
  const [newListName, setNewListName] = useState("");
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [deleteText, setDeleteText] = useState("");

  return (
    <div className="ghs-root ghs-settings">
      <section className="ghs-settings__section">
        <h2>Defaults for new Stories</h2>
        <label className="ghs-settings__field">
          <span>Default audience</span>
          <select
            value={settings.default_visibility ?? "followers_of_author"}
            onChange={(event) =>
              void props.onUpdateDefaults({
                default_visibility: event.target.value as Visibility,
              })
            }
          >
            {VISIBILITY_OPTIONS.map((visibility) => (
              <option key={visibility} value={visibility}>
                {VISIBILITY_LABELS[visibility]}
              </option>
            ))}
          </select>
        </label>
        <label className="ghs-settings__checkbox">
          <input
            type="checkbox"
            checked={settings.default_allow_replies ?? true}
            onChange={(event) =>
              void props.onUpdateDefaults({ default_allow_replies: event.target.checked })
            }
          />
          <span>Allow replies by default</span>
        </label>
        <label className="ghs-settings__checkbox">
          <input
            type="checkbox"
            checked={settings.default_allow_reactions ?? true}
            onChange={(event) =>
              void props.onUpdateDefaults({ default_allow_reactions: event.target.checked })
            }
          />
          <span>Allow reactions by default</span>
        </label>
      </section>

      <section className="ghs-settings__section">
        <h2>Audience lists</h2>
        <ul className="ghs-settings__lists">
          {(settings.audience_lists ?? []).map((list) => (
            <li key={list.id} className="ghs-settings__list">
              <div className="ghs-settings__list-header">
                <strong>{list.name}</strong>
                <span>{list.member_count ?? list.members?.length ?? 0} people</span>
                <button
                  type="button"
                  onClick={() => list.id && void props.onDeleteAudienceList(list.id)}
                >
                  Delete list
                </button>
              </div>
              {list.members && list.members.length > 0 ? (
                <ul className="ghs-settings__people">
                  {list.members.map((member) => (
                    <PersonRow
                      key={member.github_user_id}
                      user={member}
                      actionLabel="Remove"
                      onAction={() =>
                        list.id &&
                        void props.onRemoveAudienceListMember(list.id, member.github_user_id)
                      }
                    />
                  ))}
                </ul>
              ) : null}
            </li>
          ))}
        </ul>
        <form
          className="ghs-settings__new-list"
          onSubmit={(event) => {
            event.preventDefault();
            if (newListName.trim() === "") return;
            void props.onCreateAudienceList(newListName.trim());
            setNewListName("");
          }}
        >
          <input
            type="text"
            value={newListName}
            onChange={(event) => setNewListName(event.target.value)}
            placeholder="New list name"
          />
          <button type="submit" disabled={newListName.trim() === ""}>
            Create list
          </button>
        </form>
      </section>

      <section className="ghs-settings__section">
        <h2>Hidden from</h2>
        <ul className="ghs-settings__people">
          {(settings.hidden_from ?? []).map((user) => (
            <PersonRow
              key={user.github_user_id}
              user={user}
              actionLabel="Unhide"
              onAction={() => void props.onUnhide(user.github_user_id)}
            />
          ))}
          {(settings.hidden_from ?? []).length === 0 ? <li>Nobody is hidden.</li> : null}
        </ul>
      </section>

      <section className="ghs-settings__section">
        <h2>Muted</h2>
        <ul className="ghs-settings__people">
          {(settings.muted ?? []).map((user) => (
            <PersonRow
              key={user.github_user_id}
              user={user}
              actionLabel="Unmute"
              onAction={() => void props.onUnmute(user.github_user_id)}
            />
          ))}
          {(settings.muted ?? []).length === 0 ? <li>Nobody is muted.</li> : null}
        </ul>
      </section>

      <section className="ghs-settings__section">
        <h2>Blocked</h2>
        <ul className="ghs-settings__people">
          {(settings.blocked ?? []).map((user) => (
            <PersonRow
              key={user.github_user_id}
              user={user}
              actionLabel="Unblock"
              onAction={() => void props.onUnblock(user.github_user_id)}
            />
          ))}
          {(settings.blocked ?? []).length === 0 ? <li>Nobody is blocked.</li> : null}
        </ul>
      </section>

      <section className="ghs-settings__section">
        <h2>Active sessions</h2>
        <ul className="ghs-settings__sessions">
          {(settings.sessions ?? []).map((session) => (
            <li key={session.id} className="ghs-settings__session">
              <div>
                <strong>{session.client_label ?? session.client_kind ?? "Session"}</strong>
                {session.current ? <span className="ghs-settings__current">This device</span> : null}
                <div className="ghs-settings__session-meta">
                  Last used {session.last_used_at ? new Date(session.last_used_at).toLocaleString() : "—"}
                </div>
              </div>
              <button
                type="button"
                disabled={session.current}
                onClick={() => session.id && void props.onRevokeSession(session.id)}
              >
                Revoke
              </button>
            </li>
          ))}
        </ul>
      </section>

      <section className="ghs-settings__section ghs-settings__danger">
        <h2>Delete account</h2>
        <p>This permanently deletes your Stories account and everything in it.</p>
        {!confirmingDelete ? (
          <button type="button" onClick={() => setConfirmingDelete(true)}>
            Delete account…
          </button>
        ) : (
          <div className="ghs-settings__confirm">
            <label>
              Type <strong>delete</strong> to confirm
              <input value={deleteText} onChange={(event) => setDeleteText(event.target.value)} />
            </label>
            <div className="ghs-settings__confirm-actions">
              <button type="button" onClick={() => setConfirmingDelete(false)}>
                Cancel
              </button>
              <button
                type="button"
                disabled={deleteText.trim().toLowerCase() !== "delete"}
                onClick={() => void props.onDeleteAccount()}
              >
                Permanently delete
              </button>
            </div>
          </div>
        )}
      </section>
    </div>
  );
}
