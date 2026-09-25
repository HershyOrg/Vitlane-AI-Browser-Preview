const assert = require("node:assert/strict");
const { firefox } = require("playwright");

const baseURL = (process.env.E2E_BASE_URL || "http://127.0.0.1:18080").replace(/\/$/, "");
const marketingBaseURL = (process.env.MARKETING_BASE_URL || baseURL).replace(/\/$/, "");
const marketingHostHeader = process.env.MARKETING_HOST_HEADER;
const hangul = /[\uac00-\ud7a3]/u;

const operatorRoutes = [
  ["/account", "Account"],
  ["/admin/agencyOrder", "Order processing"],
  ["/admin/agencyOrder/exceptions", "Exception operations"],
  ["/admin/order-accounting", "Cash truth by order"],
  ["/admin/support", "Customer conversations"],
  ["/admin/api-usage", "API usage"],
  ["/admin/ops", "Operations status and actions"],
];

const customerRoutes = [
  ["/", "What are you looking for?", "무엇을 찾고 있나요?"],
  ["/plans/new", "What are you looking for?", "무엇을 찾고 있나요?"],
  ["/agencyOrder", "Orders, payment, and agency processing", "주문·결제와 구매대행 처리"],
  [
    "/curations/e5100000-0000-4000-8000-000000000002",
    "Research and display settings",
    "조사·보기 설정",
    "button",
  ],
];

async function visiblePresentation(page) {
  return page.locator("body").evaluate((body) => {
    const attributes = [...body.querySelectorAll("[aria-label], [title], [placeholder]")]
      .flatMap((element) => [
        element.getAttribute("aria-label"),
        element.getAttribute("title"),
        element.getAttribute("placeholder"),
      ])
      .filter(Boolean);
    return `${body.innerText}\n${attributes.join("\n")}`;
  });
}

function assertEnglishPresentation(text, route, invariantContent = []) {
  const fixedCopyOnly = invariantContent.reduce(
    (value, content) => value.replaceAll(content, ""),
    text,
  );
  const match = fixedCopyOnly.match(hangul);
  assert.equal(match, null, `${route} leaked Hangul in English fixed-copy mode near: ${fixedCopyOnly.slice(Math.max(0, match?.index - 60), (match?.index || 0) + 120)}`);
}

// Explicit account selection seeds this existing EN matrix. First-visit defaults
// are asserted in checkLogin and checkAccountDefaultsFollowBrowser.
async function selectAccountEnglish(context) {
  const read = await context.request.get(`${baseURL}/api/v1/me/preferences`);
  assert.equal(read.ok(), true);
  const { preferences } = await read.json();
  const write = await context.request.patch(`${baseURL}/api/v1/me/preferences`, {
    data: { schemaVersion: "vitlane.user-preferences.v1", expectedVersion: preferences.version, uiLocale: "en-US" },
  });
  assert.equal(write.ok(), true);
}

async function createOperatorSession(context) {
  const response = await context.request.post(`${baseURL}/api/v1/dev/auth/session`, {
    data: { profileKey: "empty-operator" },
  });
  assert.equal(response.ok(), true, `operator test session failed: ${response.status()} ${await response.text()}`);
  await selectAccountEnglish(context);
}

async function createCustomerSession(context) {
  const response = await context.request.post(`${baseURL}/api/v1/dev/auth/session`, {
    data: { profileKey: "multi-product" },
  });
  assert.equal(response.ok(), true, `customer test session failed: ${response.status()} ${await response.text()}`);
  await selectAccountEnglish(context);
}

