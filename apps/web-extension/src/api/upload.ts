/**
 * Composer upload flow, run entirely in the background: request an upload
 * authorization, PUT the bytes directly to the presigned URL with real
 * progress tracking and a working cancel, finalize, then poll until the
 * Story is `published` or `failed`. A retry after a dropped connection
 * reuses the same `Idempotency-Key` (the caller is responsible for keeping
 * one key per logical draft across retries) so it can never create a
 * duplicate Story.
 */
import type { Browser } from "wxt/browser";
import type { ApiClient } from "./client.js";
import { ApiClientError } from "./errors.js";
import type { UploadStartMessage, UploadServerMessage } from "../messaging/types.js";

const POLL_INTERVAL_MS = 1500;
const POLL_TIMEOUT_MS = 2 * 60_000;

async function sha256Hex(bytes: ArrayBuffer): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

/** Feature-detects a service worker that can stream a `ReadableStream`
 * request body (Chrome/Edge). Firefox's `fetch` does not reliably support
 * streaming upload bodies, so it falls back to a single non-streamed PUT —
 * still real (it reflects actual completion), just not incremental. */
function supportsStreamingUploadBody(): boolean {
  try {
    // Constructing the Request is enough to detect throwing implementations;
    // it is never sent.
    new Request("https://example.invalid/", {
      method: "POST",
      body: new ReadableStream(),
      // @ts-expect-error -- `duplex` is required by spec for streaming
      // bodies but not yet in all TS DOM lib versions.
      duplex: "half",
    });
    return true;
  } catch {
    return false;
  }
}

async function putWithProgress(
  url: string,
  headers: Record<string, string> | undefined,
  bytes: ArrayBuffer,
  signal: AbortSignal,
  onProgress: (loaded: number, total: number) => void,
): Promise<void> {
  const total = bytes.byteLength;

  if (supportsStreamingUploadBody()) {
    let loaded = 0;
    const chunkSize = 256 * 1024;
    const source = new Uint8Array(bytes);
    const stream = new ReadableStream<Uint8Array>({
      pull(controller) {
        if (loaded >= total) {
          controller.close();
          return;
        }
        const end = Math.min(loaded + chunkSize, total);
        controller.enqueue(source.subarray(loaded, end));
        loaded = end;
        onProgress(loaded, total);
      },
    });
    try {
      const response = await fetch(url, {
        method: "PUT",
        headers,
        body: stream,
        // @ts-expect-error -- required for streaming request bodies.
        duplex: "half",
        signal,
      });
      if (!response.ok) {
        throw new ApiClientError({ code: `http_${response.status}`, message: "Upload failed." });
      }
      return;
    } catch (error) {
      if (signal.aborted) throw new ApiClientError({ code: "cancelled", message: "Upload cancelled." });
      // Fall through to the non-streaming path below if the streaming PUT
      // itself failed for a reason unrelated to cancellation or an HTTP
      // error we've already classified (e.g. an environment that accepted
      // construction but rejects the stream at send time).
      if (error instanceof ApiClientError) throw error;
    }
  }

  onProgress(0, total);
  const response = await fetch(url, { method: "PUT", headers, body: bytes, signal });
  if (!response.ok) {
    throw new ApiClientError({ code: `http_${response.status}`, message: "Upload failed." });
  }
  onProgress(total, total);
}

export async function runUploadFlow(
  port: Browser.runtime.Port,
  message: UploadStartMessage,
  apiClient: ApiClient,
): Promise<void> {
  const controller = new AbortController();
  let cancelled = false;
  const onPortMessage = (raw: unknown) => {
    if (typeof raw === "object" && raw !== null && (raw as Record<string, unknown>).type === "cancel") {
      cancelled = true;
      controller.abort();
    }
  };
  port.onMessage.addListener(onPortMessage);

  const send = (msg: UploadServerMessage) => {
    try {
      port.postMessage(msg);
    } catch {
      // Port already disconnected (popup closed); the upload itself keeps
      // running to completion so a retry is never needed for something
      // that actually succeeded, but there's no one left to notify.
    }
  };

  try {
    const intent = await apiClient.createUpload(
      {
        mime: message.mime,
        byteSize: message.byteSize,
        filename: message.filename,
        caption: message.caption,
        altText: message.altText,
        visibility: message.visibility,
        audienceListId: message.audienceListId,
        allowReplies: message.allowReplies,
        allowReactions: message.allowReactions,
      },
      message.idempotencyKey,
      controller.signal,
    );

    send({ type: "progress", phase: "uploading", loaded: 0, total: message.byteSize });
    await putWithProgress(intent.url, intent.headers, message.bytes, controller.signal, (loaded, total) => {
      send({ type: "progress", phase: "uploading", loaded, total });
    });

    if (cancelled) {
      send({ type: "cancelled" });
      return;
    }

    const checksum = await sha256Hex(message.bytes);
    send({ type: "phase", phase: "processing" });
    const story = await apiClient.finalizeUpload(
      intent.upload_id,
      message.byteSize,
      checksum,
      message.idempotencyKey,
      controller.signal,
    );

    const finalStory = await pollUntilSettled(apiClient, story.id, controller.signal);
    if (cancelled) {
      send({ type: "cancelled" });
      return;
    }
    send({ type: "done", story: finalStory });
  } catch (error) {
    if (cancelled) {
      send({ type: "cancelled" });
      return;
    }
    const wire = error instanceof ApiClientError ? error.toWireError() : { code: "upload_failed", message: "Something went wrong while posting." };
    send({ type: "error", error: wire });
  } finally {
    port.onMessage.removeListener(onPortMessage);
  }
}

async function pollUntilSettled(apiClient: ApiClient, storyId: string | undefined, signal: AbortSignal) {
  if (!storyId) throw new ApiClientError({ code: "malformed_response", message: "Upload did not return a Story id." });
  const deadline = Date.now() + POLL_TIMEOUT_MS;
  for (;;) {
    const story = await apiClient.getStory(storyId, signal);
    if (story.state === "published" || story.state === "failed" || story.state === "deleted" || story.state === "removed") {
      return story;
    }
    if (Date.now() > deadline) {
      throw new ApiClientError({ code: "processing_timeout", message: "Still processing — check back in a moment." });
    }
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(resolve, POLL_INTERVAL_MS);
      signal.addEventListener(
        "abort",
        () => {
          clearTimeout(timer);
          reject(new ApiClientError({ code: "cancelled", message: "Cancelled." }));
        },
        { once: true },
      );
    });
  }
}
