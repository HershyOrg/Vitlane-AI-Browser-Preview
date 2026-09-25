import { afterEach, describe, expect, it, vi } from "vitest";
import { recordPayPalDisputeAction } from "./paypalDisputeApi";

describe("PayPal dispute API contract", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("binds every manual action request to the selected PayPal environment", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => jsonResponse({
      schemaVersion: "vitlane.paypal-dispute-action.v1",
      result: { case: {}, replay: false },
    }));
    vi.stubGlobal("fetch", fetchMock);

    await recordPayPalDisputeAction("LIVE", "case/1", {
      expectedVersion: 2,
      actionKind: "CASE_OBSERVED",
      externalReference: "PP-D-1",
      publicRationale: "The Resolution Center case was reviewed.",
      observedProviderStatus: "OPEN",
      observedOutcome: "NONE",
      evidenceSource: "PAYPAL_RESOLUTION_CENTER",
      evidenceHash: `0x${"a".repeat(64)}`,
      observedAt: "2026-08-27T00:00:00.000Z",
    });

    const [path, init] = fetchMock.mock.calls[0];
    expect(path).toBe(
      "/api/v1/admin/payment/paypal/disputes/case%2F1/actions?environment=LIVE",
    );
    expect(init?.method).toBe("POST");
  });
});

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}