// ADR-0080: with nothing chosen or seen, the browser language decides. Showing
// a default is recorded as seen, never as a choice; the legacy vt_locale cookie
// is ignored.
async function checkLogin(browser) {
  for (const [browserLocale, heading, lang] of [["en-US", "Sign in", "en"], ["ko-KR", "로그인", "ko"]]) {
    const context = await browser.newContext({ locale: browserLocale });
    await context.addCookies([{ name: "vt_locale", value: browserLocale === "ko-KR" ? "en-US" : "ko-KR", url: baseURL }]);
    const page = await context.newPage();
    await page.goto(`${baseURL}/login`, { waitUntil: "networkidle" });
    await page.getByRole("heading", { name: heading, exact: true }).waitFor();
    assert.equal(await page.locator("html").getAttribute("lang"), lang);
    if (lang === "en") assertEnglishPresentation(await visiblePresentation(page), "/login");
    else assert.match(await visiblePresentation(page), /로그인이 필요한 서비스입니다/u);
    const cookies = await context.cookies(baseURL);
    assert.equal(cookies.find((cookie) => cookie.name === "vt_locale_seen")?.value, browserLocale);
    assert.equal(cookies.some((cookie) => cookie.name === "vt_locale_choice"), false, "a default must not become a choice");
    await context.close();
  }

  // Arriving from the Korean landing keeps Korean in an English browser.
  const landedContext = await browser.newContext({ locale: "en-US" });
  await landedContext.addCookies([{ name: "vt_locale_seen", value: "ko-KR", url: baseURL }]);
  const landed = await landedContext.newPage();
  await landed.goto(`${baseURL}/login`, { waitUntil: "networkidle" });
  await landed.getByRole("heading", { name: "로그인", exact: true }).waitFor();
  assert.equal(await landed.locator("html").getAttribute("lang"), "ko");
  await landedContext.close();

  // An explicit choice beats both the last seen language and the browser.
  const chosenContext = await browser.newContext({ locale: "ko-KR" });
  await chosenContext.addCookies([
    { name: "vt_locale_choice", value: "en-US", url: baseURL },
    { name: "vt_locale_seen", value: "ko-KR", url: baseURL },
  ]);
  const chosen = await chosenContext.newPage();
  await chosen.goto(`${baseURL}/login`, { waitUntil: "networkidle" });
  await chosen.getByRole("heading", { name: "Sign in", exact: true }).waitFor();
  assert.equal(await chosen.locator("html").getAttribute("lang"), "en");
  await chosenContext.close();
}

// An account without selections is projected from the requesting browser and
// never stored. The profile may carry selections from earlier runs, which win.
async function checkAccountDefaultsFollowBrowser(browser) {
  for (const [browserLocale, currency, country] of [["ko-KR", "KRW", "KR"], ["en-US", "USD", "US"]]) {
    const context = await browser.newContext({ locale: browserLocale });
    const session = await context.request.post(`${baseURL}/api/v1/dev/auth/session`, {
      data: { profileKey: "empty-user" },
    });
    assert.equal(session.ok(), true, `empty-user test session failed: ${session.status()} ${await session.text()}`);
    const read = await context.request.get(`${baseURL}/api/v1/me/preferences`, {
      headers: { "Accept-Language": browserLocale },
    });
    assert.equal(read.ok(), true);
    const { preferences, effective } = await read.json();
    assert.equal(effective.uiLocale, preferences.uiLocale ?? browserLocale);
    assert.equal(effective.preferredCurrency, preferences.preferredCurrency ?? currency);
    assert.equal(effective.researchCountry, preferences.researchCountry ?? country);
    await context.close();
  }
}

