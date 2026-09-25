const assert = require("node:assert/strict");
const { firefox } = require("playwright");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:4179").replace(/\/$/, "");
const curationPath = "/curations/e5100000-0000-4000-8000-000000000002";

async function run() {
  const browser = await firefox.launch({ headless: true });
  const context = await browser.newContext({
    locale: "ko-KR",
    viewport: { width: 1440, height: 960 },
  });
  const page = await context.newPage();
  const pageErrors = [];
  let targetID = "";
  let releaseExpansion;
  let markExpansionRequested;
  const expansionRequested = new Promise((resolve) => {
    markExpansionRequested = resolve;
  });
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await page.route("**/api/v1/curations/*/workspace", async (route) => {
    const response = await route.fetch();
    const payload = await response.json();
    payload.catalogResearch = {
      schemaVersion: "vitlane.catalog-research-workspace.v1",
      pools: (payload.targets ?? []).map((target) => ({
        targetId: target.id,
        version: 1,
        expandOrdinal: 0,
        latestMode: "APPEND",
        products: [candidateProduct(`stored-${target.id}`, `Stored ${target.title}`)],
        hiddenProducts: [],
        messages: [],
      })),
      messages: [],
      configurations: [],
      interactions: [],
    };
    await route.fulfill({ response, json: payload });
  });
  await page.route("**/catalog-research/hydrations", async (route) => {
    const request = route.request();
    const body = request.postDataJSON();
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(hydrationPayload(body.targetId)),
    });
  });
  await page.route("**/catalog-research/expansions", async (route) => {
    markExpansionRequested();
    await new Promise((resolve) => {
      releaseExpansion = async () => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(expansionPayload(targetID)),
        });
        resolve();
      };
    });
  });

  try {
    await page.goto(`${baseURL}/login?returnTo=${encodeURIComponent(curationPath)}`, {
      waitUntil: "networkidle",
    });
    await page.waitForURL(`**${curationPath}`);
    await page.locator("[data-testid='phase8-curation-surface']").waitFor();
    const targets = page.locator(".catalog-ui-target");
    const targetCount = await targets.count();
    assert.ok(targetCount >= 2, "the multi-product review profile must expose at least two Targets");
    await page.locator(".catalog-ui-candidate-grid").nth(targetCount - 1).waitFor();

    const rowCounts = await page.locator(".catalog-ui-candidate-grid").evaluateAll((grids) =>
      grids.map((grid) => grid.getAttribute("data-row-count")),
    );
    assert.equal(rowCounts.length, targetCount);
    assert.ok(rowCounts.every((value) => value === "1"), "multiple Targets must use one row each");
    assert.equal(await page.locator('[data-horizontal-scroll="true"]').count(), targetCount);

    const firstTarget = targets.first();
    targetID = await firstTarget.getAttribute("data-target-id");
    assert.ok(targetID);
    const targetTitle = (await firstTarget.locator("h2").innerText()).trim();
    await firstTarget.locator(".catalog-ui-target__collapse").click();
    const targetView = firstTarget.locator(".catalog-ui-target__view");
    assert.equal(await targetView.getAttribute("hidden"), "");
    assert.ok(await firstTarget.locator("h2").isVisible(), "collapse must keep the Target header visible");
    assert.ok(
      await firstTarget.getByRole("button", { name: /상품 펼치기$/ }).isVisible(),
      "collapse must preserve a reversible view control",
    );
    await firstTarget.getByRole("button", { name: /상품 펼치기$/ }).click();
    assert.equal(await targetView.getAttribute("hidden"), null);
    assert.equal(
      await firstTarget.getByRole("button", { name: `${targetTitle} 후보를 왼쪽으로 이동` }).count(),
      1,
    );
    assert.equal(
      await firstTarget.getByRole("button", { name: `${targetTitle} 후보를 오른쪽으로 이동` }).count(),
      1,
    );

    const expand = firstTarget.getByRole("button", { name: "후보 더 찾기" });
    assert.equal(await expand.isEnabled(), true);
    await expand.click();
    await expansionRequested;
    const liveSlot = page.getByTestId("phase8-latest-slot");
    await liveSlot.waitFor();
    const activeOrder = await liveSlot.evaluate((slot) => {
      const user = slot.querySelector(".curation-conversation-bubble.is-user");
      const progress = slot.querySelector(".catalog-ui-local-activity");
      return {
        hasUser: Boolean(user),
        hasProgress: Boolean(progress),
        userBeforeProgress: Boolean(
          user && progress &&
          (user.compareDocumentPosition(progress) & Node.DOCUMENT_POSITION_FOLLOWING),
        ),
      };
    });
    assert.deepEqual(activeOrder, {
      hasUser: true,
      hasProgress: true,
      userBeforeProgress: true,
    });

    await releaseExpansion();
    const diff = page
      .locator('.curation-transcript__row--message [data-conversation-presentation="diff"]')
      .last();
    await diff.getByText("Vitlane", { exact: true }).waitFor();
    assert.equal(await diff.locator("dl").count(), 0);
    assert.equal(await diff.evaluate((node) => node.classList.contains("is-diff")), false);
    const diffBorder = await diff.evaluate((node) => {
      const style = window.getComputedStyle(node);
      return {
        leftColor: style.borderLeftColor,
        leftWidth: style.borderLeftWidth,
        topColor: style.borderTopColor,
        topWidth: style.borderTopWidth,
      };
    });
    assert.equal(diffBorder.leftColor, diffBorder.topColor);
    assert.equal(diffBorder.leftWidth, diffBorder.topWidth);
    assert.match(await diff.innerText(), /새 추천 상품 1개를 추가했습니다/);
    assert.doesNotMatch(await diff.innerText(), /Curation Density Contract Pack/);
    assert.equal(await page.getByTestId("phase8-latest-slot").count(), 0);
    const settledOrder = await page.locator(".curation-workspace").evaluate((workspace) => {
      const historyDiff = workspace.querySelector('[data-conversation-presentation="diff"]');
      const artifact = workspace.querySelector("[data-live-artifact='true']");
      const composer = workspace.querySelector(".catalog-ui-composer-dock");
      const follows = (first, second) => Boolean(
        first && second &&
        (first.compareDocumentPosition(second) & Node.DOCUMENT_POSITION_FOLLOWING),
      );
      return {
        diffBeforeArtifact: follows(historyDiff, artifact),
        artifactBeforeComposer: follows(artifact, composer),
      };
    });
    assert.deepEqual(settledOrder, {
      diffBeforeArtifact: true,
      artifactBeforeComposer: true,
    });

    await page.setViewportSize({ width: 390, height: 844 });
    const overflow = await page.evaluate(() => ({
      document: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      body: document.body.scrollWidth - document.body.clientWidth,
    }));
    assert.ok(overflow.document <= 1 && overflow.body <= 1, JSON.stringify(overflow));
    assert.deepEqual(pageErrors, []);
    console.log("curation density/conversation Firefox E2E: PASS");
  } finally {
    await context.close();
    await browser.close();
  }
}

