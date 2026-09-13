/**
 * Shared, pure identity extraction for every GitHub-surface adapter. Built
 * around semantic structure (`a[href^="/"]` profile links, `img.avatar`,
 * `data-hovercard-type`) rather than brittle presentational class names, so
 * it keeps working across GitHub's routine markup churn.
 *
 * Every function here is pure and DOM-only — no network calls, no
 * extension APIs — so it can be unit tested against plain HTML fixtures.
 */

export interface AccountIdentity {
  login: string;
  /** Present only when the avatar URL exposes it
   * (`avatars.githubusercontent.com/u/<id>?...`). Ring-status lookups fall
   * back to `login` when this is absent. */
  githubUserId?: number;
  avatarUrl: string;
  /** The GitHub-owned profile link. Never modified in place beyond a
   * `position` style needed to sit inside the reserved ring slot — its
   * href, listeners and hovercard attributes are left completely alone. */
  anchor: HTMLAnchorElement;
  avatarImg: HTMLImageElement;
}

const AVATAR_ID_PATTERN = /\/u\/(\d+)(?:[?/]|$)/;
const SINGLE_SEGMENT_LOGIN = /^\/([A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)\/?$/;

function isBotLogin(login: string): boolean {
  return /\[bot\]$/i.test(login);
}

/** `data-hovercard-type` is GitHub's own signal for what kind of entity an
 * avatar link represents (`"user"`, `"organization"`, `"team"`, ...). We
 * only ever decorate `"user"` — everything else (including values we don't
 * recognize) is skipped rather than assumed safe. */
function hovercardType(img: HTMLImageElement, anchor: Element | null): string | null {
  return img.getAttribute("data-hovercard-type") ?? anchor?.getAttribute("data-hovercard-type") ?? null;
}

/**
 * Extracts an `AccountIdentity` from a candidate `<img class="avatar">`.
 * Returns `null` for anything that is not confidently a single real GitHub
 * user: organizations/teams (via `data-hovercard-type`), bots (`[bot]`
 * logins, `/apps/*` profile links), the anonymized "ghost" account, and any
 * avatar without a resolvable single-segment profile link.
 */
export function extractIdentity(avatarImg: HTMLImageElement): AccountIdentity | null {
  if (!avatarImg.classList.contains("avatar")) return null;

  const anchor = avatarImg.closest("a[href]");
  if (!(anchor instanceof HTMLAnchorElement)) return null;

  const type = hovercardType(avatarImg, anchor);
  if (type && type !== "user") return null;

  const href = anchor.getAttribute("href") ?? "";
  if (href.startsWith("/apps/")) return null; // GitHub Apps (bots)

  const pathOnly = href.split("?")[0]?.split("#")[0] ?? "";
  const match = SINGLE_SEGMENT_LOGIN.exec(pathOnly);
  if (!match) return null;

  const login = decodeURIComponent(match[1]);
  if (login.toLowerCase() === "ghost") return null;
  if (isBotLogin(login)) return null;

  const src = avatarImg.currentSrc || avatarImg.src || avatarImg.getAttribute("src") || "";
  if (!src) return null;

  const idMatch = AVATAR_ID_PATTERN.exec(src);
  const githubUserId = idMatch ? Number(idMatch[1]) : undefined;

  return { login, githubUserId, avatarUrl: src, anchor, avatarImg };
}

/** Scans `root` for every `img.avatar` matching `selector` and returns the
 * resolved identities, silently skipping anything `extractIdentity` rejects
 * (organizations, bots, ghost, unresolvable links). Order matches document
 * order. */
export function collectIdentities(root: ParentNode, selector: string): AccountIdentity[] {
  const identities: AccountIdentity[] = [];
  for (const img of root.querySelectorAll<HTMLImageElement>(selector)) {
    const identity = extractIdentity(img);
    if (identity) identities.push(identity);
  }
  return identities;
}
