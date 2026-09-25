import type { AxisAssessment } from "../domain/researchCriteria";
import type { CandidatePresentation, CandidatePrice, CandidateVariant } from "../domain/candidatePresentation";
import type { LiveCatalogProduct, LiveCatalogProviderMessage, LiveVariantRow } from "../research/infra/liveCatalogReviewApi";

export function observedPrice(amount: number | undefined, currency: string | undefined): CandidatePrice {
  return amount !== undefined && Number.isSafeInteger(amount) && amount >= 0 && currency
    ? { kind: "OBSERVED", amountMinor: amount, currency } : { kind: "UNKNOWN" };
}
export function shopifyVariant(row: LiveVariantRow): CandidateVariant {
  return { id: row.variantId, title: row.title === "Default Title" ? undefined : row.title,
    attributes: row.selectedOptions, price: observedPrice(row.priceMinor, row.currency),
    availability: row.available ? "AVAILABLE" : "UNAVAILABLE", selectable: row.available,
    mediaUrl: row.mediaUrl, productUrl: row.productUrl };
}
export function amazonVariant(id: string, row?: LiveVariantRow, product?: LiveCatalogProduct): CandidateVariant {
  const observation = product?.variantObservation?.variantRef.asin === id ? product.variantObservation : undefined;
  return { id, title: row?.title && row.title !== id ? row.title : undefined,
    attributes: row?.selectedOptions ?? [],
    price: observation?.price.kind === "OBSERVED" ? observedPrice(observation.price.amountMinor, observation.price.currency) : { kind: "UNKNOWN" },
    // A relation's is_available is not proof of checkout inventory. Its exact
    // ASIN can still be selected, recorded or opened when price is unknown.
    availability: observation?.availability ?? "UNKNOWN", selectable: true,
    productUrl: `https://www.amazon.com/dp/${id}`, mediaUrl: observation ? product?.mediaUrl : undefined };
}
export function shopifyPresentation(input: {
  candidateId: string; title: string; merchant?: string; mediaUrl?: string; productUrl?: string;
  previewPriceMinor: number; currency: string; previewVariant?: LiveVariantRow;
  axisAssessment?: AxisAssessment; intentPoint?: string; features: string[]; specifications: string[]; searchPlatformMessages?: LiveCatalogProviderMessage[];
}, product?: LiveCatalogProduct, configured = false): CandidatePresentation {
  const selectedVariant = input.previewVariant ? shopifyVariant(input.previewVariant) : undefined;
  let price = configured ? selectedVariant?.price ?? observedPrice(input.previewPriceMinor, input.currency)
    : product ? observedPrice(product.priceMinimumMinor, product.currency) : observedPrice(input.previewPriceMinor, input.currency);
  if (!configured && product && price.kind === "OBSERVED" && typeof product.priceMaximumMinor === "number" && Number.isSafeInteger(product.priceMaximumMinor) && product.priceMaximumMinor > price.amountMinor) {
    price = { kind: "RANGE", minimumMinor: price.amountMinor, maximumMinor: product.priceMaximumMinor, currency: price.currency };
  }
  if (product?.hydration && product.hydration.status !== "READY") price = { kind: "UNKNOWN" };
  return { id: input.candidateId, source: "SHOPIFY", title: input.title,
    sellerName: input.merchant === "Shopify merchant" ? undefined : input.merchant,
    mediaUrl: input.mediaUrl, mediaAlt: product?.mediaAlt, productUrl: input.productUrl, price, selectedVariant,
    axisAssessment: input.axisAssessment ?? product?.axisAssessment, recommendation: input.intentPoint, features: input.features, specifications: input.specifications,
    disclosures: input.searchPlatformMessages ?? [], purchaseRoute: "VITLANE_CHECKOUT" };
}
export function amazonPresentation(product: LiveCatalogProduct, selectedVariant?: CandidateVariant): CandidatePresentation {
  const observation = product.variantObservation;
  const selected = selectedVariant ?? (observation ? amazonVariant(observation.variantRef.asin, undefined, product) : undefined);
  return { id: product.candidateId, source: "AMAZON", title: product.title,
    sellerName: observation?.seller.kind === "KNOWN" ? observation.seller.name : undefined,
    mediaUrl: product.mediaUrl, mediaAlt: product.mediaAlt, productUrl: selected?.productUrl,
    price: selected?.price ?? { kind: "UNKNOWN" }, selectedVariant: selected, recommendation: product.intentPoint || product.description,
    axisAssessment: product.axisAssessment, features: product.features ?? [], specifications: product.specifications ?? [], disclosures: [],
    observedAt: observation?.observedAt, purchaseRoute: "EXTERNAL" };
}
