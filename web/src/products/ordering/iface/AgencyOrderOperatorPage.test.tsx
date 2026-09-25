// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  beginProcurementMerchantEffect,
  completeProcurementTask,
  createProcurementCustomerRequest,
  failProcurementTask,
  getProcurementManualReview,
  listOperatorWorkItems,
  recordProcurementManualDecision,
  resolveProcurementCustomerRequest,
  revealProcurementShipping,
} from "../infra/agencyOrderOperatorApi";
import { getCurrentUser } from "../../account/infra/accountApi";
import { APIError } from "../../../shared/api/client";
import { LocaleProvider } from "../../../shared/i18n";
import {
  AgencyOrderOperatorPage,
  procurementCustomerResponseLabel,
} from "./AgencyOrderOperatorPage";

vi.mock("../infra/agencyOrderOperatorApi", () => ({
  claimProcurementTask: vi.fn(), completeProcurementTask: vi.fn(),
  failProcurementTask: vi.fn(), revealProcurementShipping: vi.fn(),
  revealProcurementContinueURL: vi.fn(),
  listOperatorWorkItems: vi.fn(), listOperatorWorkItemCounts: vi.fn(),
	getProcurementManualReview: vi.fn(), recordProcurementManualDecision: vi.fn(),
	createProcurementCustomerRequest: vi.fn(), resolveProcurementCustomerRequest: vi.fn(),
	beginProcurementMerchantEffect: vi.fn(),
  listOrderShipments: vi.fn().mockResolvedValue({ shipments: [] }),
  createShipment: vi.fn(), recordShipmentEvent: vi.fn(), confirmShipmentDelivered: vi.fn(),
}));
// ADR-0059: 안내 발송은 Support 대화의 주문 첨부 발신으로 흡수됐다.
vi.mock("../../support/infra/supportOperatorApi", () => ({
  sendSupportOrderMessage: vi.fn(),
}));
vi.mock("../../account/infra/accountApi", () => ({ getCurrentUser: vi.fn() }));

const operational = (stage: string, workStage: string) => ({
  stage, workStage,
  progress: { funding: "CURRENT", procurement: "CURRENT", delivery: "WAITING", resolution: "WAITING" },
  units: { total: 1, ordered: 0, procuring: 1, awaitingShipment: 0, inTransit: 0, delivered: 0, exception: 0, returnInProgress: 0, refundRequested: 0, refundPending: 0, refunded: 0, procurementFailed: 0, cancelled: 0 },
});

const procurementItem = (overrides: Record<string, unknown>) => {
  const state = String(overrides.state ?? "QUEUED");
  const defaultOperational = state === "SUCCEEDED"
    ? operational("AWAITING_SHIPMENT", "LOGISTICS")
    : state === "FAILED"
      ? operational("PROCUREMENT_FAILED", "DONE")
      : state === "CANCELLED"
        ? operational("CANCELLED", "DONE")
        : state === "CLAIMED" || state === "IN_PROGRESS"
          ? operational("PROCUREMENT_ACTIVE", "PROCUREMENT")
          : operational("PROCUREMENT_PENDING", "PROCUREMENT");
  const hasAssignee = Boolean(overrides.assignedOperatorUserId);
  const assignmentState = ["SUCCEEDED", "FAILED", "CANCELLED"].includes(state)
    ? "COMPLETED"
    : hasAssignee ? "ACTIVE" : "UNASSIGNED";
  return {
    kind: "PROCUREMENT_EXECUTION",
    updatedAt: "2026-08-21T00:00:00Z",
    actions: [],
    operational: defaultOperational,
    assignmentState,
    ...overrides,
  };
};

const baseDetail = (task: Record<string, unknown>, extra: Record<string, unknown> = {}) => ({
  task: { merchantOrderId: "mo-1", updatedAt: "2026-08-21T00:00:00Z", ...task },
  merchantOrder: {
    id: "mo-1", agencyOrderId: task.agencyOrderId, merchantId: "shop.example",
    shopDomain: "shop.example", checkoutOrdinal: 1,
    checkoutSnapshot: {
      authoritativeTotal: { amountMinor: 3_030, currency: "USD" },
      taxTotal: { amountMinor: 230, currency: "USD" },
      providerStatus: "requires_escalation",
      quoteReadiness: "CONFIRMED",
      procurementHandling: "OPERATOR_LATER",
      providerNotices: [{
        source: "MESSAGE",
        type: "error", severity: "requires_buyer_input", code: "future_shopify_code",
        safePath: "checkout", text: "Operator follow-up detail.",
        presentation: "INTERNAL", audience: "OPERATOR", registered: false,
      }, {
        source: "REQUIREMENT", type: "buyer_input", severity: "text", code: "engraving",
        safePath: "additional_requirements", text: "Confirm engraving at the merchant site.",
        presentation: "INTERNAL", audience: "OPERATOR", registered: false,
      }],
      manualSiteSteps: [{ resolution: "MANUAL_SITE_STEP", kind: "UNREGISTERED_PROVIDER_STEP", code: "future_shopify_code" }],
      expiresAt: "2026-08-21T01:00:00Z",
      deliveryGroups: [{ id: "g1", selectedOptionRef: "opt-1", options: [{ id: "opt-1", title: "Standard", amountMinor: 500, currency: "USD" }] }],
    },
    executionMode: "SIMULATED_NO_EFFECT", state: "PLANNED",
  },
  units: [{ id: "u1", lineId: "line-1", unitIndex: 1, disposition: "PENDING" }],
  agencyOrder: {
    id: task.agencyOrderId,
    paymentSelection: {
      rail: "GIWA", providerEnvironment: "TESTNET", asset: "TVITUSD",
      economicEffect: "NO_REAL_VALUE", merchantExecution: "SIMULATED",
    },
    lines: [{ lineId: "line-1", shopDomain: "shop.example", productTitle: "Commuter Pack", productUrl: "https://shop.example/p", variantTitle: "Large", selectedOptions: ["Size: Large"], quantity: 1, unitPrice: { amountMinor: 2_300, currency: "USD" }, lineSubtotal: { amountMinor: 2_300, currency: "USD" } }],
    merchantCheckouts: [{}], shippingAddress: { maskedSummary: "US · •••01" },
    passThroughTotal: { amountMinor: 3_030, currency: "USD" },
    agencyFee: { total: { amountMinor: 202, currency: "USD" } },
    customerPayableTotal: { amountMinor: 7_070, currency: "USD" },
  },
  processState: "PROCUREMENT_IN_PROGRESS",
  logisticsSummary: { expectedUnits: 0, deliveredUnits: 0 },
	funding: { state: "AVAILABLE", amountMinor: 3_232, rail: "PAYPAL" },
  ...extra,
});

const manualDecisionRecord = (decision: "WITHIN_AUTHORIZATION" | "IMMATERIAL_VARIANCE" |
  "MATERIAL_NEW_CONDITION" | "UNABLE_TO_PURCHASE", overrides: Record<string, unknown> = {}) => ({
  id: `decision-${decision.toLowerCase()}`,
  merchantOrderId: "mo-1",
  agencyOrderId: "order-mine",
  taskId: "task-mine",
  decision,
  publicRationale: "고객에게 전달할 충분한 판단 근거입니다.",
  observedCondition: "Merchant page condition observed.",
  evidenceSource: "MERCHANT_PAGE",
  evidenceHash: "a".repeat(64),
  observedAt: "2026-08-21T00:00:00Z",
  authorizationHash: "b".repeat(64),
  executionProfileHash: "c".repeat(64),
  createdAt: "2026-08-21T00:01:00Z",
  ...overrides,
});

