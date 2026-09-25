// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RecoveryLedgerPanel } from "./RecoveryLedgerPanel";

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const surface = {
  agencyOrderId: "0e5f8475-f38b-4c35-9dc1-b0148d9ea94b",
  matchedBy: "AGENCY_ORDER",
  entries: [
    {
      id: "11111111-1111-4111-8111-111111111111",
      merchantOrderId: "22222222-2222-4222-8222-222222222222",
      agencyOrderId: "0e5f8475-f38b-4c35-9dc1-b0148d9ea94b",
      cause: "RETURN", expectedAmountMinor: 3000, receivedAmountMinor: 0,
      state: "EXPECTED", manual: false, version: 1,
      createdAt: "2026-08-25T00:00:00Z", updatedAt: "2026-08-25T00:00:00Z",
    },
    {
      id: "33333333-3333-4333-8333-333333333333",
      merchantOrderId: "22222222-2222-4222-8222-222222222222",
      agencyOrderId: "0e5f8475-f38b-4c35-9dc1-b0148d9ea94b",
      cause: "MERCHANT_CANCEL", expectedAmountMinor: 1000, receivedAmountMinor: 1200,
      state: "OVER_RECOVERED", manual: true, note: "상점 취소", version: 2,
      createdAt: "2026-08-25T00:00:00Z", updatedAt: "2026-08-25T00:00:00Z",
    },
  ],
  merchantOrders: [
    { id: "22222222-2222-4222-8222-222222222222", shopDomain: "shop.example", checkoutOrdinal: 1 },
  ],
};

describe("RecoveryLedgerPanel", () => {
  let container: HTMLDivElement;
  let root: Root;

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.unstubAllGlobals();
  });

  async function mount(initialOrderId?: string) {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    await act(async () => root.render(<RecoveryLedgerPanel initialOrderId={initialOrderId} />));
    await act(async () => Promise.resolve());
  }

  it("주문 프리필로 자동 조회하고, 어휘·상태·자동/수동을 표시한다", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      calls.push(String(input));
      return jsonResponse({ schemaVersion: "vitlane.procurement-recovery.v1", surface });
    }));
    await mount(surface.agencyOrderId);

    expect(calls.some((path) => path.includes("/recovery-entries?referenceId="))).toBe(true);
    // raw enum이 아닌 한국어 어휘로 표시된다.
    expect(container.textContent).toContain("실물 회수");
    expect(container.textContent).toContain("기대 중");
    expect(container.textContent).toContain("상점 취소");
    expect(container.textContent).toContain("초과 수취");
    expect(container.textContent).toContain("자동 기록");
    expect(container.textContent).toContain("수동 기입");
    // 삭제는 수동 행에만 열린다.
    const deleteButtons = [...container.querySelectorAll("button")].filter((button) => button.textContent === "삭제");
    expect(deleteButtons).toHaveLength(1);
  });

  it("수취 기입은 달러 입력을 minor로 변환해 보내고 목록을 재조회한다", async () => {
    const bodies: Array<{ path: string; body: unknown }> = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (init?.method === "POST") {
        bodies.push({ path, body: JSON.parse(String(init.body)) });
        return jsonResponse({ entry: { ...surface.entries[0], receivedAmountMinor: 2550, state: "RECEIVED", version: 2 }, replay: false });
      }
      return jsonResponse({ schemaVersion: "vitlane.procurement-recovery.v1", surface });
    }));
    await mount(surface.agencyOrderId);

    const input = container.querySelector('input[aria-label="실물 회수 수취액"]') as HTMLInputElement;
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!;
      setter.call(input, "25.50");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const record = [...container.querySelectorAll("button")].find((button) => button.textContent === "수취 기입");
    await act(async () => record?.click());
    await act(async () => Promise.resolve());

    expect(bodies).toHaveLength(1);
    expect(bodies[0].path).toContain("/record");
    expect(bodies[0].body).toMatchObject({ receivedAmountMinor: 2550, expectedVersion: 1 });
  });

  it("MO ID 조회는 상위 주문 전체 원장을 유지하고 일치한 MO를 강조한다", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse({
      schemaVersion: "vitlane.procurement-recovery.v1",
      surface: {
        ...surface,
        matchedBy: "MERCHANT_ORDER",
        focusMerchantOrderId: surface.merchantOrders[0].id,
      },
    })));
    await mount(surface.merchantOrders[0].id);

    expect(container.textContent).toContain("상위 주문의 전체 원장을 열었고 일치한 MerchantOrder 행을 강조합니다.");
    expect(container.querySelectorAll(".funding-recovery__entry.is-focused")).toHaveLength(2);
  });
});
