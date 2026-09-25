// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { APIError } from "../../../shared/api/client";
import { adoptPayPalMORefund, adoptPayPalReauthorization, decideRefundRequest, listOperatorWorkItems, resolveLogisticsException } from "../infra/agencyOrderOperatorApi";
import { AgencyOrderExceptionsPage } from "./AgencyOrderExceptionsPage";

vi.mock("../infra/agencyOrderOperatorApi", () => ({
  adoptPayPalMORefund: vi.fn(), adoptPayPalReauthorization: vi.fn(),
  abandonProcessCommand: vi.fn(), createLogisticsReturn: vi.fn(),
  decideRefundRequest: vi.fn(), listOperatorWorkItems: vi.fn(),
  listOperatorWorkItemCounts: vi.fn(), resolveLogisticsException: vi.fn(),
  retryProcessEffect: vi.fn(), updateLogisticsReturn: vi.fn(),
}));

const openSurface = {
  schemaVersion: "vitlane.ordering-operator-work-items.v1",
  counts: { PAYMENT_RECONCILIATION: 2, PROCESS_INTERVENTION: 2, PROCUREMENT_EXECUTION: 1, REFUND_REVIEW: 1, DELIVERY_RESOLUTION: 1, RETURN_PROGRESS: 1 },
  items: [{
    kind: "PAYMENT_RECONCILIATION", id: "operation-reauth-1", agencyOrderId: "order-paypal-1",
    state: "UNKNOWN", updatedAt: "2026-08-21T00:04:00Z",
    actions: ["ADOPT_PAYPAL_REAUTHORIZATION"],
    detail: {
      reconciliationKind: "PAYPAL_REAUTHORIZATION", operationId: "operation-reauth-1",
      agencyOrderId: "order-paypal-1", merchantOrderId: "merchant-paypal-1",
      providerEnvironment: "SANDBOX", amountMinor: 17_500, currency: "USD",
      operationState: "UNKNOWN", ownerState: "PARTIALLY_CAPTURED",
      reasonCode: "PROVIDER_RESPONSE_LOST", firstSentAt: "2026-08-20T23:00:00Z",
      idempotencyDeadline: "2026-08-21T00:00:00Z", updatedAt: "2026-08-21T00:04:00Z",
    },
  }, {
    kind: "PAYMENT_RECONCILIATION", id: "operation-refund-1", agencyOrderId: "order-paypal-2",
    state: "SENT", updatedAt: "2026-08-21T00:03:00Z",
    actions: ["ADOPT_PAYPAL_MO_REFUND"],
    detail: {
      reconciliationKind: "PAYPAL_MO_REFUND", operationId: "operation-refund-1",
      agencyOrderId: "order-paypal-2", merchantOrderId: "merchant-paypal-2",
      compensationId: "compensation-paypal-2", providerEnvironment: "LIVE",
      amountMinor: 6_500, currency: "USD", operationState: "SENT",
      ownerState: "EXECUTION_PENDING", reasonCode: "HTTP_RESPONSE_LOST",
      firstSentAt: "2026-08-20T22:00:00Z", idempotencyDeadline: "2026-08-21T00:00:00Z",
      updatedAt: "2026-08-21T00:03:00Z",
    },
  }, {
    kind: "PROCUREMENT_EXECUTION", id: "task-1", agencyOrderId: "order-1",
    state: "CLAIMED", updatedAt: "2026-08-21T00:00:00Z", actions: [],
    assignmentState: "ACTIVE",
    operational: {
      stage: "PROCUREMENT_ACTIVE", workStage: "PROCUREMENT",
      progress: { funding: "DONE", procurement: "CURRENT", delivery: "WAITING", resolution: "WAITING" },
      units: { total: 0, ordered: 0, procuring: 0, awaitingShipment: 0, inTransit: 0, delivered: 0, exception: 0, returnInProgress: 0, refundRequested: 0, refundPending: 0, refunded: 0, procurementFailed: 0, cancelled: 0 },
    },
    detail: {
      task: { id: "task-1", agencyOrderId: "order-1", state: "CLAIMED", updatedAt: "2026-08-21T00:00:00Z" },
      merchantOrder: { id: "mo-1", shopDomain: "shop.example", state: "PLANNED", executionMode: "SIMULATED_NO_EFFECT", checkoutSnapshot: {} },
      units: [], agencyOrder: { id: "order-1", lines: [], shippingAddress: {} },
      processState: "PROCUREMENT_IN_PROGRESS",
    },
  }, {
    kind: "REFUND_REVIEW", id: "request-1", agencyOrderId: "order-1",
    state: "REQUESTED", updatedAt: "2026-08-21T00:00:00Z",
    actions: ["APPROVE_ITEMS", "REJECT_ITEMS"],
    detail: {
      id: "request-1", agencyOrderId: "order-1", merchantOrderId: "mo-refund-1",
      allocationId: "allocation-refund-1", requestedGrossAmount: { amountMinor: 12_000, currency: "USD" }, state: "REQUESTED",
      reasonCode: "ITEM_NOT_RECEIVED",
      publicRationale: "The package never arrived after the promised delivery window.",
      reviewContext: {
        orderNumber: "VL-2026-000042", merchantOrderId: "mo-refund-1",
        allocationId: "allocation-refund-1", shopDomain: "shop.example",
        merchantId: "merchant-shop-example", externalOrderRef: "SHOP-8842",
        merchantOrderState: "PLACED", requestedGrossAmount: { amountMinor: 12_000, currency: "USD" },
        lines: [{ lineId: "line-1", quantity: 1,
          productUrl: "https://shop.example/products/commuter-pack",
          productTitle: "Commuter Pack", variantId: "variant-black-large",
          variantTitle: "Black / Large", selectedOptions: ["Color: Black", "Size: Large"],
        }, { lineId: "line-2", quantity: 1, productTitle: "Travel Bottle",
          variantTitle: "Blue / 750 ml", selectedOptions: ["Color: Blue", "Capacity: 750 ml"],
        }],
        units: [{ merchantOrderUnitId: "unit-1", lineId: "line-1", unitIndex: 1, disposition: "MISSING",
          deliveryFacts: {
            recorded: true, expectedFulfillment: "DELIVERY", shipmentState: "LOST",
            carrier: "UPS", trackingRef: "1Z-LOST-42",
            latestEventStatus: "EXCEPTION", latestEventNote: "Package not found at depot",
            latestEventOccurredAt: "2026-08-20T03:00:00Z",
            resolutionCause: "LOST", resolutionDecision: "REFUND",
            resolutionNote: "Carrier trace exhausted", resolutionRecordedAt: "2026-08-21T01:00:00Z",
          },
          returnFacts: {
            recorded: true, state: "EXPECTED", merchantDisposition: "MERCHANT_REFUND_EXPECTED",
            note: "Await carrier claim", updatedAt: "2026-08-21T02:00:00Z",
          },
        }, { merchantOrderUnitId: "unit-2", lineId: "line-2", unitIndex: 1, disposition: "DELIVERED",
          deliveryFacts: { recorded: false }, returnFacts: { recorded: false },
        }],
      },
      createdAt: "2026-08-21T00:00:00Z", updatedAt: "2026-08-21T00:00:00Z",
    },
  }, {
    kind: "DELIVERY_RESOLUTION", id: "unit-exception", agencyOrderId: "order-2",
    state: "WRONG_ACTUAL", updatedAt: "2026-08-21T00:00:00Z",
    actions: ["RESOLVE_REFUND", "RESOLVE_DELIVERED_OK", "START_RETURN"],
    detail: {
      id: "unit-exception", merchantOrderUnitId: "mu-1", merchantOrderId: "mo-2",
      agencyOrderId: "order-2", lineId: "line-wrong", unitIndex: 1,
      fulfillment: "WRONG_ACTUAL", updatedAt: "2026-08-21T00:00:00Z",
    },
  }, {
    kind: "RETURN_PROGRESS", id: "return-1", agencyOrderId: "order-2",
    state: "RETURN_IN_TRANSIT", updatedAt: "2026-08-21T00:01:00Z",
    actions: ["MARK_RECEIVED"],
    detail: {
      id: "return-1", expectedUnitId: "unit-return", agencyOrderId: "order-2",
      state: "RETURN_IN_TRANSIT", version: 2,
      createdAt: "2026-08-21T00:00:00Z", updatedAt: "2026-08-21T00:01:00Z",
    },
  }, {
    kind: "PROCESS_INTERVENTION", id: "compensation-command", agencyOrderId: "order-3",
    state: "ATTENTION_REQUIRED", updatedAt: "2026-08-21T00:02:00Z", actions: ["RETRY"],
    detail: {
      effectId: "compensation-command", agencyOrderId: "order-3", target: "PAYMENT",
      type: "payment.execute_mo_compensation.v1", lastErrorCode: "COMPENSATION_ATTEMPT_FAILED",
      attemptCount: 5, updatedAt: "2026-08-21T00:02:00Z",
    },
  }, {
    kind: "PROCESS_INTERVENTION", id: "notice-command", agencyOrderId: "order-4",
    state: "ATTENTION_REQUIRED", updatedAt: "2026-08-21T00:01:00Z", actions: ["RETRY"],
    detail: {
      effectId: "notice-command", agencyOrderId: "order-4", target: "AGENCYORDER",
      type: "agencyorder.send_notice.v1", lastErrorCode: "COMPENSATION_ATTEMPT_FAILED",
      attemptCount: 5, updatedAt: "2026-08-21T00:01:00Z",
    },
  }],
} as never;

