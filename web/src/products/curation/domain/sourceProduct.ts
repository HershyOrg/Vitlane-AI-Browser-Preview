export type AmazonVariantRef = { source: "AMAZON"; marketplace: "US"; asin: string };
export type KoreanProductRef = { source: string; marketplace: "KR"; productId: string };
export type SourceProductRef = KoreanProductRef | { source: "SHOPIFY"; productId: string } | { source: "AMAZON"; marketplace: "US"; anchorAsin: string };
export type VariantObservation = {
  observationId: string; variantRef: AmazonVariantRef;
  price: { kind: "OBSERVED" | "UNKNOWN"; amountMinor?: number; currency?: string; reasonCode?: string };
  availability: "AVAILABLE" | "UNAVAILABLE" | "UNKNOWN";
  deliveryEligibility: "UNCONFIRMED"; seller: { kind: "UNKNOWN" | "KNOWN"; id?: string; name?: string };
  purchaseRoute: "EXTERNAL"; productUrl: string; observedAt: string; refreshAfter: string;
};

// These conditions omit Amazon research without presenting a customer-facing error.
export function isAmazonAdmissionUnavailable(reason?: string): boolean {
 return !!reason && ["AMAZON_SOURCE_DISABLED", "AMAZON_QUOTA_EXHAUSTED", "AMAZON_QUOTA_UNCONFIRMED", "AMAZON_RATE_LIMITED"].includes(reason);
}

export type ExternalProductObservation = {
 schemaVersion: "vitlane.external-product-observation.v1"; productRef: KoreanProductRef;
 productUrl: string; title: string; imageUrl?: string; price: VariantObservation["price"]; priceScope: "PRODUCT";
 seller: VariantObservation["seller"]; observedAt: string; originalItemId?: string; originalListingId?: string;
 provenance: { apiProvider: string; apiProduct: string; discoveryChannel: string; detailApiProvider?: string; detailApiProduct?: string; country: "KR"; queryLanguage: "ko"; providerLookupId?: string };
};
