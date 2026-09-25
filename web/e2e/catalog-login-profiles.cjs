const assert = require("node:assert/strict");
const { chromium } = require("playwright");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:4178").replace(/\/$/, "");
const curationPath = "/curations/e5100000-0000-4000-8000-000000000002";
const profilePickerReturnPath = "/curations";

async function dragUp(page, selector, distance) {
  const box = await page.locator(selector).boundingBox();
  assert.ok(box, `${selector} must have a box`);
  const x = box.x + Math.min(box.width * 0.82, box.width - 24);
  const startY = Math.min(box.y + box.height - 36, page.viewportSize().height - 36);
  const endY = Math.max(72, startY - distance);
  await page.mouse.move(x, startY);
  await page.mouse.down();
  await page.mouse.move(x, endY, { steps: 12 });
  await page.mouse.up();
}

async function runCase(browser, viewport, scrollSelector) {
  const context = await browser.newContext({ locale: "ko-KR", viewport });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));

  try {
    await page.goto(
      `${baseURL}/login?returnTo=${encodeURIComponent(profilePickerReturnPath)}`,
      { waitUntil: "networkidle" },
    );
    const profile = page.locator(".catalog-ui-login__profile").filter({
      hasText: "다중 상품 구매 시나리오",
    });
    await profile.waitFor();

    const before = await page.locator(scrollSelector).evaluate((element) => ({
      clientHeight: element.clientHeight,
      overflowY: getComputedStyle(element).overflowY,
      scrollHeight: element.scrollHeight,
      scrollTop: element.scrollTop,
    }));
    assert.equal(before.overflowY, "auto");
    assert.ok(before.scrollHeight > before.clientHeight, "login must own its overflow");

    await dragUp(page, scrollSelector, Math.min(470, viewport.height - 120));
    const after = await page.locator(scrollSelector).evaluate((element) => element.scrollTop);
    assert.ok(after > before.scrollTop, "mouse drag must move the login scroll owner");

    for (let attempt = 0; attempt < 3; attempt += 1) {
      const box = await profile.boundingBox();
      if (box && box.y >= 0 && box.y + box.height <= viewport.height) break;
      await dragUp(page, scrollSelector, Math.min(360, viewport.height - 120));
    }
    const visibleBox = await profile.boundingBox();
    assert.ok(visibleBox, "multi-product profile must have a box");
    assert.ok(visibleBox.y >= 0, "multi-product profile must not be clipped above viewport");
    assert.ok(
      visibleBox.y + visibleBox.height <= viewport.height,
      "multi-product profile must be fully reachable inside viewport",
    );

    const button = profile.getByRole("button", { name: "이 프로필로 시작" });
    const buttonBox = await button.boundingBox();
    assert.ok(buttonBox, "profile start button must have a box");
    await page.mouse.move(buttonBox.x + buttonBox.width / 2, buttonBox.y + buttonBox.height / 2);
    await page.mouse.down();
    await page.mouse.move(
      buttonBox.x + buttonBox.width / 2,
      Math.max(72, buttonBox.y + buttonBox.height / 2 - 50),
      { steps: 8 },
    );
    await page.mouse.up();
    await page.waitForTimeout(50);
    assert.match(page.url(), /\/login\?/, "dragging a button must suppress its click");

    await button.click();
    await page.waitForURL(`**${curationPath}`);
    assert.deepEqual(errors, [], `page errors: ${errors.join(" | ")}`);
  } finally {
    await context.close();
  }
}

async function run() {
  const browser = await chromium.launch({ headless: true });
  try {
    await runCase(browser, { width: 1200, height: 620 }, ".catalog-ui-login");
    await runCase(browser, { width: 900, height: 620 }, ".catalog-ui-login");
    await runCase(browser, { width: 390, height: 700 }, ".catalog-ui-login");
    console.log(
      "catalog login profiles Chromium E2E: PASS (centered lane scroll, mobile drag + click)",
    );
  } finally {
    await browser.close();
  }
}

run().catch((error) => {
  console.error("catalog login profiles Chromium E2E: FAIL");
  console.error(error.stack ?? error);
  process.exitCode = 1;
});
