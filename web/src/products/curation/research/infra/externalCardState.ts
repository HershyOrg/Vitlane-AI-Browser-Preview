import type { AmazonState, PurchaseFeedback } from "./amazonApi";
import type { AmazonVariantRef, KoreanProductRef, SourceProductRef } from "../../domain/sourceProduct";

export type ProductReactionState = { pinned: boolean; sentiment: "NONE" | "LIKE" | "DISLIKE"; version: number };
export type ExternalProductCardState = { purchaseFeedback: PurchaseFeedback; reactionAllowed: boolean; reaction: ProductReactionState };

// The workspace read carries every external card's state for the whole
// curation, so a card grid asks the server once instead of once per card.
export type ExternalCardStateSource = {
  purchaseFeedback?: PurchaseFeedback;
  productReactions?: Array<{ candidateId: string; pinned: boolean; sentiment: "NONE" | "LIKE" | "DISLIKE"; version: number }>;
  reactionAllowedCandidateIds?: string[];
};

export type ExternalCardState = {
  purchaseFeedback: PurchaseFeedback;
  reactions: Record<string, ProductReactionState>;
  reactionAllowed: Record<string, true>;
};

// A server that does not carry card state yet leaves purchaseFeedback out, and
// then every card falls back to reading its own state.
export function externalCardState(source: ExternalCardStateSource | undefined): ExternalCardState | undefined {
  if (!source?.purchaseFeedback) return undefined;
  const reactions: Record<string, ProductReactionState> = {};
  for (const reaction of source.productReactions ?? []) {
    reactions[reaction.candidateId] = { pinned: reaction.pinned, sentiment: reaction.sentiment, version: reaction.version };
  }
  const reactionAllowed: Record<string, true> = {};
  for (const candidateId of source.reactionAllowedCandidateIds ?? []) reactionAllowed[candidateId] = true;
  return { purchaseFeedback: source.purchaseFeedback, reactions, reactionAllowed };
}

// The saved option decides the ASIN a card acts on; without one the anchor of
// the Candidate stands in, exactly as the per-card read answered before.
export function amazonCardState(
  state: ExternalCardState | undefined,
  productRef: SourceProductRef | undefined,
  configuration: { variantId: string; version: number } | undefined,
): AmazonState | undefined {
  if (!state) return undefined;
  // A Korean source is a plain string now, so the anchor field is what tells
  // an Amazon reference apart, not the source name.
  const anchor = productRef && "anchorAsin" in productRef ? productRef.anchorAsin : "";
  const asin = configuration?.variantId || anchor;
  const variantRef: AmazonVariantRef = { source: "AMAZON", marketplace: "US", asin };
  return { variantRef, configurationVersion: configuration?.version ?? 0, purchaseFeedback: state.purchaseFeedback };
}

// A Korean product with no saved reaction starts at version 0, which is what
// the per-card read returned for it.
export function externalProductCardState(
  state: ExternalCardState | undefined,
  candidateId: string,
  productRef: KoreanProductRef | undefined,
): ExternalProductCardState | undefined {
  if (!state || !productRef) return undefined;
  return {
    purchaseFeedback: state.purchaseFeedback,
    reactionAllowed: state.reactionAllowed[candidateId] === true,
    reaction: state.reactions[candidateId] ?? { pinned: false, sentiment: "NONE", version: 0 },
  };
}
