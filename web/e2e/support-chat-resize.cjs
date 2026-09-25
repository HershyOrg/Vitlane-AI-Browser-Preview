const assert = require("node:assert/strict");
const { chromium } = require("playwright");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:4178").replace(/\/$/, "");
const curationPath = "/curations/e5100000-0000-4000-8000-000000000002";

async function openMessages(page) {
  await page.getByLabel("프로필 메뉴").click();
  await page.getByText("메시지", { exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Vitlane 메시지" });
  await dialog.waitFor();
  return dialog;
}

async function run() {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    locale: "ko-KR",
    viewport: { width: 1440, height: 960 },
  });
  const page = await context.newPage();
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  try {
    await page.goto(`${baseURL}/login?returnTo=${encodeURIComponent(curationPath)}`, {
      waitUntil: "networkidle",
    });
    await page.waitForURL(`**${curationPath}`);
    const dialog = await openMessages(page);
    const handle = page.getByRole("separator", { name: "메시지 창 크기 조절" });
    const before = await dialog.boundingBox();
    const handleBox = await handle.boundingBox();
    assert.ok(before && handleBox, "Messages window and resize handle must be visible");

    await page.mouse.move(
      handleBox.x + handleBox.width / 2,
      handleBox.y + handleBox.height / 2,
    );
    await page.mouse.down();
    await page.mouse.move(handleBox.x - 160, handleBox.y - 120, { steps: 12 });
    await page.mouse.up();

    const expanded = await dialog.boundingBox();
    assert.ok(expanded, "resized Messages window must remain visible");
    assert.ok(
      expanded.width >= before.width + 120,
      `drag must increase width: before=${before.width}, after=${expanded.width}`,
    );
    assert.ok(
      expanded.height >= before.height + 90,
      `drag must increase height: before=${before.height}, after=${expanded.height}`,
    );
    assert.ok(expanded.x >= 16 && expanded.y >= 16, "window must remain inside viewport");
    assert.ok(
      expanded.x + expanded.width <= 1440 && expanded.y + expanded.height <= 960,
      "resized window must not overflow the viewport",
    );

    const stored = await page.evaluate(() =>
      JSON.parse(localStorage.getItem("vitlane.support-chat-size.v1") ?? "null"),
    );
    assert.equal(stored.width, Math.round(expanded.width));
    assert.equal(stored.height, Math.round(expanded.height));

    await handle.focus();
    await page.keyboard.press("ArrowLeft");
    const keyboardExpanded = await dialog.boundingBox();
    assert.ok(
      keyboardExpanded && keyboardExpanded.width > expanded.width,
      "ArrowLeft on the top-left handle must increase width",
    );
    await page.keyboard.press("Home");
    assert.equal(
      await page.evaluate(() => localStorage.getItem("vitlane.support-chat-size.v1")),
      null,
      "Home must restore the responsive default",
    );

    await page.setViewportSize({ width: 390, height: 844 });
    const mobile = await dialog.boundingBox();
    assert.ok(mobile, "Messages window must remain visible on mobile");
    assert.ok(mobile.x >= 12 && mobile.x + mobile.width <= 378.5);
    assert.equal(await handle.isVisible(), false, "mobile uses the fixed responsive size");
    assert.deepEqual(pageErrors, [], `page errors: ${pageErrors.join(" | ")}`);

    console.log(
      "support Messages resize Chromium E2E: PASS (drag, keyboard, viewport clamp, mobile)",
    );
  } finally {
    await context.close();
    await browser.close();
  }
}

run().catch((error) => {
  console.error("support Messages resize Chromium E2E: FAIL");
  console.error(error.stack ?? error);
  process.exitCode = 1;
});