async function checkOperatorMatrixAndSwitch(browser) {
  const context = await browser.newContext({ locale: "en-US", viewport: { width: 1440, height: 1000 } });
  await createOperatorSession(context);
  const page = await context.newPage();

  for (const [route, heading] of operatorRoutes) {
    await page.goto(`${baseURL}${route}`, { waitUntil: "networkidle" });
    await page.getByRole("heading", { name: heading, exact: true }).first().waitFor();
    assert.equal(await page.locator("html").getAttribute("lang"), "en", `${route} did not stay in English mode`);
    assertEnglishPresentation(await visiblePresentation(page), route, ["빈 운영자 사용자", "빈"]);
  }

  await page.goto(`${baseURL}/admin/agencyOrder`, { waitUntil: "networkidle" });
  await page.getByRole("button", { name: "Profile menu" }).click();
  await page.getByText("Language", { exact: true }).click();
  await page.getByRole("radio", { name: "Korean", exact: true }).click();
  await page.getByRole("heading", { name: "주문 처리", exact: true }).waitFor();
  assert.equal(await page.locator("html").getAttribute("lang"), "ko");
  assert.match(await visiblePresentation(page), /다른 담당 범위·단계를 선택/u);

  await page.getByRole("button", { name: "프로필 메뉴" }).click();
  await page.getByText("언어", { exact: true }).click();
  await page.getByRole("radio", { name: "영어", exact: true }).click();
  await page.getByRole("heading", { name: "Order processing", exact: true }).waitFor();
  assert.equal(await page.locator("html").getAttribute("lang"), "en");
  assertEnglishPresentation(await visiblePresentation(page), "/admin/agencyOrder after UI mode switch", ["빈 운영자 사용자", "빈"]);

  const cookies = await context.cookies(baseURL);
  assert.equal(cookies.find((cookie) => cookie.name === "vt_locale_choice")?.value, "en-US");
  await context.close();
}

async function checkCustomerMatrix(browser) {
  const context = await browser.newContext({ locale: "en-US", viewport: { width: 1440, height: 1000 } });
  await createCustomerSession(context);
  const page = await context.newPage();
  for (const [route, englishCopy, koreanFixedCopy, role] of customerRoutes) {
    await page.goto(`${baseURL}${route}`, { waitUntil: "networkidle" });
    const translatedControl = role === "button"
      ? page.getByRole("button", { name: englishCopy, exact: true })
      : page.getByText(englishCopy, { exact: true });
    await translatedControl.first().waitFor();
    assert.equal(await page.locator("html").getAttribute("lang"), "en", `${route} did not stay in English mode`);
    assert.equal((await visiblePresentation(page)).includes(koreanFixedCopy), false, `${route} retained Korean fixed copy in English mode`);
  }
  await context.close();
}

