import { afterEach, describe, expect, it, vi } from "vitest";
import { getOrderAccounting, listOperatorWorkItemCounts } from "./agencyOrderOperatorApi";

describe("order accounting API contract", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("calls the v2 order-accounting route with an explicit environment", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({
      schemaVersion: "vitlane.order-accounting.v2",
      summary: { providerEnvironment: "TESTNET", orders: [] },
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    await getOrderAccounting("TESTNET");

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0][0]).toBe(
      "/api/v1/admin/ordering/order-accounting?environment=TESTNET",
    );
    expect(fetchMock.mock.calls[0][1]?.method).toBeUndefined();
  });

  it("reads the exact Live PayPal order count for operator navigation", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({
      schemaVersion: "vitlane.ordering-operator-work-item-counts.v2",
      counts: {
        PAYMENT_RECONCILIATION: 0,
        PROCESS_INTERVENTION: 0,
        PROCUREMENT_EXECUTION: 0,
        REFUND_REVIEW: 0,
        DELIVERY_RESOLUTION: 0,
        RETURN_PROGRESS: 0,
      },
      livePayPalOrderCount: 3,
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    const response = await listOperatorWorkItemCounts();

    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/admin/ordering/work-items/counts");
    expect(response.livePayPalOrderCount).toBe(3);
  });
});
