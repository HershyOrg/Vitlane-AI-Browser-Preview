// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cancelAgencyOrder,
  getAgencyOrder,
  getAgencyOrderReceipt,
  listAgencyOrders,
  requestAgencyOrderRefund,
  revealAgencyOrderShipping,
} from "../infra/agencyOrderApi";
import { LocaleProvider } from "../../../shared/i18n";
import { AgencyOrderTrackingPage } from "./AgencyOrderTrackingPage";

// 순수 헬퍼(customerActionOf 등)는 원본을 유지한다 — 자격 판정이 서버 계산
// availableActions에서 오는 계약(ADR-0055 §5)을 테스트가 그대로 지나가야 한다.
vi.mock("../infra/agencyOrderApi", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../infra/agencyOrderApi")>()),
  cancelAgencyOrder: vi.fn(),
  cancelAgencyOrderDelayRule: vi.fn(),
  getAgencyOrder: vi.fn(),
  getAgencyOrderReceipt: vi.fn().mockRejectedValue(new Error("not yet")),
  listAgencyOrders: vi.fn(),
  requestAgencyOrderRefund: vi.fn(),
  revealAgencyOrderShipping: vi.fn(),
}));

const counts = { PAYMENT_REQUIRED: 0, IN_PROGRESS: 1, NEEDS_ATTENTION: 0, FINISHED: 0, ALL: 1 };
const projection = {
  agencyOrder: {
    id: "order-1", status: "ISSUED",
    lines: [{
      lineId: "line-1", sourceCartItemId: "cart-line-1", planTargetId: "target-1",
      candidateId: "candidate-1", productTitle: "Commuter Pack",
      productUrl: "https://shop.example/products/pack", imageUrl: "https://cdn.example/pack.jpg",
      variantTitle: "Black / Large", selectedOptions: ["Color: Black", "Size: Large"],
      quantity: 1, unitPrice: { amountMinor: 7_000, currency: "USD" },
      lineSubtotal: { amountMinor: 7_000, currency: "USD" }, shopDomain: "shop.example",
    }],
    shippingAddress: { snapshotRef: "shipping-1", snapshotRevision: 1, snapshotHash: "shipping-hash", maskedSummary: "US · •••01", country: "US" },
    merchantCheckouts: [{ shopDomain: "shop.example" }],
    passThroughTotal: { amountMinor: 7_000, currency: "USD" },
    agencyFee: { variable: { amountMinor: 70, currency: "USD" }, fixed: { amountMinor: 0, currency: "USD" }, total: { amountMinor: 70, currency: "USD" }, policyVersion: "TVITUSD-1PCT" },
    customerPayableTotal: { amountMinor: 7_070, currency: "USD" }, snapshotHash: "snapshot-hash",
    issuedAt: "2026-08-15T01:00:00Z", expiresAt: "2026-08-15T02:00:00Z",
  },
  paymentInstruction: {
    paymentSelection: {
      rail: "GIWA", providerEnvironment: "TESTNET", asset: "TVITUSD",
      economicEffect: "NO_REAL_VALUE", merchantExecution: "SIMULATED",
    },
  },
  notices: [{ id: "notice-1", agencyOrderId: "order-1", kind: "OPERATOR", body: "판매처 확인 지연 안내", createdAt: "2026-08-15T01:10:00Z" }],
  process: { agencyOrderId: "order-1", state: "PROCUREMENT_IN_PROGRESS", version: 2, createdAt: "2026-08-15T01:00:00Z", updatedAt: "2026-08-15T01:05:00Z" },
  payment: { id: "payment-1", state: "FINALIZED", amountBaseUnits: "70700000", payTxHash: `0x${"1".repeat(64)}`, finalizedBlock: 12, updatedAt: "2026-08-15T01:04:00Z" },
  chainTransactions: [{ purpose: "PAY", txHash: `0x${"1".repeat(64)}`, state: "FINALIZED", blockNumber: 12, updatedAt: "2026-08-15T01:04:00Z" }],
  merchantOrders: [{
    id: "mo-1", allocationId: "allocation-1", shopDomain: "shop.example", merchantId: "merchant-1",
    checkoutOrdinal: 1, customerGrossAmount: { amountMinor: 7_070, currency: "USD" },
    state: "PLANNED", fundingState: "AVAILABLE", executionMode: "SIMULATED_NO_EFFECT",
    operational: {
      stage: "PROCUREMENT_PENDING", workStage: "PROCUREMENT",
      progress: { funding: "CURRENT", procurement: "CURRENT", delivery: "WAITING", resolution: "WAITING" },
      units: { total: 1, ordered: 1, procuring: 0, awaitingShipment: 0, inTransit: 0, delivered: 0, exception: 0, returnInProgress: 0, refundRequested: 0, refundPending: 0, refunded: 0, procurementFailed: 0, cancelled: 0 },
    },
    units: [{ id: "unit-1", lineId: "line-1", unitIndex: 1, disposition: "PENDING" }],
    updatedAt: "2026-08-15T01:05:00Z",
  }],
  shipments: [],
  units: [{
    merchantOrderUnitId: "unit-1", merchantOrderId: "mo-1", allocationId: "allocation-1",
    lineId: "line-1", unitIndex: 1, shopDomain: "shop.example", stage: "ORDERED", refundStatus: "AVAILABLE",
  }],
} as never;

