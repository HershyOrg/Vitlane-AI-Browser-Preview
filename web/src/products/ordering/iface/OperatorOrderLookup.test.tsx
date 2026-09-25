// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { APIError } from "../../../shared/api/client";
import {
  getOperatorOrderInvestigation,
  getOperatorOrderTimeline,
  lookupOperatorOrder,
} from "../infra/agencyOrderOperatorApi";
import { AgencyOrderInvestigationPage } from "./AgencyOrderInvestigationPage";
import { OperatorOrderLookup } from "./OperatorOrderLookup";

vi.mock("../infra/agencyOrderOperatorApi", () => ({
  lookupOperatorOrder: vi.fn(),
  getOperatorOrderInvestigation: vi.fn(),
  getOperatorOrderTimeline: vi.fn(),
}));

describe("operator exact order lookup", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.clearAllMocks();
  });

  it("submits an exact identifier and navigates to the canonical operator detail route", async () => {
    vi.mocked(lookupOperatorOrder).mockResolvedValue({
      schemaVersion: "vitlane.ordering-operator-order-lookup.v1",
      match: {
        agencyOrderId: "10000000-0000-4000-8000-000000000001",
        matchedBy: "PAYPAL_CAPTURE_ID",
        environment: "SANDBOX",
        paymentRail: "PAYPAL",
      },
    });
    await act(async () => root.render(
      <MemoryRouter initialEntries={["/admin/agencyOrder"]}>
        <Routes>
          <Route path="/admin/agencyOrder" element={<OperatorOrderLookup />} />
          <Route path="/admin/agencyOrder/:agencyOrderId" element={<p>canonical detail</p>} />
        </Routes>
      </MemoryRouter>,
    ));
    const input = container.querySelector<HTMLInputElement>("#order-lookup-value");
    expect(container.querySelector("#order-lookup-kind")).toBeNull();
    expect(container.textContent).toContain("상세 조회");
    await changeInput(input as HTMLInputElement, " CAPTURE-123 ");
    const submit = [...container.querySelectorAll("button")].find((button) => button.textContent?.trim() === "조회");
    await act(async () => (submit as HTMLButtonElement).click());
    await act(async () => Promise.resolve());
    expect(lookupOperatorOrder).toHaveBeenCalledWith(expect.objectContaining({
      identifierType: "AUTO", value: "CAPTURE-123", environment: "ANY",
    }));
    expect(container.textContent).toContain("canonical detail");
  });

  it("expands exact controls on demand and ignores hidden filters after collapse", async () => {
    vi.mocked(lookupOperatorOrder).mockResolvedValue({
      schemaVersion: "vitlane.ordering-operator-order-lookup.v1",
      match: {
        agencyOrderId: "10000000-0000-4000-8000-000000000001",
        matchedBy: "AGENCY_ORDER_ID",
        environment: "TESTNET",
        paymentRail: "GIWA",
      },
    });
    await act(async () => root.render(<MemoryRouter><OperatorOrderLookup /></MemoryRouter>));

    const toggle = [...container.querySelectorAll("button")].find((button) => button.textContent?.includes("상세 조회"));
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");
    expect(container.textContent).not.toContain("정확 일치 조건");

    await act(async () => (toggle as HTMLButtonElement).click());
    expect(toggle?.getAttribute("aria-expanded")).toBe("true");
    expect(container.textContent).toContain("정확 일치 조건");
    await changeSelect(container.querySelector("#order-lookup-kind") as HTMLSelectElement, "PAYPAL_CAPTURE_ID");
    await changeSelect(container.querySelector("#order-lookup-environment") as HTMLSelectElement, "SANDBOX");

    await act(async () => (toggle as HTMLButtonElement).click());
    expect(toggle?.getAttribute("aria-expanded")).toBe("false");
    expect(container.querySelector("#order-lookup-kind")).toBeNull();
    await changeInput(container.querySelector("#order-lookup-value") as HTMLInputElement, "order-id");
    const submit = [...container.querySelectorAll("button")].find((button) => button.textContent?.trim() === "조회");
    await act(async () => (submit as HTMLButtonElement).click());
    await act(async () => Promise.resolve());

    expect(lookupOperatorOrder).toHaveBeenCalledWith({
      identifierType: "AUTO",
      environment: "ANY",
      value: "order-id",
      shopDomain: undefined,
      carrier: undefined,
    });
  });

  it("localizes exact-not-found without exposing the server message", async () => {
    vi.mocked(lookupOperatorOrder).mockRejectedValue(new APIError(
      "ORDERING_OPERATOR_LOOKUP_NOT_FOUND", "server-only", 404,
    ));
    await act(async () => root.render(<MemoryRouter><OperatorOrderLookup /></MemoryRouter>));
    await changeInput(container.querySelector("#order-lookup-value") as HTMLInputElement, "missing");
    const submit = [...container.querySelectorAll("button")].find((button) => button.textContent?.trim() === "조회");
    await act(async () => (submit as HTMLButtonElement).click());
    await act(async () => Promise.resolve());
    expect(container.textContent).toContain("정확히 일치하는 주문이 없습니다");
    expect(container.textContent).not.toContain("server-only");
  });

  it("renders the safe identity rail and evidence checkpoints on the operator-only route", async () => {
    vi.mocked(getOperatorOrderInvestigation).mockResolvedValue(investigationFixture as never);
    await act(async () => root.render(
      <MemoryRouter initialEntries={["/admin/agencyOrder/10000000-0000-4000-8000-000000000001"]}>
        <Routes><Route path="/admin/agencyOrder/:agencyOrderId" element={<AgencyOrderInvestigationPage />} /></Routes>
      </MemoryRouter>,
    ));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    expect(getOperatorOrderInvestigation).toHaveBeenCalledWith("10000000-0000-4000-8000-000000000001");
    expect(container.textContent).toContain("여러 시스템에 걸친 하나의 주문");
    expect(container.textContent).toContain("CAPTURE-123");
    expect(container.textContent).toContain("PROCUREMENT_IN_PROGRESS");
    expect(container.textContent).toContain("불변 Shop 결제 경계");
    expect(container.textContent).toContain("US$35.00");
    expect(container.textContent).toContain("배분된 Vitlane 수수료가 포함");
    expect(container.textContent).toContain("physical-unit-1");
    expect(container.textContent).toContain("배송주소 원문");
    expect(container.textContent).not.toContain("providerSecret");
    // timeline은 요청 시 열람이다 — 조사 화면 로드가 자동으로 읽지 않는다.
    expect(getOperatorOrderTimeline).not.toHaveBeenCalled();
  });

  it("loads the process timeline on demand and groups events and commands by decision version", async () => {
    vi.mocked(getOperatorOrderInvestigation).mockResolvedValue(investigationFixture as never);
    vi.mocked(getOperatorOrderTimeline).mockResolvedValue({
      schemaVersion: "vitlane.ordering-operator-order-timeline.v3",
      timeline: {
        agencyOrderId: "10000000-0000-4000-8000-000000000001",
        process: { state: "PROCUREMENT_IN_PROGRESS", version: 2, lastAppliedSeq: 3, updatedAt: "2026-08-27T01:00:00Z" },
        requests: [], decisions: [
          { version: 1, seqFrom: 1, seqTo: 1, stageAfter: "WAITING_CUSTOMER_PAYMENT", stageChanged: true, merchantOrders: [], effects: [], processState: {}, decidedAt: "2026-08-27T00:10:00Z" },
          {
            version: 2, seqFrom: 2, seqTo: 3, stageBefore: "WAITING_CUSTOMER_PAYMENT", stageAfter: "PROCUREMENT_IN_PROGRESS", stageChanged: true,
            merchantOrders: [{ merchantOrderId: "merchant-order-1", phaseBefore: "PLANNED", phaseAfter: "PURCHASING", reason: "EFFECT_LOCK_STARTED" }],
            effects: [{ target: "LOGISTICS", type: "logistics.register_expected_units.v1", idempotencyKey: "k" }],
            processState: {}, decidedAt: "2026-08-27T01:00:00Z",
          },
        ],
        events: [
          { id: 11, seq: 1, source: "AGENCYORDER", type: "agencyorder.order.issued.v1", payload: {}, occurredAt: "2026-08-27T00:00:00Z", recordedAt: "2026-08-27T00:00:00Z", appliedVersion: 1 },
          { id: 12, seq: 2, source: "PAYMENT", type: "payment.customer_funding.ready.v2", payload: {}, occurredAt: "2026-08-27T00:50:00Z", recordedAt: "2026-08-27T00:50:00Z", appliedVersion: 2 },
          { id: 13, seq: 3, source: "PROCUREMENT", type: "procurement.effect_lock.state_changed.v1", payload: {}, occurredAt: "2026-08-27T00:55:00Z", recordedAt: "2026-08-27T00:55:00Z", appliedVersion: 2 },
          { id: 14, seq: 4, source: "CUSTOMER", type: "process.action.requested.v1", payload: {}, occurredAt: "2026-08-27T01:05:00Z", recordedAt: "2026-08-27T01:05:00Z" },
        ],
        effects: [{
          flowId: "flow-1", claimVersion: 0, effectId: "effect-1", target: "LOGISTICS", type: "logistics.register_expected_units.v1", deliveryState: "PENDING", idempotencyKey: "k",
          causedByEventId: 13, attemptCount: 5, nextAttemptAt: "2026-08-27T01:00:00Z", payload: {},
          createdAt: "2026-08-27T01:00:00Z",
        }],
      },
    });
    await act(async () => root.render(
      <MemoryRouter initialEntries={["/admin/agencyOrder/10000000-0000-4000-8000-000000000001"]}>
        <Routes><Route path="/admin/agencyOrder/:agencyOrderId" element={<AgencyOrderInvestigationPage />} /></Routes>
      </MemoryRouter>,
    ));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());
    const load = [...container.querySelectorAll("button")].find((button) => button.textContent?.includes("timeline 열기"));
    await act(async () => (load as HTMLButtonElement).click());
    await act(async () => Promise.resolve());
    expect(getOperatorOrderTimeline).toHaveBeenCalledWith("10000000-0000-4000-8000-000000000001");
    expect(container.textContent).toContain("WAITING_CUSTOMER_PAYMENT → PROCUREMENT_IN_PROGRESS");
    expect(container.textContent).toContain("seq 2–3");
    expect(container.textContent).toContain("아직 소비되지 않은 이벤트");
    expect(container.textContent).toContain("process.action.requested.v1");
    const decision = [...container.querySelectorAll("button")].find((button) => button.textContent?.includes("v2"));
    await act(async () => (decision as HTMLButtonElement).click());
    expect(container.textContent).toContain("procurement.effect_lock.state_changed.v1");
    expect(container.textContent).toContain("PLANNED → PURCHASING");
    expect(container.textContent).toContain("logistics.register_expected_units.v1");
    expect(container.textContent).toContain("PENDING");
  });
});

