// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AgencyOrderPaymentPage } from "./AgencyOrderPaymentPage";
import { APIError } from "../../../shared/api/client";
import { LocaleProvider } from "../../../shared/i18n";
import { runWalletRegistrationFlow } from "../../account/app/walletRegistrationFlow";
import { getAccountOverview } from "../../account/infra/accountApi";
import { readSettlementAssetStatus } from "../../payment/giwa/infra/wallet";
import { getSettlementConfig } from "../../payment/giwa/infra/settlementApi";
import {
  authorizeAgencyOrder,
  getAgencyOrder,
  getAgencyOrderCapability,
  getAgencyOrderSettlement,
  getPayPalCheckout,
  resumePayPalCheckout,
  startPayPalCheckout,
} from "../infra/agencyOrderApi";
import type { AgencyOrderCapability } from "../infra/agencyOrderApi";

function paymentCapabilityFixture(liveState: "READY" | "PAUSED" = "PAUSED") {
  const rail = (method: "TVITUSD" | "PAYPAL_SANDBOX" | "PAYPAL_LIVE", state: "READY" | "PAUSED" | "UNAVAILABLE"): AgencyOrderCapability["paymentRails"]["paypalLive"] => ({
    state, orderIssueState: state, paymentInitiationState: state,
    paymentMethod: method, providerEnvironment: method === "TVITUSD" ? "TESTNET" : method === "PAYPAL_SANDBOX" ? "SANDBOX" : "LIVE",
    asset: method === "TVITUSD" ? "TVITUSD" : "USD",
    economicEffect: method === "PAYPAL_LIVE" ? "REAL_MONEY" : "NO_REAL_VALUE",
  });
  return { schemaVersion: "vitlane.agency-order-capability.v2" as const, capability: {
    state: "READY" as const, checkoutProvider: "SHOPIFY" as const, capabilityRevision: 9,
    paymentRails: { tvitusd: rail("TVITUSD", "READY"), paypalSandbox: rail("PAYPAL_SANDBOX", "READY"), paypalLive: rail("PAYPAL_LIVE", liveState) },
  } };
}

vi.mock("../../account/app/walletRegistrationFlow", () => ({
  runWalletRegistrationFlow: vi.fn(),
}));
vi.mock("../../account/infra/accountApi", () => ({
  TEST_SETTLEMENT_POLICY_VERSION: "2026-07-24",
  acceptTestSettlementPolicy: vi.fn(),
  getAccountOverview: vi.fn(),
}));
vi.mock("../../payment/giwa/infra/settlementApi", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../payment/giwa/infra/settlementApi")>()),
  getSettlementConfig: vi.fn(),
}));
vi.mock("../../payment/giwa/infra/wallet", () => ({
  approveExact: vi.fn(),
  pay: vi.fn(),
  readSettlementAssetStatus: vi.fn(),
}));
vi.mock("../../payment/giwa/iface/TestAssetPanel", () => ({
  openTestAssetsEvent: "vitlane:test-assets:open",
  testAssetsUpdatedEvent: "vitlane:test-assets:updated",
}));
vi.mock("../infra/agencyOrderApi", () => ({
  authorizeAgencyOrder: vi.fn(),
  customerActionOf: (projection: { availableActions?: { kind: string }[] }, kind: string) =>
    (projection.availableActions ?? []).find((action) => action.kind === kind),
  getAgencyOrder: vi.fn(),
  getAgencyOrderCapability: vi.fn(),
  getAgencyOrderSettlement: vi.fn(),
  getPayPalCheckout: vi.fn(),
  resumePayPalCheckout: vi.fn(),
  startPayPalCheckout: vi.fn(),
  submitAgencyOrderPayTransaction: vi.fn(),
  submitAgencyOrderWalletTransaction: vi.fn(),
}));

