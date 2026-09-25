// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import { replySupportConversation, sendSupportOrderMessage } from "./supportOperatorApi";

describe("support operator image uploads", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("conversation reply uses multipart without setting a JSON content type", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({
      message: { id: "m1", author: "OPERATOR", body: "Evidence", createdAt: "2026-08-27T00:00:00Z" },
      replay: false,
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const image = new File([new Uint8Array([1, 2, 3])], "evidence.png", { type: "image/png" });

    await replySupportConversation("user-1", "Evidence", [image]);

    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(init.body).toBeInstanceOf(FormData);
    expect((init.body as FormData).get("body")).toBe("Evidence");
    expect((init.body as FormData).getAll("images")).toEqual([image]);
    expect(new Headers(init.headers).has("Content-Type")).toBe(false);
  });

  it("order-attached update uses the same multipart evidence contract", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({
      message: { id: "m2", author: "OPERATOR", body: "Order update", createdAt: "2026-08-27T00:00:00Z" },
      replay: false,
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    const image = new File([new Uint8Array([4, 5, 6])], "merchant.jpg", { type: "image/jpeg" });

    await sendSupportOrderMessage("order-1", "Order update", [image]);

    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(init.body).toBeInstanceOf(FormData);
    expect((init.body as FormData).getAll("images")).toEqual([image]);
    expect(String(fetchMock.mock.calls[0][0])).toContain("/admin/support/orders/order-1/messages");
  });
});