async function checkMarketingPair(browser) {
  const extraHTTPHeaders = marketingHostHeader ? { Host: marketingHostHeader } : undefined;
  const context = await browser.newContext({
    locale: "en-US",
    viewport: { width: 390, height: 844 },
    extraHTTPHeaders,
  });
  await context.route("https://static.cloudflareinsights.com/**", (route) => route.fulfill({ status: 200, body: "" }));
  const page = await context.newPage();

  await page.goto(`${marketingBaseURL}/`, { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "First day at work? On a tight budget? Particular taste? Buying on repeat? You still buy better.", exact: true }).waitFor();
  assert.equal(await page.locator("html").getAttribute("lang"), "en");
  assertEnglishPresentation(await visiblePresentation(page), "marketing /");
  const englishHeader = page.locator(".site-header");
  const koreanLink = englishHeader.getByRole("link", { name: "Korean", exact: true });
  assert.equal(await koreanLink.isVisible(), true, "mobile English header must expose the Korean link");
  assert.equal(await englishHeader.getByRole("link", { name: "Open the product", exact: true }).count(), 0);
  assert.equal(await page.locator(".hero-product-link").isVisible(), true, "body product CTA must remain visible");
  assert.equal(await page.locator(".hero-install-link").count(), 0, "English landing must leave installation to the app");
  assert.equal(
    await page.locator(".hero-social-link").getByText("See on X", { exact: true }).isVisible(),
    true,
    "English hero must expose the X link",
  );
  await englishHeader.getByRole("button", { name: "Mobile menu", exact: true }).click();
  const englishMenu = page.locator(".marketing-mobile-menu");
  await englishMenu.getByText("Service & payment terms", { exact: true }).waitFor();
  assert.equal(await englishMenu.getByText("Korean", { exact: true }).count(), 0, "locale must not be inside the dropdown");
  await page.keyboard.press("Escape");
  await englishMenu.waitFor({ state: "hidden" });
  await Promise.all([
    page.waitForURL("**/ko/", { waitUntil: "domcontentloaded" }),
    koreanLink.click(),
  ]);

  await page.getByRole("heading", { name: "첫 출근이어도, 절약이 필요해도, 세심한 취향도, 반복적인 구매도, 더 잘 삽니다.", exact: true }).waitFor();
  assert.equal(await page.locator("html").getAttribute("lang"), "ko");
  const marketingCookies = await context.cookies(marketingBaseURL);
  assert.equal(marketingCookies.find((cookie) => cookie.name === "vt_locale_choice")?.value, "ko-KR", "the language link is an explicit choice");
  assert.equal(marketingCookies.find((cookie) => cookie.name === "vt_locale_seen")?.value, "ko-KR");
  assert.match(await visiblePresentation(page), /더 잘 삽니다/u);
  const koreanHeader = page.locator(".site-header");
  assert.equal(
    await koreanHeader.getByRole("link", { name: "English", exact: true }).isVisible(),
    true,
    "mobile Korean header must expose the English link",
  );
  assert.equal(await koreanHeader.getByRole("link", { name: "제품 가기", exact: true }).count(), 0);
  assert.equal(await page.locator(".hero-install-link").count(), 0, "Korean landing must leave installation to the app");
  assert.equal(
    await page.locator(".hero-social-link").getByText("X에서 보기", { exact: true }).isVisible(),
    true,
    "Korean hero must expose the localized X link",
  );

  // ADR-0080: the English link on the Korean page keeps English; nothing bounces back.
  await Promise.all([
    page.waitForURL((url) => url.pathname === "/", { waitUntil: "domcontentloaded" }),
    koreanHeader.getByRole("link", { name: "English", exact: true }).click(),
  ]);
  await page.getByRole("heading", { name: "First day at work? On a tight budget? Particular taste? Buying on repeat? You still buy better.", exact: true }).waitFor();
  assert.equal((await context.cookies(marketingBaseURL)).find((cookie) => cookie.name === "vt_locale_choice")?.value, "en-US");
  await context.close();

  // Arrival from outside the site: only English canonical pages move, and only
  // when the visitor resolves to Korean.
  for (const [browserLocale, cookies, path, finalPath] of [
    ["ko-KR", [], "/", "/ko/"],
    ["ko-KR", [], "/privacy/", "/ko/privacy/"],
    ["en-US", [{ name: "vt_locale_seen", value: "ko-KR" }], "/", "/ko/"],
    ["en-US", [], "/", "/"],
    ["en-US", [], "/ko/", "/ko/"],
    ["ko-KR", [{ name: "vt_locale_choice", value: "en-US" }], "/", "/"],
  ]) {
    const arrival = await browser.newContext({
      locale: browserLocale,
      viewport: { width: 1280, height: 900 },
      extraHTTPHeaders,
    });
    await arrival.route("https://static.cloudflareinsights.com/**", (route) => route.fulfill({ status: 200, body: "" }));
    await arrival.addCookies(cookies.map((cookie) => ({ ...cookie, url: marketingBaseURL })));
    const arrivalPage = await arrival.newPage();
    await arrivalPage.goto(`${marketingBaseURL}${path}`, { waitUntil: "domcontentloaded" });
    assert.equal(new URL(arrivalPage.url()).pathname, finalPath, `${browserLocale} ${path} with ${JSON.stringify(cookies)}`);
    assert.equal(await arrivalPage.locator("html").getAttribute("lang"), finalPath.startsWith("/ko/") ? "ko" : "en");
    await arrival.close();
  }
}

async function run() {
  const browser = await firefox.launch({ headless: true });
  try {
    await checkLogin(browser);
    await checkAccountDefaultsFollowBrowser(browser);
    await checkCustomerMatrix(browser);
    await checkOperatorMatrixAndSwitch(browser);
    await checkMarketingPair(browser);
    console.log("English/Korean locale matrix Firefox E2E: PASS");
  } finally {
    await browser.close();
  }
}

run().catch((error) => {
  console.error("English/Korean locale matrix Firefox E2E: FAIL");
  console.error(error.stack || error);
  process.exitCode = 1;
});
