// @vitest-environment jsdom
import { readFileSync, statSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
const root = path.resolve(process.cwd(), "..");
const read = (name: string) => readFileSync(path.join(root, name), "utf8");
const routes = ["/", "/ko/", "/privacy/", "/ko/privacy/", "/terms/", "/ko/terms/"];
describe("public SEO contract", () => {
  it.each(routes)("%s exposes complete localized sharing metadata in the original HTML", (route) => {
    const document = new DOMParser().parseFromString(read(`marketing${route}index.html`), "text/html");
    const meta = (key: string) => document.querySelector<HTMLMetaElement>(`meta[property="${key}"], meta[name="${key}"]`)?.content;
    const url = `https://vitlane.com${route}`;
    const description = meta("description");
    expect(description?.length).toBeGreaterThan(30);
    expect(meta("og:url")).toBe(url);
    expect(document.querySelector<HTMLLinkElement>('link[rel="canonical"]')?.href).toBe(url);
    expect(meta("og:title")).toBe(document.title);
    expect(meta("og:description")).toBe(description);
    expect(meta("og:type")).toBe("website");
    expect(meta("og:site_name")).toBe("Vitlane");
    expect(meta("og:locale")).toBe(route.startsWith("/ko/") ? "ko_KR" : "en_US");
    expect(meta("og:locale:alternate")).toBe(route.startsWith("/ko/") ? "en_US" : "ko_KR");
    expect(meta("twitter:card")).toBe("summary_large_image");
    expect(meta("twitter:title")).toBe(document.title);
    expect(meta("twitter:description")).toBe(description);
    expect(meta("twitter:image")).toBe(meta("og:image"));
    expect(meta("twitter:image:alt")).toBe(meta("og:image:alt"));
    expect(meta("og:image:alt")?.length).toBeGreaterThan(5);
    const image = new URL(meta("og:image")!);
    expect(image.origin).toBe("https://vitlane.com");
    const bytes = readFileSync(path.join(root, "marketing", image.pathname));
    expect(bytes.subarray(1, 4).toString()).toBe("PNG");
    expect(bytes.readUInt32BE(16)).toBe(Number(meta("og:image:width")));
    expect(bytes.readUInt32BE(20)).toBe(Number(meta("og:image:height")));
    expect(bytes.readUInt32BE(16)).toBe(1200);
    expect(bytes.readUInt32BE(20)).toBe(630);
    expect(bytes.length).toBeLessThan(500_000);
    expect(document.querySelector('meta[name="robots"][content*="noindex"]')).toBeNull();
    expect(document.querySelectorAll('meta[property="og:title"]')).toHaveLength(1);
  });
  it.each(["/", "/ko/"])("%s has usable title, product explanation and navigation before JavaScript", (route) => {
    const document = new DOMParser().parseFromString(read(`marketing${route}index.html`), "text/html");
    document.querySelectorAll("script,noscript").forEach((node) => node.remove());
    expect(document.querySelectorAll("h1")).toHaveLength(1);
    expect(document.querySelector("h1")?.textContent?.trim()).toBeTruthy();
    expect(document.title).toContain("AI");
    expect(document.querySelector(".product-summary")?.textContent).toContain("AI");
    expect(document.querySelectorAll(".marketing-static-copy li")).toHaveLength(3);
    const link = document.querySelector<HTMLAnchorElement>(".hero-product-link");
    expect(link?.href).toBe("https://app.vitlane.com/");
    expect(document.querySelector(".hero-install-link")).toBeNull();
    expect(link?.textContent?.trim()).toBeTruthy();
    const pilotCopy = document.querySelector(".product-pilot")?.textContent?.replace(/\s+/g, " ");
    expect(pilotCopy).toMatch(route === "/" ? /not a public purchase path/ : /현재 공개 구매 경로가 아닙니다/);
  });
  it("both home locales expose the same owner-provided Naver verification before JavaScript", () => {
    const tags = ["/", "/ko/"].map((route) => {
      const document = new DOMParser().parseFromString(read(`marketing${route}index.html`), "text/html");
      const tags = document.head.querySelectorAll<HTMLMetaElement>('meta[name="naver-site-verification"]');
      expect(tags).toHaveLength(1);
      return tags[0].content;
    });
    expect(tags[0]).toMatch(/^[a-f0-9]{40}$/);
    expect(tags[1]).toBe(tags[0]);
  });
  it("public policies describe Live and both test rails without claiming all orders are simulated", () => {
    for (const prefix of ["", "ko/"]) {
      const terms = read(`marketing/${prefix}terms/index.html`);
      for (const name of ["PayPal Live Pilot", "PayPal Sandbox", "GIWA Testnet", "USD"]) expect(terms).toContain(name);
      expect(terms).not.toContain("The current product is a TEST environment");
      expect(terms).not.toContain("현재 제품은 경제적");
      const privacy = read(`marketing/${prefix}privacy/index.html`);
      expect(privacy).toContain("PayPal");
      expect(privacy).toContain("Google Analytics 4");
    }
  });
  it("delivered catalog derivatives fit a small image budget and keep their source snapshots", () => {
    const manifest = JSON.parse(read("marketing/assets/seo-assets.json"));
    const products = Object.keys(manifest.files).filter((name) => name.includes("/products/optimized/"));
    expect(products).toHaveLength(6);
    expect(products.reduce((sum, name) => sum + statSync(path.join(root, name)).size, 0)).toBeLessThan(500_000);
    for (const name of Object.keys(manifest.sources)) expect(statSync(path.join(root, name)).size).toBeGreaterThan(0);
  });
});
