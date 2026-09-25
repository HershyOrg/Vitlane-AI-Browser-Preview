import { describe, expect, it } from "vitest";
import type { LiveCatalogProduct } from "../research/infra/liveCatalogReviewApi";
import { productPrice } from "./candidatePresentation";

const product = (overrides: Partial<LiveCatalogProduct>): LiveCatalogProduct => ({ candidateId: "candidate", title: "Camp chair", priceMinimumMinor: 2499, priceMaximumMinor: 2499, currency: "USD", ...overrides } as LiveCatalogProduct);

describe("productPrice", () => {
  it("reads the lowest of several prices as a range and one price as observed", () => {
    expect(productPrice(product({}))).toEqual({ kind: "OBSERVED", amountMinor: 2499, currency: "USD" });
    expect(productPrice(product({ priceMaximumMinor: 3999 }))).toEqual({ kind: "RANGE", minimumMinor: 2499, maximumMinor: 3999, currency: "USD" });
    // A price the source really stated as zero, with its currency, is still a price.
    expect(productPrice(product({ priceMinimumMinor: 0, priceMaximumMinor: 0 }))).toEqual({ kind: "OBSERVED", amountMinor: 0, currency: "USD" });
  });

  it("never reads an unread Shopify product as zero", () => {
    // The saved skeleton: the Server states no numbers at all, or (an older Server) zero without a currency.
    expect(productPrice(product({ title: "", priceMinimumMinor: undefined, priceMaximumMinor: undefined, currency: "" }))).toEqual({ kind: "UNKNOWN" });
    expect(productPrice(product({ title: "", priceMinimumMinor: 0, priceMaximumMinor: 0, currency: "" }))).toEqual({ kind: "UNKNOWN" });
  });

  it("follows the card: anything Shopify did not resolve has no price", () => {
    for (const status of ["UNRESOLVED", "FAILED", "LOADING"] as const) {
      expect(productPrice(product({ hydration: { status } as LiveCatalogProduct["hydration"] }))).toEqual({ kind: "UNKNOWN" });
    }
    expect(productPrice(product({ hydration: { status: "READY" } as LiveCatalogProduct["hydration"] })).kind).toBe("OBSERVED");
  });

  it("keeps a mall's unknown price unknown", () => {
    expect(productPrice(product({ source: "ELEVENST", externalObservation: { price: { kind: "UNKNOWN" } } as LiveCatalogProduct["externalObservation"] }))).toEqual({ kind: "UNKNOWN" });
  });
});

it("uses the chosen option for budget and sorting, including unknown and failed reads", () => {
  const product = { candidateId: "p", priceMinimumMinor: 5000, priceMaximumMinor: 15000, currency: "USD" } as LiveCatalogProduct;
  expect(productPrice(product, { priceMinor: 15000, currency: "USD" })).toEqual({ kind: "OBSERVED", amountMinor: 15000, currency: "USD" });
  expect(productPrice(product, { priceMinor: 0, currency: "" })).toEqual({ kind: "UNKNOWN" });
  expect(productPrice({ ...product, hydration: { status: "UNRESOLVED", retryable: false } }, { priceMinor: 15000, currency: "USD" })).toEqual({ kind: "UNKNOWN" });
});
