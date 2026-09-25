// @vitest-environment jsdom

import { act } from "react";
import { MemoryRouter } from "react-router";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getOrderAccounting } from "../infra/agencyOrderOperatorApi";
import { OrderAccountingPage } from "./OrderAccountingPage";

vi.mock("../infra/agencyOrderOperatorApi", () => ({
  getOrderAccounting: vi.fn(),
}));

const firstMO = {
  allocationId: "allocation-1", merchantOrderId: "mo-1", shopDomain: "basalt.example.com", checkoutOrdinal: 1,
  passThroughMinor: 10_000, feeVariableMinor: 540, feeFixedMinor: 30, feeTotalMinor: 570,
  customerGrossMinor: 10_570, feePolicyVersion: "PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1",
  fundingState: "ACTIVE", merchantOrderState: "PLACED", merchantPaymentState: "SUCCEEDED",
  actualCustomerGrossInMinor: 10_570, actualProcessorFeeMinor: 496, actualNetCashInMinor: 10_074,
  actualMerchantSpendMinor: 10_000, actualCustomerCompensatedMinor: 0, actualMerchantRecoveredMinor: 500,
  unreconciledCashGrossMinor: 0, realizedBalanceMinor: 574,
  forecastNetCashInMinor: 0, forecastProcessorFeeMinor: 0, forecastMerchantSpendMinor: 0,
  expectedCompensationMinor: 0, forecastAdjustmentMinor: 0, forecastBalanceMinor: 574,
  attentionReasons: [],
};

const secondMO = {
  allocationId: "allocation-2", merchantOrderId: "mo-2", shopDomain: "cobalt.example.com", checkoutOrdinal: 2,
  passThroughMinor: 20_000, feeVariableMinor: 1_080, feeFixedMinor: 30, feeTotalMinor: 1_110,
  customerGrossMinor: 21_110, feePolicyVersion: "PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1",
  fundingState: "AVAILABLE", merchantOrderState: "PLANNED",
  actualCustomerGrossInMinor: 0, actualProcessorFeeMinor: 0, actualNetCashInMinor: 0,
  actualMerchantSpendMinor: 0, actualCustomerCompensatedMinor: 0, actualMerchantRecoveredMinor: 0,
  unreconciledCashGrossMinor: 0, realizedBalanceMinor: 0,
  forecastNetCashInMinor: 20_151, forecastProcessorFeeMinor: 959, forecastMerchantSpendMinor: 20_000,
  expectedCompensationMinor: 0, forecastAdjustmentMinor: 151, forecastBalanceMinor: 151,
  attentionReasons: [],
};

