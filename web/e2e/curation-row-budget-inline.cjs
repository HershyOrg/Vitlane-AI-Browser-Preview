// Real Row components with a synthetic budget and catalog; no provider or production writes.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { firefox } = require("playwright");
const { fixture, install } = require("./curation-row-regression.cjs");

(async () => {
  const { createServer } = await import("vite");
  const root = path.resolve(__dirname, "..");
  const vite = await createServer({
    root, configFile: path.join(root, "vite.config.ts"), logLevel: "error",
    server: { host: "127.0.0.1", port: 0, fs: { allow: [root, await fs.realpath(path.join(root, "node_modules"))] } },
  });
  await vite.listen();
  const browser = await firefox.launch();
  try {
    for (const [locale, width, zoom] of [
      ["ko-KR", 1440, 1], ["en-US", 1440, 1],
      ["ko-KR", 390, 1], ["en-US", 390, 1],
      ["ko-KR", 320, 2], ["en-US", 320, 2],
    ]) {
      const context = await browser.newContext({ viewport: { width, height: 900 } });
      const page = await context.newPage();
      const state = fixture(2, false);
      state.budget = {
        schemaVersion: "vitlane.curation-budget.v1", version: 1, researchVersion: 1,
        enabled: true, currency: "USD", totalAmount: "100",
        allocations: state.targets.map(target => ({ targetId: target.id, quantity: 1, amount: "50" })),
      };
      const writes = [], unexpected = [], errors = [];
      page.on("pageerror", error => errors.push(error.message));
      await install(page, state, locale, writes, unexpected);
      await page.goto(vite.resolvedUrls.local[0] + "curations/e5100000-0000-4000-8000-000000000002");
      await page.locator(".curation-result--row .budget-delta.is-saving").first().waitFor();
      await page.evaluate(scale => { document.documentElement.style.fontSize = String(16 * scale) + "px"; }, zoom);
      await page.evaluate(() => document.fonts.ready);
      const measured = await page.locator(".curation-result--row").first().evaluate(row => {
        const price = row.querySelector(".curation-result__price");
        const amount = price.querySelector(":scope > strong");
        const delta = price.querySelector(".budget-delta");
        const a = amount.getBoundingClientRect(), d = delta.getBoundingClientRect();
        const deltaAmount = delta.querySelector(".budget-delta__amount");
        const range = document.createRange(); range.selectNodeContents(deltaAmount);
        return {
          display: getComputedStyle(price).display,
          price: amount.textContent.trim(),
          delta: delta.textContent.trim(),
          comparisonTitle: delta.title,
          adjacent: d.left > a.right && d.top < a.bottom && d.bottom > a.top,
          deltaLines: range.getClientRects().length,
          pageWidth: document.documentElement.scrollWidth,
          viewport: innerWidth,
          rowHeight: row.getBoundingClientRect().height,
        };
      });
      assert.equal(measured.display, "flex");
      assert.match(measured.delta, /−/);
      assert(measured.comparisonTitle.includes(locale === "ko-KR" ? "예산" : "budget"));
      if (zoom === 1) assert(measured.adjacent, JSON.stringify({ locale, width, measured }));
      assert.equal(measured.deltaLines, 1);
      assert(measured.pageWidth <= measured.viewport + 1, JSON.stringify({ locale, width, zoom, measured }));
      assert.deepEqual(writes, []); assert.deepEqual(unexpected, []); assert.deepEqual(errors, []);
      await context.close();
    }

    // Rows without a budget keep their earlier grid price presentation.
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
    const page = await context.newPage(), writes = [], unexpected = [];
    await install(page, fixture(2, false), "ko-KR", writes, unexpected);
    await page.goto(vite.resolvedUrls.local[0] + "curations/e5100000-0000-4000-8000-000000000002");
    await page.locator(".curation-result--row .curation-result__price strong").first().waitFor();
    assert.equal(await page.locator(".curation-result--row .curation-result__price").first().evaluate(el => getComputedStyle(el).display), "grid");
    assert.deepEqual(writes, []); assert.deepEqual(unexpected, []);
    await context.close();
    console.log("ROW_BUDGET_INLINE_PASS: KO/EN desktop, phone, 320px 200% and no-budget baseline");
  } finally {
    await browser.close();
    await vite.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
