import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { localizeFixedCopy } from "../../../shared/i18n";
import { CandidateCard } from "../../../shared/ui";
import { CurationCandidateCard, candidateOptionLabel, candidatePriceLabel } from "./CurationCandidateCard";
import { MallLogo } from "./MallLogo";
import type { CandidatePresentation, CandidateVariant } from "../domain/candidatePresentation";

const variant: CandidateVariant = { id: "exact-id", title: "Headphones", attributes: [{ name: "Color", value: "Jet Black" }], price: { kind: "UNKNOWN" }, availability: "UNKNOWN", selectable: true };
const candidate: CandidatePresentation = { id: "candidate", source: "SHOPIFY", title: "Headphones", sellerName: "Seller name", price: { kind: "OBSERVED", amountMinor: 9999, currency: "USD" }, selectedVariant: variant, recommendation: "Original observation kept for details", features: [], specifications: [], disclosures: [], purchaseRoute: "VITLANE_CHECKOUT" };

describe("compact Candidate presentation", () => {
  it("uses compact trailing currency symbols for Candidate prices", () => {
    expect(candidatePriceLabel({ kind: "OBSERVED", amountMinor: 64_000, currency: "KRW" }, localizeFixedCopy)).toBe("64,000₩");
    expect(candidatePriceLabel({ kind: "OBSERVED", amountMinor: 6_400, currency: "USD" }, localizeFixedCopy)).toBe("64$");
    expect(candidatePriceLabel({ kind: "OBSERVED", amountMinor: 6_499, currency: "USD" }, localizeFixedCopy)).toBe("64.99$");
    expect(candidatePriceLabel({ kind: "RANGE", minimumMinor: 6_400, maximumMinor: 6_499, currency: "USD" }, localizeFixedCopy)).toBe("64$ – 64.99$");
  });
  it("uses confirmed option attributes without rewriting saved IDs or raw data", () => {
    const snapshot = JSON.stringify(variant);
    expect(candidateOptionLabel(variant, localizeFixedCopy, candidate.title)).toBe("Jet Black");
    expect(candidateOptionLabel({ ...variant, attributes: [] }, localizeFixedCopy, candidate.title)).not.toBe("Headphones");
    // Catalog previews can repeat the product title as an Option attribute.
    expect(candidateOptionLabel({ ...variant, attributes: [{ name: "Option", value: "Headphones" }] }, localizeFixedCopy, candidate.title)).not.toBe("Headphones");
    expect(JSON.stringify(variant)).toBe(snapshot);
  });
  it("restores seller and original recommendation while emphasizing the actual purchase route", () => {
    for (const source of ["SHOPIFY", "AMAZON"] as const) {
      const html = renderToStaticMarkup(<CurationCandidateCard candidate={{ ...candidate, source, purchaseRoute: source === "AMAZON" ? "EXTERNAL" : "VITLANE_CHECKOUT" }}
        primaryAction={{ label: "Purchase" }} secondaryAction={{ label: "Record" }} />);
      expect(html).toContain("is-compact"); expect(html).toContain("Jet Black"); expect(html).toContain("99.99$"); expect(html).not.toContain("USD");
      expect(html).toContain("Seller name"); expect(html).toContain(candidate.recommendation);
      expect(html.indexOf("Seller name")).toBeLessThan(html.indexOf('class="vt-candidate-card__title"'));
      expect(html.includes("vt-button--primary")).toBe(source === "SHOPIFY");
      expect(html.includes("vt-button--secondary")).toBe(source === "AMAZON");
      expect(html).toContain("vt-button--tertiary");
      expect(html.indexOf("Purchase")).toBeLessThan(html.indexOf("Record"));
    }
  });
  it("names the mall with a generic badge and its name in plain text", () => {
    for (const source of ["SHOPIFY", "AMAZON", "COUPANG", "MUSINSA"] as const) {
      const html = renderToStaticMarkup(<CurationCandidateCard candidate={{ ...candidate, source, purchaseRoute: source === "SHOPIFY" ? "VITLANE_CHECKOUT" : "EXTERNAL" }} />);
      expect(html).toContain(`<span class="candidate-source-badge" data-source="${source}"`);
      // Beside the name the logo is decorative: the badge already says the mall.
      expect(html).toMatch(new RegExp(`<img class="curation-mall-logo" src="[^"]+" alt="" width="16" height="16" decoding="async" data-mall="${source}"`));
      expect(html).not.toContain("phase8-shopify-badge");
      expect(html).not.toContain("is-amazon");
    }
  });
  it("shows a mall without a registered logo by its name only", () => {
    const html = renderToStaticMarkup(<MallLogo source="NEWMALL" />);
    expect(html).toBe('<span class="curation-mall-name" data-mall="NEWMALL">NEWMALL</span>');
    expect(renderToStaticMarkup(<MallLogo source="NEWMALL" decorative />)).toBe("");
    // On its own (a result row) the logo carries the mall's name.
    expect(renderToStaticMarkup(<MallLogo source="COUPANG" />)).toMatch(/alt="쿠팡" title="쿠팡"/);
  });
  it("labels an unscored card as scoring while its research round is open", () => {
    const open = renderToStaticMarkup(<CurationCandidateCard candidate={candidate} evaluating />);
    const closed = renderToStaticMarkup(<CurationCandidateCard candidate={candidate} />);
    expect(open).toContain("평가 중"); expect(open).not.toContain("미평가");
    expect(closed).toContain("미평가"); expect(closed).not.toContain("평가 중");
  });
  it("retains a failed observation's recovery explanation", () => {
    const html = renderToStaticMarkup(<CurationCandidateCard candidate={candidate} state="unavailable" statusMessage="Try again" />);
    expect(html).toContain(candidate.recommendation); expect(html).toContain("Try again");
  });
  it("keeps the shared default card contract and the compact cart status distinct", () => {
    const normal = renderToStaticMarkup(<CandidateCard media={{alt:"Product"}} name="Product" merchant="Seller" price="10 USD" />);
    const compact = renderToStaticMarkup(<CandidateCard density="compact" state="in-cart" media={{alt:"Product"}} name="Product" merchant="Seller" price="10 USD" />);
    expect(normal).toContain("Seller"); expect(compact).toContain("Seller"); expect(compact).toContain("담김");
  });
});
