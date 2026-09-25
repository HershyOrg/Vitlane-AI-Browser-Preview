import type { PurchaseFeedback } from "./amazonApi";

// A purchase check or a product reaction used to make every external card of
// the curation fetch its state again. The command response already carries the
// new value, so it travels with the event and the other cards apply it instead
// of asking the server. A payload older than what a card already has is ignored.
export const externalPurchaseChanged = "vitlane:external-purchase-changed";
export const productReactionsChanged = "vitlane:product-reactions-changed";

export type PurchaseFeedbackDetail = { curationId: string; feedback: PurchaseFeedback };
export type ProductReactionDetail = { curationId: string; candidateId: string };

export function publishPurchaseFeedback(curationId: string, feedback?: PurchaseFeedback) {
  window.dispatchEvent(feedback
    ? new CustomEvent<PurchaseFeedbackDetail>(externalPurchaseChanged, { detail: { curationId, feedback } })
    : new Event(externalPurchaseChanged));
}

export function publishProductReaction(curationId: string, candidateId: string) {
  window.dispatchEvent(new CustomEvent<ProductReactionDetail>(productReactionsChanged, { detail: { curationId, candidateId } }));
}

// What a listener should do with a purchase event. "apply" carries the value
// the command already returned, "ignore" is another curation or an older value,
// and "reload" is an event without a usable result, such as a conflict.
export type PurchaseFeedbackUpdate =
  | { kind: "apply"; feedback: PurchaseFeedback }
  | { kind: "ignore" }
  | { kind: "reload" };

export function purchaseFeedbackUpdate(event: Event, curationId: string, current?: PurchaseFeedback): PurchaseFeedbackUpdate {
  const detail = (event as CustomEvent<PurchaseFeedbackDetail>).detail;
  if (!detail || !detail.feedback) return { kind: "reload" };
  if (detail.curationId !== curationId) return { kind: "ignore" };
  if (current && detail.feedback.version < current.version) return { kind: "ignore" };
  return { kind: "apply", feedback: detail.feedback };
}

// True when a reaction event came from another card and this one can ignore it.
export function reactionEventIsForAnotherCard(event: Event, curationId: string, candidateId: string): boolean {
  const detail = (event as CustomEvent<ProductReactionDetail>).detail;
  return Boolean(detail && detail.curationId === curationId && detail.candidateId !== candidateId);
}
