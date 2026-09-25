import { afterEach, describe, expect, it, vi } from "vitest";
import { requestAgencyOrderRefund } from "./agencyOrderApi";
import { decideRefundRequest } from "./agencyOrderOperatorApi";

describe("refund API contract", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends the customer's required public rationale under the canonical field", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => jsonResponse({
      schemaVersion: "vitlane.order-process-receipt.v1", agencyOrderId:"order",requestId:"refund",flowId:"refund",kind:"REFUND_DECISION",outcome:"COMPLETED",guidance:{customerAction:"VIEW_RESULT",operatorAction:"VIEW_RESULT"},
    }));
    vi.stubGlobal("fetch", fetchMock);

    await requestAgencyOrderRefund(
      "order/with slash",
			"merchant-order-1",
      "ITEM_DAMAGED_DEFECTIVE",
      "The item arrived cracked.",
    );

    const [path, init] = fetchMock.mock.calls[0];
    expect(path).toBe("/api/v1/agencyOrder/order%2Fwith%20slash/refund-requests");
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toEqual({
			merchantOrderId: "merchant-order-1",
      reasonCode: "ITEM_DAMAGED_DEFECTIVE",
      publicRationale: "The item arrived cracked.",
    });
    expect(String(init?.body)).not.toContain("reasonDetail");
  });

  it("sends one whole-MO outcome with public rationale and an optional internal note", async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => jsonResponse({
      schemaVersion: "vitlane.order-process-receipt.v1", agencyOrderId:"order",requestId:"refund",flowId:"refund",kind:"REFUND_DECISION",outcome:"COMPLETED",guidance:{customerAction:"VIEW_RESULT",operatorAction:"VIEW_RESULT"},
    }));
    vi.stubGlobal("fetch", fetchMock);

    await decideRefundRequest("refund/1", {
      approve: true,
      publicRationale: "Carrier evidence confirms the loss.",
      internalNote: "Claim C-42",
		});

    const [path, init] = fetchMock.mock.calls[0];
    expect(path).toBe("/api/v1/admin/agencyOrder/refund-requests/refund%2F1/decisions");
    expect(init?.method).toBe("POST");
    expect(JSON.parse(String(init?.body))).toEqual({
			approve: true,
			publicRationale: "Carrier evidence confirms the loss.",
			internalNote: "Claim C-42",
    });
    expect(String(init?.body)).not.toContain('"reason"');
  });
});

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}
