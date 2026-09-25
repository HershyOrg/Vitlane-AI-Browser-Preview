// Isolated, read-only public-page checks. Never sends analytics or app commands.
const { firefox } = require("playwright");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const base = process.env.MARKETING_BASE_URL || "http://127.0.0.1:18119";
const evidence = process.env.MARKETING_SCREENSHOT_DIR || "/tmp/vitlane-marketing-seo";
(async () => {
  fs.mkdirSync(evidence, { recursive: true });
  const browser = await firefox.launch({ headless: true });
  const results = [];
  try {
    for (const locale of ["en-US", "ko-KR"]) {
      for (const mode of ["normal", "no-js", "bundle-failure"]) {
        for (const width of [1440, 320]) {
          console.log(`checking ${locale} ${mode} ${width}`);
          const context = await browser.newContext({ locale, javaScriptEnabled: mode !== "no-js", viewport: { width, height: 900 } });
          await context.route(/cloudflareinsights\.com/, (route) => route.abort());
          if (mode === "bundle-failure") await context.route("**/assets/generated/standard-stack.js*", (route) => route.abort());
          const page = await context.newPage();
          const errors = [];
          const productRequests = [];
          page.on("pageerror", (error) => errors.push(error.message));
          page.on("request", (request) => { if (request.url().includes("/assets/products/")) productRequests.push(request.url()); });
          const route = locale === "ko-KR" ? "/ko/" : "/";
          await page.goto(base + route, { waitUntil: "networkidle" });
          const title = page.getByRole("heading", { level: 1 });
          assert.equal(await title.count(), 1);
          assert(await title.isVisible());
          assert(await page.locator(".hero-product-link").isVisible());
          assert.equal(await page.locator(".hero-install-link").count(), 0);
          assert(await page.locator(".product-summary").textContent());
          assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
          assert.equal(await page.locator('meta[property="og:locale"]').getAttribute("content"), locale.replace("-", "_"));
          if (mode === "normal") {
            assert.equal(productRequests.length, 0, "offscreen gallery must not load on arrival");
            await page.locator("#examples").scrollIntoViewIfNeeded();
            await page.waitForFunction(() => [...document.querySelectorAll(".gallery-card img")].length === 6 && [...document.querySelectorAll(".gallery-card img")].every((image) => image.complete && image.naturalWidth > 0));
            assert.equal(await page.locator(".gallery-card img").count(), 6);
            assert(productRequests.every((url) => url.includes("/optimized/") && url.endsWith(".webp")));
            await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; window.scrollTo(0, 0); });
            await page.waitForFunction(() => document.documentElement.scrollWidth <= innerWidth, null, { timeout: 3000 });
            assert(await page.locator(".hero-product-link").isVisible());
            assert.equal(await page.locator(".hero-install-link").count(), 0);
          } else {
            assert.equal(await page.locator(".marketing-static-copy li").count(), 3);
          }
          assert.deepEqual(errors, []);
          await page.screenshot({ path: path.join(evidence, `${locale}-${mode}-${width}.png`) });
          results.push({ locale, mode, width, pass: true });
          await context.close();
        }
      }
    }
    // Metadata/image must work without browser execution on every public route.
    const context = await browser.newContext();
    for (const route of ["/", "/ko/", "/privacy/", "/ko/privacy/", "/terms/", "/ko/terms/"]) {
      const response = await context.request.get(base + route);
      assert.equal(response.status(), 200);
      const body = await response.text();
      assert(body.includes('property="og:image"'));
      assert(body.includes('name="twitter:card"'));
      assert(!response.headers()["x-robots-tag"]?.includes("noindex"));
    }
    const image = await context.request.get(base + "/assets/og/vitlane.png");
    assert.equal(image.status(), 200);
    assert(image.headers()["content-type"].includes("image/png"));
    await context.close();
  } finally { await browser.close(); }
  console.log(JSON.stringify({ cases: results, evidence }));
})().catch((error) => { console.error(error); process.exitCode = 1; });