const payer = "0xa0Ee7A142d267C1f36714E4a8F75612F20a79720";
const authorization = {
  id: "authorization-1",
  agencyOrderId: "order-1",
  authorization: {
    payer,
    token: "0x1111111111111111111111111111111111111111",
    passThroughAmount: "76870000",
    feeAmount: "770000",
    orderHash: `0x${"1".repeat(64)}`,
    merchantId: `0x${"2".repeat(64)}`,
    merchantRegistryVersion: 1,
    feeBps: 100,
    feeRecipient: "0x2222222222222222222222222222222222222222",
    principalRecipient: "0x3333333333333333333333333333333333333333",
    assuranceLevel: `0x${"3".repeat(64)}`,
    nonce: "1",
    payDeadline: 2_000_000_000,
    refundAfter: 2_000_003_600,
  },
  domain: {
    name: "Vitlane Settlement",
    version: "2",
    chainId: 91342,
    verifyingContract: "0x4444444444444444444444444444444444444444",
  },
  signer: "0x5555555555555555555555555555555555555555",
  typedDataHash: `0x${"4".repeat(64)}`,
  signature: `0x${"5".repeat(130)}`,
  createdAt: "2026-08-15T00:00:00Z",
} as const;

function livePayPalOrderResponse() {
  const paymentSelection = {
    rail: "PAYPAL", providerEnvironment: "LIVE", asset: "USD",
    economicEffect: "REAL_MONEY", merchantExecution: "LIVE",
  } as const;
  return {
    schemaVersion: "vitlane.agency-order-projection.v1",
    agencyOrder: {
      availableActions: [{ kind: "PAY", rail: "PAYPAL" }],
      agencyOrder: {
        id: "order-1", status: "ISSUED", lines: [],
        shippingAddress: {
          snapshotRef: "shipping-1", snapshotRevision: 1,
          snapshotHash: "shipping-hash", maskedSummary: "US · •••01", country: "US",
        },
        paymentSelection, merchantCheckouts: [],
        passThroughTotal: { amountMinor: 1000, currency: "USD" },
        agencyFee: {
          variable: { amountMinor: 54, currency: "USD" },
          fixed: { amountMinor: 30, currency: "USD" },
          total: { amountMinor: 84, currency: "USD" },
          policyVersion: "PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1",
        },
        customerPayableTotal: { amountMinor: 1084, currency: "USD" },
        snapshotHash: `0x${"6".repeat(64)}`,
        issuedAt: "2026-08-27T00:00:00Z", expiresAt: "2026-08-27T00:20:00Z",
      },
      paymentInstruction: {
        id: "instruction-live", agencyOrderId: "order-1",
        agencyOrderSnapshotHash: `0x${"6".repeat(64)}`,
        paymentSelection,
        customerPayableTotal: { amountMinor: 1084, currency: "USD" },
        paymentPolicyVersion: "PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1",
        state: "PENDING", expiresAt: "2026-08-27T00:20:00Z",
      },
      process: {}, executionUnits: [],
    },
  } as never;
}

