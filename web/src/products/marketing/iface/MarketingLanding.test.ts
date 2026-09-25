// @vitest-environment jsdom
// @ts-nocheck

import { readFileSync } from "node:fs";
import path from "node:path";
import { JSDOM } from "jsdom";
import { describe, expect, it } from "vitest";

const repositoryRoot = path.resolve(process.cwd(), "..");
const html = readFileSync(path.join(repositoryRoot, "marketing/index.html"), "utf8");
const script = readFileSync(path.join(repositoryRoot, "marketing/assets/marketing.js"), "utf8");
const marketingCSS = readFileSync(path.join(repositoryRoot, "marketing/assets/marketing.css"), "utf8");
const privacy = readFileSync(path.join(repositoryRoot, "marketing/privacy/index.html"), "utf8");
const terms = readFileSync(path.join(repositoryRoot, "marketing/terms/index.html"), "utf8");
const koHtml = readFileSync(path.join(repositoryRoot, "marketing/ko/index.html"), "utf8");
const koPrivacy = readFileSync(path.join(repositoryRoot, "marketing/ko/privacy/index.html"), "utf8");
const koTerms = readFileSync(path.join(repositoryRoot, "marketing/ko/terms/index.html"), "utf8");
const robots = readFileSync(path.join(repositoryRoot, "marketing/robots.txt"), "utf8");
const sitemap = readFileSync(path.join(repositoryRoot, "marketing/sitemap.xml"), "utf8");
const marketingReact = readFileSync(
  path.join(repositoryRoot, "web/src/marketing/main.tsx"),
  "utf8",
);
const cloudflareAnalyticsSnippet =
  `<!-- Cloudflare Web Analytics --><script type='module' src='https://static.cloudflareinsights.com/beacon.min.js' data-cf-beacon='{"token": "00000000000000000000000000000000"}'></script><!-- End Cloudflare Web Analytics -->`;
const brandMarks = [
  "marketing/assets/vitlane-lane-mark.svg",
  "shared/brand/vitlane-lane-mark.svg",
  "web/src/shared/ui/assets/vitlane-lane-mark.svg",
].map((asset) => readFileSync(path.join(repositoryRoot, asset), "utf8"));
const favicons = ["marketing/assets/favicon.svg", "web/public/favicon.svg"].map((asset) =>
  readFileSync(path.join(repositoryRoot, asset), "utf8"),
);
const stillSeed = JSON.parse(
  readFileSync(path.join(repositoryRoot, "web/src/shared/ui/design-system/tokens.source.json"), "utf8"),
).foundation.color.still.value as string;

