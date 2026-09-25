import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { isKoreanExternalSource, sourceLabel } from "./sourceLabels";

describe("sourceLabels", () => {
  const en = (a: string) => a;
  const ko = (_: string, b: string) => b;
  it("names registered malls in both locales and falls back to the code", () => {
    expect(sourceLabel("COUPANG", ko)).toBe("쿠팡");
    expect(sourceLabel("ELEVENST", en)).toBe("11st");
    expect(sourceLabel("MUSINSA", ko)).toBe("무신사");
    expect(sourceLabel("KURLY", en)).toBe("Kurly");
    expect(sourceLabel("SHOPIFY", ko)).toBe("Shopify");
    expect(sourceLabel("AMAZON", en)).toBe("Amazon");
    expect(sourceLabel("NEWMALL", ko)).toBe("NEWMALL");
  });
  it("treats every non-Shopify, non-Amazon source as a Korean external platform", () => {
    expect(isKoreanExternalSource("COUPANG")).toBe(true);
    expect(isKoreanExternalSource("KURLY")).toBe(true);
    expect(isKoreanExternalSource("NEWMALL")).toBe(true);
    expect(isKoreanExternalSource("SHOPIFY")).toBe(false);
    expect(isKoreanExternalSource("AMAZON")).toBe(false);
    expect(isKoreanExternalSource(undefined)).toBe(false);
  });
  it("registers a generic merchant badge for exactly the malls it names", () => {
    const labels = readFileSync(new URL("./sourceLabels.ts", import.meta.url), "utf8");
    const named = [...labels.matchAll(/case "([A-Z0-9_]+)":/g)].map((match) => match[1]);
    const registry = JSON.parse(readFileSync(new URL("../../../shared/ui/design-system/mall-logos.source.json", import.meta.url), "utf8")) as { malls: Record<string, unknown> };
    expect(Object.keys(registry.malls).sort()).toEqual(named.sort());
  });
});
