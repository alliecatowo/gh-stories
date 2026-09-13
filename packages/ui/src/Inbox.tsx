import { useState } from "react";
import type { InboxEntry } from "@gh-stories/contracts";
import "./Inbox.css";

export interface InboxProps {
  entries: InboxEntry[];
  /** Resolves an authorization-gateway thumbnail path to a fetchable URL. */
  resolveThumbUrl: (path: string) => string;
  /** Called when an entry is opened; the host is responsible for marking it
   * read server-side and passing back updated `entries`. */
  onOpenEntry: (id: string) => void;
  onReply: (storyId: string, body: string) => Promise<void>;
}

function summaryFor(entry: InboxEntry): string {
  const login = entry.actor?.login ?? "someone";
  switch (entry.kind) {
    case "reaction":
      return `${login} reacted ${entry.emoji ?? ""}`.trim();
    case "follow":
      return `${login} started following you`;
    case "reply":
    default:
      return `${login} replied`;
  }
}

/** Private inbox: replies, reactions and new followers, with a reply
 * composer for the selected thread. Every body is rendered as plain text. */
export function Inbox(props: InboxProps): React.JSX.Element {
  const { entries, resolveThumbUrl, onOpenEntry, onReply } = props;
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const selected = entries.find((entry) => entry.id === selectedId) ?? null;

  function select(entry: InboxEntry) {
    if (!entry.id) return;
    setSelectedId(entry.id);
    setDraft("");
    setError(null);
    onOpenEntry(entry.id);
  }

  async function submitReply() {
    if (!selected?.story_id || draft.trim() === "") return;
    setSending(true);
    setError(null);
    try {
      await onReply(selected.story_id, draft.trim());
      setDraft("");
    } catch {
      setError("Couldn't send. Try again.");
    } finally {
      setSending(false);
    }
  }

  return (
    <div className="ghs-root ghs-inbox">
      <ul className="ghs-inbox__list" role="list">
        {entries.length === 0 ? (
          <li className="ghs-inbox__empty">Nothing here yet.</li>
        ) : (
          entries.map((entry) => {
            const unread = !entry.read_at;
            const thumbUrl = entry.story_expired ? undefined : entry.story_thumb_url;
            return (
              <li key={entry.id ?? summaryFor(entry)}>
                <button
                  type="button"
                  className="ghs-inbox__row"
                  data-unread={unread || undefined}
                  data-selected={entry.id === selectedId || undefined}
                  onClick={() => select(entry)}
                >
                  <span className="ghs-inbox__thumb" aria-hidden="true">
                    {thumbUrl ? (
                      <img src={resolveThumbUrl(thumbUrl)} alt="" />
                    ) : entry.story_id ? (
                      <span className="ghs-inbox__thumb-expired">Expired</span>
                    ) : null}
                  </span>
                  <span className="ghs-inbox__body">
                    <span className="ghs-inbox__summary">{summaryFor(entry)}</span>
                    {entry.kind === "reply" && entry.body ? (
                      <span className="ghs-inbox__preview">{entry.body}</span>
                    ) : null}
                  </span>
                  {unread ? (
                    <span className="ghs-inbox__dot" aria-label="Unread" />
                  ) : null}
                </button>
              </li>
            );
          })
        )}
      </ul>

      {selected ? (
        <div className="ghs-inbox__thread">
          <div className="ghs-inbox__thread-header">
            <strong>{selected.actor?.login ?? "Unknown"}</strong>
            {selected.story_expired ? (
              <span className="ghs-inbox__thread-expired">Story expired</span>
            ) : null}
          </div>
          {selected.kind === "reply" && selected.body ? (
            <p className="ghs-inbox__thread-body">{selected.body}</p>
          ) : null}

          {selected.story_id && !selected.story_expired ? (
            <form
              className="ghs-inbox__composer"
              onSubmit={(event) => {
                event.preventDefault();
                void submitReply();
              }}
            >
              <label className="ghs-visually-hidden" htmlFor="ghs-inbox-reply">
                Reply
              </label>
              <input
                id="ghs-inbox-reply"
                type="text"
                value={draft}
                onChange={(event) => setDraft(event.target.value)}
                placeholder="Send a reply"
                disabled={sending}
              />
              <button type="submit" disabled={sending || draft.trim() === ""}>
                {sending ? "Sending…" : "Send"}
              </button>
            </form>
          ) : selected.story_id ? (
            <p className="ghs-inbox__thread-expired-note">
              This Story expired — you can no longer reply here.
            </p>
          ) : null}
          {error ? <p className="ghs-inbox__error">{error}</p> : null}
        </div>
      ) : null}
    </div>
  );
}
