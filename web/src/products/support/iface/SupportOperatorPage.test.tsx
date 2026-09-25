// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  getSupportThread,
  handleSupportConversationWithoutReply,
  listSupportConversations,
  replySupportConversation,
} from "../infra/supportOperatorApi";
import { customerLabel, SupportOperatorPage } from "./SupportOperatorPage";

vi.mock("../../account/app/useCurrentUser", () => ({
  useCurrentUser: () => ({
    user: {
      id: "op-1",
      email: "operator@vitlane.test",
      displayName: "운영자",
      marketingAdmin: false,
      phase5Operator: true,
    },
    logout: vi.fn(),
  }),
}));

vi.mock("../infra/supportOperatorApi", () => ({
  getSupportCounts: vi.fn(),
  getSupportThread: vi.fn(),
  handleSupportConversationWithoutReply: vi.fn(),
  listSupportConversations: vi.fn(),
  replySupportConversation: vi.fn(),
  sendSupportOrderMessage: vi.fn(),
  supportOperatorImageURL: (userId: string, id: string) => `/api/v1/admin/support/conversations/${userId}/images/${id}`,
}));

const conversations = [
  {
    userId: "u1",
    customer: { email: "buyer@example.com", displayName: "구매자" },
    lastMessage: {
      id: "m2",
      author: "CUSTOMER",
      body: "배송이 언제쯤 될까요?",
      createdAt: "2026-08-23T09:00:00Z",
    },
    awaitingReply: true,
    awaitingCustomerMessageId: "m2",
  },
  {
    userId: "u2",
    customer: { email: "", displayName: "" },
    lastMessage: {
      id: "m9",
      author: "OPERATOR",
      body: "확인 후 안내드렸습니다.",
      createdAt: "2026-08-22T09:00:00Z",
    },
    awaitingReply: false,
  },
];

const thread = {
  schemaVersion: "vitlane.support-thread.v2",
  userId: "u1",
  customer: { email: "buyer@example.com", displayName: "구매자" },
  messages: [
    {
      id: "m2",
      author: "CUSTOMER",
      body: "배송이 언제쯤 될까요?",
      createdAt: "2026-08-23T09:00:00Z",
    },
    {
      id: "m1",
      author: "OPERATOR",
      body: "주문이 곧 발송됩니다.",
      agencyOrderId: "aaaa1111-0000-4000-8000-000000000001",
      createdAt: "2026-08-23T08:00:00Z",
    },
  ],
  awaitingReply: true,
  awaitingCustomerMessageId: "m2",
};

