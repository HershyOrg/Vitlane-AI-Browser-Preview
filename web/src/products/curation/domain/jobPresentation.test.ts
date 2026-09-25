import { describe, expect, it } from "vitest";
import { jobReasonLabel } from "./jobPresentation";

describe("jobReasonLabel", () => {
  it("names the source, admission and assessment failures instead of the generic fallback", () => {
    const en = (a: string) => a;
    const ko = (_: string, b: string) => b;
    const fallbackEn = jobReasonLabel("SOMETHING_ELSE", en);
    for (const code of [
      "KOREAN_CATALOG_UNAVAILABLE", "SHOPIFY_SEARCH_FAILED", "CATALOG_API_RATE_LIMITED",
      "RESEARCH_INPUT_NORMALIZATION_REQUIRED", "PHASE8_MANAGED_RANKING_INVALID",
      "PHASE8_MANAGED_RANKING_UNOBSERVED_PRODUCT", "PHASE8_MANAGED_RANKING_DUPLICATE_PRODUCT",
      "RESEARCH_UNOBSERVED_FACT", "RESEARCH_PRICE_AXIS_REQUIRED",
    ]) {
      expect(jobReasonLabel(code, en)).not.toBe(fallbackEn);
      expect(jobReasonLabel(code, ko)).not.toBe(jobReasonLabel("SOMETHING_ELSE", ko));
    }
    expect(jobReasonLabel("KOREAN_CATALOG_UNAVAILABLE", ko)).toContain("한국 상품 소스");
    expect(jobReasonLabel(undefined, en)).toBe(fallbackEn);
  });
});
