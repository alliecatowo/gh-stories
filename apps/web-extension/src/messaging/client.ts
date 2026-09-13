/**
 * Thin, typed wrapper around `browser.runtime.sendMessage` for content
 * scripts, the popup and the options page. This module never touches the
 * session token — it only ever talks to the background over messages.
 */
import { browser } from "wxt/browser";
import type { Result, RuntimeRequest, RuntimeResponseDataMap } from "./types.js";

export async function callBackground<T extends RuntimeRequest>(
  request: T,
): Promise<Result<RuntimeResponseDataMap[T["type"]]>> {
  try {
    const response = (await browser.runtime.sendMessage(request)) as
      | Result<RuntimeResponseDataMap[T["type"]]>
      | undefined;
    if (!response) {
      return { ok: false, error: { code: "no_response", message: "The extension background did not respond." } };
    }
    return response;
  } catch (error) {
    return {
      ok: false,
      error: {
        code: "messaging_failed",
        message: error instanceof Error ? error.message : "Could not reach the extension background.",
      },
    };
  }
}
