const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { firefox } = require("playwright");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:18080").replace(/\/$/, "");
const agencyOrderID = process.env.E2E_AGENCY_ORDER_ID;
const screenshotDir = process.env.E2E_SCREENSHOT_DIR;

assert.ok(agencyOrderID, "E2E_AGENCY_ORDER_ID is required");

async function login(page) {
  const response = await page.request.post(`${baseURL}/api/v1/dev/auth/session`, {
    data: { profileKey: "empty-operator" },
  });
  assert.equal(response.status(), 201, await response.text());
}

async function assertNoHorizontalOverflow(page, label) {
  const dimensions = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }));
  assert.ok(
    dimensions.scrollWidth <= dimensions.clientWidth + 1,
    `${label} horizontally overflows: ${JSON.stringify(dimensions)}`,
  );
}

async function assertNoOverlap(first, second, label) {
  const [firstBox, secondBox] = await Promise.all([
    first.boundingBox(),
    second.boundingBox(),
  ]);
  assert.ok(firstBox && secondBox, `${label} controls must be visible`);
  const overlaps = firstBox.x < secondBox.x + secondBox.width
    && firstBox.x + firstBox.width > secondBox.x
    && firstBox.y < secondBox.y + secondBox.height
    && firstBox.y + firstBox.height > secondBox.y;
  assert.equal(overlaps, false, `${label} controls overlap`);
}

async function run() {
  const browser = await firefox.launch({ headless: true });
  const artifacts = [];
  try {
    const desktop = await browser.newContext({
      locale: "ko-KR",
      viewport: { width: 1440, height: 960 },
    });
    const page = await desktop.newPage();
    await page.goto(`${baseURL}/admin/agencyOrder`, { waitUntil: "networkidle" });
    await page.getByRole("search", { name: "정확 주문 조회" }).waitFor();
    assert.equal(
      new URL(page.url()).pathname,
      "/admin/agencyOrder",
      "exact operator startPath must create the local-review session automatically",
    );
    const detailedLookup = page.locator('[aria-controls="operator-order-lookup-options"]');
    assert.equal((await detailedLookup.innerText()).trim(), "상세 조회");
    assert.equal(await detailedLookup.getAttribute("aria-expanded"), "false");
    assert.equal(await page.getByLabel("식별자 종류").count(), 0);
    if (screenshotDir) {
      await fs.mkdir(screenshotDir, { recursive: true });
      const target = path.join(screenshotDir, "operator-order-lookup-entry.png");
      await page.screenshot({ path: target, fullPage: true });
      artifacts.push(target);
    }
    await detailedLookup.click();
    assert.equal(await detailedLookup.getAttribute("aria-expanded"), "true");
    assert.equal((await detailedLookup.innerText()).trim(), "접기");
    await page.getByLabel("식별자 종류").selectOption("AGENCY_ORDER_ID");
    await page.getByLabel("정확한 주문 식별자").fill(`  ${agencyOrderID}  `);
    await page.getByLabel("환경").selectOption("TESTNET");
    if (screenshotDir) {
      const target = path.join(screenshotDir, "operator-order-lookup-expanded.png");
      await page.screenshot({ path: target, fullPage: true });
      artifacts.push(target);
    }
    await page.getByRole("button", { name: "조회", exact: true }).click();
    await page.waitForURL(`${baseURL}/admin/agencyOrder/${agencyOrderID}`);
    await page.getByRole("heading", { name: "주문 evidence" }).waitFor();
    await page.getByRole("heading", { name: "여러 시스템에 걸친 하나의 주문" }).waitFor();
    await page.getByText("안전한 운영자 사영입니다.", { exact: false }).waitFor();
    await assertNoHorizontalOverflow(page, "desktop");

    const bodyText = await page.locator("body").innerText();
    assert.equal(bodyText.includes("123 Test Street"), false);
    assert.equal(bodyText.includes("local-empty-user@example.com"), false);
    assert.equal(bodyText.includes("Test Buyer"), false);
    if (screenshotDir) {
      await fs.mkdir(screenshotDir, { recursive: true });
      const target = path.join(screenshotDir, "operator-order-lookup-desktop.png");
      await page.screenshot({ path: target, fullPage: true });
      artifacts.push(target);
    }

    await page.evaluate(() => {
      document.documentElement.style.fontSize = "200%";
    });
    await assertNoHorizontalOverflow(page, "desktop at 200% text zoom");

    const mobile = await browser.newContext({
      locale: "en-US",
      viewport: { width: 320, height: 900 },
    });
    const mobilePage = await mobile.newPage();
    await login(mobilePage);
    await mobilePage.goto(`${baseURL}/admin/agencyOrder`, {
      waitUntil: "networkidle",
    });
    await mobilePage.getByRole("search", { name: "Exact order lookup" }).waitFor();
    assert.equal(await mobilePage.getByLabel("Identifier", { exact: true }).count(), 0);
    await assertNoHorizontalOverflow(mobilePage, "320px compact lookup");
    await assertNoOverlap(
      mobilePage.getByRole("button", { name: "Lookup", exact: true }),
      mobilePage.locator('[aria-controls="operator-order-lookup-options"]'),
      "320px compact lookup",
    );
    if (screenshotDir) {
      const target = path.join(screenshotDir, "operator-order-lookup-mobile-entry.png");
      await mobilePage.screenshot({ path: target, fullPage: true });
      artifacts.push(target);
    }
    await mobilePage.locator('[aria-controls="operator-order-lookup-options"]').click();
    await mobilePage.getByLabel("Identifier", { exact: true }).waitFor();
    await assertNoHorizontalOverflow(mobilePage, "320px expanded lookup");
    if (screenshotDir) {
      const target = path.join(screenshotDir, "operator-order-lookup-mobile-expanded.png");
      await mobilePage.screenshot({ path: target, fullPage: true });
      artifacts.push(target);
    }
    await mobilePage.goto(`${baseURL}/admin/agencyOrder/${agencyOrderID}`, {
      waitUntil: "networkidle",
    });
    await mobilePage.getByRole("heading", { name: "Order evidence" }).waitFor();
    await mobilePage.getByRole("heading", { name: "One order across every system" }).waitFor();
    await assertNoHorizontalOverflow(mobilePage, "320px mobile");
    if (screenshotDir) {
      const target = path.join(screenshotDir, "operator-order-lookup-mobile.png");
      await mobilePage.screenshot({ path: target, fullPage: true });
      artifacts.push(target);
    }
    await mobile.close();
    await desktop.close();

    console.log(JSON.stringify({
      result: "PASS",
      suite: "operator-order-lookup",
      agencyOrderID,
      checks: [
        "direct Order processing entry creates the matching local operator session automatically",
        "lookup defaults to one compact search bar and reveals exact controls on demand",
        "expanded Order processing lookup navigates to the canonical detail route",
        "Korean desktop and English 320px compact, expanded, and detail routes render without page overflow",
        "200% text zoom does not create horizontal page overflow",
        "full shipping address, buyer identity, and recipient name remain excluded",
      ],
      artifacts,
    }, null, 2));
  } finally {
    await browser.close();
  }
}

run().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