const surface = {
  schemaVersion: "vitlane.ordering-operator-work-items.v1",
  counts: { PAYMENT_RECONCILIATION: 0, PROCESS_INTERVENTION: 0, PROCUREMENT_EXECUTION: 3, REFUND_REVIEW: 1, DELIVERY_RESOLUTION: 0, RETURN_PROGRESS: 0 },
  items: [
    procurementItem({
      id: "task-mine", agencyOrderId: "order-mine", state: "CLAIMED",
      actions: ["REVEAL_SHIPPING", "REVEAL_CONTINUE_URL", "RECORD_PLACED", "RECORD_FAILURE"],
      accounting: {
        agencyOrderId: "order-mine", customerPaymentId: "payment-1", rail: "PAYPAL",
        providerEnvironment: "SANDBOX", paymentState: "AUTHORIZED", currency: "USD",
        actualCustomerGrossInMinor: 0, actualProcessorFeeMinor: 0, actualNetCashInMinor: 0,
        actualMerchantSpendMinor: 0, actualCustomerCompensatedMinor: 0,
        actualMerchantRecoveredMinor: 0, unreconciledCashGrossMinor: 0,
        realizedBalanceMinor: 0, forecastNetCashInMinor: 3_059,
        forecastProcessorFeeMinor: 173, forecastMerchantSpendMinor: 3_030,
        expectedCompensationMinor: 0, forecastAdjustmentMinor: 29, forecastBalanceMinor: 29,
        requiresAttention: false, attentionReasons: [], events: [], createdAt: "2026-08-21T00:00:00Z",
        merchantOrders: [{
          allocationId: "allocation-1", merchantOrderId: "mo-1", shopDomain: "shop.example", checkoutOrdinal: 1,
          passThroughMinor: 3_030, feeVariableMinor: 172, feeFixedMinor: 30,
          feeTotalMinor: 202, customerGrossMinor: 3_232,
          feePolicyVersion: "PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1", fundingState: "AVAILABLE",
          merchantOrderState: "PLANNED", actualCustomerGrossInMinor: 0,
          actualProcessorFeeMinor: 0, actualNetCashInMinor: 0, actualMerchantSpendMinor: 0,
          actualCustomerCompensatedMinor: 0, actualMerchantRecoveredMinor: 0,
          unreconciledCashGrossMinor: 0, realizedBalanceMinor: 0,
          forecastNetCashInMinor: 3_059, forecastProcessorFeeMinor: 173,
          forecastMerchantSpendMinor: 3_030, expectedCompensationMinor: 0,
          forecastAdjustmentMinor: 29, forecastBalanceMinor: 29, attentionReasons: [],
        }],
      },
      detail: baseDetail(
        { id: "task-mine", agencyOrderId: "order-mine", state: "CLAIMED", assignedOperatorUserId: "operator-1" },
        { delivery: { derived: true, title: "Standard", amountMinor: 500 } },
      ),
		assignedOperatorUserId: "operator-1",
	}),
	procurementItem({
		id: "task-cancelled-unassigned", agencyOrderId: "order-cancelled-unassigned",
		state: "CANCELLED", actions: [],
		detail: baseDetail(
			{ id: "task-cancelled-unassigned", agencyOrderId: "order-cancelled-unassigned", state: "CANCELLED" },
			{ processState: "TERMINAL", terminalReason: "CANCELLED" },
		),
	}),
	procurementItem({
		id: "task-other", agencyOrderId: "order-other", state: "CLAIMED", actions: [],
		detail: baseDetail({
			id: "task-other", agencyOrderId: "order-other", state: "CLAIMED",
			assignedOperatorUserId: "operator-2",
		}),
		assignedOperatorUserId: "operator-2",
	}),
    procurementItem({
      id: "task-queued", agencyOrderId: "order-queued", state: "QUEUED", actions: ["CLAIM"],
      detail: baseDetail({ id: "task-queued", agencyOrderId: "order-queued", state: "QUEUED" }),
    }),
    procurementItem({
      id: "task-done", agencyOrderId: "order-done", state: "SUCCEEDED", actions: [],
      operational: operational("REFUNDED", "DONE"),
      detail: baseDetail(
        { id: "task-done", agencyOrderId: "order-done", state: "SUCCEEDED", assignedOperatorUserId: "operator-1" },
        { processState: "TERMINAL", terminalReason: "REFUNDED_ALL" },
      ),
      assignedOperatorUserId: "operator-1",
    }),
    procurementItem({
      id: "task-delivered", agencyOrderId: "order-partial", state: "SUCCEEDED", actions: [],
      operational: operational("DELIVERED", "DONE"),
      detail: baseDetail(
        { id: "task-delivered", agencyOrderId: "order-partial", state: "SUCCEEDED", assignedOperatorUserId: "operator-1" },
        { processState: "LOGISTICS_IN_PROGRESS", logisticsSummary: { expectedUnits: 1, deliveredUnits: 1, exceptionUnits: 0 } },
      ),
      assignedOperatorUserId: "operator-1",
    }),
    procurementItem({
      id: "task-issue", agencyOrderId: "order-issue", state: "CLAIMED", actions: [],
      operational: operational("DELIVERY_EXCEPTION", "ISSUE"),
      detail: baseDetail(
        { id: "task-issue", agencyOrderId: "order-issue", state: "CLAIMED", assignedOperatorUserId: "operator-1" },
        { processState: "RESOLUTION_IN_PROGRESS" },
      ),
      assignedOperatorUserId: "operator-1",
    }),
    {
      kind: "REFUND_REVIEW", id: "request-1", agencyOrderId: "order-mine",
      state: "REQUESTED", updatedAt: "2026-08-21T00:00:00Z",
      actions: ["APPROVE_ITEMS", "REJECT_ITEMS"],
      detail: {
        id: "request-1", agencyOrderId: "order-mine", merchantOrderId: "mo-1",
        allocationId: "allocation-1", requestedGrossAmount: { amountMinor: 7_070, currency: "USD" },
        state: "REQUESTED", reasonCode: "ITEM_NOT_RECEIVED", publicRationale: "Package missing",
        createdAt: "2026-08-21T00:00:00Z", updatedAt: "2026-08-21T00:00:00Z",
      },
    },
  ],
} as never;

