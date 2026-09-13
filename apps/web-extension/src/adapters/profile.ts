/**
 * Profile page adapter: finds the page owner's login and a stable insertion
 * point for a "view/follow on Stories" affordance, without depending on
 * exact Primer class names.
 */
const PROFILE_HEADER_SELECTORS = ['[data-testid="profile-header"]', ".js-profile-editable-area", ".vcard-names-container"];
const LOGIN_SELECTORS = ['[data-testid="profile-login"]', ".p-nickname", "[itemprop='additionalName']"];

export interface ProfilePageInfo {
  login: string;
  container: Element;
}

/** Only matches when the current document is actually a profile page and a
 * login can be confidently read from it — never guesses from the URL alone,
 * since `/settings`, `/notifications`, etc. also live at a single path
 * segment. */
export function findProfilePage(root: ParentNode = document): ProfilePageInfo | null {
  let container: Element | null = null;
  for (const selector of PROFILE_HEADER_SELECTORS) {
    container = root.querySelector(selector);
    if (container) break;
  }
  if (!container) return null;

  let login: string | null = null;
  for (const selector of LOGIN_SELECTORS) {
    const el = container.querySelector(selector);
    const text = el?.textContent?.trim();
    if (text) {
      login = text.replace(/^@/, "");
      break;
    }
  }
  if (!login) return null;

  return { login, container };
}
