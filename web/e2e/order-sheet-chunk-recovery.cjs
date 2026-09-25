const assert = require("node:assert/strict");
const { firefox } = require("playwright");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:18080").replace(/\/$/, "");

async function run() {
  const browser = await firefox.launch({ headless: true });
  try {
    const context = await browser.newContext({ locale: "ko-KR" });
    const page = await context.newPage();
    let failedChunkRequests = 0;
    let documentRequests = 0;

    page.on("request", (request) => {
      if (request.resourceType() === "document") documentRequests += 1;
    });
    await page.route(/OrderSheetPage-.*\.js(?:\?.*)?$/, async (route) => {
      if (failedChunkRequests === 0) {
        failedChunkRequests += 1;
        await route.fulfill({ status: 404, contentType: "text/plain", body: "stale chunk" });
        return;
      }
      await route.continue();
    });

    const session = await context.request.post(`${baseURL}/api/v1/dev/auth/session`, {
      data: { profileKey: "empty-user" },
    });
    assert.equal(session.status(), 201, await session.text());

    await page.goto(`${baseURL}/curations/chunk-recovery-probe/order-sheet?cartVersion=1`, {
      waitUntil: "domcontentloaded",
    });
    await page.locator("main.agency-order-page").waitFor({ timeout: 15_000 });

    assert.equal(failedChunkRequests, 1, "the stale OrderSheet chunk must be failed exactly once");
    assert.ok(documentRequests >= 2, `the route must reload once after the stale chunk: ${documentRequests}`);
    assert.ok((await page.locator("body").innerText()).trim().length > 0, "the route must not remain white");
    assert.equal(new URL(page.url()).pathname, "/curations/chunk-recovery-probe/order-sheet");
    assert.deepEqual(
      await page.evaluate(() => Object.keys(sessionStorage).filter((key) => key.startsWith("vitlane.route-chunk-reload.v1"))),
      [],
      "the successful current chunk must clear the one-reload marker",
    );
    await context.close();
  } finally {
    await browser.close();
  }
}

run().catch((error) => {
  process.stderr.write(`${error.stack || error}\n`);
  process.exitCode = 1;
});