describe("AgencyOrderTrackingPage", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    vi.mocked(listAgencyOrders).mockResolvedValue({ schemaVersion: "vitlane.agency-order-list.v1", agencyOrders: [projection], countsByView: counts });
    vi.mocked(getAgencyOrderReceipt).mockRejectedValue(new Error("not yet"));
    vi.mocked(revealAgencyOrderShipping).mockResolvedValue({ address: { recipientName: "Review Buyer", addressLine1: "1 Market St", city: "San Francisco", region: "CA", postalCode: "94105", country: "US" } });
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.clearAllMocks();
  });

  it.each(["en-US", "ko-KR"] as const)("%s waits for finality and retries receipt lookup after Owner delay", async (locale) => {
    vi.useFakeTimers();
    document.cookie = `vt_locale_choice=${locale};path=/`;
    window.localStorage.setItem("vitlane.locale.v1", locale);
    const item = {
      ...(projection as Record<string, unknown>),
      process: { agencyOrderId: "order-1", state: "TERMINAL", terminalReason: "COMPLETED_ALL", version: 8, updatedAt: "2026-09-10T01:00:00Z" },
      payment: { id: "payment-1", rail: "GIWA", state: "COMPLETION_SUBMITTED" },
    };
    const response = () => ({ schemaVersion: "vitlane.agency-order-list.v1" as const, agencyOrders: [structuredClone(item) as never], countsByView: counts });
    vi.mocked(listAgencyOrders).mockImplementation(async () => response());
    try {
      await act(async () => root.render(<LocaleProvider><MemoryRouter><AgencyOrderTrackingPage /></MemoryRouter></LocaleProvider>));
      await act(async () => Promise.resolve());
      expect(container.textContent).toContain(locale === "en-US" ? "Delivery complete · awaiting settlement confirmation" : "배송 완료 · 정산 확정 대기");
      expect(getAgencyOrderReceipt).not.toHaveBeenCalled();
      item.payment.state = "COMPLETED";
      await act(async () => vi.advanceTimersByTimeAsync(5000));
      expect(getAgencyOrderReceipt).toHaveBeenCalledTimes(1);
      vi.mocked(getAgencyOrderReceipt).mockResolvedValue({ receipt: {
        id: "receipt-1", agencyOrderId: "order-1", kind: "TEST", paymentRail: "GIWA", providerEnvironment: "TESTNET", asset: "TVITUSD", economicEffect: "NO_REAL_VALUE", merchantExecutionMode: "SIMULATED_NO_EFFECT", executionProfileHash: "0xprofile", legalSale: false,
        terminalState: "COMPLETED_ALL", terminalTxHash: "0xcomplete", receiptHash: "0xfinalized", payload: {}, createdAt: "2026-09-10T02:00:00Z",
      } });
      await act(async () => vi.advanceTimersByTimeAsync(5000));
      const summary = [...container.querySelectorAll("button")].find((button) => button.textContent?.includes("Commuter Pack"));
      await act(async () => summary?.click());
      expect(container.textContent).toContain("0xfinalized");
      expect(container.textContent).not.toContain(locale === "en-US" ? "awaiting settlement confirmation" : "정산 확정 대기");
      expect(getAgencyOrderReceipt).toHaveBeenCalledTimes(2);
      await act(async () => vi.advanceTimersByTimeAsync(5000));
      expect(getAgencyOrderReceipt).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
      document.cookie = "vt_locale_choice=;max-age=0;path=/";
      window.localStorage.removeItem("vitlane.locale.v1");
    }
  });

  // 상태 headline·탭·rail은 process.state의 사영이다(ADR-0057 §1·§6) — 이원
  // 시각화(체인 lane)는 더 이상 없고 단일 선형 rail만 렌더된다.
  it("process 상태와 MO resolution rail·불변 gross·배송 snapshot을 한 화면에 보존한다", async () => {
    await act(async () => root.render(<MemoryRouter><AgencyOrderTrackingPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("결제 필요 0");
    expect(container.textContent).toContain("진행 중 1");
    // 맨 왼쪽 탭 = 기본(2차 P5·D-c): 진행 중이 첫 자리다.
    const firstView = container.querySelector('[aria-label="AgencyOrder 상태"] button');
    expect(firstView?.textContent).toContain("진행 중");
    expect(container.textContent).toContain("Commuter Pack");
    expect(container.textContent).toContain("구매 진행 중");

    const summary = [...container.querySelectorAll("button")].find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => summary?.click());

    // 단일 선형 rail: 판매처 주문(3번째)이 현재이고 결제·확인은 완료다.
    const railSteps = [...container.querySelectorAll(".order-ui-tracking-lane li")];
    expect(railSteps.map((item) => item.querySelector("b")?.textContent)).toEqual([
      "결제", "확인", "판매처 주문", "배송", "완료",
    ]);
    expect(railSteps[1].classList.contains("is-done")).toBe(true);
    expect(railSteps[2].classList.contains("is-current")).toBe(true);
    // 체인 보조 lane은 제거됐다 — 결제는 한 줄 요약만 남는다.
    expect(container.querySelector(".order-ui-chain-lane")).toBeNull();
    expect(container.textContent).toContain("tVITUSD · 확정됨");
    expect(container.textContent).not.toContain("FINALIZED");
    // 고객 금액 경계는 MerchantOrder이며 physical unit 금액을 만들지 않는다.
    expect(container.textContent).toContain("Shop별 구매·해결 현황");
    expect(container.textContent).toContain("MO 전체 환불액");
    expect(container.textContent).toContain("$70.70");
    expect(container.textContent).toContain("상품·배송비·배분 수수료 포함");
    expect(container.textContent).toContain("판매처 주문 · 예정");
    expect(container.textContent).not.toContain("· PLANNED");
    expect(container.querySelectorAll(".merchant-order-status-rail li")).toHaveLength(4);
    // 주문별 결제 모드 배지(2차 P0) — 이 주문의 economicEffect에서 파생.
    expect(container.textContent).toContain("테스트 결제 · 실제 청구 없음 (TESTNET)");
    // 운영자 안내는 목록의 펼친 카드에서도 보인다(2차 P1).
    expect(container.textContent).toContain("담당자 안내 판매처 확인 지연 안내");
    expect(container.textContent).not.toContain("MerchantOrder 전체만 처리됩니다");
    expect(container.textContent).not.toContain("다른 Shop 결제 단위의 상품은 그대로 진행됩니다");

    const reveal = [...container.querySelectorAll("button")].find((item) => item.textContent?.includes("주문 시 배송정보 보기"));
    await act(async () => reveal?.click());
    await act(async () => Promise.resolve());
    expect(container.textContent).toContain("Review Buyer");
    expect(revealAgencyOrderShipping).toHaveBeenCalledWith("order-1");
  });

  it("PayPal LIVE 주문 요약은 실제 금액 결제로 표시하고 Sandbox 문구를 쓰지 않는다", async () => {
    const liveSelection = {
      rail: "PAYPAL", providerEnvironment: "LIVE", asset: "USD",
      economicEffect: "REAL_MONEY", merchantExecution: "LIVE",
    };
    const liveProjection = {
      ...(projection as Record<string, unknown>),
      agencyOrder: {
        ...((projection as { agencyOrder: Record<string, unknown> }).agencyOrder),
        paymentSelection: liveSelection,
      },
      paymentInstruction: { paymentSelection: liveSelection },
      payment: {
        id: "payment-live", rail: "PAYPAL", providerEnvironment: "LIVE",
        state: "AUTHORIZED", updatedAt: "2026-08-27T01:04:00Z",
      },
    } as never;
    vi.mocked(listAgencyOrders).mockResolvedValue({
      schemaVersion: "vitlane.agency-order-list.v1",
      agencyOrders: [liveProjection], countsByView: counts,
    });

    await act(async () => root.render(<MemoryRouter><AgencyOrderTrackingPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    const summary = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => summary?.click());

    expect(container.textContent).toContain("PayPal Live · 승인 완료 · Shop별 구매 시작 시 캡처 · 실제 금액 결제");
    expect(container.textContent).toContain("Live 결제 · 실제 청구 (LIVE)");
    expect(container.textContent).not.toContain("PayPal Sandbox");
  });

  it("고객 취소 terminal은 완료 내역에 포함하고 감사 reason을 위험 배너로 노출하지 않는다", async () => {
    const cancelled = {
      ...(projection as unknown as Record<string, unknown>),
      process: {
        agencyOrderId: "order-1", state: "TERMINAL", terminalReason: "CANCELLED",
        lastReasonCode: "CUSTOMER_CANCELLED", version: 3,
        createdAt: "2026-08-15T01:00:00Z", updatedAt: "2026-08-15T01:06:00Z",
      },
    } as never;
    vi.mocked(listAgencyOrders).mockResolvedValue({
      schemaVersion: "vitlane.agency-order-list.v1",
      agencyOrders: [cancelled],
      countsByView: { PAYMENT_REQUIRED: 0, IN_PROGRESS: 0, NEEDS_ATTENTION: 0, FINISHED: 1, ALL: 1 },
    });

    await act(async () => root.render(<MemoryRouter><AgencyOrderTrackingPage /></MemoryRouter>));
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("완료 내역 1");
    expect(container.textContent).toContain("취소됨");
    expect(container.textContent).not.toContain("확인이 필요합니다");
    expect(container.textContent).not.toContain("CUSTOMER_CANCELLED");
  });

  it("같은 주문의 환불 완료 MO와 배송 중 MO를 서로의 상태로 덮어쓰지 않는다", async () => {
    const base = projection as unknown as {
      agencyOrder: Record<string, unknown> & { lines: Array<Record<string, unknown>> };
      merchantOrders: Array<Record<string, unknown>>;
      units: Array<Record<string, unknown>>;
    };
    const secondLine = {
      ...base.agencyOrder.lines[0], lineId: "line-2", sourceCartItemId: "cart-line-2",
      productTitle: "Transit Bottle", shopDomain: "second.example",
    };
    const siblingProjection = {
      ...(projection as unknown as Record<string, unknown>),
      agencyOrder: { ...base.agencyOrder, lines: [...base.agencyOrder.lines, secondLine] },
      merchantOrders: [{
        ...base.merchantOrders[0],
        state: "PLACED", fundingState: "RELEASED", compensationAction: "TVIT_REFUND",
        compensationState: "SUCCEEDED",
        operational: {
          stage: "REFUNDED", workStage: "DONE", terminalReason: "REFUNDED",
          progress: { funding: "DONE", procurement: "DONE", delivery: "DONE", resolution: "DONE" },
          units: { total: 1, ordered: 0, procuring: 0, awaitingShipment: 0, inTransit: 0, delivered: 0, exception: 0, returnInProgress: 0, refundRequested: 0, refundPending: 0, refunded: 1, procurementFailed: 0, cancelled: 0 },
        },
      }, {
        ...base.merchantOrders[0], id: "mo-2", allocationId: "allocation-2",
        shopDomain: "second.example", merchantId: "merchant-2", checkoutOrdinal: 2,
        state: "PLACED", fundingState: "ACTIVE",
        operational: {
          stage: "IN_TRANSIT", workStage: "LOGISTICS",
          progress: { funding: "DONE", procurement: "DONE", delivery: "CURRENT", resolution: "WAITING" },
          units: { total: 1, ordered: 0, procuring: 0, awaitingShipment: 0, inTransit: 1, delivered: 0, exception: 0, returnInProgress: 0, refundRequested: 0, refundPending: 0, refunded: 0, procurementFailed: 0, cancelled: 0 },
        },
        units: [{ id: "unit-2", lineId: "line-2", unitIndex: 1, disposition: "PENDING" }],
      }],
      units: [{
        ...base.units[0], stage: "REFUNDED",
      }, {
        ...base.units[0], merchantOrderUnitId: "unit-2", merchantOrderId: "mo-2",
        allocationId: "allocation-2", lineId: "line-2", shopDomain: "second.example",
        stage: "IN_TRANSIT",
      }],
    } as never;
    vi.mocked(listAgencyOrders).mockResolvedValue({
      schemaVersion: "vitlane.agency-order-list.v1", agencyOrders: [siblingProjection], countsByView: counts,
    });

    await act(async () => root.render(<MemoryRouter><AgencyOrderTrackingPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    const summary = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => summary?.click());

    const cards = [...container.querySelectorAll(".merchant-order-card")];
    expect(cards).toHaveLength(2);
    expect(cards[0].textContent).toContain("현재 단계 · 환불 완료");
    expect(cards[0].textContent).not.toContain("현재 단계 · 배송 중");
    expect(cards[1].textContent).toContain("현재 단계 · 배송 중");
    expect(cards[1].textContent).not.toContain("현재 단계 · 환불 완료");
  });

  it("PayPal LIVE terminal 기록은 TEST 영수증으로 오표시하지 않는다", async () => {
    const liveSelection = {
      rail: "PAYPAL", providerEnvironment: "LIVE", asset: "USD",
      economicEffect: "REAL_MONEY", merchantExecution: "LIVE",
    };
    const terminalProjection = {
      ...(projection as Record<string, unknown>),
      agencyOrder: {
        ...((projection as { agencyOrder: Record<string, unknown> }).agencyOrder),
        paymentSelection: liveSelection,
      },
      paymentInstruction: { paymentSelection: liveSelection },
      process: {
        agencyOrderId: "order-1", state: "TERMINAL", terminalReason: "COMPLETED_ALL",
        version: 8, createdAt: "2026-08-27T01:00:00Z", updatedAt: "2026-08-27T03:00:00Z",
      },
		payment: { id: "payment-live", rail: "PAYPAL", state: "CAPTURED", captureId: "CAPTURE-LIVE-1" },
    } as never;
    vi.mocked(listAgencyOrders).mockResolvedValue({
      schemaVersion: "vitlane.agency-order-list.v1",
      agencyOrders: [terminalProjection], countsByView: { ...counts, IN_PROGRESS: 0, FINISHED: 1 },
    });
    vi.mocked(getAgencyOrderReceipt).mockResolvedValue({
      receipt: {
        id: "receipt-live", agencyOrderId: "order-1", customerPaymentId: "payment-live",
        kind: "LIVE_ORDER_RECORD", paymentRail: "PAYPAL", providerEnvironment: "LIVE",
        asset: "USD", economicEffect: "REAL_MONEY", merchantExecutionMode: "LIVE_MERCHANT_EFFECT",
        executionProfileHash: `0x${"a".repeat(64)}`, legalSale: true,
        terminalState: "COMPLETED_ALL", terminalTxHash: "CAPTURE-LIVE-1",
        receiptHash: `0x${"b".repeat(64)}`, payload: {}, createdAt: "2026-08-27T03:00:00Z",
      },
    });

    await act(async () => root.render(<MemoryRouter><AgencyOrderTrackingPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    const summary = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => summary?.click());

    expect(container.textContent).toContain("PayPal Live 주문 처리 기록");
    expect(container.textContent).toContain("실제 금액이 처리되고 판매처 구매가 기록되었습니다");
    expect(container.textContent).toContain("PAYPAL · LIVE · REAL_MONEY");
    expect(container.textContent).not.toContain("TEST 영수증");
    expect(container.textContent).not.toContain("실제 판매가 아닌 TEST 정산 기록");
  });

  it("PayPal SANDBOX TEST 영수증의 Capture ID를 chain 정산 tx로 오표시하지 않는다", async () => {
    const sandboxSelection = {
      rail: "PAYPAL", providerEnvironment: "SANDBOX", asset: "USD",
      economicEffect: "NO_REAL_VALUE", merchantExecution: "SIMULATED",
    };
    const terminalProjection = {
      ...(projection as Record<string, unknown>),
      agencyOrder: {
        ...((projection as { agencyOrder: Record<string, unknown> }).agencyOrder),
        paymentSelection: sandboxSelection,
      },
      paymentInstruction: { paymentSelection: sandboxSelection },
      process: {
        agencyOrderId: "order-1", state: "TERMINAL", terminalReason: "COMPLETED_ALL",
        version: 8, createdAt: "2026-08-27T01:00:00Z", updatedAt: "2026-08-27T03:00:00Z",
      },
      payment: {
        id: "payment-sandbox", rail: "PAYPAL", providerEnvironment: "SANDBOX",
		state: "CAPTURED", captureId: "CAPTURE-SANDBOX-1",
      },
    } as never;
    vi.mocked(listAgencyOrders).mockResolvedValue({
      schemaVersion: "vitlane.agency-order-list.v1",
      agencyOrders: [terminalProjection], countsByView: { ...counts, IN_PROGRESS: 0, FINISHED: 1 },
    });
    vi.mocked(getAgencyOrderReceipt).mockResolvedValue({
      receipt: {
        id: "receipt-sandbox", agencyOrderId: "order-1", customerPaymentId: "payment-sandbox",
        kind: "TEST", paymentRail: "PAYPAL", providerEnvironment: "SANDBOX",
        asset: "USD", economicEffect: "NO_REAL_VALUE", merchantExecutionMode: "SIMULATED_NO_EFFECT",
        executionProfileHash: `0x${"a".repeat(64)}`, legalSale: false,
        terminalState: "COMPLETED_ALL", terminalTxHash: "CAPTURE-SANDBOX-1",
        receiptHash: `0x${"b".repeat(64)}`, payload: {}, createdAt: "2026-08-27T03:00:00Z",
      },
    });

    await act(async () => root.render(<MemoryRouter><AgencyOrderTrackingPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    const summary = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => summary?.click());

    expect(container.textContent).toContain("TEST 영수증");
    expect(container.textContent).toContain("결제 제공자 참조");
    expect(container.textContent).toContain("CAPTURE-SANDBOX-1");
    expect(container.textContent).not.toContain("정산 tx");
  });

  // 자격 판정은 서버 계산 availableActions가 소유한다(ADR-0055 §5) — FE는
  // 내려온 명령 공간만 렌더하므로, 같은 상태라도 액션이 없으면 버튼이 없다.
  it("취소·환불 명령 공간은 availableActions에서만 열린다", async () => {
    const withActions = {
      ...(projection as Record<string, unknown>),
      availableActions: [
        { kind: "CANCEL_PRE_EFFECT", eligibleMerchantOrderIds: ["mo-1"] },
        {
          kind: "REQUEST_REFUND",
          eligibleMerchantOrderIds: ["mo-1"],
          reasonCodes: [
            "ITEM_NOT_RECEIVED",
            "ITEM_DAMAGED_DEFECTIVE",
            "WRONG_ITEM_RECEIVED",
            "ORDER_DELAYED",
            "OTHER_SERVICE_FAULT",
          ],
        },
      ],
    } as never;
    vi.mocked(getAgencyOrder).mockResolvedValue({
      schemaVersion: "vitlane.agency-order-projection.v1", agencyOrder: withActions,
    });
    await act(async () => root.render(
      <MemoryRouter initialEntries={["/agencyOrder/order-1"]}>
        <Routes><Route element={<AgencyOrderTrackingPage />} path="/agencyOrder/:agencyOrderId" /></Routes>
      </MemoryRouter>,
    ));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    expect(container.textContent).toContain("구매 전 이 Shop 결제 단위 취소");
    expect(container.textContent).toContain("MO 전체 환불 요청 · $70.70");
    expect(container.textContent).toContain("다른 Shop 결제 단위의 상품은 그대로 진행됩니다");
    // 고객 접수 typed 사유 5종은 모두 노출되고 단순변심 어휘는 구조적으로 없다.
    const reasonOptions = [...container.querySelectorAll("select option")].map((option) => option.getAttribute("value"));
    expect(reasonOptions).toEqual([
      "",
      "ITEM_NOT_RECEIVED",
      "ITEM_DAMAGED_DEFECTIVE",
      "WRONG_ITEM_RECEIVED",
      "ORDER_DELAYED",
      "OTHER_SERVICE_FAULT",
    ]);
    expect(reasonOptions).not.toContain("CHANGE_OF_MIND");
    expect(container.textContent).toContain("구매 시작 후 단순변심 환불은 제공되지 않습니다");

    // 자유 서술은 Live 심사 근거이므로 선택 사항이 아니다. 상품·typed 사유와
    // 1~500자 공개 근거가 모두 있어야 접수가 열린다.
    const cancelButton = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("구매 전 이 Shop 결제 단위 취소")) as HTMLButtonElement;
    await act(async () => cancelButton.click());
    await act(async () => Promise.resolve());
    expect(vi.mocked(cancelAgencyOrder)).toHaveBeenCalledWith("order-1", "mo-1");

    const requestButton = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("MO 전체 환불 요청")) as HTMLButtonElement;
    const rationale = container.querySelector<HTMLTextAreaElement>("#refund-rationale-mo-1");
    expect(rationale?.required).toBe(true);
    expect(rationale?.maxLength).toBe(500);
    expect(requestButton.disabled).toBe(true);

    const reason = container.querySelector<HTMLSelectElement>('select[aria-label="shop.example 환불 사유"]');
    await act(async () => setControlValue(reason, "ITEM_DAMAGED_DEFECTIVE"));
    expect(requestButton.disabled).toBe(true);
    await act(async () => setControlValue(rationale, "포장이 찢어져 상품이 파손된 채 도착했습니다."));
    expect(requestButton.disabled).toBe(false);
    await act(async () => requestButton.click());
    await act(async () => Promise.resolve());
    expect(vi.mocked(requestAgencyOrderRefund)).toHaveBeenCalledWith(
      "order-1",
      "mo-1",
      "ITEM_DAMAGED_DEFECTIVE",
      "포장이 찢어져 상품이 파손된 채 도착했습니다.",
    );

    // 액션이 비면 같은 결제·조달 상태여도 명령 공간이 닫힌다(새 마운트로 재로드).
    await act(async () => root.unmount());
    root = createRoot(container);
    vi.mocked(getAgencyOrder).mockResolvedValue({
      schemaVersion: "vitlane.agency-order-projection.v1",
      agencyOrder: { ...(withActions as Record<string, unknown>), availableActions: [] } as never,
    });
    await act(async () => root.render(
      <MemoryRouter initialEntries={["/agencyOrder/order-1"]}>
        <Routes><Route element={<AgencyOrderTrackingPage />} path="/agencyOrder/:agencyOrderId" /></Routes>
      </MemoryRouter>,
    ));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    expect(container.textContent).not.toContain("구매 전 이 Shop 결제 단위 취소");
    expect(container.textContent).not.toContain("MO 전체 환불 요청");
  });

  it("고객의 요청 설명과 승인·거절 판단 근거를 환불 내역에 표시한다", async () => {
    const withDecision = {
      ...(projection as Record<string, unknown>),
      refundRequests: [{
        id: "refund-1", agencyOrderId: "order-1", merchantOrderId: "mo-1",
        allocationId: "allocation-1", requestedGrossAmount: { amountMinor: 7_070, currency: "USD" }, state: "RESOLVED",
        reasonCode: "ITEM_DAMAGED_DEFECTIVE",
        publicRationale: "The zipper arrived broken.",
        decision: "REJECTED",
        decisionPublicRationale: "The merchant evidence shows transit damage is excluded from this refund.",
        createdAt: "2026-08-15T02:00:00Z", updatedAt: "2026-08-15T03:00:00Z",
      }],
    } as never;
    vi.mocked(getAgencyOrder).mockResolvedValue({
      schemaVersion: "vitlane.agency-order-projection.v1", agencyOrder: withDecision,
    });

    await act(async () => root.render(
      <MemoryRouter initialEntries={["/agencyOrder/order-1"]}>
        <Routes><Route element={<AgencyOrderTrackingPage />} path="/agencyOrder/:agencyOrderId" /></Routes>
      </MemoryRouter>,
    ));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("내가 제출한 설명");
    expect(container.textContent).toContain("The zipper arrived broken.");
    expect(container.textContent).toContain("환불 거절");
    expect(container.textContent).toContain("The merchant evidence shows transit damage is excluded from this refund.");
  });

  // Physical unit은 금액 카드가 아니라 물류 패키지 상세에만 나타난다.
  it("예외 physical unit은 물류 패키지 상세에만 렌더한다", async () => {
    const withException = {
      ...(projection as Record<string, unknown>),
      process: { agencyOrderId: "order-1", state: "LOGISTICS_IN_PROGRESS", version: 5, createdAt: "2026-08-15T01:00:00Z", updatedAt: "2026-08-15T01:20:00Z" },
      shipments: [{
        id: "ship-1", merchantOrderId: "mo-1", carrier: "SANDBOX", trackingRef: "SBX-1",
        state: "EXCEPTION", units: [{ id: "unit-1", lineId: "line-1", unitIndex: 1, disposition: "WRONG_ACTUAL" }],
        updatedAt: "2026-08-15T01:20:00Z",
      }],
      units: [{
        merchantOrderUnitId: "unit-1", merchantOrderId: "mo-1", allocationId: "allocation-1",
        lineId: "line-1", unitIndex: 1, shopDomain: "shop.example", stage: "EXCEPTION", refundStatus: "AVAILABLE",
        shipment: { id: "ship-1", carrier: "SANDBOX", trackingRef: "SBX-1", state: "DELIVERED" },
      }],
    } as never;
    vi.mocked(getAgencyOrder).mockResolvedValue({
      schemaVersion: "vitlane.agency-order-projection.v1", agencyOrder: withException,
    });
    await act(async () => root.render(
      <MemoryRouter initialEntries={["/agencyOrder/order-1"]}>
        <Routes><Route element={<AgencyOrderTrackingPage />} path="/agencyOrder/:agencyOrderId" /></Routes>
      </MemoryRouter>,
    ));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    expect(container.textContent).toContain("배송 확인 필요 · 예외 확인 중");
    expect(container.textContent).toContain("SBX-1");
    expect(container.textContent).not.toContain("slice-1");
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