const resolvedSurface = {
  schemaVersion: "vitlane.ordering-operator-work-items.v1",
  counts: { PAYMENT_RECONCILIATION: 0, PROCESS_INTERVENTION: 0, PROCUREMENT_EXECUTION: 0, REFUND_REVIEW: 1, DELIVERY_RESOLUTION: 0, RETURN_PROGRESS: 1 },
  items: [{
    kind: "REFUND_REVIEW", id: "request-9", agencyOrderId: "order-9",
    state: "RESOLVED", updatedAt: "2026-08-20T00:00:00Z", actions: [],
    detail: {
      id: "request-9", agencyOrderId: "order-9", merchantOrderId: "mo-9",
      allocationId: "allocation-9", requestedGrossAmount: { amountMinor: 4_200, currency: "USD" },
      state: "RESOLVED", reasonCode: "ITEM_DAMAGED_DEFECTIVE", publicRationale: "Broken on arrival",
      decision: "APPROVED", decisionPublicRationale: "Damage verified",
      createdAt: "2026-08-19T00:00:00Z", updatedAt: "2026-08-20T00:00:00Z",
    },
  }, {
    kind: "RETURN_PROGRESS", id: "return-9", agencyOrderId: "order-9",
    state: "CLOSED", updatedAt: "2026-08-20T01:00:00Z", actions: [],
    detail: {
      id: "return-9", expectedUnitId: "unit-9", agencyOrderId: "order-9",
      state: "CLOSED", version: 5,
      createdAt: "2026-08-19T00:00:00Z", updatedAt: "2026-08-20T01:00:00Z",
    },
  }],
} as never;

