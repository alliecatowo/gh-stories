# Security Policy

## Reporting a vulnerability

Please report privately through GitHub's
[private vulnerability reporting](https://github.com/alliecatowo/gh-stories/security/advisories/new)
for this repository. Do not open a public issue, and do not include other
people's private media or messages in a report.

Expect an acknowledgement within 7 days. This is a small independent project
run by one person; there is no paid bug bounty.

## Scope

In scope: the API and worker in `apps/`, the CLI in `cli/`, the browser
extension, the media authorization gateway, and the published release
artifacts.

Particularly interesting: anything that lets one account obtain another
account's private media, ring status, viewer list, or inbox; anything that
bypasses 24-hour expiry, a block, a hide rule, or an audience narrowing;
anything that makes the media worker execute attacker-controlled input; and
any terminal escape sequence that survives into a user's terminal.

## What the product does and does not protect

- Media lives in a **private** bucket. Every request — including thumbnails and
  video range requests — is checked against session, audience, block, hide,
  suspension, deletion and expiry. There are no public media URLs.
- Expiry is enforced on every read and write, independently of background
  cleanup. Restarting a worker or database does not revive an expired Story.
- Logical expiry is exact. Physical object deletion is an operational target
  (within 15 minutes under healthy conditions), with retries.
- **Pixels already delivered to someone's device cannot be recalled.** A
  viewer that has already downloaded an image can keep it. We do not claim
  otherwise.
- Browser extension storage is not an OS secrets vault. Session tokens are kept
  in trusted extension contexts and never in synchronized storage, but a user
  with local access to the profile can read them.
