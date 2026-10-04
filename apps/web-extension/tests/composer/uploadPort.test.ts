import { describe, expect, it } from "vitest";
import { runUploadOverPort, type UploadPortLike } from "../../src/composer/uploadPort.js";
import type { UploadStartMessage } from "../../src/messaging/types.js";

function fakePort() {
  let onMessage: (m: unknown) => void = () => {};
  let onDisconnect: () => void = () => {};
  const port: UploadPortLike & { sent: unknown[]; emit(m: unknown): void; drop(): void } = {
    sent: [],
    onMessage: { addListener: (l) => (onMessage = l) },
    onDisconnect: { addListener: (l) => (onDisconnect = l) },
    postMessage(m) {
      this.sent.push(m);
    },
    disconnect() {},
    emit: (m) => onMessage(m),
    drop: () => onDisconnect(),
  };
  return port;
}
const start = { type: "start" } as unknown as UploadStartMessage;

describe("runUploadOverPort", () => {
  it("reports real progress and resolves on done", async () => {
    const port = fakePort();
    const seen: number[] = [];
    const p = runUploadOverPort(port, start, (n) => seen.push(n));
    port.emit({ type: "progress", phase: "uploading", loaded: 50, total: 200 });
    port.emit({ type: "done", story: {} });
    await p;
    expect(seen).toEqual([25]);
  });

  it("rejects with the service's own message", async () => {
    const port = fakePort();
    const p = runUploadOverPort(port, start, () => {});
    port.emit({ type: "error", error: { code: "x", message: "That file is too large." } });
    await expect(p).rejects.toThrow("That file is too large.");
  });

  it("rejects instead of hanging when the background disconnects", async () => {
    const port = fakePort();
    const p = runUploadOverPort(port, start, () => {});
    port.drop();
    await expect(p).rejects.toThrow(/interrupted/);
  });
});