async function changeInput(element: HTMLInputElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(element, value);
    element.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function changeSelect(element: HTMLSelectElement, value: string) {
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")?.set?.call(element, value);
    element.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

const investigationFixture = {
  schemaVersion: "vitlane.ordering-operator-order-investigation.v1",
  investigation: {
    order: {
      agencyOrderId: "10000000-0000-4000-8000-000000000001", status: "ISSUED",
      issuedAt: "2026-08-27T00:00:00Z", expiresAt: "2026-08-28T00:00:00Z",
      snapshotHash: "snapshot-hash", executionProfileHash: "profile-hash",
      executionProfile: { paymentRail: "PAYPAL", providerEnvironment: "SANDBOX", asset: "USD", economicEffect: "NO_REAL_VALUE", merchantExecutionMode: "SIMULATED_NO_EFFECT" },
      customerPayableTotal: { amountMinor: 3500, currency: "USD" },
      passThroughTotal: { amountMinor: 3000, currency: "USD" },
      agencyFeeTotal: { amountMinor: 500, currency: "USD" },
      shippingMasked: "US · •••01", shippingCountry: "US",
      issuanceEvidence: { orderSheetSessionId: "sheet-1", displayedSnapshotHash: "display-hash", disclosureVersion: "v1", idempotencyKeyHash: "idem-hash" },
      lines: [{ lineId: "line-1", productTitle: "Commuter bag", quantity: 1, shopDomain: "shop.example", observedAt: "2026-08-27T00:00:00Z", evidenceHash: "line-hash" }],
    },
    process: { agencyOrderId: "10000000-0000-4000-8000-000000000001", state: "PROCUREMENT_IN_PROGRESS", version: 2, createdAt: "2026-08-27T00:00:00Z", updatedAt: "2026-08-27T01:00:00Z" },
    paymentInstruction: { id: "instruction-1", state: "CONSUMED", agencyOrderSnapshotHash: "snapshot-hash", executionProfileHash: "profile-hash", customerPayableTotal: { amountMinor: 3500, currency: "USD" }, paymentPolicyVersion: "v1", idempotencyKeyHash: "instruction-idem", createdAt: "2026-08-27T00:00:00Z", expiresAt: "2026-08-28T00:00:00Z" },
    identifiers: [
      { kind: "AGENCY_ORDER_ID", value: "10000000-0000-4000-8000-000000000001", relatedResourceId: "10000000-0000-4000-8000-000000000001" },
      { kind: "PAYPAL_CAPTURE_ID", value: "CAPTURE-123", relatedResourceId: "payment-1" },
    ],
    chainTransactions: [],
    merchantOrders: [{
      id: "merchant-order-1", allocationId: "allocation-1", shopDomain: "shop.example", merchantId: "merchant-1",
      checkoutOrdinal: 1, customerGrossAmount: { amountMinor: 3500, currency: "USD" }, executionMode: "SIMULATED_NO_EFFECT",
      state: "PLANNED", fundingState: "AVAILABLE", units: [{ id: "physical-unit-1", lineId: "line-1", unitIndex: 1, disposition: "PENDING" }],
      operational: {
        stage: "PROCUREMENT_PENDING", workStage: "PROCUREMENT",
        progress: { funding: "CURRENT", procurement: "CURRENT", delivery: "WAITING", resolution: "WAITING" },
        units: { total: 1, ordered: 1, procuring: 0, awaitingShipment: 0, inTransit: 0, delivered: 0, exception: 0, returnInProgress: 0, refundRequested: 0, refundPending: 0, refunded: 0, procurementFailed: 0, cancelled: 0 },
      },
      updatedAt: "2026-08-27T01:00:00Z",
    }],
    shipments: [], units: [{
      merchantOrderUnitId: "physical-unit-1", merchantOrderId: "merchant-order-1", allocationId: "allocation-1",
      lineId: "line-1", unitIndex: 1, shopDomain: "shop.example", stage: "ORDERED", refundStatus: "AVAILABLE",
    }],
    checkpoints: [{ kind: "PROCESS", state: "PROCUREMENT_IN_PROGRESS", observedAt: "2026-08-27T01:00:00Z" }],
  },
};