describe("AgencyOrderExceptionsPage", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    vi.mocked(listOperatorWorkItems).mockImplementation(async (view?: "OPEN" | "RESOLVED") =>
      view === "RESOLVED" ? resolvedSurface : openSurface);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.clearAllMocks();
  });

  it("환불 심사에 MO 전체 근거·불변 gross를 표시하고 단일 판정만 전송한다", async () => {
    await act(async () => root.render(<MemoryRouter><AgencyOrderExceptionsPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("환불 요청 심사 (1)");
    expect(container.querySelectorAll("select").length).toBe(1);
    expect(container.textContent).toContain("VL-2026-000042");
    expect(container.textContent).toContain("US$120.00");
    expect(container.textContent).toContain("shop.example");
    expect(container.textContent).toContain("merchant-shop-example");
    expect(container.textContent).toContain("SHOP-8842");
    expect(container.textContent).toContain("Commuter Pack");
    expect(container.textContent).toContain("Black / Large");
    expect(container.textContent).toContain("Color: Black · Size: Large");
    expect(container.textContent).toContain("배분된 Vitlane 수수료 포함");
    expect(container.textContent).toContain("1Z-LOST-42");
    expect(container.textContent).toContain("Package not found at depot");
    expect(container.textContent).toContain("Await carrier claim");
    expect(container.textContent).toContain("The package never arrived after the promised delivery window.");
    expect(container.textContent).not.toContain("운영자 거절");

    const confirm = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("MO 전체 환불 승인")) as HTMLButtonElement;
    const publicRationale = container.querySelector<HTMLTextAreaElement>("#refund-public-rationale-request-1");
    const internalNote = container.querySelector<HTMLTextAreaElement>("#refund-internal-note-request-1");
    expect(publicRationale?.required).toBe(true);
    expect(publicRationale?.maxLength).toBe(2_000);
    expect(internalNote?.maxLength).toBe(4_000);
    expect(confirm.disabled).toBe(true);

    await act(async () => setControlValue(publicRationale, "배송 추적과 판매처 확인 결과 이 MerchantOrder의 분실이 확인되어 전체 환불을 승인합니다."));
    await act(async () => setControlValue(internalNote, "Carrier claim UPS-991 후속 회계 확인"));
    expect(confirm.disabled).toBe(false);
    await act(async () => confirm.click());
    await act(async () => Promise.resolve());

    expect(vi.mocked(decideRefundRequest)).toHaveBeenCalledWith("request-1", {
      approve: true,
      publicRationale: "배송 추적과 판매처 확인 결과 이 MerchantOrder의 분실이 확인되어 전체 환불을 승인합니다.",
      internalNote: "Carrier claim UPS-991 후속 회계 확인",
    });
    // 같은 AgencyOrder여도 다른 MO의 조달 상태는 이 환불 카드에 전파하지 않는다.
    expect(container.textContent).not.toContain("이 MerchantOrder에 열린 처리 행동도 있습니다");
  });

  it("배송 예외·회수 탭은 서버 actions 파생 버튼만 연다", async () => {
    await act(async () => root.render(<MemoryRouter><AgencyOrderExceptionsPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    const deliveryTab = [...container.querySelectorAll("button")].find((item) => item.textContent?.startsWith("배송 예외"));
    await act(async () => deliveryTab?.click());
    expect(container.textContent).toContain("MerchantOrder 전체 환불");
    expect(container.textContent).toContain("회수 시작");
    const refundDecision = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("MerchantOrder 전체 환불")) as HTMLButtonElement;
    const rationale = container.querySelector<HTMLTextAreaElement>("#delivery-public-rationale-unit-exception");
    expect(rationale?.required).toBe(true);
    expect(refundDecision.disabled).toBe(true);
    await act(async () => setControlValue(rationale, "배송 추적과 고객 수령 확인 결과 상품 분실이 확인되었습니다."));
    expect(refundDecision.disabled).toBe(false);
    await act(async () => refundDecision.click());
    await act(async () => Promise.resolve());
    expect(vi.mocked(resolveLogisticsException)).toHaveBeenCalledWith(
      "unit-exception", "REFUND", "배송 추적과 고객 수령 확인 결과 상품 분실이 확인되었습니다.",
    );
    // 배송 예외 카드에는 회수 진행 버튼이 없다 — 회수 진행 탭이 소유.
    expect(container.textContent).not.toContain("회수 진행:");

    const returnTab = [...container.querySelectorAll("button")].find((item) => item.textContent?.startsWith("회수 진행"));
    await act(async () => returnTab?.click());
    expect(container.textContent).toContain("회수 진행: RETURN_IN_TRANSIT → 수취 확인");
  });

  it("처리 완료 탭은 RESOLVED 열람만 하고 행동을 열지 않는다", async () => {
    await act(async () => root.render(<MemoryRouter><AgencyOrderExceptionsPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    const resolvedTab = [...container.querySelectorAll("button")].find((item) => item.textContent?.startsWith("처리 완료"));
    await act(async () => resolvedTab?.click());
    await act(async () => Promise.resolve());

    expect(vi.mocked(listOperatorWorkItems)).toHaveBeenCalledWith("RESOLVED");
    expect(container.textContent).toContain("환불 심사 종결");
    expect(container.textContent).toContain("MO 전체 환불 승인");
    expect(container.textContent).toContain("회수 종결");
    expect(container.textContent).not.toContain("판정 확정");
    expect(container.textContent).not.toContain("회수 진행:");
  });

  it("자금 보상 process 개입은 재시도만 노출한다", async () => {
    await act(async () => root.render(<MemoryRouter><AgencyOrderExceptionsPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    const interventionTab = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.startsWith("process 개입"));
    await act(async () => interventionTab?.click());

    const compensationCard = [...container.querySelectorAll("article")]
      .find((item) => item.textContent?.includes("payment.execute_mo_compensation.v1"));
    const noticeCard = [...container.querySelectorAll("article")]
      .find((item) => item.textContent?.includes("agencyorder.send_notice.v1"));
    expect(compensationCard?.textContent).toContain("재시도");
    expect(compensationCard?.textContent).not.toContain("포기 기록");
    expect(compensationCard?.querySelector("input")).toBeNull();
    expect(noticeCard?.textContent).not.toContain("포기 기록");
    expect(container.textContent).not.toContain("포기 기록");
  });

  it("결제 대사는 eligible GET-only 채택 카드만 열고 typed evidence를 전송한다", async () => {
    await act(async () => root.render(<MemoryRouter><AgencyOrderExceptionsPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    const paymentTab = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.startsWith("결제 대사"));
    await act(async () => paymentTab?.click());

    expect(container.textContent).toContain("기존 resource 검증 전용입니다");
    expect(container.textContent).toContain("새 PayPal 승인이나 환불을 만들지 않습니다");
    expect(container.textContent).toContain("기존 PayPal 승인 채택");
    expect(container.textContent).toContain("기존 PayPal 환불 채택");

    const reauthButton = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("기존 승인 검증 후 채택")) as HTMLButtonElement;
    expect(reauthButton.disabled).toBe(true);
    await act(async () => setControlValue(
      container.querySelector<HTMLInputElement>("#paypal-resource-operation-reauth-1"),
      "AUTH-EXISTING-1",
    ));
    await act(async () => setControlValue(
      container.querySelector<HTMLInputElement>("#paypal-adoption-hash-operation-reauth-1"),
      `0x${"a".repeat(64)}`,
    ));
    expect(reauthButton.disabled).toBe(false);
    await act(async () => reauthButton.click());
    await act(async () => Promise.resolve());
    expect(vi.mocked(adoptPayPalReauthorization)).toHaveBeenCalledWith(
      expect.objectContaining({ operationId: "operation-reauth-1", merchantOrderId: "merchant-paypal-1" }),
      expect.objectContaining({
        providerAuthorizationId: "AUTH-EXISTING-1",
        evidenceSource: "PAYPAL_DASHBOARD",
        evidenceHash: `0x${"a".repeat(64)}`,
        observedAt: expect.any(String),
      }),
    );

    const refundButton = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("기존 환불 검증 후 채택")) as HTMLButtonElement;
    await act(async () => setControlValue(
      container.querySelector<HTMLInputElement>("#paypal-resource-operation-refund-1"),
      "REFUND-EXISTING-1",
    ));
    await act(async () => setControlValue(
      container.querySelector<HTMLInputElement>("#paypal-adoption-hash-operation-refund-1"),
      `0x${"b".repeat(64)}`,
    ));
    expect(refundButton.disabled).toBe(true);
    await act(async () => setControlValue(
      container.querySelector<HTMLTextAreaElement>("#paypal-adoption-note-operation-refund-1"),
      "원래 요청으로 생성된 PayPal 환불임을 대시보드에서 확인했습니다.",
    ));
    expect(refundButton.disabled).toBe(false);
    await act(async () => refundButton.click());
    await act(async () => Promise.resolve());
    expect(vi.mocked(adoptPayPalMORefund)).toHaveBeenCalledWith(
      "compensation-paypal-2",
      expect.objectContaining({
        providerRefundId: "REFUND-EXISTING-1",
        publicRationale: "원래 요청으로 생성된 PayPal 환불임을 대시보드에서 확인했습니다.",
        evidenceSource: "PAYPAL_DASHBOARD",
        evidenceHash: `0x${"b".repeat(64)}`,
        observedAt: expect.any(String),
      }),
    );
  });

  it("PayPal 채택 오류는 stable code를 locale 문구로 표시한다", async () => {
    vi.mocked(adoptPayPalReauthorization).mockRejectedValueOnce(new APIError(
      "PAYPAL_RESOURCE_ADOPTION_MISMATCH",
      "The handler English message must not be rendered.",
      422,
    ));
    await act(async () => root.render(<MemoryRouter><AgencyOrderExceptionsPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    const paymentTab = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.startsWith("결제 대사"));
    await act(async () => paymentTab?.click());
    await act(async () => setControlValue(
      container.querySelector<HTMLInputElement>("#paypal-resource-operation-reauth-1"),
      "AUTH-MISMATCH-1",
    ));
    await act(async () => setControlValue(
      container.querySelector<HTMLInputElement>("#paypal-adoption-hash-operation-reauth-1"),
      `0x${"c".repeat(64)}`,
    ));
    const submit = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("기존 승인 검증 후 채택"));
    await act(async () => submit?.click());
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("저장된 금액 또는 연결과 정확히 일치하지 않습니다");
    expect(container.textContent).not.toContain("The handler English message must not be rendered.");
  });
});

function setControlValue(
  control: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement | null,
  value: string,
) {
  if (!control) throw new Error("expected form control");
  const prototype = control instanceof HTMLTextAreaElement
    ? HTMLTextAreaElement.prototype
    : control instanceof HTMLSelectElement
      ? HTMLSelectElement.prototype
      : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(prototype, "value")?.set?.call(control, value);
  control.dispatchEvent(new Event(control instanceof HTMLSelectElement ? "change" : "input", { bubbles: true }));
}
