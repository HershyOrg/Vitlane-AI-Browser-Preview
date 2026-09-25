import { expect, it } from "vitest";
import { amazonVariant, amazonPresentation, shopifyPresentation, shopifyVariant } from "./candidatePresentation";
import type { LiveCatalogProduct, LiveVariantRow } from "../research/infra/liveCatalogReviewApi";
const row: LiveVariantRow = { variantId: "B012345678", title: "Blue", selectedOptions: [{ name: "Color", value: "Blue" }], available: false, priceMinor: 0, currency: "USD" };
it("keeps linked Amazon variants selectable without inventing price or availability from legacy zero fields", () => {
  const variant = amazonVariant(row.variantId, row);
  expect(variant.price.kind).toBe("UNKNOWN"); expect(variant.availability).toBe("UNKNOWN"); expect(variant.selectable).toBe(true);
  expect(variant.attributes).toEqual(row.selectedOptions);
  expect(shopifyVariant(row).selectable).toBe(false);
});
it("never borrows the price or seller of a different selected ASIN", () => {
  const product = { candidateId: "candidate", source: "AMAZON", title: "Headphones", features: [], specifications: [], variantObservation: {
    variantRef: { asin: "B987654321" }, price: { kind: "OBSERVED", amountMinor: 3997, currency: "USD" }, seller: { kind: "UNKNOWN" }, availability: "UNKNOWN",
  } } as unknown as LiveCatalogProduct;
  const model = amazonPresentation(product, amazonVariant(row.variantId, row, product));
  expect(model.price.kind).toBe("UNKNOWN"); expect(model.sellerName).toBeUndefined(); expect(model.source).toBe("AMAZON");
  expect(model.purchaseRoute).toBe("EXTERNAL");
});
it("preserves Shopify price ranges and saved variant prices through the same explicit price model", () => {
  const input = { candidateId: "shop", title: "Headphones", merchant: "Shop", previewPriceMinor: 1000, currency: "USD", previewVariant: { ...row, priceMinor: 1200, available: true }, features: [], specifications: [] };
  const product = { priceMinimumMinor: 1000, priceMaximumMinor: 2000, currency: "USD" } as LiveCatalogProduct;
  expect(shopifyPresentation(input, product).price).toEqual({kind:"RANGE",minimumMinor:1000,maximumMinor:2000,currency:"USD"});
  expect(shopifyPresentation(input, product, true).price).toEqual({kind:"OBSERVED",amountMinor:1200,currency:"USD"});
  expect(shopifyPresentation(input).purchaseRoute).toBe("VITLANE_CHECKOUT");
});
