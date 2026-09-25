// Explicit opt-in: this suite sends consented synthetic page views to production.
// It never logs cookies, client IDs, raw payloads, or request URLs.
const assert = require("node:assert/strict");
const { firefox, webkit } = require("playwright");

const origin = "https://vitlane.com";
const canaries = ["analytics-private-canary", "synthetic@example.invalid"];
const google = host => /(^|\.)(google-analytics\.com|googletagmanager\.com|doubleclick\.net|googleadservices\.com)$/.test(host);
async function until(check, page) {
  const end = Date.now() + 20000;
  while (!check() && Date.now() < end) await page.waitForTimeout(100);
  assert(check(), "expected Google collection request was not observed");
}
async function main() {
  assert.equal(process.env.VITLANE_ANALYTICS_LIVE, "1", "live verification requires explicit opt-in");
  const kind = process.env.ANALYTICS_BROWSER || "firefox";
  assert(["firefox", "webkit"].includes(kind));
  const browser = await ({ firefox, webkit })[kind].launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, locale: "ko-KR" });
    const page = await context.newPage();
    const requests = [], events = [], leaks = [], responses = [], failures = [];
    page.on("request", req => {
      const url = new URL(req.url());
      if (!google(url.hostname)) return;
      requests.push(url.hostname);
      const raw = url.search + "&" + (req.postData() || "");
      let decoded = raw;
      for (let i = 0; i < 2; i++) {
        try { decoded = decodeURIComponent(decoded); } catch {}
      }
      if (canaries.some(value => decoded.includes(value))) leaks.push("raw_input");
      const referer = req.headers()["referer"] || "";
      if (canaries.some(value => referer.includes(value))) leaks.push("raw_referrer");
      if (!url.pathname.endsWith("/collect")) return;
      const lines = (req.postData() || "").split("\n");
      for (const line of lines) {
        const fields = new URLSearchParams(url.search + "&" + line);
        const name = fields.get("en");
        if (!name) continue;
        const dl = fields.get("dl");
        if (!["https://vitlane.com/landing", "https://vitlane.com/privacy", "https://vitlane.com/terms"].includes(dl)) leaks.push("unexpected_page_location");
        const customKeys = [...fields.keys()].filter(key => /^epn?\./.test(key));
        if (customKeys.some(key => /email|address|query|intent|phone|raw_url/.test(key))) leaks.push("unexpected_custom_field");
        events.push({ name, location: dl, customKeys, hasEventKey: /^[a-f0-9-]{36}$/.test(fields.get("ep.event_key") || "") });
      }
    });
    page.on("response", res => {
      const url = new URL(res.url());
      if (google(url.hostname) && url.pathname.endsWith("/collect")) responses.push(res.status());
    });
    page.on("requestfailed", req => {
      const url = new URL(req.url());
      if (google(url.hostname)) failures.push(url.hostname);
    });
    const banner = () => page.getByRole("region", { name: /이용 정보 제공 동의|Usage information consent/ });
    const settings = () => page.getByRole("button", { name: /정보 제공 설정|Usage information preferences/, exact: true });
    const decline = () => page.getByRole("button", { name: /동의하지 않기|Decline/, exact: true });
    const allow = () => page.getByRole("button", { name: /허용하기|Allow/, exact: true });
    const query = "?q=" + encodeURIComponent(canaries[0]) + "&email=" + encodeURIComponent(canaries[1]);
    const configResponse = await context.request.get(origin + "/api/v1/analytics/config");
    assert.equal(configResponse.status(), 200);
    const config = await configResponse.json();
    assert.equal(config.mode, "ga4", "deployed GA4 mode is not active");
    assert.match(config.release, /^[a-f0-9]{40}$/, "runtime source revision is missing");
    const first = await page.goto(origin + "/ko/" + query, { waitUntil: "domcontentloaded" });
    assert.equal(first.status(), 200);
    await banner().waitFor();
    await page.waitForTimeout(1500);
    assert.equal(requests.length, 0, "Google request before consent");
    const unknown = requests.length;
    await decline().click();
    const privacy = await page.goto(origin + "/ko/privacy/" + query, { waitUntil: "domcontentloaded" });
    assert.equal(privacy.status(), 200);
    await settings().waitFor();
    await page.waitForTimeout(1500);
    assert.equal(requests.length, 0, "Google request after refusal");
    const denied = requests.length;
    await settings().click();
    await allow().click();
    await until(() => events.length > 0 && responses.length > 0, page);
    const firstCount = events.length;
    await page.goto(origin + "/ko/terms/" + query, { waitUntil: "domcontentloaded" });
    await settings().waitFor();
    await until(() => events.length > firstCount, page);
    await page.waitForTimeout(2000);
    assert(events.every(e => ["page_view", "user_engagement", "session_start", "first_visit"].includes(e.name)), "unexpected automatic event");
    assert.equal(leaks.length, 0, "payload privacy check failed");
    assert(events.filter(e => e.name === "page_view").every(e => e.hasEventKey), "exported event key is missing");
    await settings().click();
    const beforeWithdraw = requests.length;
    await decline().click();
    await page.goto(origin + "/ko/" + query, { waitUntil: "domcontentloaded" });
    await settings().waitFor();
    await page.waitForTimeout(2000);
    assert.equal(requests.length, beforeWithdraw, "Google request after withdrawal");
    assert(responses.length > 0 && responses.every(status => status >= 200 && status < 300), "Google collection transport did not succeed");
    assert.equal(failures.length, 0, "Google request failed");
    console.log(JSON.stringify({
      browser: kind, release: config.release, beforeConsentRequests: unknown, deniedRequests: denied,
      allowedEvents: events.map(e => ({ name: e.name, location: e.location })),
      afterWithdrawalRequests: requests.length - beforeWithdraw, googleHTTPStatuses: responses,
      privacyViolations: leaks.length, customKeys: [...new Set(events.flatMap(e => e.customKeys))].sort(),
      reportingReceipt: "NOT_PROVEN_BY_HTTP",
    }));
    await context.close();
  } finally { await browser.close(); }
}
main().catch(error => { console.error("LIVE_ANALYTICS_BROWSER=FAIL " + String(error.message).split(String.fromCharCode(10))[0].replace(new RegExp("https?://[^ ]+", "g"), "[URL]")); process.exitCode = 1; });
