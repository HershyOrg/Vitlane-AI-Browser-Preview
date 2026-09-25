const assert = require("node:assert/strict");
const { firefox } = require("playwright");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:4178").replace(/\/$/, "");
const curationPath = "/curations/e5100000-0000-4000-8000-000000000002";

async function run() {
  const browser = await firefox.launch({ headless: true });
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
    await page.locator("[data-testid='phase8-curation-surface']").waitFor();

    const fixtureResponse = await page.request.get(
      `${baseURL}/api/v1/curations/e5100000-0000-4000-8000-000000000002/workspace`,
    );
    assert.equal(fixtureResponse.status(), 200);
    const fixture = await fixtureResponse.json();
    const activeFixtureTargets = (fixture.targets ?? []).filter(({ removedAt }) => !removedAt);
    assert.equal(fixture.curation?.phase, "CURATING");
    assert.ok(
      activeFixtureTargets.length >= 2,
      "the review route must start with an already-curated multi-Target fixture",
    );
    assert.equal(
      await page.locator(".catalog-ui-target").count(),
      activeFixtureTargets.length,
      "the UI must render every active fixture Target before Auto mode is reviewed",
    );

    const composer = page.locator(".catalog-ui-focus-composer");
    const textarea = composer.locator('textarea[aria-label="조사 요청"]');
    const modeToken = composer.locator(".catalog-ui-focus-token");
    assert.equal(await composer.count(), 1);
    assert.equal(await textarea.isEnabled(), true);
    assert.match(await modeToken.innerText(), /@Auto/);
    assert.equal(
      await textarea.getAttribute("placeholder"),
      "추가하거나 보완할 조사 내용을 입력하세요",
    );

    await textarea.fill("초안은 mode를 바꿔도 유지합니다");
    await modeToken.click();
    const selector = page.locator(".catalog-ui-mode-selector");
    await selector.waitFor();
    assert.match(await selector.innerText(), /요청을 해석해 상품을 추가하거나 다시 조사합니다/);
    assert.ok(
      await selector.locator(".catalog-ui-mode-selector__option").count() >= 3,
      "Auto, Add Target and active Target modes must be reachable",
    );
    const desktopOptionGeometry = await selector
      .locator(".catalog-ui-mode-selector__option")
      .evaluateAll((options) => options.map((option) => {
        const optionRect = option.getBoundingClientRect();
        const copyRect = option
          .querySelector(".catalog-ui-mode-selector__copy")
          ?.getBoundingClientRect();
        return {
          copyTop: copyRect?.top ?? 0,
          copyBottom: copyRect?.bottom ?? 0,
          optionTop: optionRect.top,
          optionBottom: optionRect.bottom,
        };
      }));
    assert.ok(
      desktopOptionGeometry.every(({ copyTop, copyBottom, optionTop, optionBottom }) =>
        copyTop >= optionTop && copyBottom <= optionBottom),
      `desktop mode option copy must stay inside each row: ${JSON.stringify(desktopOptionGeometry)}`,
    );
    if (process.env.E2E_DESKTOP_SCREENSHOT_PATH) {
      await page.screenshot({ path: process.env.E2E_DESKTOP_SCREENSHOT_PATH });
    }
    await selector.getByText("상품 추가", { exact: true }).click();
    assert.equal(await textarea.inputValue(), "초안은 mode를 바꿔도 유지합니다");
    assert.match(await composer.locator(".catalog-ui-focus-token").innerText(), /@상품 추가/);

    await composer.locator(".catalog-ui-focus-token").click();
    await selector.getByRole("button", { name: "닫기" }).click();
    assert.equal(await textarea.isEnabled(), true);
    assert.equal(await textarea.inputValue(), "초안은 mode를 바꿔도 유지합니다");
    await composer.locator(".catalog-ui-focus-token").click();
    await selector.getByText("Auto", { exact: true }).click();
    assert.equal(await textarea.isEnabled(), true);

    let releaseAutoResponse;
    let observeAutoRequest;
    let autoRequestCount = 0;
    const autoRequestObserved = new Promise((resolve) => {
      observeAutoRequest = resolve;
    });
    const autoResponseGate = new Promise((resolve) => {
      releaseAutoResponse = resolve;
    });
    const autoRoutePattern = "**/api/v1/curations/*/conversation-requests";
    const autoRouteHandler = async (route) => {
      autoRequestCount += 1;
      observeAutoRequest();
      await autoResponseGate;
      await route.fulfill({
        status: 202,
        contentType: "application/json",
        body: JSON.stringify({
          status: "EXECUTED",
          decision: "RESEARCH_AGAIN",
          source: "MANAGED",
          targetId: activeFixtureTargets[0].id,
          reasonCode: "MANAGED_NORMALIZED_TARGET_MATCH",
          replay: false,
        }),
      });
    };
    await page.route(autoRoutePattern, autoRouteHandler);
    const autoDraft = "Auto 요청 처리 중에는 중복 전송하지 않습니다";
    await textarea.fill(autoDraft);
    await composer.getByRole("button", { name: "조사 요청 보내기" }).click();
    await autoRequestObserved;
    assert.equal(await textarea.isDisabled(), true);
    assert.equal(
      await composer.getByRole("button", { name: "조사 요청 보내기" }).isDisabled(),
      true,
    );
    assert.equal(await modeToken.isDisabled(), true);
    assert.equal(
      await composer.getByRole("button", { name: "상품 추가" }).isDisabled(),
      true,
    );
    assert.equal(autoRequestCount, 1);
    releaseAutoResponse();
    await page.waitForFunction(() => {
      const input = document.querySelector('textarea[aria-label="조사 요청"]');
      return input instanceof HTMLTextAreaElement && !input.disabled && input.value === "";
    });
    assert.equal(autoRequestCount, 1);
    // This fixture acknowledges the command without adding a server timeline row.
    // The submitted text stays visible once through the optimistic transcript.
    assert.equal(await page.getByText(autoDraft, { exact: true }).count(), 1);
    await page.unroute(autoRoutePattern, autoRouteHandler);

    await page.setViewportSize({ width: 320, height: 720 });
    await page.evaluate(() => {
      document.documentElement.style.fontSize = "200%";
    });
    await composer.locator(".catalog-ui-focus-token").click();
    await selector.waitFor();
    const geometry = await page.evaluate(() => {
      const composerNode = document.querySelector(".catalog-ui-focus-composer");
      const selectorNode = document.querySelector(".catalog-ui-mode-selector");
      const composerRect = composerNode?.getBoundingClientRect();
      const selectorRect = selectorNode?.getBoundingClientRect();
      return {
        viewportWidth: window.innerWidth,
        documentWidth: document.documentElement.scrollWidth,
        composer: composerRect
          ? { left: composerRect.left, right: composerRect.right }
          : null,
        selector: selectorRect
          ? {
              left: selectorRect.left,
              right: selectorRect.right,
              top: selectorRect.top,
              bottom: selectorRect.bottom,
            }
          : null,
      };
    });
    assert.equal(geometry.documentWidth, geometry.viewportWidth);
    assert.ok(
      geometry.composer && geometry.composer.left >= 0,
      `composer must stay inside the left viewport edge: ${JSON.stringify(geometry)}`,
    );
    assert.ok(
      geometry.composer && geometry.composer.right <= geometry.viewportWidth + 1,
      `composer must stay inside the right viewport edge: ${JSON.stringify(geometry)}`,
    );
    assert.ok(geometry.selector && geometry.selector.left >= 0);
    assert.ok(geometry.selector && geometry.selector.right <= geometry.viewportWidth + 1);
    assert.ok(geometry.selector && geometry.selector.top >= 0);
    assert.ok(geometry.selector && geometry.selector.bottom <= 720);
    const optionGeometry = await selector
      .locator(".catalog-ui-mode-selector__option")
      .evaluateAll((options) => options.map((option) => {
        const optionRect = option.getBoundingClientRect();
        const copyRect = option
          .querySelector(".catalog-ui-mode-selector__copy")
          ?.getBoundingClientRect();
        return {
          height: optionRect.height,
          copyTop: copyRect?.top ?? 0,
          copyBottom: copyRect?.bottom ?? 0,
          optionTop: optionRect.top,
          optionBottom: optionRect.bottom,
          computedHeight: getComputedStyle(option).height,
        };
      }));
    assert.ok(
      optionGeometry.every(({ copyTop, copyBottom, optionTop, optionBottom }) =>
        copyTop >= optionTop && copyBottom <= optionBottom),
      `mode option copy must stay inside each row: ${JSON.stringify(optionGeometry)}`,
    );
    if (process.env.E2E_SCREENSHOT_PATH) {
      await page.screenshot({ path: process.env.E2E_SCREENSHOT_PATH });
    }
    await page.keyboard.press("Escape");
    await selector.waitFor({ state: "hidden" });

    await context.addCookies([{ name: "vt_locale_choice", value: "en-US", url: baseURL }]);
    await page.reload({ waitUntil: "networkidle" });
    await page.locator("[data-testid='phase8-curation-surface']").waitFor();
    assert.equal(
      await page.locator('textarea[aria-label="Research request"]').getAttribute("placeholder"),
      "Describe what to add or refine",
    );
    await page.locator(".catalog-ui-focus-token").click();
    assert.match(
      await page.locator(".catalog-ui-mode-selector").innerText(),
      /Interpret the request and add or revisit a product/,
    );

    const freshWorkspaceResponse = await page.request.get(
      `${baseURL}/api/v1/curations/e5100000-0000-4000-8000-000000000002/workspace`,
    );
    assert.equal(freshWorkspaceResponse.status(), 200);
    const freshWorkspace = await freshWorkspaceResponse.json();
    const adapterTarget = freshWorkspace.targets.find(({ normalizedIntent }) =>
      normalizedIntent.includes("travel adapter"),
    );
    assert.ok(adapterTarget, "the curated fixture must include the normalized travel adapter Target");
    const clientRequestId = crypto.randomUUID();
    const autoResponse = await page.request.post(
      `${baseURL}/api/v1/curations/${freshWorkspace.curation.id}/auto-research`,
      {
        headers: { "Idempotency-Key": clientRequestId },
        data: {
          request: "여행용 어뎁터 좀더 조사해봐",
          expectedCurationVersion: freshWorkspace.curation.version,
          clientRequestId,
        },
      },
    );
    assert.equal(autoResponse.status(), 202, await autoResponse.text());
    const autoResult = await autoResponse.json();
    assert.equal(autoResult.status, "EXECUTED");
    assert.equal(autoResult.decision, "RESEARCH_AGAIN");
    assert.equal(autoResult.source, "MANAGED");
    assert.equal(autoResult.targetId, adapterTarget.id);

    let englishWorkspace;
    for (let attempt = 0; attempt < 60; attempt += 1) {
      const response = await page.request.get(
        `${baseURL}/api/v1/curations/${freshWorkspace.curation.id}/workspace`,
      );
      assert.equal(response.status(), 200);
      englishWorkspace = await response.json();
      const adapterGroup = englishWorkspace.research.groups.find(
        ({ session }) => session.planTargetId === adapterTarget.id,
      );
      if (adapterGroup?.session.status === "REVIEWING") break;
      await new Promise((resolve) => setTimeout(resolve, 500));
    }
    const adapterGroup = englishWorkspace?.research.groups.find(
      ({ session }) => session.planTargetId === adapterTarget.id,
    );
    assert.equal(adapterGroup?.session.status, "REVIEWING", "Korean Auto research must finish before the English gate assertion");
    const englishRequestId = crypto.randomUUID();
    const englishResponse = await page.request.post(
      `${baseURL}/api/v1/curations/${freshWorkspace.curation.id}/auto-research`,
      {
        headers: { "Idempotency-Key": englishRequestId },
        data: {
          request: "Research the travel adaptor more",
          expectedCurationVersion: englishWorkspace.curation.version,
          clientRequestId: englishRequestId,
        },
      },
    );
    assert.equal(englishResponse.status(), 202, await englishResponse.text());
    const englishResult = await englishResponse.json();
    assert.equal(englishResult.status, "EXECUTED");
    assert.equal(englishResult.decision, "RESEARCH_AGAIN");
    assert.equal(englishResult.source, "DETERMINISTIC");
    assert.equal(englishResult.targetId, adapterTarget.id);

    assert.deepEqual(pageErrors, []);
    console.log("curation Auto mode E2E passed");
  } finally {
    await browser.close();
  }
}

run().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
