// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { listPayPalDisputes, recordPayPalDisputeAction } from "../infra/paypalDisputeApi";
import { PayPalDisputePanel } from "./PayPalDisputePanel";

vi.mock("../infra/paypalDisputeApi", () => ({
  listPayPalDisputes: vi.fn(),
  recordPayPalDisputeAction: vi.fn(),
}));

const dispute = {
  id: "00000000-0000-4000-8000-000000000901",
  environment: "SANDBOX",
  disputeId: "PP-DISPUTE-901",
  agencyOrderId: "00000000-0000-4000-8000-000000000101",
  captureId: "PP-CAPTURE-101",
  state: "OPEN",
  providerStatus: "WAITING_FOR_SELLER_RESPONSE",
  outcome: "NONE",
  reason: "MERCHANDISE_OR_SERVICE_NOT_RECEIVED",
  lifecycleStage: "INQUIRY",
  sellerResponseDueAt: "2026-08-30T00:00:00Z",
  lastObservedAt: "2026-08-27T00:00:00Z",
  version: 3,
} as const;

describe("PayPalDisputePanel", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
    vi.mocked(listPayPalDisputes).mockResolvedValue({
      schemaVersion: "vitlane.paypal-dispute-queue.v1",
      disputes: [dispute],
    });
    vi.mocked(recordPayPalDisputeAction).mockResolvedValue({
      schemaVersion: "vitlane.paypal-dispute-action.v1",
      result: { case: { ...dispute, version: 4 }, replay: false },
    });
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.clearAllMocks();
  });

  it("환경을 섞지 않고 고객 공개 근거와 증거가 있는 수동 행동만 기록한다", async () => {
    await act(async () => root.render(<MemoryRouter><PayPalDisputePanel /></MemoryRouter>));
    await act(async () => Promise.resolve());
    await act(async () => Promise.resolve());

    expect(listPayPalDisputes).toHaveBeenCalledWith("SANDBOX", "OPEN");
    expect(container.textContent).toContain("PAYPAL · SANDBOX · NO REAL VALUE");
    expect(container.textContent).toContain("PP-DISPUTE-901");
    const submit = [...container.querySelectorAll("button")]
      .find((button) => button.textContent?.includes("Resolution Center 행동 기록")) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);

    setValue(container.querySelector("#paypal-rationale-00000000-0000-4000-8000-000000000901"), "PayPal case와 배송 증거를 확인했으며 판매자 답변을 제출했습니다.");
    setValue(container.querySelector("#paypal-evidence-hash-00000000-0000-4000-8000-000000000901"), `0x${"a".repeat(64)}`);
    await act(async () => Promise.resolve());
    expect(submit.disabled).toBe(false);
    await act(async () => submit.click());
    await act(async () => Promise.resolve());

    expect(recordPayPalDisputeAction).toHaveBeenCalledWith(dispute.environment, dispute.id, expect.objectContaining({
      expectedVersion: 3,
      externalReference: "PP-DISPUTE-901",
      publicRationale: "PayPal case와 배송 증거를 확인했으며 판매자 답변을 제출했습니다.",
      evidenceHash: `0x${"a".repeat(64)}`,
      evidenceSource: "PAYPAL_RESOLUTION_CENTER",
      observedProviderStatus: "WAITING_FOR_SELLER_RESPONSE",
      observedOutcome: "NONE",
    }));
  });
});

function setValue(control: Element | null, value: string) {
  if (!(control instanceof HTMLInputElement || control instanceof HTMLTextAreaElement)) {
    throw new Error("expected input control");
  }
  const prototype = control instanceof HTMLTextAreaElement
    ? HTMLTextAreaElement.prototype
    : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(prototype, "value")?.set?.call(control, value);
  control.dispatchEvent(new Event("input", { bubbles: true }));
}