const sandboxResponse = {
  schemaVersion: "vitlane.order-accounting.v2" as const,
  summary: {
    providerEnvironment: "SANDBOX" as const, currency: "USD" as const,
    actualCustomerGrossInMinor: 10_570, actualProcessorFeeMinor: 496, actualNetCashInMinor: 10_074,
    actualMerchantSpendMinor: 10_000, actualCustomerCompensatedMinor: 0, actualMerchantRecoveredMinor: 500,
    unreconciledCashGrossMinor: 0, realizedBalanceMinor: 574,
    forecastNetCashInMinor: 20_151, forecastProcessorFeeMinor: 959, forecastMerchantSpendMinor: 20_000,
    expectedCompensationMinor: 0, forecastAdjustmentMinor: 151, forecastBalanceMinor: 725,
    orderCount: 1, attentionOrderCount: 0, asOf: "2026-08-27T00:00:00Z",
    orders: [{
      agencyOrderId: "order-1", customerPaymentId: "payment-1", rail: "PAYPAL" as const,
      providerEnvironment: "SANDBOX" as const, paymentState: "PARTIALLY_CAPTURED", currency: "USD" as const,
      actualCustomerGrossInMinor: 10_570, actualProcessorFeeMinor: 496, actualNetCashInMinor: 10_074,
      actualMerchantSpendMinor: 10_000, actualCustomerCompensatedMinor: 0, actualMerchantRecoveredMinor: 500,
      unreconciledCashGrossMinor: 0, realizedBalanceMinor: 574,
      forecastNetCashInMinor: 20_151, forecastProcessorFeeMinor: 959, forecastMerchantSpendMinor: 20_000,
      expectedCompensationMinor: 0, forecastAdjustmentMinor: 151, forecastBalanceMinor: 725,
      requiresAttention: false, attentionReasons: [], merchantOrders: [firstMO, secondMO],
      events: [{
        id: "cash-1", kind: "CUSTOMER_CASH_IN" as const, direction: "CREDIT" as const,
        merchantOrderId: "mo-1", allocationId: "allocation-1", amountMinor: 10_074,
        customerGrossMinor: 10_570, processorFeeMinor: 496, economicsReconciled: true,
        source: "PAYPAL_MO_CAPTURE", occurredAt: "2026-08-27T00:01:00Z",
      }, {
        id: "spend-1", kind: "MERCHANT_PURCHASE" as const, direction: "DEBIT" as const,
        merchantOrderId: "mo-1", allocationId: "allocation-1", amountMinor: 10_000,
        economicsReconciled: true, source: "MERCHANT_PAYMENT", occurredAt: "2026-08-27T00:02:00Z",
      }, {
        id: "recovery-1", kind: "MERCHANT_RECOVERY" as const, direction: "CREDIT" as const,
        merchantOrderId: "mo-1", allocationId: "allocation-1", amountMinor: 500,
        economicsReconciled: true, source: "PROCUREMENT_RECOVERY", occurredAt: "2026-08-27T00:03:00Z",
      }, {
        id: "void-2", kind: "AUTHORIZATION_RELEASE" as const, direction: "NEUTRAL" as const,
        merchantOrderId: "mo-2", allocationId: "allocation-2", amountMinor: 0,
        customerGrossMinor: 21_110, economicsReconciled: true, source: "MO_COMPENSATION",
        cause: "CUSTOMER_CANCEL_PRE_EFFECT", occurredAt: "2026-08-27T00:04:00Z",
      }],
      createdAt: "2026-08-27T00:00:00Z",
    }],
  },
};

describe("OrderAccountingPage", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    vi.mocked(getOrderAccounting).mockResolvedValue(sandboxResponse);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.clearAllMocks();
  });

  it("실제 이벤트와 forecast를 분리하고 주문·MO·중립 승인 해제를 한 원장에 표시한다", async () => {
    await act(async () => root.render(<MemoryRouter><OrderAccountingPage /></MemoryRouter>));
    await act(async () => Promise.resolve());

    expect(getOrderAccounting).toHaveBeenCalledWith("LIVE");
    expect(container.textContent).toContain("주문별 현금 원장");
    expect(container.textContent).toContain("실현 잔고");
    expect(container.textContent).toContain("예상 조정");
    expect(container.textContent).toContain("예상 잔고");
    expect(container.querySelector('[aria-label="전체 잔고 계산"]')?.textContent).toContain("US$7.25");

    expect(container.textContent).toContain("basalt.example.com");
    expect(container.textContent).toContain("cobalt.example.com");
    expect(container.textContent).toContain("고객 수납");
    expect(container.textContent).toContain("상점 구매");
    expect(container.textContent).toContain("상점 회수");
    expect(container.textContent).toContain("승인 해제");
    expect(container.querySelector('[aria-label="주문 현금 이벤트 타임라인"]')?.textContent).toContain("현금 이동 없음");

    expect(container.textContent).not.toContain("Vitlane 부담");
    expect(container.textContent).not.toContain("지급 의무");
    expect(container.textContent).not.toContain("커버리지");
  });

  it("SANDBOX·LIVE·TESTNET을 별도 필터로 요청한다", async () => {
    await act(async () => root.render(<MemoryRouter><OrderAccountingPage /></MemoryRouter>));
    await act(async () => Promise.resolve());

    const select = container.querySelector<HTMLSelectElement>('[aria-label="회계 환경"]');
    expect([...select!.options].map((option) => option.value)).toEqual(["LIVE", "SANDBOX", "TESTNET"]);
    vi.mocked(getOrderAccounting).mockResolvedValue({
      ...sandboxResponse,
      summary: { ...sandboxResponse.summary, providerEnvironment: "TESTNET", orderCount: 0, orders: [] },
    });
    await act(async () => {
      select!.value = "TESTNET";
      select!.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await act(async () => Promise.resolve());

    expect(getOrderAccounting).toHaveBeenLastCalledWith("TESTNET");
    expect(container.textContent).toContain("TESTNET · tVitUSDC");
  });
});
