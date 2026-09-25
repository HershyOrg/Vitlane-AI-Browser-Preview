const assert = require("node:assert/strict");
const { firefox } = require("playwright");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:18080").replace(/\/$/, "");
const returnTo = "/session-expiry-regression?view=cart";
const loginPath = `/login?returnTo=${encodeURIComponent(returnTo)}`;
const user = {
  id: "session-expiry-fixture", email: "fixture@vitlane.test", displayName: "Fixture User",
  createdAt: "2026-09-01T00:00:00Z", marketingAdmin: false, phase5Operator: false,
};

// Exercise the shipped router/provider/login components with isolated HTTP
// fixtures. No real session, Google request, or product mutation is made.
async function check(browser, locale, viewport) {
  const context = await browser.newContext({ locale, viewport });
  await context.addInitScript(value => localStorage.setItem("vitlane.locale.v2", value), locale);
  const page = await context.newPage();
  let expired = false;
  let documentRequests = 0;
  let googleStarts = 0;
  const unexpectedRequests = [];
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("request", (request) => {
    if (request.resourceType() === "document") documentRequests += 1;
  });
  await context.route(`${baseURL}/api/**`, async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const noticeSync = request.method() === "POST" && url.pathname === "/api/v1/curations/product-notices/sync";
    if (request.method() !== "GET" && !noticeSync) {
      unexpectedRequests.push(`${request.method()} ${url.pathname}`);
      return route.abort();
    }
    if (url.pathname === "/api/v1/analytics/config") {
      return route.fulfill({ json: { schemaVersion: "vitlane.analytics-config.v1", mode: "disabled", measurementId: "", release: "fixture" } });
    }
    if (url.pathname === "/api/v1/auth/capabilities") {
      return route.fulfill({ json: {
        googleEnabled: true, localReviewEnabled: false, localReviewSeeded: false,
        localReviewProfiles: [], merchantEffectMode: "SANDBOX",
      } });
    }
    if (url.pathname === "/api/v1/auth/google/start") {
      googleStarts += 1;
      assert.equal(url.searchParams.get("returnTo"), returnTo);
      expired = false;
      return route.fulfill({ status: 302, headers: { location: `${baseURL}${returnTo}` } });
    }
    if (expired) {
      return route.fulfill({ status: 401, json: { error: {
        code: "AUTH_SESSION_EXPIRED", message: "Session expired",
      } } });
    }
    if (noticeSync) return route.fulfill({ json: { schemaVersion: "vitlane.curation-notices.v1", curationIds: [] } });
    if (url.pathname === "/api/v1/me/preferences") {
      const preferences = { schemaVersion: "vitlane.user-preferences.v1", version: 0, uiLocale: locale, preferredCurrency: "KRW", researchCountry: "KR" };
      return route.fulfill({ json: { preferences, effective: preferences } });
    }
    if (url.pathname === "/api/v1/me") return route.fulfill({ json: { user } });
    // One sidebar row gives the tab a newest-known cursor to ask after (ADR-0079).
    if (url.pathname === "/api/v1/curations") return route.fulfill({ json: {
      schemaVersion: "vitlane.curation-list.v2",
      curations: [{ curationId: "5e551010-0000-4000-8000-000000000001", intentSummary: "Session expiry fixture", createdAt: "2026-09-01T00:00:00Z" }],
      latestCursor: "session-expiry-latest",
    } });
    if (url.pathname === "/api/v1/support/summary") return route.fulfill({ json: { schemaVersion: "support.summary.v1", unread: 0 } });
    unexpectedRequests.push(`${request.method()} ${url.pathname}`);
    return route.fulfill({ status: 404, json: { error: { code: "NOT_FOUND" } } });
  });

  try {
    await page.goto(`${baseURL}${returnTo}`, { waitUntil: "networkidle" });
    // The protected catch-all route avoids needing any Curation/Order fixture.
    await page.locator(".catalog-ui-not-found").waitFor();
    expired = true;
    // Trigger a protected sidebar request on focus instead of waiting for the
    // product-notice poll. Both paths must honor the expired session.
    await page.evaluate(() => window.dispatchEvent(new Event("focus")));
    const expiredHeading = locale === "en-US" ? "Your session has expired" : "로그인 세션이 만료되었습니다";
    await page.getByRole("heading", { name: expiredHeading, exact: true }).waitFor({ timeout: 12_000 });
    assert.equal(await page.getByRole("heading", { name: expiredHeading, exact: true }).count(), 1);
    await page.getByRole("button", { name: locale === "en-US" ? "Sign in again" : "다시 로그인", exact: true }).click();
    await page.waitForURL(`${baseURL}${loginPath}`);
    const google = page.getByRole("link", { name: locale === "en-US" ? "Continue with Google" : "Google로 계속", exact: true });
    await google.waitFor();
    assert.equal(await google.getAttribute("href"), `/api/v1/auth/google/start?returnTo=${encodeURIComponent(returnTo)}`);
    assert.equal(documentRequests, 1, "sign-in recovery must work without reloading the document");
    assert.equal(await page.locator(".catalog-ui-not-found").count(), 0, "stale user must not bounce back to the protected route");
    await google.click();
    await page.waitForURL(`${baseURL}${returnTo}`);
    await page.locator(".catalog-ui-not-found").waitFor();
    assert.equal(googleStarts, 1);
    assert.deepEqual(pageErrors, []);
    assert.deepEqual(unexpectedRequests, []);
    console.log(`auth-session-expiry: ${locale} ${viewport.width}px PASS (HTTP fixtures; Google handoff simulated)`);
  } finally {
    await context.close();
  }
}

async function run() {
  const browser = await firefox.launch({ headless: true });
  try {
    for (const locale of ["en-US", "ko-KR"]) {
      for (const viewport of [{ width: 1440, height: 900 }, { width: 320, height: 568 }]) {
        await check(browser, locale, viewport);
      }
    }
  } finally {
    await browser.close();
  }
}

run().catch((error) => {
  console.error(error.stack ?? error);
  process.exitCode = 1;
});