function expansionPayload(targetId) {
  return {
    schemaVersion: "vitlane.phase8-live-catalog-review.v3",
    source: "LIVE_SHOPIFY_GLOBAL_CATALOG",
    provider: "shopify",
    protocolVersion: "2026-04-08",
    outcome: "SUCCESS",
    products: [candidateProduct(
      "curation-density-contract-pack",
      "Curation Density Contract Pack",
    )],
    messages: [],
    candidateEligibleCount: 1,
    discardedNoLocatorCount: 0,
    hasNextPage: false,
    appliedFilterVerified: true,
    appliedFilterCapability: "dev.shopify.catalog.global",
    targetId,
    poolVersion: 99,
    expandOrdinal: 99,
    mode: "APPEND",
    replay: false,
    metrics: {
      policyVersion: "phase8-live-review.v1",
      durationMilliseconds: 1,
      aiCallCount: 0,
      aiCostUsd: "0.00",
      shopifyCallCount: 1,
      providerCostStatus: "NO_BILLING_CREDENTIAL",
      providerBillingCredential: false,
      localCallsUsed: 1,
      localCallsRemaining: 9,
      localRateLimit: 10,
      localRateWindowSeconds: 60,
      externalEffect: "CATALOG_READ_ONLY",
    },
  };
}

function hydrationPayload(targetId) {
  const suffix = targetId.slice(-8);
  return {
    schemaVersion: "vitlane.catalog-research-hydration.v3",
    messages: [],
    pools: [{
      targetId,
      version: 1,
      expandOrdinal: 0,
      latestMode: "APPEND",
      latestDurationMilliseconds: 1,
      latestShopifyCalls: 1,
      latestRateRemaining: 9,
      products: [
        candidateProduct(`fixture-${suffix}-one`, `Fixture ${suffix} One`),
        candidateProduct(`fixture-${suffix}-two`, `Fixture ${suffix} Two`),
      ],
      hiddenProducts: [],
      messages: [],
    }],
    configurations: [],
    interactions: [],
    metrics: expansionPayload(targetId).metrics,
  };
}

function candidateProduct(id, title) {
  return {
    candidateId: id,
    source: "SHOPIFY",
    sourceProductRef: { source: "SHOPIFY", productId: id },
    purchaseRoute: "VITLANE_CHECKOUT",
    title,
    description: "Deterministic browser contract fixture",
    priceMinimumMinor: 8400,
    priceMaximumMinor: 8400,
    currency: "USD",
    categories: ["Bags"],
    features: [],
    specifications: [],
    locator: {
      kind: "PRODUCT_URL",
      productUrl: `https://shop.example/products/${id}`,
    },
    previewVariant: {
      id: `gid://shopify/ProductVariant/${id}`,
      title: "Default Title",
      priceMinor: 8400,
      currency: "USD",
      available: true,
      sellerName: "Shop Example",
      sellerDomain: "shop.example",
    },
  };
}

run().catch((error) => {
  console.error("curation density/conversation Firefox E2E: FAIL");
  console.error(error);
  process.exitCode = 1;
});
