# Self-hosting runbook

## Moderation

Moderator access is granted **only** by configuration, never from inside the
product. There is deliberately no "make me a moderator" path: a compromised
account cannot promote itself, and you can see who has access by reading the
deployment.

```bash
# GitHub NUMERIC ids, comma separated. Numeric because logins are renameable.
GHS_MODERATOR_GITHUB_IDS=98134425,4242
```

Find an id with:

```bash
curl -s https://api.github.com/users/USERNAME | jq .id
```

The list is applied at startup, and it is authoritative in both directions:
anyone removed from it is demoted the next time the service starts.

Moderators can then reach `/account/moderation` on the service, and the
`/v1/moderation/*` endpoints. Everyone else gets a 404 from those routes —
not a 403 — so the queue's existence is not advertised.

Taking an action (removing a Story, suspending an account) automatically
resolves every open report that action addresses, so the queue reflects
outstanding work rather than work already done.

### What a moderator can do

| Action | Effect |
|---|---|
| `remove_story` | The Story stops being served immediately; its files are scheduled for physical deletion. |
| `suspend_user` | The account can neither act nor be seen. Their Stories stop being served. |
| `unsuspend_user` | Reverses a suspension. |
| `dismiss_report` | Closes a report without acting on it. |

Every action is recorded with the moderator, the target and a reason. Reported
evidence is reachable only by moderators; a reporter cannot read back someone
else's private Story through the reporting flow.
