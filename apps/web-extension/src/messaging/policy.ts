/**
 * Per-message sender policy. The github.com content script parses
 * attacker-influenced DOM inside a page shared with a large first-party app,
 * so it only gets the messages it actually needs. Anything that touches the
 * account, the session or the service origin is reserved for our own
 * extension pages (popup / options).
 */
import type { RuntimeRequest } from "./types.js";
import type { SenderKind } from "./sender.js";

const CONTENT_SCRIPT_TYPES: ReadonlySet<string> = new Set([
  "ghs:session/get",
  "ghs:ring/status",
  "ghs:ring/cancel",
  "ghs:feed/get",
  "ghs:stories/mine",
  "ghs:stories/user",
  "ghs:story/get",
  "ghs:story/view",
  "ghs:story/react",
  "ghs:story/reply",
  "ghs:story/report",
  "ghs:story/delete",
  "ghs:media/fetch",
  "ghs:settings/get",
  "ghs:graph/action",
  "ghs:graph/following",
]);

/** Graph actions a content script (profile "Follow on Stories") may take. */
const CONTENT_SCRIPT_GRAPH_ACTIONS: ReadonlySet<string> = new Set(["follow", "unfollow"]);

export function isMessageAllowed(message: RuntimeRequest, kind: SenderKind): boolean {
  if (kind === "extension-page") return true;
  if (!CONTENT_SCRIPT_TYPES.has(message.type)) return false;
  if (message.type === "ghs:graph/action") {
    return CONTENT_SCRIPT_GRAPH_ACTIONS.has(message.action);
  }
  return true;
}
