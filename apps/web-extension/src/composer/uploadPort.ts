/**
 * Drives one upload over the background's upload port and settles exactly
 * once: resolves on `done`, rejects with the service's own (user-safe)
 * message on `error`, and rejects if the background goes away mid-upload
 * (service worker restart, extension reload) instead of hanging forever.
 */
import type { UploadServerMessage, UploadStartMessage } from "../messaging/types.js";

export interface UploadPortLike {
  onMessage: { addListener(listener: (message: unknown) => void): void };
  onDisconnect: { addListener(listener: () => void): void };
  postMessage(message: unknown): void;
  disconnect(): void;
}

export function runUploadOverPort(
  port: UploadPortLike,
  start: UploadStartMessage,
  onProgress: (percent: number) => void,
): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    let settled = false;
    const finish = (action: () => void) => {
      if (settled) return;
      settled = true;
      try {
        port.disconnect();
      } catch {
        /* already closed */
      }
      action();
    };
    port.onMessage.addListener((raw) => {
      const msg = raw as UploadServerMessage;
      if (msg.type === "progress") {
        if (msg.total > 0) onProgress((msg.loaded / msg.total) * 100);
      } else if (msg.type === "done") {
        finish(resolve);
      } else if (msg.type === "error") {
        finish(() => reject(new Error(msg.error.message || "The upload failed.")));
      } else if (msg.type === "cancelled") {
        finish(() => reject(new Error("Upload cancelled.")));
      }
    });
    port.onDisconnect.addListener(() => {
      if (settled) return;
      settled = true;
      reject(new Error("The extension was interrupted before the upload finished. Try again."));
    });
    port.postMessage(start);
  });
}
