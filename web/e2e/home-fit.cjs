// The home screen is a single composer that must fit the window exactly.
// A body scroll there means a nesting level outgrew the viewport, which is
// invisible on a tall monitor and obvious on a laptop, so it is measured
// rather than eyeballed.
const { firefox } = require("playwright");
const assert = require("node:assert/strict");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:8080").replace(
  /\/$/,
  "",
);

// Two realistic viewports: a laptop is where the overflow shows up first.
const viewports = [
  { name: "mobile-compact", width: 320, height: 568 },
  { name: "mobile", width: 390, height: 844 },
  { name: "laptop", width: 1366, height: 768 },
  { name: "desktop", width: 1920, height: 1080 },
];

async function run() {
  const browser = await firefox.launch();
  try {
    for (const viewport of viewports) {
      const context = await browser.newContext({
        locale: "ko-KR",
        viewport: { width: viewport.width, height: viewport.height },
      });
      const page = await context.newPage();
      const session = await page.request.post(
        `${baseURL}/api/v1/dev/auth/session`,
        // The default development session needs no seeded fixture, so this suite
        // runs against a freshly migrated database.
        { data: {} },
      );
      assert.equal(session.status(), 201);
      await page.goto(`${baseURL}/`, { waitUntil: "networkidle" });

      const formBox = await page.locator(".init-request-form").boundingBox();
      const inputBox = await page.locator("#curation-intent").boundingBox();
      assert.ok(formBox, `${viewport.name}: request form has no box`);
      assert.ok(inputBox, `${viewport.name}: request input has no box`);
      assert.ok(
        inputBox.height >= 112,
        `${viewport.name}: request input is only ${Math.round(inputBox.height)}px tall`,
      );
      if (viewport.width >= 1366) {
        assert.ok(
          formBox.width >= 880,
          `${viewport.name}: request form is only ${Math.round(formBox.width)}px wide`,
        );
      }

      const metrics = await page.evaluate(() => ({
        scrollHeight: document.documentElement.scrollHeight,
        innerHeight: window.innerHeight,
        bodyScrollHeight: document.body.scrollHeight,
      }));
      // One pixel of slack for sub-pixel rounding; anything more is a real
      // layout overflow.
      assert.ok(
        metrics.scrollHeight <= metrics.innerHeight + 1,
        `${viewport.name}: document scrolls ${metrics.scrollHeight} into a ` +
          `${metrics.innerHeight} viewport`,
      );
      assert.ok(
        metrics.bodyScrollHeight <= metrics.innerHeight + 1,
        `${viewport.name}: body scrolls ${metrics.bodyScrollHeight} into a ` +
          `${metrics.innerHeight} viewport`,
      );
      // Fitting the window is worthless if it was achieved by clipping the
      // composer. The send row has to stay inside the viewport and remain
      // clickable, with the settings popover both closed and open.
      const bottoms = {};
      for (const state of ["closed", "open"]) {
        if (state === "open") {
          const trigger = page.getByRole('button', { name: '예산 설정', exact: true });
          await trigger.click();
          await page
            .locator(".init-settings")
            .waitFor({ state: "visible", timeout: 5_000 });
          const [triggerBox, popoverBox, arrowBox] = await Promise.all([
            trigger.boundingBox(),
            page.locator(".init-settings").boundingBox(),
            page.locator(".init-settings .budget-popover__arrow").boundingBox(),
          ]);
          assert.ok(triggerBox && popoverBox && arrowBox, `${viewport.name}: popover geometry is incomplete`);
          assert.ok(
            popoverBox.y + popoverBox.height <= triggerBox.y,
            `${viewport.name}: settings popover does not sit above its trigger`,
          );
          const arrowCenter = arrowBox.x + arrowBox.width / 2;
          assert.ok(
            arrowCenter >= triggerBox.x && arrowCenter <= triggerBox.x + triggerBox.width,
            `${viewport.name}: popover arrow does not point at its trigger`,
          );
        }
        const submit = page.locator(".shell-intent-composer__submit");
        await submit.waitFor({ state: "visible", timeout: 5_000 });
        await submit.scrollIntoViewIfNeeded();
        const box = await submit.boundingBox();
        assert.ok(box, `${viewport.name}/${state}: submit has no box`);
        assert.ok(
          box.y + box.height <= viewport.height + 1,
          `${viewport.name}/${state}: submit bottom ${Math.round(
            box.y + box.height,
          )} is below the ${viewport.height} viewport`,
        );
        bottoms[state] = box.y + box.height;
        // Playwright's actionability check covers the case where the button is
        // on screen but covered or clipped by an ancestor.
        await page
          .locator("#curation-intent")
          .fill(state === "open" ? "설정 열고 입력" : "입력");
        await submit.click({ trial: true, timeout: 5_000 });
      }

      // An anchored settings popover must not move the composer in document flow.
      assert.ok(
        Math.abs(bottoms.open - bottoms.closed) <= 2,
        `${viewport.name}: send row moved from ${Math.round(
          bottoms.closed,
        )} to ${Math.round(bottoms.open)} when settings opened`,
      );

      console.log(
        `  ${viewport.name} ${viewport.width}x${viewport.height}: ` +
          `document ${metrics.scrollHeight} <= viewport ${metrics.innerHeight}, ` +
          `send button reachable closed and open`,
      );
      await context.close();
    }
    console.log("home-fit E2E: PASS");
  } finally {
    await browser.close();
  }
}

run().catch((error) => {
  console.error("home-fit E2E: FAIL");
  console.error(error.message ?? error);
  process.exitCode = 1;
});