describe("marketing landing", () => {
  it("공개 문의 UI와 전송 코드를 노출하지 않고 제품 CTA로 바로 연결한다", () => {
    expect(html).not.toContain("marketing-inquiry-root");
    expect(html).not.toContain("inquiry-panel");
    expect(marketingReact).not.toContain("MarketingInquiry");
    expect(marketingReact).not.toContain("/api/v1/marketing/inquiries");
    expect(marketingReact).not.toContain("vitlane:inquiry-length");
    expect(marketingReact).not.toContain("inquiry-message");
    expect(marketingCSS).not.toContain(".inquiry-panel");
    expect(marketingCSS).not.toContain(".submitted-state");
    expect(marketingReact.match(/appHref\("\/"\)/g)).toHaveLength(3);
  });

  it("문의 입력 없이 첫 화면의 모루유리 배경과 TEST pipeline을 유지한다", () => {
    // ADR-0078: reeded glass replaces the Beam; the light gathers behind the
    // product action and swells slowly. No input or pointer drives it.
    expect(marketingReact).toContain(
      '<ReededGlass anchor=".hero-product-link" motion="swell" />',
    );
    expect(marketingReact).not.toContain("ArcBeamCanvas");
    expect(marketingReact).not.toContain("beamAmbientCycleMs");
    expect(marketingReact).not.toContain("beamInputTravelMs");
    expect(marketingReact).not.toContain("data-intensity");
    expect(marketingCSS).not.toContain(".living-beam");
    expect(marketingCSS).toContain(".living-lane .vt-reeded-glass");
    // 2026-09-16: the glass carries the Still light, so the action is a plain
    // plate of the theme's own background with the brand color in the words.
    expect(marketingCSS).not.toContain("--vt-marketing-action-glass");
    expect(marketingCSS).toContain(
      "  --vt-marketing-action-plate: var(--vt-marketing-surface);",
    );
    expect(marketingCSS).toContain(
      "  --vt-marketing-action-plate: var(--vt-marketing-canvas);",
    );
    expect(marketingCSS).toContain(
      "  color: var(--vt-marketing-route);\n  background: var(--vt-marketing-action-plate);",
    );
    // ADR-0074: one Lane — "왜 Vitlane인가" and the scroll demo are one section
    // of six moments on the real app frames; the TEST receipt closes it.
    expect(marketingReact).toContain('ml("Why Vitlane", "왜 Vitlane인가")');
    expect(marketingReact).not.toContain("Start to finish, on the real screens.");
    expect(marketingReact).not.toContain("시작부터 끝까지, 실제 화면으로.");
    expect(marketingReact).not.toContain("lane-subheading");
    expect(marketingReact).toContain("const laneFrameCount = 5");
    expect(marketingReact).not.toContain("JourneyExperience");
    expect(marketingReact).not.toContain("WhyVitlane");
    expect(marketingCSS).toContain("align-items: start;");
    expect(marketingCSS).toContain(
      "--vt-marketing-frame-veil-base: var(--vt-semantic-color-text-primary);",
    );
    expect(marketingCSS).toContain(":root.dark .marketing-home");
    expect(marketingCSS).toContain(
      "--vt-marketing-frame-veil-base: var(--vt-semantic-color-surface-canvas);",
    );
    expect(marketingCSS).toContain(
      "color-mix(in srgb, var(--vt-marketing-frame-veil-base) 45%, transparent)",
    );
    for (const document of [html, koHtml]) {
      expect(document).toContain('<section class="lane-section" id="lane" aria-labelledby="lane-title">');
      expect(document).not.toContain("marketing-why-root");
      expect(document).not.toContain("marketing-journey-root");
    }
    expect(marketingReact).toContain("TEST order receipt");
    expect(marketingReact).toContain('ml("Simulated order · SIMULATED", "모의 주문 처리 · SIMULATED")');
    expect(marketingReact).toContain("Vitlane 열기");
    expect(marketingReact).toContain("No real purchase occurs.");
  });

  it("휴대폰도 같은 sticky Lane을 scroll로 진행하고, reduced motion·짧은 화면만 세로 나열한다 (ADR-0076)", () => {
    expect(marketingReact).not.toContain('useMediaQuery("(max-width: 48rem)") || reducedMotion');
    expect(marketingReact).toContain('useMediaQuery("(max-height: 30rem)") || reducedMotion || (narrow && phoneSceneOverflows)');
    expect(marketingReact).toContain("laneCamera(active.spot)");
    expect(marketingReact).toContain('behavior: smooth ? "smooth" : "instant"');
    expect(marketingCSS).toContain("@media (prefers-reduced-motion: reduce), (max-height: 30rem) {");
    expect(marketingCSS).not.toContain("@media (max-width: 48rem), (prefers-reduced-motion: reduce) {");
    // The phone scene follows the 64rem rules so its one-column grid wins.
    const phoneScene = marketingCSS.indexOf("Phones run the same scene in one column (ADR-0076)");
    expect(phoneScene).toBeGreaterThan(marketingCSS.indexOf("@media (max-width: 64rem) {"));
    const phoneBlock = marketingCSS.slice(phoneScene, phoneScene + 4000);
    expect(phoneBlock).toContain("transform: translateX(calc(var(--lane-stage) * -100%));");
    expect(phoneBlock).toContain("scale(var(--lane-camera-zoom));");
    expect(phoneBlock).toContain("scale: var(--lane-frame-scale);");
    expect(phoneBlock).toContain('.lane-sticky:not([data-stage="0"]) .lane-request');
    // Gallery cards keep room for maker, name and price on small phones.
    expect(marketingCSS).not.toContain("height: 17rem;");
  });

  it("hero 우측에서 제품 CTA를 강조하고 X 링크는 조용한 보조 action으로 둔다", () => {
    expect(marketingReact).toContain('className="hero-thesis-copy"');
    expect(marketingReact).toContain('className="hero-actions"');
    expect(marketingReact).toContain('className="hero-product-link"');
    expect(marketingReact).toContain('className="hero-social-link"');
    expect(marketingReact).toContain('href="https://x.com/Vitlane_"');
    expect(marketingReact).toContain('target="_blank"');
    expect(marketingReact).toContain('rel="noopener noreferrer"');
    expect(marketingReact).toContain('ml("See on X", "X에서 보기")');
    expect(marketingReact).not.toContain("SiX");
    expect(marketingCSS).toContain(
      "minmax(var(--vt-foundation-space-8), 0.16fr)",
    );
    expect(marketingCSS).toContain(
      "backdrop-filter: var(--vt-semantic-effect-glass-blur)",
    );
    expect(marketingCSS).toContain(
      ".hero-product-link:hover span:last-child",
    );
    expect(marketingCSS).toContain(
      "font-size: clamp(\n    var(--vt-foundation-font-size-heading),",
    );
    expect(marketingCSS).toContain(
      "gap: var(--vt-foundation-space-6)",
    );
    expect(marketingCSS).toContain(
      ".hero-secondary-actions {",
    );
    expect(marketingCSS).toContain(
      ".hero-social-link {\n  min-height: var(--vt-foundation-size-touch);\n  padding: var(--vt-foundation-space-2) 0;\n  display: inline-flex;\n  align-items: center;\n  border: none;\n  color: var(--vt-marketing-text);\n  background: transparent;\n  box-shadow: none;\n  font-size: var(--vt-foundation-font-size-body-strong);\n  font-weight: var(--vt-foundation-font-weight-regular);",
    );
    expect(marketingCSS).toContain(
      "font-weight: var(--vt-foundation-font-weight-medium)",
    );
    expect(marketingCSS).toContain("text-align: left");
    expect(marketingCSS).toContain("text-decoration-line: underline");
    expect(marketingCSS).toContain("text-decoration-color: currentColor");
    expect(marketingCSS).toContain(
      "text-decoration-thickness: calc(var(--vt-foundation-border-thin) * 2)",
    );
    expect(marketingCSS).toContain(
      ".hero-social-link:hover {\n  color: var(--vt-marketing-route);",
    );
    expect(marketingCSS).toContain("background: transparent");
    expect(marketingCSS).toContain("box-shadow: none");
    expect(marketingCSS).toContain("justify-content: center");
    expect(marketingCSS).toContain("border: none");
  });

  it("첫 화면 표어는 조건절이 돌고 결과절이 고정되며, Searching 밴드는 유리 안의 흐린 띠다 (2026-09-18)", () => {
    // Brackets mark the accented key words (owner 2026-09-18).
    for (const [english, korean] of [
      ["[First day] at work?", "[첫 출근]이어도,"],
      ["On a [tight budget]?", "[절약]이 필요해도,"],
      ["Particular [taste]?", "세심한 [취향]도,"],
      ["[Buying on repeat]?", "[반복적인 구매]도,"],
    ]) {
      expect(marketingReact).toContain(`ml("${english}", "${korean}")`);
    }
    expect(marketingReact).toContain('ml("You still buy better.", "더 잘 삽니다.")');
    expect(marketingReact).toContain('ml("Research, compare, and stay on budget.", "조사, 비교, 예산 맞춤까지.")');
    expect(marketingReact).toContain('<span className="sr-only">{heroTitleName}</span>');
    expect(marketingCSS).toContain(".hero-title-key {\n  color: var(--vt-marketing-route);\n}");
    expect(marketingCSS).toContain("  .hero-thesis-block {\n    grid-template-columns: minmax(0, 1fr);\n    gap: var(--vt-foundation-space-18);\n  }");
    // The pilot sentence left the first viewport (owner 2026-09-18).
    expect(marketingReact).not.toContain("hero-live-pilot");
    expect(marketingCSS).not.toContain(".hero-live-pilot");
    expect(marketingCSS).toContain("font-size: clamp(\n    var(--vt-foundation-font-size-body-strong),\n    1.25vw,\n    var(--vt-foundation-font-size-heading-small)\n  );");
    expect(marketingReact).not.toContain("Less deciding");
    expect(marketingReact).not.toContain("hero-route");
    expect(marketingReact).not.toContain("SiAirbnb");
    expect(marketingCSS).not.toContain(".hero-route");
    expect(marketingCSS).toContain("var(--vt-foundation-font-size-hero)");
    // The band is its own soft frosted strip on the glass: band blur, no rule lines.
    const band = marketingCSS.slice(marketingCSS.indexOf(".source-band {"), marketingCSS.indexOf(".source-band-inner {"));
    expect(band).toContain("backdrop-filter: var(--vt-semantic-effect-band-blur);");
    expect(band).not.toContain("border");
    // No visible label (owner 2026-09-18): the band shows only the names and keeps a spoken name.
    expect(marketingReact).not.toContain('ml("Searching", "Searching")');
    expect(marketingCSS).not.toContain(".source-band-label");
    expect(marketingReact).toContain('<p className="sr-only" id="source-band-title">');
    // Full-bleed like the header, with a little more room above and below the names.
    const bandInner = marketingCSS.slice(marketingCSS.indexOf(".source-band-inner {"), marketingCSS.indexOf(".source-band-viewport {"));
    expect(bandInner).toContain("padding-inline: clamp(\n    var(--vt-foundation-space-4),\n    1.667vw,\n    var(--vt-foundation-space-8)\n  );");
    expect(bandInner).toContain("padding-block: var(--vt-foundation-space-5);");
    expect(bandInner).not.toContain("var(--vt-foundation-size-content)");
    // The header keeps its rule line and never blurs.
    const header = marketingCSS.slice(marketingCSS.indexOf(".site-header--hero {"), marketingCSS.indexOf(".site-header--hero .brand-mark"));
    expect(header).toContain("border-color: color-mix(in srgb, var(--vt-marketing-text) 16%, transparent);");
    expect(header).not.toContain("backdrop-filter");
    for (const document of [html, koHtml]) {
      expect(document).toContain('<main class="hero-main">');
      expect(document).toContain('<section class="source-band" aria-labelledby="source-band-title">');
      expect(document).toContain('id="marketing-payment-pilot-root"');
      expect(document).not.toContain("hero-route");
    }
    expect(html).toContain('<p class="test-subtitle">Live payment and ordering are disabled by default</p>');
    expect(koHtml).toContain('<p class="test-subtitle">실결제와 실제 주문은 기본적으로 비활성화되어 있습니다</p>');
    expect(marketingReact).toContain("Gated payment pilots: PayPal, GIWA, and USDC. Activation required.");
  });

  it("marketing과 정책 문서가 현재 TEST·Live activation 경계를 같은 의미로 설명한다", () => {
    expect(html).toContain("Live payment and ordering are disabled by default");
    expect(html).toContain("It is not a public purchase path");
    expect(koHtml).toContain("실결제와 실제 주문은 기본적으로 비활성화되어 있습니다");
    expect(koHtml).toContain("현재 공개 구매 경로가 아닙니다");
    expect(marketingReact).toContain('ml("Current catalog source", "현재 카탈로그 소스")');
    expect(marketingReact).toContain('{ id: "shopify", label: ml("Shopify", "Shopify"), Icon: SiShopify }');
    expect(marketingReact).not.toContain('{ id: "amazon"');
    expect(marketingReact).not.toContain('{ id: "naver"');
    // Still Water PR F: light by default, dark by system preference or the
    // header toggle; theme.js runs before the stylesheets.
    for (const [document, lang] of [[html, "en"], [privacy, "en"], [terms, "en"], [koHtml, "ko"], [koPrivacy, "ko"], [koTerms, "ko"]] as const) {
      expect(document).toContain(`<html lang="${lang}" data-accent="blue">`);
      expect(document).not.toContain('data-theme="light"');
      expect(document.indexOf('<script src="/assets/theme.js')).toBeLessThan(document.indexOf('<link rel="stylesheet"'));
      expect(document).toContain('data-theme-toggle');
    }
    expect(readFileSync(path.join(repositoryRoot, "marketing/assets/theme.js"), "utf8")).toContain('"vitlane.marketing.theme"');
    expect(readFileSync(path.join(repositoryRoot, "marketing/assets/theme.js"), "utf8")).not.toContain("vitlane.appearance");
    expect(privacy).not.toContain("공개 문의");
    expect(privacy).not.toContain("구매 문의 폼");
    expect(terms).not.toContain("구매 문의 폼");
    expect(terms.replace(/\s+/g, " ")).toContain("GIWA Testnet refunds return no-value tVITUSD");
    expect(koTerms.replace(/\s+/g, " ")).toContain("TEST 정산 규칙에 따른 무가치 tVITUSD 반환");
    expect(terms).not.toContain("미사용금");
    for (const document of [html, privacy, terms, koHtml, koPrivacy, koTerms]) {
      expect(document.split(cloudflareAnalyticsSnippet)).toHaveLength(1);
      expect(document).not.toContain("cloudflareinsights.com");
      expect(document).toContain('id="marketing-analytics-root"');
    }
    expect(privacy.replace(/\s+/g, " ")).toContain("Google Analytics 4");
  });

  it("landing header는 언어 전환을 직접 노출하고 중복 제품 CTA를 두지 않는다", () => {
    for (const [documentSource, language, href] of [
      [html, "Korean", "/ko/"],
      [koHtml, "English", "/"],
    ]) {
      const document = new JSDOM(documentSource).window.document;
      const header = document.querySelector(".site-header");
      const localeLink = header?.querySelector<HTMLAnchorElement>(".header-locale-link");
      expect(localeLink?.textContent).toBe(language);
      expect(localeLink?.getAttribute("href")).toBe(href);
      expect(header?.querySelector(".header-product-link")).toBeNull();
    }
    expect(marketingCSS).toContain(".site-header nav > a.header-locale-link");
    expect(marketingCSS).toContain("display: inline-flex");
    expect(marketingReact).toContain('className="hero-product-link"');
  });

  it("페이지 언어는 seen으로, 언어 링크를 누를 때만 choice로 기록한다 (ADR-0080)", () => {
    const localeScript = readFileSync(path.join(repositoryRoot, "marketing/assets/locale.js"), "utf8");
    for (const [documentSource, pathname, seen, chosen] of [
      [html, "/", "en-US", "ko-KR"],
      [koHtml, "/ko/", "ko-KR", "en-US"],
    ]) {
      const dom = new JSDOM(documentSource, { url: `https://vitlane.test${pathname}`, runScripts: "outside-only" });
      const cookie = () => dom.window.document.cookie;
      dom.window.eval(localeScript);
      expect(cookie()).toContain(`vt_locale_seen=${seen}`);
      expect(cookie()).not.toMatch(/(^|; )vt_locale(_choice)?=/);
      dom.window.document.querySelector(".brand-mark").dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
      expect(cookie()).not.toContain("vt_locale_choice=");
      const link = dom.window.document.querySelector(".header-locale-link");
      link.addEventListener("click", (event) => event.preventDefault());
      link.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, cancelable: true }));
      expect(cookie()).toContain(`vt_locale_choice=${chosen}`);
    }
    expect(marketingReact).not.toContain("document.cookie");
    for (const document of [html, koHtml, privacy, koPrivacy, terms, koTerms]) {
      expect(document).toContain('<script src="/assets/locale.js?v=');
    }
  });

  it("영어 canonical fallback과 Korean alternate를 완전한 URL로 게시한다", () => {
    const documents = [
      [html, "https://vitlane.com/", "https://vitlane.com/", "https://vitlane.com/ko/"],
      [koHtml, "https://vitlane.com/ko/", "https://vitlane.com/", "https://vitlane.com/ko/"],
      [privacy, "https://vitlane.com/privacy/", "https://vitlane.com/privacy/", "https://vitlane.com/ko/privacy/"],
      [koPrivacy, "https://vitlane.com/ko/privacy/", "https://vitlane.com/privacy/", "https://vitlane.com/ko/privacy/"],
      [terms, "https://vitlane.com/terms/", "https://vitlane.com/terms/", "https://vitlane.com/ko/terms/"],
      [koTerms, "https://vitlane.com/ko/terms/", "https://vitlane.com/terms/", "https://vitlane.com/ko/terms/"],
    ];
    for (const [documentSource, canonical, english, korean] of documents) {
      const document = new JSDOM(documentSource).window.document;
      expect(document.querySelector<HTMLLinkElement>('link[rel="canonical"]')?.href).toBe(canonical);
      const alternates = Object.fromEntries(
        [...document.querySelectorAll<HTMLLinkElement>('link[rel="alternate"][hreflang]')]
          .map((link) => [link.hreflang, link.href]),
      );
      expect(alternates).toEqual({ en: english, ko: korean, "x-default": english });
    }

    const sitemapDocument = new JSDOM(sitemap, { contentType: "application/xml" }).window.document;
    const urls = [...sitemapDocument.getElementsByTagName("url")];
    expect(urls).toHaveLength(6);
    for (const url of urls) {
      expect(url.getElementsByTagName("loc")[0]?.textContent).toMatch(/^https:\/\/vitlane\.com\//);
      const alternates = url.getElementsByTagNameNS("http://www.w3.org/1999/xhtml", "link");
      expect(alternates).toHaveLength(3);
      expect([...alternates].map((link) => link.getAttribute("hreflang"))).toEqual([
        "en",
        "ko",
        "x-default",
      ]);
    }
    expect(robots).toBe("User-agent: *\nAllow: /\n\nSitemap: https://vitlane.com/sitemap.xml\n");
  });

  it("로컬 marketing CTA는 운영 app이 아니라 같은 로컬 서버로 이어진다", () => {
    const dom = marketingDOM("http://marketing.localhost:18080/");
    const productLink = dom.window.document.createElement("a");
    productLink.setAttribute("data-vitlane-app-path", "/");
    productLink.href = "https://app.vitlane.com/";
    dom.window.document.body.append(productLink);
    dom.window.eval(script);
    expect(productLink.href).toBe("http://127.0.0.1:18080/");
    expect(marketingReact).not.toContain('appHref("/plans/new")');
    dom.window.close();
  });

  it("landing은 설치 action을 노출하지 않는다", () => {
    for (const source of [html, koHtml]) {
      const document = new JSDOM(source).window.document;
      expect(document.querySelector(".hero-install-link")).toBeNull();
    }
  });

  it("모든 로고가 같은 lane stroke를 Still Water 시드로 사용한다", () => {
    expect(new Set(brandMarks).size).toBe(1);
    expect(brandMarks[0]).toContain(
      'd="M4.5 5.5 12.25 26.5 20 5.5M19.75 26.5 27.5 5.5"',
    );
    expect(brandMarks[0]).toContain(`stroke="${stillSeed}"`);
    expect(brandMarks[0]).toContain('stroke-linecap="round"');
    expect(brandMarks[0]).toContain('stroke-linejoin="round"');
  });

  it("파비콘은 같은 mark를 Still Water 타일 위에 흰색으로 올린 파생물이다", () => {
    expect(new Set(favicons).size).toBe(1);
    expect(favicons[0]).toContain(`fill="${stillSeed}"`);
    expect(favicons[0]).toContain('d="M4.5 5.5 12.25 26.5 20 5.5M19.75 26.5 27.5 5.5"');
    expect(favicons[0]).toContain('rx="7"');
    expect(html).toContain('<link rel="apple-touch-icon" href="/assets/apple-touch-icon.png" />');
    expect(html).toContain('<link rel="icon" href="/assets/favicon.ico" sizes="32x32" />');
  });
});

function marketingDOM(url = "https://vitlane.com/") {
  const dom = new JSDOM(html, { runScripts: "outside-only", url });
  Object.defineProperty(dom.window.document, "hidden", { value: false });
  Object.defineProperty(dom.window, "matchMedia", { value: () => ({ matches: false }) });
  Object.defineProperty(dom.window, "requestAnimationFrame", {
    value: (callback: FrameRequestCallback) => { callback(0); return 1; },
  });
  return dom;
}
