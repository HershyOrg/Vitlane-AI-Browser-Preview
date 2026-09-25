const { firefox } = require("playwright");
const fs = require("node:fs");
const path = require("node:path");

const baseURL = process.env.MARKETING_BASE_URL || "http://marketing.localhost:8080";
const appBaseURL = (process.env.APP_BASE_URL || "http://127.0.0.1:8080").replace(/\/$/, "");
const expectedAppBaseURL = (process.env.EXPECTED_APP_BASE_URL || appBaseURL).replace(/\/$/, "");
const marketingHostHeader = process.env.MARKETING_HOST_HEADER;
const marketingOriginHeader = process.env.MARKETING_ORIGIN_HEADER;
const adminUserID = process.env.MARKETING_ADMIN_USER_ID;
const screenshotDir = process.env.MARKETING_SCREENSHOT_DIR || "/tmp/vitlane-marketing-smoke";
let browser;

(async () => {
  fs.mkdirSync(screenshotDir, { recursive: true });
  browser = await firefox.launch({ headless: true });
  const extraHTTPHeaders = {};
  if (marketingHostHeader) extraHTTPHeaders.Host = marketingHostHeader;
  if (marketingOriginHeader) extraHTTPHeaders.Origin = marketingOriginHeader;
  const page = await browser.newPage({
    viewport: { width: 1440, height: 1000 },
    reducedMotion: "no-preference",
    ...(Object.keys(extraHTTPHeaders).length > 0 ? { extraHTTPHeaders } : {}),
  });
  await page.route("https://static.cloudflareinsights.com/**", (route) =>
    route.fulfill({ status: 200, contentType: "application/javascript", body: "" }),
  );
  await page.goto(`${baseURL}/`, { waitUntil: "domcontentloaded" });

  await page.locator("#hero-title").waitFor();
  // 2026-09-18: one condition at a time rotates over the fixed result clause.
  const initialResult = await page.locator(".hero-title-result").innerText();
  const observedConditions = new Set();
  for (let index = 0; index < 9; index += 1) {
    observedConditions.add((await page.locator(".hero-title-live").innerText()).trim());
    await page.waitForTimeout(500);
  }
  const changedResult = await page.locator(".hero-title-result").innerText();
  const productHref = await page
    .getByRole("link", { name: "Open the product", exact: true })
    .first()
    .getAttribute("href");
  const xLink = page.locator(".hero-social-link");
  await page.locator("#lane").scrollIntoViewIfNeeded();
  await page.waitForFunction(() => [...document.querySelectorAll(".lane-frame > img")].every((image) => image.complete && image.naturalWidth > 0));
  await page.evaluate(() => window.scrollTo(0, 0));
  const result = {
    heroTitleName: (await page.getByRole("heading", { level: 1 }).getAttribute("id")) === "hero-title" &&
      await page.getByRole("heading", { name: "First day at work? On a tight budget? Particular taste? Buying on repeat? You still buy better.", exact: true }).count() === 1,
    heroResultStill: initialResult === "You still buy better." && changedResult === initialResult,
    heroConditionRotates:
      observedConditions.size >= 2 &&
      [...observedConditions].every((condition) =>
        ["First day at work?", "On a tight budget?", "Particular taste?", "Buying on repeat?"].includes(condition)),
    routeLineRemoved: await page.locator(".hero-route").count() === 0,
    sourceBandVisible: await page.locator(".source-band .source-band-list").first().isVisible(),
    sourceBandMatchesDefaultProvider:
      await page.locator(".source-band-store", { hasText: "Shopify" }).count() >= 1 &&
      await page.locator(".source-band-store", { hasText: /Amazon|Naver|Coupang/ }).count() === 0,
    paymentPilotTitle: await page.getByRole("heading", {
      name: "Gated payment pilots: PayPal, GIWA, and USDC. Activation required.",
      exact: true,
    }).count() === 1,
    // The pilot sentence left the first viewport (owner 2026-09-18).
    paypalLivePilotCopyRemoved: await page.locator(".hero-live-pilot").count() === 0,
    heroKeyWordAccented: await page.locator(".hero-title-live .hero-title-key").evaluate((key) =>
      getComputedStyle(key).color !== getComputedStyle(document.querySelector(".hero-title-result")).color),
    livePaymentDisabledCopy: await page.getByText(
      "Live payment and ordering are disabled by default",
      { exact: false },
    ).isVisible(),
    inquiryRemoved: await page.locator("#marketing-inquiry-root, #inquiry-form").count() === 0,
    heroGlassVisible: await page.locator(".living-lane .vt-reeded-glass canvas").isVisible(),
    productLinkCorrect: productHref === `${expectedAppBaseURL}/`,
    installLinkAbsent: await page.locator(".hero-install-link").count() === 0,
    xLinkVisible: await xLink.getByText("See on X", { exact: true }).isVisible(),
    xLinkCorrect: await xLink.getAttribute("href") === "https://x.com/Vitlane_",
    xLinkOpensNewTab: await xLink.getAttribute("target") === "_blank",
    // N8 (ADR-0073): placeholder quotes never reach a release build. Dev
    // builds (VITE_ALLOW_DEV_AUTH_UI=true, as in CI compose) may show them.
    buildMode: (await page.evaluate(() => document.documentElement.dataset.releaseBuild === "true")) ? "release" : "dev",
    stubVoicesAbsent:
      (await page.evaluate(() => document.documentElement.dataset.releaseBuild !== "true")) ||
      (await page.locator(".voice[data-stub]").count()) === 0,
    // ADR-0074: the one Lane section keeps its five app frames loaded.
    laneFramesLoaded: await page.locator(".lane-frame > img").evaluateAll((images) =>
      images.length === 5 && images.every((image) => image.complete && image.naturalWidth > 0),
    ),
  };
  await page.screenshot({ path: path.join(screenshotDir, "marketing-landing.png"), fullPage: true });
  await page.goto(`${baseURL}/ko/`, { waitUntil: "domcontentloaded" });
  result.koreanPayPalLivePilotCopyRemoved = await page.locator(".hero-live-pilot").count() === 0;
  result.koreanLivePaymentDisabledCopy = await page.getByText(
    "실결제와 실제 주문은 기본적으로 비활성화되어 있습니다",
    { exact: false },
  ).isVisible();
  result.koreanXLinkVisible = await page.locator(".hero-social-link")
    .getByText("X에서 보기", { exact: true })
    .isVisible();
  result.koreanInstallLinkAbsent = await page.locator(".hero-install-link").count() === 0;
  await page.close();

  if (adminUserID) {
    const adminPage = await browser.newPage({ locale: "ko-KR", viewport: { width: 1440, height: 1000 } });
    await adminPage.goto(`${appBaseURL}/`, { waitUntil: "domcontentloaded" });
    const session = await adminPage.evaluate(async (userID) => {
      const response = await fetch("/api/v1/dev/auth/session", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ userId: userID }),
      });
      return { ok: response.ok, body: await response.json() };
    }, adminUserID);
    await adminPage.goto(`${appBaseURL}/admin/ops`, { waitUntil: "domcontentloaded" });
    result.adminSessionAllowed = session.ok && session.body.user.marketingAdmin === true;
    result.adminTabRemoved = await adminPage.getByRole("link", { name: "문의함" }).count() === 0;
    result.inquiryAPIRemoved = (await adminPage.request.get(`${appBaseURL}/api/v1/admin/marketing-inquiries`)).status() === 404;
    await adminPage.close();
  }

  await browser.close();
  browser = undefined;
  console.log(JSON.stringify(result));
  if (!Object.values(result).every(Boolean)) process.exitCode = 1;
})().catch(async (error) => {
  console.error(error);
  await browser?.close();
  process.exitCode = 1;
});
