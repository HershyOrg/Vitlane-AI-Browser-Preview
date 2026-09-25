import type { AxisAssessment } from "./researchCriteria";
import type { LiveCatalogProduct } from "../research/infra/liveCatalogReviewApi";
// Source adapters produce this model before rendering. Unknown observations
// and purchase capabilities are explicit; neither is inferred from missing UI.
// SHOPIFY, AMAZON or a registered Korean mall code (see sourceLabels.ts).
export type CandidateSource = string;
export type CandidatePrice =
  | { kind: "OBSERVED"; amountMinor: number; currency: string }
  | { kind: "RANGE"; minimumMinor: number; maximumMinor: number; currency: string }
  | { kind: "UNKNOWN" };
export type CandidateVariant = {
  id: string;
  title?: string;
  attributes: Array<{ name: string; value: string }>;
  price: CandidatePrice;
  availability: "AVAILABLE" | "UNAVAILABLE" | "UNKNOWN";
  selectable: boolean;
  mediaUrl?: string;
  productUrl?: string;
};
export type CandidateDisclosure = {
  content: string; url?: string; imageUrl?: string;
  contentType?: string; code?: string; path?: string; presentation?: string; severity?: string;
};
export type CandidatePresentation = {
  priceScope?: "PRODUCT";
  originalProductId?: string;
  provenance?: { apiProvider: string; apiProduct: string; discoveryChannel: string; detailApiProvider?: string; detailApiProduct?: string };
  id: string;
  source: CandidateSource;
  title: string;
  sellerName?: string;
  mediaUrl?: string;
  mediaAlt?: string;
  productUrl?: string;
  price: CandidatePrice;
  selectedVariant?: CandidateVariant;
  recommendation?: string;
  axisAssessment?: AxisAssessment;
  features: string[];
  specifications: string[];
  disclosures: CandidateDisclosure[];
  observedAt?: string;
  purchaseRoute: "VITLANE_CHECKOUT" | "EXTERNAL";
};
export type VariantInteraction = { pinned: boolean; sentiment: "NONE" | "LIKE" | "DISLIKE" };
export const variantInteractionKey = (candidateId: string, variantId: string) => `${candidateId}::${variantId}`;

/**
 * The price a product states for itself, before any option is chosen: what ordering, the budget-aware Pick and a
 * representative row of a non-Shopify product read. It follows the same rules as the card's presentation. A Shopify
 * product is durable only as an identity; its price is read from Shopify for the screen, so a product that has not
 * been read yet, or that Shopify could not resolve, has no price — never zero. The lowest of several prices stands
 * for the product; a supplied selected option takes precedence for ordering and budget comparisons too.
 */
export function productPrice(product: LiveCatalogProduct, selected?: { priceMinor: number; currency: string; priceUnknown?: boolean }): CandidatePrice {
  if (selected) {
    if (selected.priceUnknown || !selected.currency || !Number.isSafeInteger(selected.priceMinor) || selected.priceMinor < 0
      || (product.hydration && product.hydration.status !== "READY")) return { kind: "UNKNOWN" };
    return { kind: "OBSERVED", amountMinor: selected.priceMinor, currency: selected.currency };
  }
  const minimum = product.priceMinimumMinor;
  if (minimum === undefined || !Number.isSafeInteger(minimum) || minimum < 0 || !product.currency
    || product.externalObservation?.price.kind === "UNKNOWN" || (product.hydration && product.hydration.status !== "READY")) return { kind: "UNKNOWN" };
  const maximum = product.priceMaximumMinor;
  return maximum !== undefined && Number.isSafeInteger(maximum) && maximum > minimum ? { kind: "RANGE", minimumMinor: minimum, maximumMinor: maximum, currency: product.currency }
    : { kind: "OBSERVED", amountMinor: minimum, currency: product.currency };
}
