// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  listSupportMessages,
  markSupportRead,
  sendSupportMessage,
} from "../infra/supportApi";
import { listAgencyOrders } from "../../ordering/infra/agencyOrderApi";
import {
  clampSupportChatSize,
  openSupportChatEvent,
  orderAttachLabel,
  SupportChatPanel,
  supportSummaryRefreshEvent,
} from "./SupportChatPanel";

vi.mock("../infra/supportApi", () => ({
  getSupportSummary: vi.fn(),
  listSupportMessages: vi.fn(),
  markSupportRead: vi.fn(),
  respondToProcurementRequest: vi.fn(),
  sendSupportMessage: vi.fn(),
  supportImageURL: (id: string) => `/api/v1/support/images/${id}`,
}));
vi.mock("../../ordering/infra/agencyOrderApi", () => ({
  listAgencyOrders: vi.fn(),
}));

const emptyMessages = { schemaVersion: "vitlane.support-messages.v1", messages: [] };

describe("SupportChatPanel", () => {
  let root: Root | undefined;
  let host: HTMLDivElement | undefined;

  afterEach(async () => {
    if (root) await act(async () => root?.unmount());
    host?.remove();
    root = undefined;
    host = undefined;
    document.body.innerHTML = "";
    window.localStorage.clear();
    vi.clearAllMocks();
  });

  async function render(unread = 0) {
    host = document.createElement("div");
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => {
      root?.render(
        <MemoryRouter>
          <SupportChatPanel unread={unread} />
        </MemoryRouter>,
      );
    });
  }

  async function open() {
    await act(async () => {
      window.dispatchEvent(new CustomEvent(openSupportChatEvent));
    });
  }

  it("숨김으로 시작하고 열기 이벤트로 창·런처·환영 카피가 나타난다", async () => {
    vi.mocked(listSupportMessages).mockResolvedValue(emptyMessages as never);
    await render();
    expect(host?.querySelector('[data-testid="support-chat-widget"]')).toBeNull();

    await open();
    expect(host?.textContent).toContain("메시지");
    expect(host?.textContent).toContain("주문 진행 안내와 문의 답변을 한곳에서 확인하세요");
    expect(host?.textContent).toContain("응답이 필요한 요청");
    expect(host?.querySelector(".support-chat__launcher")).not.toBeNull();
  });

  it("좌상단 handle drag와 방향키로 창을 늘리고 viewport·최소 크기에 고정한다", async () => {
    expect(clampSupportChatSize(
      { width: 100, height: 900 },
      { minWidth: 320, minHeight: 352, maxWidth: 760, maxHeight: 700 },
    )).toEqual({ width: 320, height: 700 });

    vi.mocked(listSupportMessages).mockResolvedValue(emptyMessages as never);
    await render();
    await open();

    const panel = host?.querySelector<HTMLElement>(".support-chat__window");
    const handle = host?.querySelector<HTMLElement>(
      '[role="separator"][aria-label="메시지 창 크기 조절"]',
    );
    if (!panel || !handle) throw new Error("resizable Messages window not found");
    vi.spyOn(panel, "getBoundingClientRect").mockReturnValue({
      bottom: 812,
      height: 544,
      left: 820,
      right: 1188,
      top: 268,
      width: 368,
      x: 820,
      y: 268,
      toJSON: () => ({}),
    } as DOMRect);

    await act(async () => {
      handle.dispatchEvent(new PointerEvent("pointerdown", {
        bubbles: true,
        button: 0,
        clientX: 820,
        clientY: 268,
      }));
      handle.dispatchEvent(new PointerEvent("pointermove", {
        bubbles: true,
        clientX: 620,
        clientY: 168,
      }));
      handle.dispatchEvent(new PointerEvent("pointerup", { bubbles: true }));
    });

    expect(panel.style.getPropertyValue("--support-chat-width")).toBe("568px");
    expect(panel.style.getPropertyValue("--support-chat-height")).toBe("644px");
    expect(handle.getAttribute("aria-valuetext")).toBe("너비 568, 높이 644 픽셀");
    expect(JSON.parse(
      window.localStorage.getItem("vitlane.support-chat-size.v1") ?? "null",
    )).toEqual({ width: 568, height: 644 });

    await act(async () => {
      handle.dispatchEvent(new KeyboardEvent("keydown", {
        bubbles: true,
        key: "ArrowLeft",
      }));
    });
    expect(panel.style.getPropertyValue("--support-chat-width")).toBe("584px");

    await act(async () => {
      handle.dispatchEvent(new KeyboardEvent("keydown", {
        bubbles: true,
        key: "Home",
      }));
    });
    expect(panel.style.getPropertyValue("--support-chat-width")).toBe("");
    expect(window.localStorage.getItem("vitlane.support-chat-size.v1")).toBeNull();
  });

  it("저장한 desktop 창 크기를 다음 mount에서 복원한다", async () => {
    window.localStorage.setItem(
      "vitlane.support-chat-size.v1",
      JSON.stringify({ width: 640, height: 680 }),
    );
    vi.mocked(listSupportMessages).mockResolvedValue(emptyMessages as never);
    await render();
    await open();

    const panel = host?.querySelector<HTMLElement>(".support-chat__window");
    expect(panel?.style.getPropertyValue("--support-chat-width")).toBe("640px");
    expect(panel?.style.getPropertyValue("--support-chat-height")).toBe("680px");
  });

  it("주문 첨부 답변에만 관련 주문 링크 칩을 렌더하고 raw enum을 노출하지 않는다", async () => {
    vi.mocked(listSupportMessages).mockResolvedValue({
      schemaVersion: "vitlane.support-messages.v1",
      messages: [
        {
          id: "m2",
          author: "OPERATOR",
          body: "주문이 곧 발송됩니다.",
          agencyOrderId: "aaaa1111-0000-4000-8000-000000000001",
          createdAt: "2026-08-23T09:00:00Z",
          readAt: "2026-08-23T09:10:00Z",
        },
        {
          id: "m1",
          author: "CUSTOMER",
          body: "배송이 궁금해요",
          createdAt: "2026-08-23T08:00:00Z",
        },
      ],
    } as never);
    await render();
    await open();

    const log = host?.querySelector('[data-testid="support-chat-log"]');
    expect(log?.textContent).toContain("주문이 곧 발송됩니다.");
    expect(log?.textContent).toContain("배송이 궁금해요");
    expect(log?.textContent).not.toContain("OPERATOR");
    const chip = host?.querySelector<HTMLAnchorElement>(".support-chat__order-ref");
    expect(chip?.getAttribute("href")).toBe(
      "/agencyOrder/aaaa1111-0000-4000-8000-000000000001",
    );
    expect(host?.querySelectorAll(".support-chat__order-ref")).toHaveLength(1);
  });

  it("안 읽은 답변이 있으면 읽음 처리하고 뱃지 갱신 이벤트를 쏜다", async () => {
    vi.mocked(listSupportMessages).mockResolvedValue({
      schemaVersion: "vitlane.support-messages.v1",
      messages: [
        {
          id: "m2",
          author: "OPERATOR",
          body: "답변입니다",
          createdAt: "2026-08-23T09:00:00Z",
        },
      ],
    } as never);
    vi.mocked(markSupportRead).mockResolvedValue({ read: true } as never);
    const refreshed = vi.fn();
    window.addEventListener(supportSummaryRefreshEvent, refreshed);
    await render();
    await open();
    expect(vi.mocked(markSupportRead)).toHaveBeenCalled();
    expect(refreshed).toHaveBeenCalled();
    window.removeEventListener(supportSummaryRefreshEvent, refreshed);
  });

  it("메시지를 보내면 대화에 붙는다", async () => {
    vi.mocked(listSupportMessages).mockResolvedValue(emptyMessages as never);
    vi.mocked(sendSupportMessage).mockResolvedValue({
      message: {
        id: "m9",
        author: "CUSTOMER",
        body: "도와주세요",
        createdAt: "2026-08-23T10:00:00Z",
      },
      replay: false,
    } as never);
    await render();
    await open();

    const textarea = host?.querySelector("textarea");
    await act(async () => {
      if (!textarea) return;
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      setter?.call(textarea, "도와주세요");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const send = [...(host?.querySelectorAll("button") ?? [])].find(
      (button) => button.getAttribute("aria-label") === "메시지 보내기",
    );
    await act(async () => {
      send?.click();
    });
    expect(vi.mocked(sendSupportMessage)).toHaveBeenCalledWith("도와주세요", "", []);
    expect(host?.textContent).toContain("도와주세요");
  });

  it("JPEG/PNG 증거 사진을 메시지와 함께 보내고 암호화 다운로드 경로로 렌더한다", async () => {
    vi.mocked(listSupportMessages).mockResolvedValue(emptyMessages as never);
    const image = new File([new Uint8Array([1, 2, 3])], "local-only.png", { type: "image/png" });
    vi.mocked(sendSupportMessage).mockResolvedValue({
      message: {
        id: "m-photo", author: "CUSTOMER", body: "파손 사진입니다.",
        createdAt: "2026-08-23T10:00:00Z",
        attachments: [{
          id: "image-1", mediaType: "image/png", width: 100, height: 80,
          sizeBytes: 3, downloadName: "support-image.png",
        }],
      },
      replay: false,
    } as never);
    await render();
    await open();

    const tools = host?.querySelector(".support-chat__composer-tools");
    const composerRow = host?.querySelector(".support-chat__composer-row");
    expect(tools?.textContent).toContain("주문");
    expect(tools?.textContent).toContain("사진");
    expect(tools?.nextElementSibling).toBe(composerRow);
    expect(composerRow?.querySelector("textarea")).not.toBeNull();

    const input = host?.querySelector<HTMLInputElement>('[aria-label="증거 이미지 선택"]');
    if (!input) throw new Error("image input not found");
    Object.defineProperty(input, "files", { configurable: true, value: [image] });
    await act(async () => input?.dispatchEvent(new Event("change", { bubbles: true })));
    const textarea = host?.querySelector("textarea");
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLTextAreaElement.prototype, "value",
      )?.set;
      setter?.call(textarea, "파손 사진입니다.");
      textarea?.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const send = [...(host?.querySelectorAll("button") ?? [])].find(
      (candidate) => candidate.getAttribute("aria-label") === "메시지 보내기",
    );
    await act(async () => send?.click());

    expect(vi.mocked(sendSupportMessage)).toHaveBeenCalledWith(
      "파손 사진입니다.", "", [image],
    );
    expect(host?.querySelector<HTMLImageElement>('img[alt="첨부 증거 이미지 1"]')?.src)
      .toContain("/api/v1/support/images/image-1");
  });

  it("고객도 자기 주문을 구조화 참조로 첨부해 보낼 수 있다(대칭)", async () => {
    vi.mocked(listSupportMessages).mockResolvedValue(emptyMessages as never);
    vi.mocked(listAgencyOrders).mockResolvedValue({
      schemaVersion: "vitlane.agency-orders.v1",
      agencyOrders: [{
        agencyOrder: {
          id: "aaaa1111-0000-4000-8000-000000000009",
          lines: [{ productTitle: "통근 백팩" }, { productTitle: "보조 파우치" }],
          issuedAt: "2026-08-20T09:00:00Z",
        },
      }],
      countsByView: {},
    } as never);
    vi.mocked(sendSupportMessage).mockResolvedValue({
      message: {
        id: "m9",
        author: "CUSTOMER",
        body: "이 주문 관련 문의예요",
        agencyOrderId: "aaaa1111-0000-4000-8000-000000000009",
        createdAt: "2026-08-23T10:00:00Z",
      },
      replay: false,
    } as never);
    await render();
    await open();

    expect(orderAttachLabel([{ productTitle: "통근 백팩" }, { productTitle: "보조 파우치" }]))
      .toBe("통근 백팩 외 1건");

    const toggle = [...(host?.querySelectorAll("button") ?? [])].find(
      (button) => button.getAttribute("aria-label") === "주문 첨부",
    );
    await act(async () => {
      toggle?.click();
    });
    const pick = [...(host?.querySelectorAll("button") ?? [])].find((button) =>
      button.textContent?.includes("통근 백팩 외 1건"),
    );
    await act(async () => {
      pick?.click();
    });
    expect(host?.textContent).toContain("주문 첨부 · 통근 백팩 외 1건");

    const textarea = host?.querySelector("textarea");
    await act(async () => {
      if (!textarea) return;
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      setter?.call(textarea, "이 주문 관련 문의예요");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const send = [...(host?.querySelectorAll("button") ?? [])].find(
      (button) => button.getAttribute("aria-label") === "메시지 보내기",
    );
    await act(async () => {
      send?.click();
    });
    expect(vi.mocked(sendSupportMessage)).toHaveBeenCalledWith(
      "이 주문 관련 문의예요",
      "aaaa1111-0000-4000-8000-000000000009",
      [],
    );
    // 발신 후 첨부는 해제되고, 붙은 메시지에 링크 칩이 렌더된다.
    expect(host?.textContent).not.toContain("주문 첨부 · 통근 백팩");
    expect(
      host?.querySelector<HTMLAnchorElement>(".support-chat__order-ref")?.getAttribute("href"),
    ).toBe("/agencyOrder/aaaa1111-0000-4000-8000-000000000009");
  });

  it("업무 카드에서 고객 응답·환불 근거·30일 무비용 취소 안내를 구체적으로 보여준다", async () => {
    vi.mocked(listSupportMessages).mockResolvedValue({
      schemaVersion: "vitlane.support-messages.v1",
      messages: [
        {
          id: "cancelled", author: "SYSTEM", body: "Order cancelled", agencyOrderId: "order-1",
          createdAt: "2026-08-23T12:00:00Z",
          businessCard: {
            type: "ORDER_CANCELLATION", reference: { type: "PROCUREMENT", id: "order-1" },
            actionRequired: false, publicPayload: { state: "CANCELLED", kind: "DELAY_RULE", refundBasis: "GROSS" },
          },
        },
        {
          id: "delivery", author: "SYSTEM", body: "Delivery delay", agencyOrderId: "order-1",
          createdAt: "2026-08-23T11:00:00Z",
          businessCard: {
            type: "DELIVERY_DELAY", reference: { type: "DELIVERY", id: "order-1:30-day" },
            actionRequired: false, publicPayload: { state: "ELIGIBLE_FOR_DELAY_CANCELLATION", delayedDays: 30 },
          },
        },
        {
          id: "refund", author: "OPERATOR", body: "Refund review completed", agencyOrderId: "order-1",
          createdAt: "2026-08-23T10:00:00Z",
          businessCard: {
            type: "REFUND_DECISION", reference: { type: "REFUND", id: "refund-1" },
            actionRequired: false, publicPayload: { state: "RESOLVED", items: [{ itemId: "item-1", state: "APPROVED", publicRationale: "Merchant confirmed the item was never shipped." }] },
          },
        },
        {
          id: "answer", author: "CUSTOMER", body: "Customer response recorded", agencyOrderId: "order-1",
          createdAt: "2026-08-23T09:00:00Z",
          businessCard: {
            type: "PROCUREMENT_RESPONSE", reference: { type: "PROCUREMENT", id: "request-1" },
            actionRequired: false, publicPayload: { state: "ANSWERED", response: { choice: "Ship without gift wrap" } },
          },
        },
      ],
    } as never);
    await render();
    await open();
    expect(host?.textContent).toContain("내 응답 · Ship without gift wrap");
    expect(host?.textContent).toContain("Merchant confirmed the item was never shipped.");
    expect(host?.textContent).toContain("30일 지연되었습니다");
    expect(host?.textContent).toContain("비용 없이 취소");
    expect(host?.textContent).toContain("주문 취소");
    expect(host?.textContent).toContain("취소됨");
  });

  it("런처 클릭은 창만 닫고, 헤더 X는 위젯 전체를 지운다", async () => {
    vi.mocked(listSupportMessages).mockResolvedValue(emptyMessages as never);
    await render();
    await open();

    const launcher = host?.querySelector<HTMLButtonElement>(".support-chat__launcher");
    await act(async () => {
      launcher?.click();
    });
    // 창은 닫혔지만 런처는 남는다.
    expect(host?.querySelector(".support-chat__window")).toBeNull();
    expect(host?.querySelector(".support-chat__launcher")).not.toBeNull();

    await act(async () => {
      launcher?.click();
    });
    expect(host?.querySelector(".support-chat__window")).not.toBeNull();

    const close = [...(host?.querySelectorAll("button") ?? [])].find(
      (button) => button.getAttribute("aria-label") === "메시지 닫기",
    );
    await act(async () => {
      close?.click();
    });
    expect(host?.querySelector('[data-testid="support-chat-widget"]')).toBeNull();
  });
});
