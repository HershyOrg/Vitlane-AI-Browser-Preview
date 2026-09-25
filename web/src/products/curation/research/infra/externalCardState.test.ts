import { expect, it } from "vitest";
import { amazonCardState, externalCardState, externalProductCardState } from "./externalCardState";

const feedback = { schemaVersion: "vitlane.external-purchase-feedback.v4", version: 7, records: [] };

it("카드 상태를 싣지 않는 서버 응답에는 아무 것도 만들지 않는다", () => {
  expect(externalCardState(undefined)).toBeUndefined();
  expect(externalCardState({})).toBeUndefined();
  expect(amazonCardState(undefined, { source: "AMAZON", marketplace: "US", anchorAsin: "B0" }, undefined)).toBeUndefined();
  expect(externalProductCardState(undefined, "candidate", { source: "COUPANG", marketplace: "KR", productId: "1" })).toBeUndefined();
});

it("저장한 옵션이 있으면 그 ASIN과 버전을, 없으면 Candidate의 앵커를 쓴다", () => {
  const state = externalCardState({ purchaseFeedback: feedback })!;
  const anchor = { source: "AMAZON", marketplace: "US", anchorAsin: "B0ANCHOR" } as const;
  expect(amazonCardState(state, anchor, undefined)).toEqual({ variantRef: { source: "AMAZON", marketplace: "US", asin: "B0ANCHOR" }, configurationVersion: 0, purchaseFeedback: feedback });
  expect(amazonCardState(state, anchor, { variantId: "B0SAVED", version: 4 })).toEqual({ variantRef: { source: "AMAZON", marketplace: "US", asin: "B0SAVED" }, configurationVersion: 4, purchaseFeedback: feedback });
});

it("반응이 없는 한국 상품은 버전 0에서 시작하고, 허용 목록에 없으면 반응을 막는다", () => {
  const state = externalCardState({
    purchaseFeedback: feedback,
    productReactions: [{ candidateId: "saved", pinned: true, sentiment: "LIKE", version: 2 }],
    reactionAllowedCandidateIds: ["saved", "fresh"],
  })!;
  const ref = { source: "COUPANG", marketplace: "KR", productId: "1" } as const;
  expect(externalProductCardState(state, "saved", ref)).toEqual({ purchaseFeedback: feedback, reactionAllowed: true, reaction: { pinned: true, sentiment: "LIKE", version: 2 } });
  expect(externalProductCardState(state, "fresh", ref)).toEqual({ purchaseFeedback: feedback, reactionAllowed: true, reaction: { pinned: false, sentiment: "NONE", version: 0 } });
  expect(externalProductCardState(state, "blocked", ref)?.reactionAllowed).toBe(false);
  expect(externalProductCardState(state, "saved", undefined)).toBeUndefined();
});
