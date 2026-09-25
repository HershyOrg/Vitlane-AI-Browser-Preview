// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";
import {
  externalPurchaseChanged,
  productReactionsChanged,
  publishProductReaction,
  publishPurchaseFeedback,
  purchaseFeedbackUpdate,
  reactionEventIsForAnotherCard,
} from "./externalStateEvents";

const feedback = (version: number) => ({ schemaVersion: "vitlane.external-purchase-feedback.v4", version, records: [] });

describe("externalStateEvents", () => {
  it("실은 결과를 같은 큐레이션의 카드에만 전달한다", () => {
    const received: Event[] = [];
    const listener = (event: Event) => received.push(event);
    window.addEventListener(externalPurchaseChanged, listener);
    publishPurchaseFeedback("curation-1", feedback(3));
    window.removeEventListener(externalPurchaseChanged, listener);
    const mine = purchaseFeedbackUpdate(received[0], "curation-1");
    expect(mine).toEqual({ kind: "apply", feedback: feedback(3) });
    expect(purchaseFeedbackUpdate(received[0], "curation-2")).toEqual({ kind: "ignore" });
  });

  it("먼저 도착한 최신 값을 늦게 온 옛 값이 덮지 않는다", () => {
    const received: Event[] = [];
    const listener = (event: Event) => received.push(event);
    window.addEventListener(externalPurchaseChanged, listener);
    publishPurchaseFeedback("curation-1", feedback(2));
    window.removeEventListener(externalPurchaseChanged, listener);
    expect(purchaseFeedbackUpdate(received[0], "curation-1", feedback(5))).toEqual({ kind: "ignore" });
    expect(purchaseFeedbackUpdate(received[0], "curation-1", feedback(1))).toEqual({ kind: "apply", feedback: feedback(2) });
  });

  it("결과 없이 알리면 카드가 서버에 다시 물어야 한다", () => {
    const received: Event[] = [];
    const listener = (event: Event) => received.push(event);
    window.addEventListener(externalPurchaseChanged, listener);
    publishPurchaseFeedback("curation-1");
    window.removeEventListener(externalPurchaseChanged, listener);
    expect(purchaseFeedbackUpdate(received[0], "curation-1")).toEqual({ kind: "reload" });
  });

  it("다른 카드의 반응은 무시하고 자기 반응만 다시 읽는다", () => {
    const received: Event[] = [];
    const listener = (event: Event) => received.push(event);
    window.addEventListener(productReactionsChanged, listener);
    publishProductReaction("curation-1", "cand-a");
    window.removeEventListener(productReactionsChanged, listener);
    expect(reactionEventIsForAnotherCard(received[0], "curation-1", "cand-b")).toBe(true);
    expect(reactionEventIsForAnotherCard(received[0], "curation-1", "cand-a")).toBe(false);
    expect(reactionEventIsForAnotherCard(new Event(productReactionsChanged), "curation-1", "cand-b")).toBe(false);
  });

  it("이벤트 이름은 기존 이름을 유지해 계정 목록 같은 기존 구독자를 깨지 않는다", () => {
    const account = vi.fn();
    window.addEventListener("vitlane:external-purchase-changed", account);
    publishPurchaseFeedback("curation-1", feedback(1));
    window.removeEventListener("vitlane:external-purchase-changed", account);
    expect(account).toHaveBeenCalledTimes(1);
  });
});
