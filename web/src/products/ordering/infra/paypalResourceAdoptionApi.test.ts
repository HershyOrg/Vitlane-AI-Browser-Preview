import { afterEach, describe, expect, it, vi } from "vitest";
import { adoptPayPalMORefund, adoptPayPalReauthorization } from "./agencyOrderOperatorApi";

describe("PayPal resource adoption API", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends a stable request key and explicit existing authorization evidence", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => jsonResponse({
      schemaVersion: "vitlane.paypal-reauthorization-adoption.v1",
      result: { adoption: {}, replay: false },
    }));
    vi.stubGlobal("fetch", fetchMock);
    const evidenceHash = `0x${"a".repeat(64)}`;

    await adoptPayPalReauthorization({
      operationId: "operation-1",
      merchantOrderId: "merchant/order 1",
    }, {
      providerAuthorizationId: "AUTH-EXISTING-1",
      evidenceSource: "PAYPAL_DASHBOARD",
      evidenceHash,
      internalNote: "Dashboard and support case reviewed.",
      observedAt: "2026-08-27T08:00:00.000Z",
    });

    const [path, init] = fetchMock.mock.calls[0];
    expect(path).toBe("/api/v1/admin/payment/paypal/merchant-orders/merchant%2Forder%201/reauthorization-adoptions");
    expect(init?.method).toBe("POST");
    expect(new Headers(init?.headers).get("Idempotency-Key")).toBe(
      `paypal-reauthorization-adoption:operation-1:${"a".repeat(64)}`,
    );
    expect(JSON.parse(String(init?.body))).toEqual({
      providerAuthorizationId: "AUTH-EXISTING-1",
      evidenceSource: "PAYPAL_DASHBOARD",
      evidenceHash,
      internalNote: "Dashboard and support case reviewed.",
      observedAt: "2026-08-27T08:00:00.000Z",
    });
  });

  it("sends an existing refund ID and customer-visible rationale without a create key", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => jsonResponse({
      schemaVersion: "vitlane.paypal-mo-refund-adoption.v1",
      result: { compensation: {}, adoption: {}, replay: false },
    }));
    vi.stubGlobal("fetch", fetchMock);

    await adoptPayPalMORefund("compensation/refund 1", {
      providerRefundId: "REFUND-EXISTING-1",
      publicRationale: "PayPal shows the refund created by the original request.",
      evidenceSource: "PAYPAL_API",
      evidenceHash: `0x${"b".repeat(64)}`,
      observedAt: "2026-08-27T08:30:00.000Z",
    });

    const [path, init] = fetchMock.mock.calls[0];
    expect(path).toBe("/api/v1/admin/payment/paypal/mo-compensations/compensation%2Frefund%201/refund-adoptions");
    expect(init?.method).toBe("POST");
    expect(new Headers(init?.headers).get("Idempotency-Key")).toBeNull();
    expect(JSON.parse(String(init?.body))).toEqual({
      providerRefundId: "REFUND-EXISTING-1",
      publicRationale: "PayPal shows the refund created by the original request.",
      evidenceSource: "PAYPAL_API",
      evidenceHash: `0x${"b".repeat(64)}`,
      observedAt: "2026-08-27T08:30:00.000Z",
    });
  });
});

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}
