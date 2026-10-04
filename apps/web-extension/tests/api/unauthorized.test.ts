import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "../../src/api/client.js";

afterEach(() => vi.unstubAllGlobals());

describe("ApiClient 401 handling", () => {
  it("reports the rejected token so the account can be dropped", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 401 })));
    const onUnauthorized = vi.fn();
    const client = new ApiClient({
      getToken: async () => "tok-123",
      getServiceOrigin: async () => "https://svc.example",
      onUnauthorized,
    });
    await expect(client.mine()).rejects.toMatchObject({ code: "unauthenticated" });
    expect(onUnauthorized).toHaveBeenCalledWith("tok-123");
  });

  it("does not fire for a missing token or a non-401", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 500 })));
    const onUnauthorized = vi.fn();
    const client = new ApiClient({ getToken: async () => "t", getServiceOrigin: async () => "https://svc.example", onUnauthorized });
    await expect(client.mine()).rejects.toBeTruthy();
    const none = new ApiClient({ getToken: async () => null, getServiceOrigin: async () => "https://svc.example", onUnauthorized });
    await expect(none.mine()).rejects.toMatchObject({ code: "unauthenticated" });
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it("finds a followed user across pages", async () => {
    const pages = [
      { relationships: [{ user: { login: "a" } }], next_cursor: "c2" },
      { relationships: [{ user: { login: "Target" } }] },
    ];
    let i = 0;
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify(pages[i++]), { status: 200 })));
    const client = new ApiClient({ getToken: async () => "t", getServiceOrigin: async () => "https://svc.example" });
    await expect(client.isFollowing("target")).resolves.toBe(true);
  });
});