describe("AgencyOrderPaymentPage", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);

    vi.mocked(getAgencyOrder).mockResolvedValue({
      schemaVersion: "vitlane.agency-order-projection.v1",
      agencyOrder: {
        availableActions: [{ kind: "PAY", rail: "GIWA" }],
        agencyOrder: {
          id: "order-1",
          status: "ISSUED",
          lines: [],
          shippingAddress: {
            snapshotRef: "shipping-1",
            snapshotRevision: 1,
            snapshotHash: "shipping-hash",
            maskedSummary: "US · •••01",
            country: "US",
          },
          merchantCheckouts: [],
          passThroughTotal: { amountMinor: 7687, currency: "USD" },
          agencyFee: {
            variable: { amountMinor: 77, currency: "USD" },
            fixed: { amountMinor: 0, currency: "USD" },
            total: { amountMinor: 77, currency: "USD" },
            policyVersion: "TVITUSD_1_PERCENT_2026_08_14",
          },
          customerPayableTotal: { amountMinor: 7764, currency: "USD" },
          snapshotHash: `0x${"6".repeat(64)}`,
          issuedAt: "2026-08-15T00:00:00Z",
          expiresAt: "2026-08-16T00:00:00Z",
        },
        paymentInstruction: {
          id: "instruction-1",
          agencyOrderId: "order-1",
          agencyOrderSnapshotHash: `0x${"6".repeat(64)}`,
          customerPayableTotal: { amountMinor: 7764, currency: "USD" },
          paymentPolicyVersion: "TVITUSD_1_PERCENT_2026_08_14",
          state: "CONSUMED",
          expiresAt: "2026-08-16T00:00:00Z",
        },
        process: {},
        executionUnits: [],
      },
    } as never);
    vi.mocked(getSettlementConfig).mockResolvedValue({
      settlement: {
        environment: "LOCAL",
        chainId: 91342,
        chainCaip2: "eip155:91342",
        rpcUrl: "http://127.0.0.1:28545",
        explorerUrl: "http://127.0.0.1:28545",
        tokenAddress: "0x1111111111111111111111111111111111111111",
        faucetAddress: "0x2222222222222222222222222222222222222222",
        settlementAddress: "0x4444444444444444444444444444444444444444",
        tokenSymbol: "tVITUSD",
        tokenDecimals: 6,
        feeBps: 100,
        feeRecipient: "0x3333333333333333333333333333333333333333",
        claimAmountBaseUnits: "1000000000",
      },
    });
    vi.mocked(getAccountOverview).mockResolvedValue({
      account: {
        wallets: [],
        buyerProfiles: [],
        shippingProfiles: [],
        policyAcceptances: [{
          policyId: "PHASE5_TEST_SETTLEMENT",
          policyVersion: "2026-07-24",
          acceptedAt: "2026-08-15T00:00:00Z",
        }],
        assurancePolicy: {},
      },
    } as never);
    vi.mocked(getAgencyOrderSettlement).mockResolvedValue({
      payment: {
        id: "payment-1",
        agencyOrderId: "order-1",
        orderHash: `0x${"1".repeat(64)}`,
        chainId: 91342,
        payer,
        settlementAddress: "0x4444444444444444444444444444444444444444",
        amountBaseUnits: "77640000",
        state: "AWAITING_ALLOWANCE",
        createdAt: "2026-08-15T00:00:00Z",
        updatedAt: "2026-08-15T00:00:00Z",
      },
      authorization,
    });
    vi.mocked(runWalletRegistrationFlow).mockResolvedValue({
      account: payer,
      wallet: { wallet: { id: "wallet-1" } },
      ownershipProof: { id: "proof-1" },
      replay: true,
      reused: true,
      provider: {},
    } as never);
    vi.mocked(readSettlementAssetStatus).mockResolvedValue({
      account: payer,
      tokenBalance: 0n,
      nativeBalance: 0n,
      allowance: 0n,
    });
    vi.mocked(authorizeAgencyOrder).mockResolvedValue(authorization as never);
    vi.mocked(getAgencyOrderCapability).mockResolvedValue(paymentCapabilityFixture("READY"));
    vi.mocked(getPayPalCheckout).mockRejectedValue(new Error("not started"));
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.clearAllMocks();
  });

  it("새로고침 뒤 기존 authorization을 복원하고 지갑 인증 후 조건 확정을 반복하지 않는다", async () => {
    await act(async () => {
      root.render(
        <MemoryRouter initialEntries={["/agencyOrder/order-1/payment"]}>
          <Routes>
            <Route path="/agencyOrder/:agencyOrderId/payment" element={<AgencyOrderPaymentPage />} />
          </Routes>
        </MemoryRouter>,
      );
    });
    await settle();

    await act(async () => button("결제 지갑 인증").click());
    await settle();

    expect(container.textContent).toContain("주문 금액의 결제 조건을 확정했습니다");
    expect(container.textContent).toContain("결제 조건 확정은 완료됐지만 tVITUSD 잔액이 부족합니다");
    expect(container.textContent).not.toContain("주문 금액으로 결제 조건 확정");
    expect(authorizeAgencyOrder).not.toHaveBeenCalled();
  });

  it("잔액 부족일 때만 Faucet을 열고 잔액 갱신 뒤 조건 확정을 활성화한다", async () => {
    vi.mocked(getAgencyOrderSettlement).mockRejectedValueOnce(new Error("not issued"));
    const faucetEvents: Event[] = [];
    const recordFaucetEvent = (event: Event) => faucetEvents.push(event);
    window.addEventListener("vitlane:test-assets:open", recordFaucetEvent);

    try {
      await act(async () => {
        root.render(
          <MemoryRouter initialEntries={["/agencyOrder/order-1/payment"]}>
            <Routes>
              <Route path="/agencyOrder/:agencyOrderId/payment" element={<AgencyOrderPaymentPage />} />
            </Routes>
          </MemoryRouter>,
        );
      });
      await settle();

      await act(async () => button("결제 지갑 인증").click());
      await settle();

      const confirm = button("주문 금액으로 결제 조건 확정");
      expect(confirm.disabled).toBe(true);
      expect(container.textContent).toContain("이 주문을 결제할 tVITUSD 잔액이 부족합니다");
      expect(faucetEvents).toHaveLength(1);

      vi.mocked(readSettlementAssetStatus).mockResolvedValue({
        account: payer,
        tokenBalance: 100_000_000n,
        nativeBalance: 1_000_000_000_000_000_000n,
        allowance: 0n,
      });
      await act(async () => {
        window.dispatchEvent(new CustomEvent("vitlane:test-assets:updated"));
      });
      await settle();

      expect(confirm.disabled).toBe(false);
      expect(faucetEvents).toHaveLength(1);
      await act(async () => confirm.click());
      await settle();
      expect(authorizeAgencyOrder).toHaveBeenCalledWith("order-1", "wallet-1", "proof-1");
    } finally {
      window.removeEventListener("vitlane:test-assets:open", recordFaucetEvent);
    }
  });

  it.each([
    ["en-US", "Authenticate payment wallet", "Confirm payment terms for order total", "Your wallet verification has changed or expired. Reload this page and authenticate the payment wallet again."],
    ["ko-KR", "결제 지갑 인증", "주문 금액으로 결제 조건 확정", "지갑 인증이 변경되었거나 만료됐습니다. 이 페이지를 새로고침한 뒤 결제 지갑을 다시 인증해 주세요."],
  ])("%s에서 proof 거절 뒤 재인증 안내를 표시하고 승인을 자동 재시도하지 않는다", async (locale, authenticate, confirm, guidance) => {
    document.cookie = `vt_locale_choice=${locale}; Path=/`;
    vi.mocked(getAgencyOrderSettlement).mockRejectedValueOnce(new Error("not issued"));
    vi.mocked(readSettlementAssetStatus).mockResolvedValue({ account: payer, tokenBalance: 100_000_000n, nativeBalance: 1_000_000_000_000_000_000n, allowance: 0n });
    vi.mocked(authorizeAgencyOrder).mockRejectedValueOnce(new APIError("SETTLEMENT_WALLET_OWNERSHIP_REQUIRED", "generic error", 422));
    try {
      await act(async () => { root.render(<LocaleProvider><MemoryRouter initialEntries={["/agencyOrder/order-1/payment"]}><Routes><Route path="/agencyOrder/:agencyOrderId/payment" element={<AgencyOrderPaymentPage />} /></Routes></MemoryRouter></LocaleProvider>); });
      await settle();
      await act(async () => button(authenticate).click());
      await settle();
      await act(async () => button(confirm).click());
      await settle();
      expect(container.querySelector(".agency-payment-error")?.textContent).toContain(guidance);
      expect(authorizeAgencyOrder).toHaveBeenCalledTimes(1);
      expect(container.textContent).not.toContain("generic error");
    } finally { document.cookie = "vt_locale_choice=; Max-Age=0; Path=/"; }
  });

  it("주문 확인 대기를 실패나 결제 완료로 표시하지 않고 같은 승인을 다시 조회한다", async () => {
    vi.mocked(getAgencyOrderSettlement).mockRejectedValueOnce(new Error("not issued"));
    vi.mocked(readSettlementAssetStatus).mockResolvedValue({ account: payer, tokenBalance: 100_000_000n, nativeBalance: 1_000_000_000_000_000_000n, allowance: 0n });
    vi.mocked(authorizeAgencyOrder).mockResolvedValueOnce({ schemaVersion: "vitlane.payment-instruction-confirmation.v1", outcome: "WAITING", reasonCode: "PAYMENT_INSTRUCTION_CONFIRMATION_PENDING" });
    await act(async () => { root.render(<MemoryRouter initialEntries={["/agencyOrder/order-1/payment"]}><Routes><Route path="/agencyOrder/:agencyOrderId/payment" element={<AgencyOrderPaymentPage />} /></Routes></MemoryRouter>); });
    await settle();
    await act(async () => button("결제 지갑 인증").click());
    await settle();
    await act(async () => button("주문 금액으로 결제 조건 확정").click());
    await settle();
    expect(container.textContent).toContain("주문 확인이 대기 중입니다.");
    expect(container.textContent).toContain("결제는 제출되지 않았습니다.");
    expect(container.querySelector(".agency-payment-error")).toBeNull();
    expect(container.textContent).not.toContain("주문 금액의 결제 조건을 확정했습니다.");
    await act(async () => button("주문 확인 상태 조회").click());
    await settle();
    expect(authorizeAgencyOrder).toHaveBeenNthCalledWith(2, "order-1", "wallet-1", "proof-1");
    expect(container.textContent).toContain("주문 금액의 결제 조건을 확정했습니다.");
    expect(container.textContent).not.toContain("주문 확인이 대기 중입니다.");
  });

  it("잔액 RPC가 pending이어도 완료된 지갑 인증을 busy로 남기지 않는다", async () => {
    vi.mocked(getAgencyOrderSettlement).mockRejectedValueOnce(new Error("not issued"));
    vi.mocked(readSettlementAssetStatus).mockImplementationOnce(
      () => new Promise<never>(() => undefined),
    );

    await act(async () => {
      root.render(
        <MemoryRouter initialEntries={["/agencyOrder/order-1/payment"]}>
          <Routes>
            <Route path="/agencyOrder/:agencyOrderId/payment" element={<AgencyOrderPaymentPage />} />
          </Routes>
        </MemoryRouter>,
      );
    });
    await settle();

    await act(async () => button("결제 지갑 인증").click());
    await settle();

    const confirm = button("주문 금액으로 결제 조건 확정");
    expect(confirm.disabled).toBe(true);
    expect(confirm.getAttribute("aria-busy")).toBeNull();
    expect(container.textContent).toContain("결제 지갑의 tVITUSD 잔액을 확인하고 있습니다");
  });

  it("PayPal LIVE issue-only 주문은 실제 금액 경고를 유지하되 승인 CTA를 숨긴다", async () => {
    vi.mocked(getAgencyOrder).mockResolvedValueOnce(livePayPalOrderResponse());
    vi.mocked(getAgencyOrderCapability).mockResolvedValueOnce(paymentCapabilityFixture("PAUSED"));
    vi.mocked(getPayPalCheckout).mockResolvedValueOnce({
      schemaVersion: "vitlane.payment-paypal-checkout.v1",
      payment: {
        id: "payment-live", agencyOrderId: "order-1", rail: "PAYPAL",
        providerEnvironment: "LIVE", asset: "USD", economicEffect: "REAL_MONEY",
        amountMinor: 1100, currency: "USD", state: "ACTION_REQUIRED",
        updatedAt: "2026-08-27T00:01:00Z",
      },
      attempt: { id: "attempt-live", sequence: 1, state: "PAYER_ACTION_REQUIRED" },
      approvalUrl: "https://www.paypal.com/checkoutnow?token=live-order",
      returnNonce: "nonce-live",
    });

    await act(async () => {
      root.render(
        <MemoryRouter initialEntries={["/agencyOrder/order-1/payment"]}>
          <Routes>
            <Route path="/agencyOrder/:agencyOrderId/payment" element={<AgencyOrderPaymentPage />} />
          </Routes>
        </MemoryRouter>,
      );
    });
    await settle();

    expect(container.textContent).toContain("PayPal Live 결제");
    expect(container.textContent).toContain("LIVE 결제 · 실제 금액");
    expect(container.textContent).toContain("표시된 USD 총액을 승인합니다");
    expect(container.textContent).not.toContain("실제 청구는 없습니다");
    expect(container.textContent).toContain("PayPal 결제 시작이 일시 중지되었습니다");
		expect(container.textContent).not.toContain("PayPal에서 승인");
    expect(container.textContent).not.toContain("PayPal 승인 계속하기");
  });

	it("PayPal LIVE exact profile과 authorization gate가 열리면 승인 CTA를 표시한다", async () => {
    vi.mocked(getAgencyOrder).mockResolvedValueOnce(livePayPalOrderResponse());
    vi.mocked(getAgencyOrderCapability).mockResolvedValueOnce(paymentCapabilityFixture("READY"));

    await act(async () => {
      root.render(
        <MemoryRouter initialEntries={["/agencyOrder/order-1/payment"]}>
          <Routes>
            <Route path="/agencyOrder/:agencyOrderId/payment" element={<AgencyOrderPaymentPage />} />
          </Routes>
        </MemoryRouter>,
      );
    });
    await settle();

		expect(container.textContent).toContain("PayPal에서 승인");
    expect(container.textContent).not.toContain("PayPal 결제 시작이 일시 중지되었습니다");
  });

  it("historical PayPal 주문은 저장된 environment의 독립 capability로 CTA를 연다", async () => {
    vi.mocked(getAgencyOrder).mockResolvedValueOnce(livePayPalOrderResponse());

    await act(async () => {
      root.render(
        <MemoryRouter initialEntries={["/agencyOrder/order-1/payment"]}>
          <Routes>
            <Route path="/agencyOrder/:agencyOrderId/payment" element={<AgencyOrderPaymentPage />} />
          </Routes>
        </MemoryRouter>,
      );
    });
    await settle();

    expect(container.textContent).not.toContain("PayPal 결제 시작이 일시 중지되었습니다");
		expect(container.textContent).toContain("PayPal에서 승인");
  });

	it("결과 불명 Authorization은 재승인을 막고 같은 PayPal Order 재조회를 안내한다", async () => {
    vi.mocked(getAgencyOrder).mockResolvedValueOnce(livePayPalOrderResponse());
    vi.mocked(getPayPalCheckout).mockResolvedValueOnce({
      schemaVersion: "vitlane.payment-paypal-checkout.v1",
      payment: {
        id: "payment-review", agencyOrderId: "order-1", rail: "PAYPAL",
        providerEnvironment: "LIVE", asset: "USD", economicEffect: "REAL_MONEY",
				amountMinor: 1100, currency: "USD", state: "OUTCOME_UNKNOWN",
				lastReasonCode: "AUTHORIZATION_OUTCOME_UNKNOWN",
        updatedAt: "2026-08-27T00:01:00Z",
      },
      attempt: {
				id: "attempt-review", sequence: 1, state: "AUTHORIZE_OUTCOME_UNKNOWN",
				paypalOrderId: "paypal-order-review",
      },
    });

    await act(async () => {
      root.render(
        <MemoryRouter initialEntries={["/agencyOrder/order-1/payment"]}>
          <Routes>
            <Route path="/agencyOrder/:agencyOrderId/payment" element={<AgencyOrderPaymentPage />} />
          </Routes>
        </MemoryRouter>,
      );
    });
    await settle();

		expect(container.textContent).toContain("추가 결제를 시도하지 마세요.");
    expect(container.textContent).toContain("같은 PayPal Order");
		expect(container.textContent).toContain("추가 청구는 보내지 않습니다");
		expect(container.textContent).not.toContain("서버가 PayPal 결과를 확정하지 못했거나");
		expect(container.textContent).not.toContain("PayPal에서 승인");
  });

  function button(name: string) {
    const match = [...container.querySelectorAll<HTMLButtonElement>("button")]
      .find((candidate) => candidate.textContent?.includes(name));
    if (!match) throw new Error(`button not found: ${name}`);
    return match;
  }
});

async function settle() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}