describe("AgencyOrderOperatorPage", () => {
  let container: HTMLDivElement;
  let root: Root;

  async function renderAndOpenMineOrder() {
    await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    const summary = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => summary?.click());
    await act(async () => Promise.resolve());
  }

  async function changeValue(element: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, value: string) {
    await act(async () => {
      const prototype = element instanceof HTMLSelectElement
        ? HTMLSelectElement.prototype
        : element instanceof HTMLTextAreaElement
          ? HTMLTextAreaElement.prototype
          : HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(prototype, "value")?.set?.call(element, value);
      element.dispatchEvent(new Event(element instanceof HTMLSelectElement ? "change" : "input", { bubbles: true }));
    });
  }

  async function revealShippingAddress() {
    const reason = container.querySelector<HTMLTextAreaElement>(".agency-order-operator__reason textarea");
    await changeValue(reason as HTMLTextAreaElement, "구매대행 배송정보 확인을 위한 감사 열람");
    const reveal = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("배송정보 감사 열람"));
    await act(async () => (reveal as HTMLButtonElement).click());
    await act(async () => Promise.resolve());
  }

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    vi.mocked(getCurrentUser).mockResolvedValue({ user: { id: "operator-1" } } as never);
    vi.mocked(listOperatorWorkItems).mockResolvedValue(surface);
	vi.mocked(getProcurementManualReview).mockResolvedValue({
	  schemaVersion: "vitlane.procurement-manual-review.v1",
	  decisions: [], customerRequests: [],
	} as never);
    vi.mocked(revealProcurementShipping).mockResolvedValue({
      shippingAddress: {
        recipientName: "Test Recipient", addressLine1: "1 Main St", city: "Austin",
        region: "TX", postalCode: "78701", country: "US",
      },
      replay: false,
    } as never);
    vi.mocked(recordProcurementManualDecision).mockImplementation(async (_taskID, input) => ({
      decision: manualDecisionRecord(input.decision, {
        publicRationale: input.publicRationale,
        internalNote: input.internalNote || undefined,
        observedCondition: input.observedCondition,
        evidenceSource: input.evidenceSource,
      }),
      replay: false,
    } as never));
    vi.mocked(beginProcurementMerchantEffect).mockResolvedValue({schemaVersion:"vitlane.order-process-receipt.v1",agencyOrderId:"order-1",requestId:"purchase",flowId:"purchase",kind:"PURCHASE",outcome:"ACCEPTED",guidance:{customerAction:"WAIT",operatorAction:"WAIT"}});
    vi.mocked(completeProcurementTask).mockResolvedValue({} as never);
    vi.mocked(createProcurementCustomerRequest).mockResolvedValue({} as never);
    vi.mocked(failProcurementTask).mockResolvedValue({} as never);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.clearAllMocks();
  });

  it("기본은 활성 lease의 내 담당·조달이고 stage는 서버 MO projection의 사영이다", async () => {
    await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    // 맨 왼쪽 탭이 기본이다(P5) — 내 담당·조달만 보인다.
		expect(container.textContent).toContain("내 담당 2");
		expect(container.textContent).toContain("담당 가능 1");
		expect(container.textContent).toContain("다른 담당 1");
		expect(container.textContent).toContain("활성 전체 4");
		expect(container.textContent).toContain("담당 완료 3");
    expect(container.textContent).toContain("조달 진행 중");
    expect(container.textContent).not.toContain("조달 대기");
    // 예외 큐 섹션은 이 페이지에 없다(별도 페이지 소유).
    expect(container.textContent).not.toContain("환불 요청 심사");
    // 결제 rail과 환경은 주문 snapshot에서 분리 표기된다. Merchant 실행 mode는
    // 별도 축이며 아래 실행-mode filter를 그대로 사용한다.
    expect(container.querySelector(".vt-chip--mode.is-test")?.textContent)
      .toBe("GIWA · TESTNET · 실제 청구 없음");

    // 완료 stage: 주문 종결은 terminalReason 세분(P4), MO-완료(주문 미종결)는
    // 이 Shop 사실 + 주문 보조줄 2축 고지(4차 §2 — 소유자 지적 4번).
    const allStageScope = [...container.querySelectorAll('[aria-label="담당 범위"] button')]
      .find((item) => item.textContent?.startsWith("전체"));
    await act(async () => (allStageScope as HTMLButtonElement).click());
    const doneStage = [...container.querySelectorAll("button")].find((item) => item.textContent?.startsWith("완료"));
    await act(async () => doneStage?.click());
    expect(container.textContent).toContain("환불 완료");
    expect(container.textContent).toContain("정상 배송 완료");
    expect(container.textContent).toContain("주문: 정산 대기");
  });

  it("provider code와 미등록 여부를 읽기 전용으로 표시한다", async () => {
    await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("Provider 관찰값 · 읽기 전용");
    expect(container.textContent).toContain("future_shopify_code");
    expect(container.textContent).toContain("미등록 provider 코드");
    expect(container.textContent).toContain("미등록 provider 요구");
    expect(container.textContent).toContain("Confirm engraving at the merchant site.");
  });

  it("영문 mode에서 서버의 한국어 오류 원문을 노출하지 않는다", async () => {
    document.cookie = "vt_locale_choice=en-US; Path=/";
    vi.mocked(listOperatorWorkItems).mockRejectedValue(new APIError(
      "ORDERING_OPERATOR_WORK_ITEMS_UNAVAILABLE",
      "운영 작업 목록을 불러오지 못했습니다.",
      500,
    ));

    await act(async () => root.render(
      <LocaleProvider><MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter></LocaleProvider>,
    ));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("We couldn't load operator work items.");
    expect(container.textContent).not.toContain("운영 작업 목록을 불러오지 못했습니다.");
    document.cookie = "vt_locale_choice=; Path=/; Max-Age=0";
  });

  it("영문 mode의 action 실패도 stable code만 남기고 서버 한국어를 노출하지 않는다", async () => {
    document.cookie = "vt_locale_choice=en-US; Path=/";
    vi.mocked(revealProcurementShipping).mockRejectedValueOnce(new APIError(
      "PROCUREMENT_SHIPPING_REVEAL_REJECTED",
      "배송정보를 열람할 수 없습니다.",
      409,
      { reasonCode: "FUNDING_NOT_ACTIVE" },
    ));

    await act(async () => root.render(
      <LocaleProvider><MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter></LocaleProvider>,
    ));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    const summary = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => summary?.click());
    const reason = container.querySelector<HTMLTextAreaElement>(".agency-order-operator__reason textarea");
    await changeValue(reason as HTMLTextAreaElement, "Audit reason for revealing the shipping address");
    const reveal = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("Audit and reveal shipping information"));
    await act(async () => (reveal as HTMLButtonElement).click());
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("We couldn't complete the action. (FUNDING_NOT_ACTIVE)");
    expect(container.textContent).not.toContain("배송정보를 열람할 수 없습니다.");
    document.cookie = "vt_locale_choice=; Path=/; Max-Age=0";
  });

  it("PayPal Sandbox 결제를 GIWA TESTNET과 구분해 행과 카드에 표시한다", async () => {
    const detail = baseDetail(
      { id: "task-paypal", agencyOrderId: "order-paypal", state: "CLAIMED", assignedOperatorUserId: "operator-1" },
    );
    detail.agencyOrder.paymentSelection = {
      rail: "PAYPAL", providerEnvironment: "SANDBOX", asset: "USD",
      economicEffect: "NO_REAL_VALUE", merchantExecution: "SIMULATED",
    };
    vi.mocked(listOperatorWorkItems).mockResolvedValue({
      schemaVersion: "vitlane.ordering-operator-work-items.v1",
      counts: { PAYMENT_RECONCILIATION: 0, PROCESS_INTERVENTION: 0, PROCUREMENT_EXECUTION: 1, REFUND_REVIEW: 0, DELIVERY_RESOLUTION: 0, RETURN_PROGRESS: 0 },
      items: [procurementItem({
        id: "task-paypal", agencyOrderId: "order-paypal", state: "CLAIMED",
        actions: ["RECORD_PLACED", "RECORD_FAILURE"], detail,
        assignedOperatorUserId: "operator-1",
      })],
    } as never);

    await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    expect(container.querySelector(".vt-chip--mode.is-test")?.textContent)
      .toBe("PAYPAL · SANDBOX · 실제 청구 없음");
    const summary = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => summary?.click());
    expect(container.querySelectorAll(".vt-chip--mode.is-test")[1]?.textContent)
      .toBe("PAYPAL · SANDBOX · 실제 청구 없음");
    expect(container.textContent).toContain("Merchant simulated");
    expect(container.textContent).toContain("Merchant SIMULATED · 실제 주문 없음");
  });

  it("수납 미확정 주문을 Procurement가 아닌 결제 검토 항목으로 노출한다", async () => {
    vi.mocked(listOperatorWorkItems).mockResolvedValue({
      schemaVersion: "vitlane.ordering-operator-work-items.v1",
      counts: { PAYMENT_RECONCILIATION: 1, PROCESS_INTERVENTION: 0, PROCUREMENT_EXECUTION: 0, REFUND_REVIEW: 0, DELIVERY_RESOLUTION: 0, RETURN_PROGRESS: 0 },
      items: [{
        kind: "PAYMENT_RECONCILIATION", id: "payment-1", agencyOrderId: "order-review",
		state: "OUTCOME_UNKNOWN", updatedAt: "2026-08-27T07:00:00Z", actions: [],
        detail: {
          paymentId: "payment-1", agencyOrderId: "order-review",
          paypalAttemptId: "attempt-1", paypalOrderId: "PP-ORDER-1",
          providerEnvironment: "SANDBOX", amountMinor: 5223, currency: "USD",
			paymentState: "OUTCOME_UNKNOWN",
			attemptState: "AUTHORIZE_OUTCOME_UNKNOWN",
			reasonCode: "AUTHORIZATION_OUTCOME_UNKNOWN", updatedAt: "2026-08-27T07:00:00Z",
        },
      }],
    } as never);

    await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("PayPal 결제 대사가 필요합니다");
	expect(container.textContent).toContain("주문 1건이 조달 전에 일시 중지되었습니다");
    const review = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("결제 검토"));
    await act(async () => review?.click());

		expect(container.textContent).toContain("PayPal 승인 결과 확인 중");
    expect(container.textContent).toContain("PP-ORDER-1");
    expect(container.textContent).toContain("$52.23");
		expect(container.textContent).toContain("승인이 검증된 뒤에만 Procurement가 열립니다");
    expect(container.textContent).not.toContain("내가 맡기");
  });

  it("stage 축은 완전 분할이다 — 조달+배송+예외+완료 = 전체 (3차 #6)", async () => {
    await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    const stageGroup = container.querySelector('[aria-label="처리 단계"]');
    const countOf = (label: string) => {
      const button = [...(stageGroup?.querySelectorAll("button") ?? [])]
        .find((item) => item.textContent?.startsWith(label));
      return Number(button?.querySelector("span")?.textContent ?? NaN);
    };
    const partition = countOf("조달") + countOf("배송") + countOf("예외") + countOf("완료");
    expect(countOf("전체")).toBeGreaterThan(0);
    expect(partition).toBe(countOf("전체"));

    // 예외 탭은 read-only 열람이다: 예외 국면 카드가 보이고, 행동은
    // 예외 처리 페이지로의 이동 링크만 연다.
    const issueTab = [...(stageGroup?.querySelectorAll("button") ?? [])].find((item) => item.textContent?.startsWith("예외"));
    await act(async () => (issueTab as HTMLButtonElement).click());
    expect(container.textContent).toContain("확인 필요");
    const summary = [...container.querySelectorAll("button")].find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => summary?.click());
    expect(container.textContent).toContain("배송 예외");
    expect(container.textContent).toContain("예외 처리로 이동");
    expect(container.textContent).not.toContain("배송정보 감사 열람");
  });

  it("행 금액은 checkout 실비이고 담당 가능 탭에서 담당을 연다", async () => {
    await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    const queuedTab = [...container.querySelectorAll("button")].find((item) => item.textContent?.startsWith("담당 가능"));
    await act(async () => queuedTab?.click());
    const summary = [...container.querySelectorAll("button")].find((item) => item.textContent?.includes("Commuter Pack"));
    // 재발 방지(ADR-0052 §4.3): 행 요약에는 checkout 실비 총액($30.30)만 표시되고
    // 주문 전체 금액($70.70)은 접힌 참고 요약에만 있다.
    expect(summary?.textContent).toContain("$30.30");
    expect(summary?.textContent).not.toContain("$70.70");
    await act(async () => summary?.click());
    expect(container.textContent).toContain("내가 맡기");
    expect(container.textContent).toContain("Merchant SIMULATED · 실제 주문 없음");
  });

  it("선택 배송은 서버 파생을 소비하고, 파생 실패는 명시된다 (운영정합 5차 C2·C3)", async () => {
    vi.mocked(listOperatorWorkItems).mockResolvedValue({
      schemaVersion: "vitlane.ordering-operator-work-items.v1",
      counts: { PAYMENT_RECONCILIATION: 0, PROCESS_INTERVENTION: 0, PROCUREMENT_EXECUTION: 2, REFUND_REVIEW: 0, DELIVERY_RESOLUTION: 0, RETURN_PROGRESS: 0 },
      items: [
        procurementItem({
          id: "task-derived", agencyOrderId: "order-derived", state: "CLAIMED",
          actions: ["REVEAL_SHIPPING", "REVEAL_CONTINUE_URL", "RECORD_PLACED", "RECORD_FAILURE"],
          detail: baseDetail(
            { id: "task-derived", agencyOrderId: "order-derived", state: "CLAIMED", assignedOperatorUserId: "operator-1" },
            { delivery: { derived: true, title: "Express", amountMinor: 1500 } },
          ),
          assignedOperatorUserId: "operator-1",
        }),
        procurementItem({
          id: "task-underived", agencyOrderId: "order-underived", state: "SUCCEEDED", actions: [],
          operational: {
            ...operational("IN_TRANSIT", "LOGISTICS"),
            units: { ...operational("IN_TRANSIT", "LOGISTICS").units, total: 3, inTransit: 1, exception: 2 },
          },
          detail: baseDetail(
            { id: "task-underived", agencyOrderId: "order-underived", state: "SUCCEEDED", assignedOperatorUserId: "operator-1" },
            {
              merchantOrder: { ...baseDetail({ id: "x", agencyOrderId: "order-underived" }).merchantOrder, state: "PLACED" },
              delivery: { derived: false, amountMinor: 0 },
              logisticsSummary: { expectedUnits: 3, deliveredUnits: 1, exceptionUnits: 2 },
            },
          ),
          assignedOperatorUserId: "operator-1",
        }),
      ],
    } as never);
    await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    const derivedRow = [...container.querySelectorAll("button")].find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => derivedRow?.click());
    expect(container.textContent).toContain("Express ($15.00)");
    expect(container.textContent).not.toContain("정보 없음");

    // 배송 stage와 상태명은 서버 MO projection을 그대로 소비하고, 파생 실패는 명시 문구다.
    const allScope = [...container.querySelectorAll('[aria-label="담당 범위"] button')].find((item) => item.textContent?.startsWith("전체"));
    await act(async () => (allScope as HTMLButtonElement).click());
    const logisticsTab = [...container.querySelectorAll("button")].find((item) => item.textContent?.startsWith("배송"));
    await act(async () => (logisticsTab as HTMLButtonElement).click());
    expect(container.textContent).toContain("배송 중");
    const underivedRow = [...container.querySelectorAll("button")].find((item) => item.textContent?.includes("배송 중"));
    await act(async () => underivedRow?.click());
    expect(container.textContent).toContain("파생 실패 — 스냅샷 원문 확인 필요");
  });

  it("담당 lease 만료 카드는 재담당만 연다 (운영정합 5차 B1)", async () => {
    vi.mocked(listOperatorWorkItems).mockResolvedValue({
      schemaVersion: "vitlane.ordering-operator-work-items.v1",
      counts: { PAYMENT_RECONCILIATION: 0, PROCESS_INTERVENTION: 0, PROCUREMENT_EXECUTION: 1, REFUND_REVIEW: 0, DELIVERY_RESOLUTION: 0, RETURN_PROGRESS: 0 },
      items: [procurementItem({
        // 서버가 할당된 CLAIMED에 CLAIM을 다시 열었다 = lease 만료 신호.
        id: "task-expired", agencyOrderId: "order-expired", state: "CLAIMED", actions: ["CLAIM"],
        detail: baseDetail({ id: "task-expired", agencyOrderId: "order-expired", state: "CLAIMED", assignedOperatorUserId: "operator-1" }),
        assignedOperatorUserId: "operator-1",
        assignmentState: "EXPIRED",
      })],
    } as never);
    await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    const availableTab = [...container.querySelectorAll("button")].find((item) => item.textContent?.startsWith("담당 가능"));
    await act(async () => (availableTab as HTMLButtonElement).click());
    const summary = [...container.querySelectorAll("button")].find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => summary?.click());
    expect(container.textContent).toContain("담당 lease가 만료되었습니다");
    expect(container.textContent).toContain("다시 담당하기");
    // 진행 행동·사유 입력은 재담당 전에는 열리지 않는다.
    expect(container.textContent).not.toContain("배송정보 감사 열람");
    expect(container.textContent).not.toContain("배송정보 열람 사유");
    expect(container.textContent).not.toContain("내가 맡기");
  });

  it("같은 주문의 예외 처리 존재를 붉게 알리고, 열람 사유는 프리필되지 않는다", async () => {
    await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    // 4차 §4: 접힌 행 요약에서도 예외 뱃지가 보인다(펼침 불필요).
    const issueBadge = container.querySelector(".product-ui-operator-row__issues");
    expect(issueBadge?.textContent).toBe("예외 1");
    const summary = [...container.querySelectorAll("button")].find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => summary?.click());
    // 갭2: order-mine에는 환불 심사 1건이 열려 있다.
    expect(container.textContent).toContain("예외 처리 요청도 존재합니다");
    expect(container.textContent).toContain("환불 심사 1건");
    // 이벤트 기반 회계 view: 실제 현금과 미종결 MO forecast를 분리한다.
    expect(container.textContent).toContain("이 MerchantOrder");
    expect(container.textContent).toContain("실현 잔고");
    expect(container.textContent).toContain("예상 조정");
    expect(container.textContent).toContain("고객 총액");
    expect(container.textContent).not.toContain("잔액 부족(주문 전체)");
    expect(container.textContent).not.toContain("보전액 배정");
    // P6: 사유 textarea는 빈 값 + placeholder이고, 8자 전에는 열람이 잠긴다.
    const reason = container.querySelector("textarea");
    expect(reason?.value).toBe("");
    // 규칙과 현재 글자 수를 힌트로 보여주고, 아직 입력 전이므로 붉게 강조하지는 않는다.
    expect(container.textContent).toContain("8~500자 · 현재 0자");
    expect(reason?.getAttribute("aria-invalid")).toBeNull();
    const reveal = [...container.querySelectorAll("button")].find((item) => item.textContent?.includes("배송정보 감사 열람"));
    expect((reveal as HTMLButtonElement).disabled).toBe(true);
  });


  it("승인 범위 내는 추가 폼 없이 배송지 열람 뒤 단일 주문 처리 행동을 연다", async () => {
    await renderAndOpenMineOrder();

    const decision = container.querySelector<HTMLSelectElement>('select[aria-label="판단"]');
    expect(decision?.value).toBe("WITHIN_AUTHORIZATION");
    expect(container.querySelector('textarea[aria-label="관찰한 merchant 조건"]')).toBeNull();
    expect(container.querySelector('textarea[aria-label="고객 공개 근거"]')).toBeNull();
    const start = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("주문 처리 시작"));
    expect((start as HTMLButtonElement).disabled).toBe(true);
    expect(container.textContent).toContain("배송정보 감사 열람을 완료하세요");

    await revealShippingAddress();
    expect(container.querySelector<HTMLTextAreaElement>(".agency-order-operator__reason textarea")).toBeNull();
    expect(container.textContent).toContain("배송정보 감사 열람 완료");
    expect((start as HTMLButtonElement).disabled).toBe(false);
    await act(async () => (start as HTMLButtonElement).click());

    expect(recordProcurementManualDecision).toHaveBeenCalledWith("task-mine", expect.objectContaining({
      decision: "WITHIN_AUTHORIZATION",
      evidenceSource: "OPERATOR_OBSERVATION",
    }));
    expect(beginProcurementMerchantEffect).toHaveBeenCalledWith("task-mine");
    expect(createProcurementCustomerRequest).not.toHaveBeenCalled();
    expect(failProcurementTask).not.toHaveBeenCalled();
  });

	it("결과 미상 MO 자금은 재판정 폼 대신 기존 capture 재확인 행동만 연다", async () => {
		const detail = baseDetail(
			{ id: "task-unknown", agencyOrderId: "order-unknown", state: "CLAIMED", assignedOperatorUserId: "operator-1" },
			{ funding: { positionId: "position-1", state: "ACTIVATION_UNKNOWN", amountMinor: 3_232, rail: "PAYPAL" } },
		);
		vi.mocked(listOperatorWorkItems).mockResolvedValue({
			schemaVersion: "vitlane.ordering-operator-work-items.v1",
			counts: { PAYMENT_RECONCILIATION: 0, PROCESS_INTERVENTION: 0, PROCUREMENT_EXECUTION: 1, REFUND_REVIEW: 0, DELIVERY_RESOLUTION: 0, RETURN_PROGRESS: 0 },
			items: [procurementItem({
				id: "task-unknown", agencyOrderId: "order-unknown", state: "CLAIMED",
				actions: ["REVEAL_SHIPPING", "RECORD_PLACED", "RECORD_FAILURE"],
				detail, assignedOperatorUserId: "operator-1",
			})],
		} as never);
		vi.mocked(getProcurementManualReview).mockResolvedValue({
			schemaVersion: "vitlane.procurement-manual-review.v1",
			decisions: [manualDecisionRecord("WITHIN_AUTHORIZATION", {
				taskId: "task-unknown", agencyOrderId: "order-unknown",
			})],
			customerRequests: [],
		} as never);

		await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
		await act(async () => Promise.resolve());
		await act(async () => Promise.resolve());
		const summary = [...container.querySelectorAll("button")]
			.find((item) => item.textContent?.includes("Commuter Pack"));
		await act(async () => summary?.click());
		await act(async () => Promise.resolve());

		expect(container.textContent).toContain("판매처 구매 전 PayPal 자금 대사가 필요합니다");
		expect(container.querySelector('select[aria-label="판단"]')).toBeNull();
		expect(container.textContent).not.toContain("주문 처리 시작");
		const retry = [...container.querySelectorAll("button")]
			.find((item) => item.textContent?.includes("자금 재확인·처리 재개"));
		expect(retry).toBeDefined();
		await act(async () => (retry as HTMLButtonElement).click());
		expect(beginProcurementMerchantEffect).toHaveBeenCalledWith("task-unknown");
		expect(recordProcurementManualDecision).not.toHaveBeenCalled();
	});

	it("활성 담당 범위와 완료 담당을 겹치지 않게 분리한다", async () => {
		await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
		await act(async () => Promise.resolve());
		await act(async () => Promise.resolve());

		const scopeGroup = container.querySelector('[aria-label="담당 범위"]');
		const countOf = (label: string) => {
			const button = [...(scopeGroup?.querySelectorAll("button") ?? [])]
				.find((item) => item.textContent?.startsWith(label));
			return Number(button?.querySelector("span")?.textContent ?? NaN);
		};
		expect(countOf("내 담당") + countOf("담당 가능") + countOf("다른 담당")).toBe(4);
		expect(container.textContent).toContain("담당 완료 3");

		const all = [...(scopeGroup?.querySelectorAll("button") ?? [])]
			.find((item) => item.textContent?.startsWith("전체"));
		await act(async () => (all as HTMLButtonElement).click());
		const done = [...container.querySelectorAll('[aria-label="처리 단계"] button')]
			.find((item) => item.textContent?.startsWith("완료"));
		await act(async () => (done as HTMLButtonElement).click());
		expect(container.textContent).toContain("환불 완료");
	});

  it("경미한 차이는 근거 폼과 근거 기록·주문 처리 버튼만 표시한다", async () => {
    await renderAndOpenMineOrder();
    await revealShippingAddress();
    const decision = container.querySelector<HTMLSelectElement>('select[aria-label="판단"]');
    await changeValue(decision as HTMLSelectElement, "IMMATERIAL_VARIANCE");

    const observed = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="관찰한 merchant 조건"]');
    const rationale = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="고객 공개 근거"]');
    expect(observed).not.toBeNull();
    expect(rationale).not.toBeNull();
    expect(container.querySelector('textarea[aria-label="고객 질문"]')).toBeNull();
    const action = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("경미한 차이 근거 기록·주문 처리"));
    expect((action as HTMLButtonElement).disabled).toBe(true);

    await changeValue(observed as HTMLTextAreaElement, "동일 variant의 판매처 표기만 변경됨");
    await changeValue(rationale as HTMLTextAreaElement, "상품과 금액은 같고 판매처 표기만 달라졌습니다.");
    expect((action as HTMLButtonElement).disabled).toBe(false);
    await act(async () => (action as HTMLButtonElement).click());

    expect(recordProcurementManualDecision).toHaveBeenCalledWith("task-mine", expect.objectContaining({
      decision: "IMMATERIAL_VARIANCE",
    }));
    expect(beginProcurementMerchantEffect).toHaveBeenCalledWith("task-mine");
  });

  it("중대한 차이는 요청 입력만 표시하고 판단 기록과 Messages 요청을 한 번에 보낸다", async () => {
    await renderAndOpenMineOrder();
    const decision = container.querySelector<HTMLSelectElement>('select[aria-label="판단"]');
    await changeValue(decision as HTMLSelectElement, "MATERIAL_NEW_CONDITION");

    expect(container.querySelector('textarea[aria-label="고객 공개 근거"]')).toBeNull();
    const observed = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="관찰한 merchant 조건"]');
    const context = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="고객 공개 배경"]');
    const question = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="고객 질문"]');
    const action = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("Messages로 요청 보내기"));
    expect(observed).not.toBeNull();
    expect(context).not.toBeNull();
    expect(question).not.toBeNull();
    expect((action as HTMLButtonElement).disabled).toBe(true);

    await changeValue(observed as HTMLTextAreaElement, "판매처가 다른 배송 조건을 새로 요구함");
    await changeValue(question as HTMLTextAreaElement, "새 배송 조건으로 진행할까요?");
    await changeValue(context as HTMLTextAreaElement, "짧음");
    expect((action as HTMLButtonElement).disabled).toBe(true);
    expect(container.textContent).toContain("고객 공개 배경·판단 근거를 8–2,000자로 입력하세요");
    // 조건 미달은 필드 자체가 붉게(aria-invalid) 강조되고 부족한 글자 수를 말한다.
    expect(context?.getAttribute("aria-invalid")).toBe("true");
    expect(container.textContent).toContain("8자 이상 입력하세요 (6자 더 필요)");
    expect(container.textContent).toContain("현재 2자");
    await changeValue(context as HTMLTextAreaElement, "새 배송 조건에 대한 고객 확인이 필요합니다.");
    expect(context?.getAttribute("aria-invalid")).toBeNull();
    expect(container.textContent).not.toContain("더 필요)");
    expect((action as HTMLButtonElement).disabled).toBe(false);
    await act(async () => (action as HTMLButtonElement).click());

		expect(recordProcurementManualDecision).not.toHaveBeenCalled();
		expect(createProcurementCustomerRequest).toHaveBeenCalledWith("task-mine", expect.objectContaining({
			observedCondition: "판매처가 다른 배송 조건을 새로 요구함",
			evidenceSource: "MERCHANT_PAGE",
			prompt: "새 배송 조건으로 진행할까요?",
      publicContext: "새 배송 조건에 대한 고객 확인이 필요합니다.",
    }));
    expect(beginProcurementMerchantEffect).not.toHaveBeenCalled();
    expect(failProcurementTask).not.toHaveBeenCalled();
  });

  it("중대한 조건 요청이 열리면 주문을 pending으로 두고 다른 판정·실행 폼을 숨긴다", async () => {
    vi.mocked(getProcurementManualReview).mockResolvedValue({
      schemaVersion: "vitlane.procurement-manual-review.v1",
      decisions: [manualDecisionRecord("MATERIAL_NEW_CONDITION")],
      customerRequests: [{
        id: "request-pending", merchantOrderId: "mo-1", agencyOrderId: "order-mine",
        kind: "CONSENT", prompt: "새 조건으로 진행할까요?",
        responseType: "BOOLEAN_CONSENT", publicContext: "새 조건 확인이 필요합니다.",
        state: "PENDING", requestedAt: "2026-08-21T00:00:00Z",
        dueAt: "2026-08-28T00:00:00Z", version: 1,
      }],
    } as never);
    await renderAndOpenMineOrder();

    expect(container.textContent).toContain("고객 응답 대기");
    expect(container.textContent).toContain("새 조건으로 진행할까요?");
    expect(container.querySelector('select[aria-label="판단"]')).toBeNull();
    expect(container.textContent).not.toContain("주문 처리 시작");
    expect(container.textContent).not.toContain("Messages로 요청 보내기");
    expect(container.textContent).toContain("질문 취소");
  });

  it("기한이 지난 무응답 종료는 MO 영향액을 확인한 뒤 exact 요청만 종료한다", async () => {
    vi.mocked(getProcurementManualReview).mockResolvedValue({
      schemaVersion: "vitlane.procurement-manual-review.v1",
      decisions: [manualDecisionRecord("MATERIAL_NEW_CONDITION")],
      customerRequests: [{
        id: "request-no-response", merchantOrderId: "mo-1", agencyOrderId: "order-mine",
        kind: "CONSENT", prompt: "새 조건으로 진행할까요?",
        responseType: "BOOLEAN_CONSENT", publicContext: "새 조건 확인이 필요합니다.",
        state: "PENDING", requestedAt: "2026-08-01T00:00:00Z",
        dueAt: "2026-08-08T00:00:00Z", version: 3,
      }],
    } as never);
    vi.mocked(resolveProcurementCustomerRequest).mockResolvedValue({} as never);
    await renderAndOpenMineOrder();

    const open = [...container.querySelectorAll("button")]
      .find((item) => item.textContent === "무응답으로 종료") as HTMLButtonElement;
    expect(open.disabled).toBe(false);
    await act(async () => open.click());

    expect(document.body.textContent).toContain("무응답 종료 확인");
    expect(document.body.textContent).toContain("MO 전체 환불·승인 해제액");
    expect(document.body.textContent).toContain("$32.32");
    expect(resolveProcurementCustomerRequest).not.toHaveBeenCalled();
    const confirm = [...document.body.querySelectorAll("button")]
      .find((item) => item.textContent === "무응답 종료 확정") as HTMLButtonElement;
    await act(async () => confirm.click());
    expect(resolveProcurementCustomerRequest).toHaveBeenCalledWith(
      "request-no-response", 3, false, "NO_RESPONSE_AFTER_DUE",
    );
  });

  it("고객 답변 뒤 직전 요청을 재판단 폼에 복원하고 즉시 재요청할 수 있다", async () => {
    const materialDecision = manualDecisionRecord("MATERIAL_NEW_CONDITION", {
      observedCondition: "판매처가 수령인 전화번호를 추가로 요구함",
      evidenceSource: "MERCHANT_POLICY",
    });
    vi.mocked(getProcurementManualReview).mockResolvedValue({
      schemaVersion: "vitlane.procurement-manual-review.v1",
      decisions: [materialDecision],
      customerRequests: [{
        id: "request-answered", merchantOrderId: "mo-1", agencyOrderId: "order-mine",
        sourceDecisionId: materialDecision.id,
        kind: "INFORMATION", prompt: "연락 가능한 전화번호를 알려주시겠어요?",
        responseType: "TEXT", publicContext: "판매처 주문에 수령인 연락처가 필요합니다.",
        state: "ANSWERED", response: { text: "+1 555 0100" },
        requestedAt: "2026-08-21T00:00:00Z", dueAt: "2026-08-28T00:00:00Z",
        resolvedAt: "2026-08-22T00:00:00Z", version: 2,
      }],
    } as never);

    await renderAndOpenMineOrder();
    await act(async () => Promise.resolve());

    const decision = container.querySelector<HTMLSelectElement>('select[aria-label="판단"]');
    const observed = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="관찰한 merchant 조건"]');
    const context = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="고객 공개 배경"]');
    const question = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="고객 질문"]');
    const action = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("Messages로 재요청 보내기"));

    expect(decision?.value).toBe("MATERIAL_NEW_CONDITION");
    expect(observed?.value).toBe("판매처가 수령인 전화번호를 추가로 요구함");
    expect(context?.value).toBe("판매처 주문에 수령인 연락처가 필요합니다.");
    expect(question?.value).toBe("연락 가능한 전화번호를 알려주시겠어요?");
    expect(container.textContent).toContain("+1 555 0100");
    expect((action as HTMLButtonElement).disabled).toBe(false);

    await changeValue(question as HTMLTextAreaElement, "추가 연락처도 알려주시겠어요?");
    await act(async () => (action as HTMLButtonElement).click());

    expect(createProcurementCustomerRequest).toHaveBeenCalledWith("task-mine", expect.objectContaining({
      observedCondition: "판매처가 수령인 전화번호를 추가로 요구함",
      evidenceSource: "MERCHANT_POLICY",
      prompt: "추가 연락처도 알려주시겠어요?",
      publicContext: "판매처 주문에 수령인 연락처가 필요합니다.",
    }));
    expect(beginProcurementMerchantEffect).not.toHaveBeenCalled();
    expect(failProcurementTask).not.toHaveBeenCalled();
  });

  it("구매 불가는 근거 폼과 실패·해당 Shop 환불 버튼만 표시한다", async () => {
    await renderAndOpenMineOrder();
    const decision = container.querySelector<HTMLSelectElement>('select[aria-label="판단"]');
    await changeValue(decision as HTMLSelectElement, "UNABLE_TO_PURCHASE");

    const observed = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="관찰한 merchant 조건"]');
    const rationale = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="고객 공개 근거"]');
    const action = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("구매 불가 기록·해당 Shop 환불"));
    expect((action as HTMLButtonElement).disabled).toBe(true);
    expect(container.querySelector('textarea[aria-label="고객 질문"]')).toBeNull();

    await changeValue(observed as HTMLTextAreaElement, "승인된 variant가 품절됨");
    await changeValue(rationale as HTMLTextAreaElement, "승인된 상품 옵션이 품절되어 구매할 수 없습니다.");
    expect((action as HTMLButtonElement).disabled).toBe(false);
    await act(async () => (action as HTMLButtonElement).click());

    expect(recordProcurementManualDecision).toHaveBeenCalledWith("task-mine", expect.objectContaining({
      decision: "UNABLE_TO_PURCHASE",
    }));
    expect(failProcurementTask).toHaveBeenCalledWith("task-mine", "PRODUCT_UNAVAILABLE");
    expect(beginProcurementMerchantEffect).not.toHaveBeenCalled();
    expect(createProcurementCustomerRequest).not.toHaveBeenCalled();
  });

  it("최근 구매 불가 판단이 있으면 근거 재입력 없이 Shop 실패·환불 행동을 연다", async () => {
    vi.mocked(getProcurementManualReview).mockResolvedValue({
      schemaVersion: "vitlane.procurement-manual-review.v1",
      decisions: [manualDecisionRecord("UNABLE_TO_PURCHASE", {
        publicRationale: "판매처에서 승인된 variant를 구매할 수 없습니다.",
      })],
      customerRequests: [],
    } as never);
    await renderAndOpenMineOrder();

    const decision = container.querySelector<HTMLSelectElement>('select[aria-label="판단"]');
    await changeValue(decision as HTMLSelectElement, "UNABLE_TO_PURCHASE");
    const fail = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("구매 불가 기록·해당 Shop 환불"));
    expect((fail as HTMLButtonElement).disabled).toBe(false);
  });

	it("placement가 시작된 뒤 기존 판단을 구매 불가로 덮지 않고 실패 처리를 별도 제공한다", async () => {
		const pendingBase = baseDetail({
			id: "task-pending", agencyOrderId: "order-pending", state: "IN_PROGRESS",
			assignedOperatorUserId: "operator-1",
		});
		vi.mocked(listOperatorWorkItems).mockResolvedValue({
			schemaVersion: "vitlane.ordering-operator-work-items.v1",
			counts: { PAYMENT_RECONCILIATION: 0, PROCESS_INTERVENTION: 0, PROCUREMENT_EXECUTION: 1, REFUND_REVIEW: 0, DELIVERY_RESOLUTION: 0, RETURN_PROGRESS: 0 },
			items: [procurementItem({
				id: "task-pending", agencyOrderId: "order-pending", state: "IN_PROGRESS",
				actions: ["RECORD_PLACED", "RECORD_FAILURE"],
				detail: {
					...pendingBase,
					merchantOrder: { ...pendingBase.merchantOrder, state: "PLACEMENT_PENDING" },
				},
				assignedOperatorUserId: "operator-1",
			})],
		} as never);
		vi.mocked(getProcurementManualReview).mockResolvedValue({
			schemaVersion: "vitlane.procurement-manual-review.v1",
			decisions: [manualDecisionRecord("WITHIN_AUTHORIZATION", {
				taskId: "task-pending", agencyOrderId: "order-pending",
			})],
			customerRequests: [],
		} as never);
		await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
		await act(async () => Promise.resolve());
		await act(async () => Promise.resolve());
		const summary = [...container.querySelectorAll("button")]
			.find((item) => item.textContent?.includes("Commuter Pack"));
		await act(async () => summary?.click());
		await act(async () => Promise.resolve());

		expect(container.textContent).toContain("승인 범위 내");
		expect(container.querySelector('select[aria-label="판단"]')).toBeNull();
		expect(container.querySelector('textarea[aria-label="고객 공개 근거"]')).toBeNull();
		const startFailure = [...container.querySelectorAll("button")]
			.find((item) => item.textContent?.includes("구매 실패 처리 시작"));
		expect(startFailure).not.toBeUndefined();
		await act(async () => (startFailure as HTMLButtonElement).click());
		expect(container.textContent).toContain("merchant 주문 시도가 이미 시작되었습니다");
		expect(container.textContent).toContain("구매 실패·환불 처리");
		expect(container.querySelector('textarea[aria-label="고객 공개 근거"]')).not.toBeNull();
		expect(container.querySelector('select[aria-label="판단"]')).toBeNull();
	});

  it("Sandbox placement는 실제 merchant effect로 오인하지 않는 TEST 증거 전체를 기록한다", async () => {
    const pendingBase = baseDetail({
      id: "task-test-evidence", agencyOrderId: "order-test-evidence", state: "IN_PROGRESS",
      assignedOperatorUserId: "operator-1",
    });
    vi.mocked(listOperatorWorkItems).mockResolvedValue({
      schemaVersion: "vitlane.ordering-operator-work-items.v1",
      counts: { PAYMENT_RECONCILIATION: 0, PROCESS_INTERVENTION: 0, PROCUREMENT_EXECUTION: 1, REFUND_REVIEW: 0, DELIVERY_RESOLUTION: 0, RETURN_PROGRESS: 0 },
      items: [procurementItem({
        id: "task-test-evidence", agencyOrderId: "order-test-evidence", state: "IN_PROGRESS",
        actions: ["REVEAL_SHIPPING", "RECORD_PLACED", "RECORD_FAILURE"],
        detail: {
          ...pendingBase,
          merchantOrder: { ...pendingBase.merchantOrder, state: "PLACEMENT_PENDING" },
        },
        assignedOperatorUserId: "operator-1",
      })],
    } as never);

    await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    const summary = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => summary?.click());
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("TEST 구매 증거");
    expect(container.textContent).toContain("실제 merchant 구매가 발생했다고 주장하지 않습니다");
    const submit = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("TEST 구매 증거 기록")) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);

		await changeValue(
      container.querySelector('input[placeholder="예: TEST-ORDER-8842"]') as HTMLInputElement,
			"https://merchant.example/order/8842",
		);
		expect(submit.disabled).toBe(true);
		expect(container.textContent).toContain("URL·줄바꿈·탭이 없는 1–255자 opaque 참조");
		await changeValue(
      container.querySelector('input[placeholder="예: TEST-ORDER-8842"]') as HTMLInputElement,
      "TEST-ORDER-8842",
    );
    await changeValue(
      container.querySelector('input[placeholder="opaque 영수증 참조"]') as HTMLInputElement,
      "receipt:test:8842",
    );
    const amountMode = container.querySelectorAll<HTMLSelectElement>(".agency-order-operator__placement-evidence select")[0];
    await changeValue(amountMode, "CHANGED");
    await changeValue(
			container.querySelector('input[placeholder="예: 52.23"]') as HTMLInputElement,
			"30.001",
    );
		expect(submit.disabled).toBe(true);
		expect(container.textContent).toContain("소수점 최대 2자리");
		await changeValue(
			container.querySelector('input[placeholder="예: 52.23"]') as HTMLInputElement,
			"30.00",
		);
    await changeValue(
			container.querySelectorAll<HTMLSelectElement>(".agency-order-operator__placement-evidence select")[1],
      "MERCHANT_PAGE",
    );
		expect(container.textContent).toContain("opaque ID만, URL 불가. 1~255자 · 현재 15자");
		expect(container.textContent).toContain("서버가 기록 승인 시 SHA-256 증거 해시와 관찰 시각을 자동 생성합니다");
		expect(container.querySelector('input[aria-label="구매 증거 해시"]')).toBeNull();
		expect(container.querySelector('input[aria-label="구매 관찰 시각"]')).toBeNull();
    expect(submit.disabled).toBe(false);
    await act(async () => submit.click());

    expect(completeProcurementTask).toHaveBeenCalledWith("task-test-evidence", {
      evidenceKind: "SANDBOX_TEST_EVIDENCE",
      amountMode: "CHANGED",
      externalOrderRef: "TEST-ORDER-8842",
      receiptSafeRef: "receipt:test:8842",
      actualAmountMinor: 3000,
      evidenceSource: "MERCHANT_PAGE",
    });
  });

  it("Live placement는 실지출 경고와 Live 증거 필드를 명시한다", async () => {
    const pendingBase = baseDetail({
      id: "task-live-evidence", agencyOrderId: "order-live-evidence", state: "IN_PROGRESS",
      assignedOperatorUserId: "operator-1",
    });
    pendingBase.merchantOrder = {
      ...pendingBase.merchantOrder,
      executionMode: "LIVE_MERCHANT_EFFECT",
      state: "PLACEMENT_PENDING",
    };
    pendingBase.agencyOrder.paymentSelection = {
      rail: "PAYPAL", providerEnvironment: "LIVE", asset: "USD",
      economicEffect: "REAL_MONEY", merchantExecution: "LIVE",
    };
    vi.mocked(listOperatorWorkItems).mockResolvedValue({
      schemaVersion: "vitlane.ordering-operator-work-items.v1",
      counts: { PAYMENT_RECONCILIATION: 0, PROCESS_INTERVENTION: 0, PROCUREMENT_EXECUTION: 1, REFUND_REVIEW: 0, DELIVERY_RESOLUTION: 0, RETURN_PROGRESS: 0 },
      items: [procurementItem({
        id: "task-live-evidence", agencyOrderId: "order-live-evidence", state: "IN_PROGRESS",
        actions: ["REVEAL_SHIPPING", "RECORD_PLACED", "RECORD_FAILURE"],
        detail: pendingBase,
        assignedOperatorUserId: "operator-1",
      })],
    } as never);

    await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    const summary = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => summary?.click());

    expect(container.querySelector(".agency-order-operator__placement-evidence.is-live")).not.toBeNull();
    expect(container.textContent).toContain("Live merchant effect 증거");
    expect(container.textContent).toContain("판매처에서 실제 돈이 지출됐다는 기록입니다");
		expect(container.querySelector('input[placeholder="실제 주문 번호"]')).not.toBeNull();
		expect(container.querySelector('input[placeholder="예: 52.23"]')).toBeNull();
		expect(container.textContent).toContain("변동 없음 · 승인액 $30.30 사용");
		const amountMode = container.querySelectorAll<HTMLSelectElement>(".agency-order-operator__placement-evidence select")[0];
		await changeValue(amountMode, "CHANGED");
		expect(container.querySelector('input[placeholder="예: 52.23"]')).not.toBeNull();
		expect(container.textContent).toContain("실제 결제액(USD)");
    const submit = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("Live 구매 증거 기록")) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
  });

  it("기록된 Sandbox TEST 증거를 후속 심사용 안전 참조와 함께 보여준다", async () => {
    const placedBase = baseDetail(
      {
        id: "task-recorded-test", agencyOrderId: "order-recorded-test", state: "SUCCEEDED",
        assignedOperatorUserId: "operator-1",
      },
      {
        processState: "LOGISTICS_IN_PROGRESS",
        logisticsSummary: { expectedUnits: 1, deliveredUnits: 0, exceptionUnits: 0 },
      },
    );
    const placedDetail = {
      ...placedBase,
      merchantOrder: {
        ...placedBase.merchantOrder,
        state: "PLACED",
        externalOrderRef: "TEST-ORDER-RECORDED",
        placementEvidence: {
          kind: "SANDBOX_TEST_EVIDENCE",
          externalOrderRef: "TEST-ORDER-RECORDED",
          receiptSafeRef: "receipt:test:recorded",
          actualAmountMinor: 3030,
          currency: "USD",
          evidenceSource: "RECEIPT",
          evidenceHash: "sha256:recorded-test-placement",
          observedAt: "2026-08-27T09:30:00Z",
          recordedByUserId: "operator-1",
          recordedAt: "2026-08-27T09:31:00Z",
          claimsExternalLiveEffect: false,
        },
      },
    };
    vi.mocked(listOperatorWorkItems).mockResolvedValue({
      schemaVersion: "vitlane.ordering-operator-work-items.v1",
      counts: { PAYMENT_RECONCILIATION: 0, PROCESS_INTERVENTION: 0, PROCUREMENT_EXECUTION: 1, REFUND_REVIEW: 0, DELIVERY_RESOLUTION: 0, RETURN_PROGRESS: 0 },
      items: [procurementItem({
        id: "task-recorded-test", agencyOrderId: "order-recorded-test", state: "SUCCEEDED",
        actions: [], detail: placedDetail, assignedOperatorUserId: "operator-1",
      })],
    } as never);

    await act(async () => root.render(<MemoryRouter><AgencyOrderOperatorPage /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    const allScope = [...container.querySelectorAll('[aria-label="담당 범위"] button')]
      .find((item) => item.textContent?.startsWith("전체"));
    await act(async () => (allScope as HTMLButtonElement).click());
    const logisticsTab = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.startsWith("배송"));
    await act(async () => (logisticsTab as HTMLButtonElement).click());
    const summary = [...container.querySelectorAll("button")]
      .find((item) => item.textContent?.includes("Commuter Pack"));
    await act(async () => summary?.click());

    expect(container.textContent).toContain("기록된 TEST 구매 증거");
    expect(container.textContent).toContain("실제 merchant 구매를 주장하지 않는 기록입니다");
    expect(container.textContent).toContain("TEST-ORDER-RECORDED");
    expect(container.textContent).toContain("receipt:test:recorded");
    expect(container.textContent).toContain("$30.30");
    expect(container.textContent).toContain("sha256:recorded-test-placement");
    expect(container.textContent).toContain("operator-1");
  });
});

describe("Procurement 고객 응답 표시", () => {
  it("TEXT·choice·동의를 실제 응답값으로 표시한다", () => {
    const base = {
      id: "request-1", merchantOrderId: "mo-1", agencyOrderId: "order-1",
      sourceDecisionId: "decision-1",
      kind: "INFORMATION" as const, prompt: "Choose", responseType: "TEXT" as const,
      publicContext: "Why", requestedAt: "2026-08-21T00:00:00Z",
      dueAt: "2026-08-28T00:00:00Z", version: 2,
    };
    expect(procurementCustomerResponseLabel({ ...base, state: "ANSWERED", response: { text: "Navy" } })).toBe("Navy");
    expect(procurementCustomerResponseLabel({ ...base, state: "ANSWERED", response: { choice: "Large" } })).toBe("Large");
    expect(procurementCustomerResponseLabel({ ...base, state: "ANSWERED", response: { accepted: true } })).toBe("동의함");
  });
});