describe("SupportOperatorPage", () => {
  let root: Root | undefined;
  let host: HTMLDivElement | undefined;

  afterEach(async () => {
    if (root) await act(async () => root?.unmount());
    host?.remove();
    root = undefined;
    host = undefined;
    document.body.innerHTML = "";
    vi.clearAllMocks();
    vi.useRealTimers();
  });

  async function render() {
    host = document.createElement("div");
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => {
      root?.render(
        <MemoryRouter>
          <SupportOperatorPage />
        </MemoryRouter>,
      );
    });
  }

  it("답변 대기 탭이 기본이고 목록·미리보기를 렌더한다", async () => {
    vi.mocked(listSupportConversations).mockResolvedValue({
      schemaVersion: "vitlane.support-conversations.v2",
      conversations,
    } as never);
    await render();
    expect(vi.mocked(listSupportConversations)).toHaveBeenCalledWith("AWAITING_REPLY");
    expect(host?.textContent).toContain("고객 대화");
    expect(host?.textContent).toContain("배송이 언제쯤 될까요?");
    expect(host?.textContent).toContain("구매자 · buyer@example.com");
    expect(host?.textContent).toContain("답변 대기");
  });

  it("탈퇴 계정은 이름 표기로 접는다", () => {
    expect(customerLabel(undefined)).toBe("탈퇴한 사용자");
    expect(customerLabel({ email: "", displayName: "" })).toBe("탈퇴한 사용자");
    expect(customerLabel({ email: "a@b.c", displayName: "" })).toBe("a@b.c");
  });

  it("대화를 선택하면 스레드와 주문 첨부 배지를 보여주고 답변을 보낸다", async () => {
    vi.mocked(listSupportConversations).mockResolvedValue({
      schemaVersion: "vitlane.support-conversations.v2",
      conversations,
    } as never);
    vi.mocked(getSupportThread).mockResolvedValue(thread as never);
    vi.mocked(replySupportConversation).mockResolvedValue({
      message: thread.messages[0],
      replay: false,
    } as never);
    await render();

    const item = [...(host?.querySelectorAll("button") ?? [])].find((button) =>
      button.textContent?.includes("배송이 언제쯤 될까요?"),
    );
    expect(item).toBeTruthy();
    await act(async () => {
      item?.click();
    });
    expect(vi.mocked(getSupportThread)).toHaveBeenCalledWith("u1");
    const log = host?.querySelector('[data-testid="support-thread-log"]');
    expect(log?.textContent).toContain("주문이 곧 발송됩니다.");
    expect(log?.textContent).toContain("주문 첨부 · aaaa1111");
    // raw enum 노출 금지(카피 게이트) — author는 한국어 라벨로만 렌더된다.
    expect(log?.textContent).not.toContain("CUSTOMER");

    const textarea = host?.querySelector("textarea");
    expect(textarea).toBeTruthy();
    await act(async () => {
      if (!textarea) return;
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      setter?.call(textarea, "확인해 보겠습니다.");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const send = [...(host?.querySelectorAll("button") ?? [])].find(
      (button) => button.textContent === "답변 발송",
    );
    await act(async () => {
      send?.click();
    });
    expect(vi.mocked(replySupportConversation)).toHaveBeenCalledWith(
      "u1",
      "확인해 보겠습니다.",
      [],
    );
  });

  it("운영자 답변에 JPEG/PNG 증거 이미지를 함께 보낸다", async () => {
    vi.mocked(listSupportConversations).mockResolvedValue({
      schemaVersion: "vitlane.support-conversations.v2", conversations,
    } as never);
    vi.mocked(getSupportThread).mockResolvedValue(thread as never);
    vi.mocked(replySupportConversation).mockResolvedValue({ message: thread.messages[0], replay: false } as never);
    await render();
    const item = [...(host?.querySelectorAll("button") ?? [])].find((button) =>
      button.textContent?.includes("배송이 언제쯤 될까요?"),
    );
    await act(async () => item?.click());
    const image = new File([new Uint8Array([1, 2, 3])], "merchant-proof.png", { type: "image/png" });
    const input = host?.querySelector<HTMLInputElement>('[aria-label="답변 증거 이미지 첨부"]');
    if (!input) throw new Error("reply image input not found");
    Object.defineProperty(input, "files", { configurable: true, value: [image] });
    await act(async () => input.dispatchEvent(new Event("change", { bubbles: true })));
    const textarea = host?.querySelector("textarea");
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value")?.set;
      setter?.call(textarea, "증거를 첨부합니다.");
      textarea?.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const send = [...(host?.querySelectorAll("button") ?? [])].find((button) => button.textContent === "답변 발송");
    await act(async () => send?.click());
    expect(vi.mocked(replySupportConversation)).toHaveBeenCalledWith("u1", "증거를 첨부합니다.", [image]);
  });

  it("Procurement 응답 카드의 실제 고객 답변을 운영자에게 보여준다", async () => {
    vi.mocked(listSupportConversations).mockResolvedValue({
      schemaVersion: "vitlane.support-conversations.v2", conversations,
    } as never);
    vi.mocked(getSupportThread).mockResolvedValue({
      ...thread,
      messages: [{
        id: "response-card", author: "CUSTOMER", body: "Customer response recorded",
        createdAt: "2026-08-23T10:00:00Z",
        businessCard: {
          type: "PROCUREMENT_RESPONSE", reference: { type: "PROCUREMENT", id: "request-1" },
          actionRequired: false, publicPayload: { state: "ANSWERED", response: { text: "Large, navy please" } },
        },
      }],
    } as never);
    await render();
    const item = [...(host?.querySelectorAll("button") ?? [])].find((button) =>
      button.textContent?.includes("배송이 언제쯤 될까요?"),
    );
    await act(async () => item?.click());
    expect(host?.textContent).toContain("고객 응답 · Large, navy please");
  });

  it("답변 후 답변 대기 목록이 비어도 열린 스레드는 유지된다", async () => {
    vi.mocked(listSupportConversations)
      .mockResolvedValueOnce({
        schemaVersion: "vitlane.support-conversations.v2",
        conversations: [conversations[0]],
      } as never)
      // 답변 뒤 갱신: 답변 대기 목록이 빈다.
      .mockResolvedValue({
        schemaVersion: "vitlane.support-conversations.v2",
        conversations: [],
      } as never);
    vi.mocked(getSupportThread).mockResolvedValue(thread as never);
    vi.mocked(replySupportConversation).mockResolvedValue({
      message: thread.messages[0],
      replay: false,
    } as never);
    await render();

    const item = [...(host?.querySelectorAll("button") ?? [])].find((button) =>
      button.textContent?.includes("배송이 언제쯤 될까요?"),
    );
    await act(async () => {
      item?.click();
    });
    const textarea = host?.querySelector("textarea");
    await act(async () => {
      if (!textarea) return;
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      setter?.call(textarea, "답변드립니다.");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const send = [...(host?.querySelectorAll("button") ?? [])].find(
      (button) => button.textContent === "답변 발송",
    );
    await act(async () => {
      send?.click();
    });
    // 목록은 비었지만 스레드 로그와 컴포저는 남는다.
    expect(host?.querySelector('[data-testid="support-thread-log"]')).not.toBeNull();
    expect(host?.textContent).not.toContain("답변을 기다리는 대화가 없습니다");
  });

  it("답변 완료 표시는 exact 고객 메시지를 닫고 열린 스레드를 유지한다", async () => {
    vi.mocked(listSupportConversations)
      .mockResolvedValueOnce({
        schemaVersion: "vitlane.support-conversations.v2",
        conversations: [conversations[0]],
      } as never)
      .mockResolvedValue({
        schemaVersion: "vitlane.support-conversations.v2",
        conversations: [],
      } as never);
    vi.mocked(getSupportThread)
      .mockResolvedValueOnce({
        ...thread,
        messages: [{
          id: "card-later", author: "SYSTEM", body: "업무 카드가 갱신되었습니다.",
          contentKind: "BUSINESS_CARD", createdAt: "2026-08-23T10:00:00Z",
        }, ...thread.messages],
      } as never)
      .mockResolvedValue({ ...thread, awaitingReply: false } as never);
    vi.mocked(handleSupportConversationWithoutReply).mockResolvedValue({
      schemaVersion: "vitlane.support-no-reply-resolution.v1",
      handled: true,
      replay: false,
    } as never);
    await render();

    const item = [...(host?.querySelectorAll("button") ?? [])].find((button) =>
      button.textContent?.includes("구매자"),
    );
    await act(async () => {
      item?.click();
    });
    const handle = [...(host?.querySelectorAll("button") ?? [])].find(
      (button) => button.textContent === "답변 완료로 표시",
    );
    expect(handle).toBeTruthy();
    await act(async () => {
      handle?.click();
    });

    expect(vi.mocked(handleSupportConversationWithoutReply)).toHaveBeenCalledWith("u1", "m2");
    expect([...(host?.querySelectorAll("button") ?? [])].some(
      (button) => button.textContent === "답변 완료로 표시",
    )).toBe(false);
    expect(host?.querySelector('[data-testid="support-thread-log"]')).not.toBeNull();
    expect(host?.textContent).not.toContain("답변을 기다리는 대화가 없습니다");
  });
});
