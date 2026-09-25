const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { firefox } = require("playwright");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:4178").replace(/\/$/, "");
const curationPath = "/curations/e5100000-0000-4000-8000-000000000002";
const artifactDir = process.env.E2E_ARTIFACT_DIR;

async function run() {
  const browser = await firefox.launch({ headless: true });
  const context = await browser.newContext({
    locale: "ko-KR",
    viewport: { width: 1440, height: 960 },
  });
  const page = await context.newPage();
  const pageErrors = [];
  const liveCalls = [];
  const forbiddenCalls = [];
  const safeMetricsEvidence = {
    search: [],
    variant: [],
    prepare: [],
  };
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("request", (request) => {
    const pathname = new URL(request.url()).pathname;
    if (/\/api\/v1\/curations\/[^/]+\/(catalog-research|cart|prepare-agency-order)/.test(pathname) ||
        /\/targets\/[^/]+\/catalog-research\/expansions$/.test(pathname)) {
      liveCalls.push(`${request.method()} ${pathname}`);
    }
    if (/payment|approvals|settlement-transactions|agencyOrder/.test(pathname)) {
      forbiddenCalls.push(pathname);
    }
  });
  await page.route(
    /\/api\/v1\/curations\/[^/]+\/targets\/[^/]+\/catalog-research\/expansions$/,
    async (route) => {
      const response = await route.fetch();
      if (!response.ok()) {
        await route.fulfill({ response });
        return;
      }
      const payload = await response.json();
      recordSafeLiveMetrics(safeMetricsEvidence, "search", payload.metrics);
      if (Array.isArray(payload.products) && payload.products.length > 0) {
        payload.messages = [
          ...(Array.isArray(payload.messages) ? payload.messages : []),
          {
            type: "warning",
            code: "e2e_required_disclosure",
            path: "$.products[0]",
            contentType: "markdown",
            content: "E2E required Shopify disclosure",
            severity: "warning",
            presentation: "disclosure",
            url: "https://shopify.dev/docs/agents/catalog/global-catalog",
          },
          {
            type: "info",
            code: "e2e_optional_info",
            path: "$.products[0]",
            content: "E2E optional Shopify info must remain hidden",
            presentation: "notice",
          },
        ];
      }
      await route.fulfill({ response, json: payload });
    },
  );
  await page.route(
    /\/api\/v1\/curations\/[^/]+\/catalog-research\/hydrations$/,
    async (route) => {
      const response = await route.fetch();
      if (!response.ok()) {
        await route.fulfill({ response });
        return;
      }
      const payload = await response.json();
      const pool = Array.isArray(payload.pools)
        ? payload.pools.find((candidatePool) =>
          Array.isArray(candidatePool.products) && candidatePool.products.length > 0)
        : undefined;
      const product = pool?.products?.[0];
      if (product) {
        pool.messages = [
          ...(Array.isArray(pool.messages) ? pool.messages : []),
          {
            type: "warning",
            code: "e2e_required_disclosure",
            path: "$.products[0]",
            subjectKind: "PRODUCT",
            subjectRef: product.candidateId,
            contentType: "markdown",
            content: "E2E required Shopify disclosure",
            severity: "warning",
            presentation: "disclosure",
            url: "https://shopify.dev/docs/agents/catalog/global-catalog",
          },
        ];
      }
      await route.fulfill({ response, json: payload });
    },
  );

  try {
    await page.goto(`${baseURL}/login?returnTo=${encodeURIComponent(curationPath)}`, {
      waitUntil: "networkidle",
    });
    // The dedicated review server maps this exact return path to the
    // multi-product TEST profile and starts it automatically.
    await page.waitForURL(`**${curationPath}`);
    await page.locator("[data-testid='phase8-curation-surface']").waitFor();

    await page.goto(`${baseURL}/`, { waitUntil: "networkidle" });
    await page.locator(".shell-shipping-context").waitFor();
    assert.match(
      await page.locator(".shell-shipping-context").innerText(),
      /현재 배송 국가/,
      "Home composer must always show the shipping country",
    );
    await page.getByRole("button", { name: /설정하기/ }).click();
    assert.equal(
      await page.getByRole("radio", { name: "비용 제한 없음" }).getAttribute("data-state"),
      "on",
      "Research price must default to None",
    );
    assert.equal(await page.locator("#purchase-budget").count(), 0);
    assert.equal(await page.locator("#curation-min-price").isDisabled(), true);
    await page.getByLabel("배송 국가").selectOption("US");
    await page.getByLabel("배송 도시").fill("Seattle");
    await page.getByRole("button", { name: /설정 접기/ }).click();

    // One initial user request must traverse the preserved asynchronous
    // CurationAction → Round → IntelligenceJob control plane and populate the
    // Phase 8 CandidatePool. The browser only submits and observes; it must not
    // need an Expand command to create the first Candidates.
    const directSearchMutationsBeforeIntent = liveCalls.filter((value) =>
      /POST .*\/targets\/[^/]+\/catalog-research\/expansions$/.test(value)
    ).length;
    await page.locator("#curation-intent").fill("camping chair");
    await page.locator(".shell-intent-composer__submit").click();
    await page.waitForURL(/\/curations\/[0-9a-f-]+$/i, { timeout: 30000 });
    const automaticCurationID = new URL(page.url()).pathname.split("/").pop();
    assert.ok(automaticCurationID, "initial request must create a Curation");
    await page.locator("[data-testid='phase8-curation-surface']").waitFor();
    const automaticTarget = page.locator(".catalog-ui-target").first();
    await automaticTarget.locator(".vt-candidate-card").first().waitFor({
      timeout: 120000,
    });
    assert.equal(
      liveCalls.filter((value) =>
        /POST .*\/targets\/[^/]+\/catalog-research\/expansions$/.test(value)
      ).length,
      directSearchMutationsBeforeIntent,
      "initial Candidate generation must not require browser-side Expand",
    );
    const automaticWorkspaceResponse = await page.request.get(
      `${baseURL}/api/v1/curations/${automaticCurationID}/workspace`,
    );
    assert.equal(automaticWorkspaceResponse.status(), 200);
    const automaticWorkspace = await automaticWorkspaceResponse.json();
    const automaticGroups = automaticWorkspace.research?.groups ?? [];
    const automaticJobs = automaticWorkspace.intelligence ?? [];
    assert.ok(automaticGroups.length >= 1);
    for (const group of automaticGroups) {
      assert.equal(Object.hasOwn(group, "candidates"), false);
      assert.equal(Object.hasOwn(group, "submission"), false);
      assert.equal(
        automaticJobs.filter(
          ({ targetKind, targetId }) =>
            targetKind === "RESEARCH_ROUND" && targetId === group.round?.id,
        ).length,
        1,
        "each initial Target must settle through exactly one Research Job",
      );
    }
    const automaticPoolResponse = await hydrateWhenNotBusy(
      page,
      automaticCurationID,
      automaticGroups[0].session.planTargetId,
    );
    assert.equal(automaticPoolResponse.status(), 200);
    const automaticPool = await automaticPoolResponse.json();
    assert.ok(
      (automaticPool.pools ?? []).some(({ products }) => products?.length > 0),
      "the initial worker must durably populate CandidatePool",
    );

    await page.goto(`${baseURL}${curationPath}`, { waitUntil: "networkidle" });
    await page.locator("[data-testid='phase8-curation-surface']").waitFor();

    assert.equal(
      await page.locator(".catalog-ui-focus-composer").count(),
      1,
      "the actual Curation route must have one composer",
    );
    assert.equal(await page.locator(".phase8-research-again").count(), 0);
    assert.equal(await page.locator(".shell-curation-expansion").count(), 0);
    assert.equal(await page.locator(".catalog-ui-target").first().evaluate((node) => node.tagName), "SECTION");
    await page.locator(".catalog-ui-focus-composer__context").waitFor();
    assert.match(
      await page.locator(".catalog-ui-focus-composer__context").innerText(),
      /배송 국가\s+US/,
    );
    const firstTarget = page.locator(".catalog-ui-target").first();
    const firstTargetID = await firstTarget.getAttribute("data-target-id");
    assert.ok(firstTargetID);
    await page.setViewportSize({ width: 900, height: 960 });
    const compactTargetHeader = await firstTarget.evaluate((node) => {
      const heading = node.querySelector(".catalog-ui-target__identity h2");
      const actions = node.querySelector(".catalog-ui-target__actions");
      const headingRect = heading?.getBoundingClientRect();
      const actionsRect = actions?.getBoundingClientRect();
      return {
        heading: headingRect
          ? { bottom: headingRect.bottom, height: headingRect.height, width: headingRect.width }
          : null,
        actions: actionsRect
          ? { top: actionsRect.top, width: actionsRect.width }
          : null,
        headingWhiteSpace: heading ? getComputedStyle(heading).whiteSpace : null,
      };
    });
    assert.ok(compactTargetHeader.heading, "target heading must be visible");
    assert.ok(compactTargetHeader.actions, "target actions must be visible");
    assert.equal(compactTargetHeader.headingWhiteSpace, "nowrap");
    assert.ok(
      compactTargetHeader.heading.width > 300,
      `target heading must stay horizontal, width=${compactTargetHeader.heading.width}`,
    );
    assert.ok(
      compactTargetHeader.actions.top >= compactTargetHeader.heading.bottom,
      "target actions must move below the heading before squeezing it vertically",
    );
    await page.setViewportSize({ width: 1440, height: 960 });
    const seededCatalogResponse = await hydrateWhenNotBusy(
      page,
      "e5100000-0000-4000-8000-000000000002",
      firstTargetID,
    );
    assert.equal(seededCatalogResponse.status(), 200);
    const seededCatalogWorkspace = await seededCatalogResponse.json();
    const seededCatalogPool = (seededCatalogWorkspace.pools ?? []).find(
      ({ targetId }) => targetId === firstTargetID,
    );
    assert.equal(
      await firstTarget.locator(".vt-candidate-card").count(),
      seededCatalogPool?.products?.length ?? 0,
      "the Phase 8 surface must render only its durable CandidatePool projection",
    );
    const firstExpandResponsePromise = page.waitForResponse((response) =>
      new URL(response.url()).pathname.endsWith(`/targets/${firstTargetID}/catalog-research/expansions`) &&
      response.request().method() === "POST",
    );
    await firstTarget.getByRole("button", { name: "후보 더 찾기" }).click();
    const firstExpandResponse = await firstExpandResponsePromise;
    const firstExpandBody = await firstExpandResponse.text();
    assert.equal(
      firstExpandResponse.status(),
      200,
      firstExpandBody,
    );
    const firstExpandPayload = JSON.parse(firstExpandBody);
    let disclosureCandidateID = firstExpandPayload.products?.[0]?.candidateId;
    assert.ok(disclosureCandidateID, "mandatory disclosure must bind to a returned Candidate");
    const latestConversation = page
      .locator('.curation-transcript__row--message [data-conversation-presentation="diff"]')
      .last();
    await latestConversation.waitFor();
    assert.equal(
      await page.getByTestId("phase8-latest-slot").count(),
      0,
      "the live turn must leave no bubble or idle gap below the settled artifact",
    );
    await assertResearchTelemetryHidden(firstTarget);
    const conversationOrder = await page.locator(".curation-workspace").evaluate((root) => {
      const history = root.querySelector(
        ".curation-transcript__row--message .curation-conversation-bubble",
      );
      const artifact = root.querySelector(".catalog-ui-curation__targets");
      const tail = root.querySelector("[data-testid='phase8-latest-slot']");
      const composer = root.querySelector(".catalog-ui-composer-dock");
      const follows = (first, second) => Boolean(
        first && second &&
        (first.compareDocumentPosition(second) & Node.DOCUMENT_POSITION_FOLLOWING),
      );
      return {
        historyBeforeArtifact: follows(history, artifact),
        artifactBeforeComposer: follows(artifact, composer),
        tailAbsent: tail === null,
      };
    });
    assert.deepEqual(conversationOrder, {
      historyBeforeArtifact: true,
      artifactBeforeComposer: true,
      tailAbsent: true,
    });
    assert.equal(
      await page.locator(".phase8-conversation-log").count(),
      0,
      "the Candidate artifact must not create a nested conversation scroll owner",
    );
    const seededCount = await firstTarget.locator(".vt-candidate-card").count();
    assert.ok(seededCount > 0, "first Expand must seed the Phase 8 CandidatePool");
    await assertCandidateShopifyBadges(firstTarget);
    assert.equal(
      await firstTarget.locator(".catalog-ui-candidate-grid").getAttribute("data-row-count"),
      "2",
      "a single Target Candidate rail must use at most two rows",
    );
    const existingName = (
      await firstTarget.locator(".vt-candidate-card__title").first().innerText()
    ).trim();
    const secondExpandResponsePromise = page.waitForResponse((response) =>
      new URL(response.url()).pathname.endsWith(`/targets/${firstTargetID}/catalog-research/expansions`) &&
      response.request().method() === "POST",
    );
    await firstTarget.getByRole("button", { name: "후보 더 찾기" }).click();
    const secondExpandResponse = await secondExpandResponsePromise;
    const secondExpandBody = await secondExpandResponse.text();
    assert.equal(secondExpandResponse.status(), 200, secondExpandBody);
    const secondExpandPayload = JSON.parse(secondExpandBody);
    disclosureCandidateID = secondExpandPayload.products?.[0]?.candidateId;
    assert.ok(
      disclosureCandidateID,
      "the latest mandatory disclosure must bind to the latest returned Candidate",
    );
    const secondDiff = page
      .locator('.curation-transcript__row--message [data-conversation-presentation="diff"]')
      .last();
    await secondDiff.getByText(/기존 추천 상품.*유지/).waitFor();
    const afterExpand = await firstTarget.locator(".vt-candidate-card").count();
    assert.ok(afterExpand >= seededCount, "Expand must preserve the existing CandidatePool");
    await assertCandidateShopifyBadges(firstTarget);
    assert.ok(await firstTarget.getByText(existingName, { exact: true }).isVisible());
    assert.match(await secondDiff.innerText(), /기존 추천 상품.*유지/);
    assert.equal(await page.getByTestId("phase8-latest-slot").count(), 0);
    assert.ok(liveCalls.some((value) => value.includes(`/targets/${firstTargetID}/catalog-research/expansions`)));

    const disclosureEntry = firstTarget.locator(
      `.catalog-ui-candidate-entry[data-candidate-id=${JSON.stringify(disclosureCandidateID)}]`,
    );
    const liveCard = disclosureEntry.locator(":scope > .vt-candidate-card");
    const liveCandidateID = await disclosureEntry.getAttribute("data-candidate-id");
    assert.equal(liveCandidateID, disclosureCandidateID);
    const liveCardTitle = (
      await liveCard.locator(".vt-candidate-card__title").innerText()
    ).trim();
    await page.setViewportSize({ width: 390, height: 844 });
    const mobileBadgeBox = await liveCard.locator('.candidate-source-badge[data-source="SHOPIFY"]').boundingBox();
    assert.ok(mobileBadgeBox, "Shopify badge must remain visible on mobile");
    assert.ok(
      mobileBadgeBox.x >= 0 && mobileBadgeBox.x + mobileBadgeBox.width <= 390,
      "Shopify badge must remain inside the mobile viewport",
    );
    await assertCandidateCardCompactDensity(liveCard, { touch: true });
    await page.setViewportSize({ width: 1440, height: 960 });
    await assertCandidateCardCompactDensity(liveCard);
    assert.equal(await liveCard.locator(".vt-candidate-card__hit-area").count(), 1);
    assert.equal(await liveCard.locator(".vt-candidate-card__icon-actions").count(), 0);
    const variantResponsePromise = page.waitForResponse((response) =>
      /\/catalog-research\/candidates\/[^/]+\/variant-pages$/.test(new URL(response.url()).pathname),
    );
    const liveCardActionName = await liveCard.locator(".vt-candidate-card__hit-area").getAttribute("aria-label");
    assert.ok(liveCardActionName, "Candidate card must expose an accessible modal action");
    await clickCandidateCardBody(liveCard);
    const searchPlatform = page.locator(
      ".catalog-ui-candidate-modal .catalog-ui-candidate-search-platform",
    );
    await searchPlatform.getByText("E2E required Shopify disclosure", { exact: true }).waitFor();
    assert.match(await searchPlatform.getAttribute("aria-label"), /SearchPlatform 필수 안내/);
    assert.equal(await searchPlatform.locator('[role="note"]').count(), 1);
    assert.equal(await searchPlatform.locator("blockquote").count(), 0);
    assert.equal(await searchPlatform.getByText("E2E optional Shopify info must remain hidden").count(), 0);
    assert.equal(await searchPlatform.locator("button").count(), 0, "provider disclosure must not be dismissible");
    assert.equal(
      await searchPlatform.getByRole("link", { name: /Shopify 안내 자세히 보기/ }).getAttribute("rel"),
      "nofollow noopener noreferrer",
    );
    assert.equal(
      await searchPlatform.evaluate(
        (node) => node.nextElementSibling?.querySelector("h3")?.textContent,
      ),
      "Intent Point",
      "SearchPlatform must sit immediately above Intent Point",
    );
    const candidateModal = page.locator(".catalog-ui-candidate-modal");
    await candidateModal.getByRole("heading", { name: "옵션", exact: true }).waitFor();
    const variantResponse = await variantResponsePromise;
    assert.equal(
      variantResponse.status(),
      200,
      `${await variantResponse.text()}\nrequest=${variantResponse.request().postData()}`,
    );
    recordSafeLiveMetrics(
      safeMetricsEvidence,
      "variant",
      (await variantResponse.json()).metrics,
    );
    await candidateModal.locator(".catalog-ui-variant-row").first().waitFor();
    assert.equal(
      await candidateModal.getByText(
        "옵션을 새로 불러오지 못했습니다. 저장된 옵션이 최신 정보가 아닐 수 있습니다.",
        { exact: true },
      ).count(),
      0,
      "live E2E must not pass through the preview-only fallback",
    );
    assert.ok(liveCalls.some((value) => value.includes("POST") && value.includes("/variant-pages")));
    const nextVariantPage = candidateModal.getByRole("button", { name: "다음" });
    if (await nextVariantPage.count() && !(await nextVariantPage.isDisabled())) {
      const nextResponse = page.waitForResponse((response) =>
        /\/catalog-research\/candidates\/[^/]+\/variant-pages$/.test(new URL(response.url()).pathname),
      );
      await nextVariantPage.click();
      const nextVariantResponse = await nextResponse;
      assert.equal(nextVariantResponse.status(), 200);
      recordSafeLiveMetrics(
        safeMetricsEvidence,
        "variant",
        (await nextVariantResponse.json()).metrics,
      );
      await candidateModal.getByRole("button", { name: "이전" }).click();
      await candidateModal.locator(".catalog-ui-variant-row").first().waitFor();
    }
    const firstVariant = candidateModal.locator(".catalog-ui-variant-row").first();
    if (await firstVariant.count()) await firstVariant.click();
    const evidenceLists = candidateModal.locator(
      ".catalog-ui-candidate-evidence > section:not(.catalog-ui-candidate-search-platform) ul",
    );
    assert.equal(await evidenceLists.count(), 2, "Feature and Spec must always be separate semantic lists");
    assert.deepEqual(
      await candidateModal.locator(".catalog-ui-candidate-evidence h3").allTextContents(),
      ["SearchPlatform", "Intent Point", "Feature", "Spec"],
    );
    assert.equal(
      await evidenceLists.first().evaluate((node) => getComputedStyle(node).listStyleType),
      "disc",
      "Feature/Spec lines must show explicit bullet markers",
    );
    if (artifactDir) {
      await fs.mkdir(artifactDir, { recursive: true });
      await page.screenshot({
        path: path.join(artifactDir, "phase8-curations-variant-modal.png"),
        fullPage: true,
      });
    }
    const likedResponsePromise = page.waitForResponse((response) =>
      new URL(response.url()).pathname.endsWith("/interaction") &&
      response.request().method() === "PUT",
    );
    await candidateModal.getByRole("button", { name: "좋아요" }).click();
    const likedResponse = await likedResponsePromise;
    assert.equal(likedResponse.status(), 204, "liked interaction must commit atomically");
    assert.equal(
      await candidateModal.getByRole("button", { name: "좋아요" }).getAttribute("aria-pressed"),
      "true",
    );
    const selectedVariantTitle = (await firstVariant.locator("strong").first().innerText()).trim();
    const saveConfigurationResponse = page.waitForResponse((response) =>
      new URL(response.url()).pathname.endsWith("/configuration") &&
      response.request().method() === "PUT",
    );
    await candidateModal.getByRole("button", { name: "옵션 저장" }).click();
    assert.equal((await saveConfigurationResponse).status(), 204);
    await liveCard.getByText(selectedVariantTitle, { exact: true }).waitFor();

    const reopenVariantResponsePromise = page.waitForResponse((response) =>
      /\/catalog-research\/candidates\/[^/]+\/variant-pages$/.test(new URL(response.url()).pathname),
    );
    await clickCandidateCardBody(liveCard);
    const reopenedModal = page.locator(".catalog-ui-candidate-modal");
    await reopenedModal.getByRole("heading", { name: "옵션", exact: true }).waitFor();
    const reopenVariantResponse = await reopenVariantResponsePromise;
    assert.equal(reopenVariantResponse.status(), 200);
    recordSafeLiveMetrics(
      safeMetricsEvidence,
      "variant",
      (await reopenVariantResponse.json()).metrics,
    );
    await reopenedModal.locator(".catalog-ui-variant-row").first().waitFor();
    assert.equal(
      await reopenedModal.locator(".catalog-ui-variant-row").filter({ hasText: selectedVariantTitle }).getAttribute("aria-checked"),
      "true",
      "saved CandidateConfiguration must be the modal default",
    );
    const providerReadsBeforeCartMutation = liveCalls.filter((value) =>
      value.includes("/variant-pages") ||
      value.includes("/prepare-agency-order") ||
      /\/targets\/[^/]+\/catalog-research\/expansions/.test(value)
    ).length;
    await reopenedModal.getByRole("button", { name: "장바구니 담기" }).click();
    assert.equal(
      liveCalls.filter((value) =>
        value.includes("/variant-pages") ||
        value.includes("/prepare-agency-order") ||
        /\/targets\/[^/]+\/catalog-research\/expansions/.test(value)
      ).length,
      providerReadsBeforeCartMutation,
      "adding a CartView item may persist to Vitlane but must issue no Shopify read",
    );
    assert.ok(liveCalls.some((value) => value.includes("PUT") && value.endsWith("/cart")));
    await liveCard.getByRole("button", { name: "장바구니 제거" }).waitFor();

    const reloadPage = await context.newPage();
    try {
      await reloadPage.goto(`${baseURL}${curationPath}`, { waitUntil: "networkidle" });
      await reloadPage.locator("[data-testid='phase8-curation-surface']").waitFor();
      const reloadedCard = reloadPage.locator(
        `.catalog-ui-candidate-entry[data-candidate-id="${liveCandidateID}"] .vt-candidate-card`,
      );
      await reloadedCard.waitFor();
      await reloadedCard.getByText(selectedVariantTitle, { exact: true }).waitFor();
      await reloadedCard.getByRole("button", { name: "장바구니 제거" }).waitFor();
      const reloadedCartButton = reloadPage.getByRole("button", {
        name: /장바구니 1/,
      });
      await reloadedCartButton.waitFor();
    } finally {
      await reloadPage.close();
    }

    await page.getByRole("button", { name: /장바구니 1/ }).click();
    const prepareResponsePromise = page.waitForResponse((response) =>
      new URL(response.url()).pathname.endsWith("/prepare-agency-order") &&
      response.request().method() === "POST",
    );
    await page.getByRole("button", { name: "주문하기" }).click();
    const prepareResponse = await prepareResponsePromise;
    assert.equal(prepareResponse.status(), 200, await prepareResponse.text());
    recordSafeLiveMetrics(
      safeMetricsEvidence,
      "prepare",
      (await prepareResponse.json()).metrics,
    );
    await page
      .locator(".catalog-ui-draft-result.is-ready")
      .getByText("준비됨", { exact: true })
      .waitFor({ timeout: 30000 });
    assert.ok(liveCalls.some((value) => value.includes("POST") && value.endsWith("/prepare-agency-order")));
    assert.deepEqual(forbiddenCalls, []);
    const editVariantResponsePromise = page.waitForResponse((response) =>
      /\/catalog-research\/candidates\/[^/]+\/variant-pages$/.test(new URL(response.url()).pathname),
    );
    await page.getByRole("button", { name: "옵션 변경" }).click();
    const editModal = page.locator(".catalog-ui-candidate-modal");
    await editModal.getByRole("heading", { name: "옵션", exact: true }).waitFor();
    const editVariantResponse = await editVariantResponsePromise;
    assert.equal(editVariantResponse.status(), 200, await editVariantResponse.text());
    recordSafeLiveMetrics(
      safeMetricsEvidence,
      "variant",
      (await editVariantResponse.json()).metrics,
    );
    await editModal.getByRole("button", { name: "장바구니 옵션 적용" }).waitFor();
    const modalBackdrop = page.locator(".catalog-ui-candidate-modal__backdrop");
    // A product's details are a sheet over the page: no scrim, no blur (ADR-0086). A click outside still closes it.
    assert.equal(await modalBackdrop.evaluate((node) => getComputedStyle(node).backdropFilter), "none");
    await modalBackdrop.click({ position: { x: 4, y: 4 } });
    await editModal.waitFor({ state: "hidden" });
    const cartBackdrop = page.locator(".catalog-ui-draft-backdrop");
    // The cart drawer stops the flow, so its scrim darkens the page — without blurring it (owner 2026-09-23).
    assert.deepEqual(await cartBackdrop.evaluate((node) => [getComputedStyle(node).backdropFilter, getComputedStyle(node).backgroundColor !== "rgba(0, 0, 0, 0)"]), ["none", true]);
    await cartBackdrop.click({ position: { x: 4, y: 4 } });
    await page.getByRole("heading", { name: "장바구니" }).waitFor({ state: "hidden" });

    const secondAddVariantPagePromise = page.waitForResponse((response) =>
      /\/catalog-research\/candidates\/[^/]+\/variant-pages$/.test(new URL(response.url()).pathname),
    );
    await clickCandidateCardBody(liveCard);
    const secondAddModal = page.locator(".catalog-ui-candidate-modal");
    await secondAddModal.getByRole("heading", { name: "옵션", exact: true }).waitFor();
    const secondAddVariantPage = await secondAddVariantPagePromise;
    assert.equal(secondAddVariantPage.status(), 200, await secondAddVariantPage.text());
    recordSafeLiveMetrics(
      safeMetricsEvidence,
      "variant",
      (await secondAddVariantPage.json()).metrics,
    );
    const addAnotherButton = secondAddModal.getByRole("button", { name: "한 개 더 담기" });
    await addAnotherButton.waitFor();
    const secondCartMutationPromise = page.waitForResponse((response) =>
      new URL(response.url()).pathname.endsWith("/cart") &&
      response.request().method() === "PUT",
    );
    await addAnotherButton.click();
    const secondCartMutation = await secondCartMutationPromise;
    assert.equal(secondCartMutation.status(), 200, await secondCartMutation.text());
    const secondCartPayload = await secondCartMutation.json();
    assert.equal(
      secondCartPayload.items?.[0]?.quantity,
      2,
      "re-adding the same Candidate Variant must increment its durable quantity",
    );
    const secondCartCommand = JSON.parse(secondCartMutation.request().postData());
    assert.equal(
      Object.hasOwn(secondCartCommand.items?.[0] ?? {}, "addedAt"),
      false,
      "response-only Cart metadata must not be reflected into the strict command body",
    );
    await secondAddModal.waitFor({ state: "hidden" });
    await page.getByRole("button", { name: /장바구니 1/ }).click();
    assert.equal(
      await cartBackdrop.locator("select").first().inputValue(),
      "2",
      "the open CartView must immediately show the incremented quantity",
    );
    await cartBackdrop.click({ position: { x: 4, y: 4 } });
    await page.getByRole("heading", { name: "장바구니" }).waitFor({ state: "hidden" });

    await firstTarget.getByRole("button", { name: "재조사" }).click();
    assert.equal(
      await firstTarget.getByRole("button", { name: "재조사" }).getAttribute("aria-pressed"),
      "true",
    );
    const token = page.locator(".catalog-ui-focus-token");
    assert.match(await token.innerText(), /@재조사/);
    const textarea = page.locator('textarea[aria-label="조사 요청"]');
    await textarea.fill("wireless over ear headphones for travel");
    const replacementHydrationPromise = page.waitForResponse((response) =>
      new URL(response.url()).pathname.endsWith("/catalog-research/hydrations") &&
      response.request().method() === "POST" &&
      response.request().postData()?.includes(firstTargetID) &&
      response.status() === 200,
      { timeout: 120000 },
    );
    await page.getByRole("button", { name: "조사 요청 보내기" }).click();
    const replacedWorkspaceResponse = await replacementHydrationPromise;
    assert.equal(replacedWorkspaceResponse.status(), 200);
    const replacedWorkspace = await replacedWorkspaceResponse.json();
    const replacedPool = (replacedWorkspace.pools ?? []).find(
      ({ targetId }) => targetId === firstTargetID,
    );
    assert.equal(replacedPool?.latestMode, "REPLACE");
    const visibleCandidateIDs = (await firstTarget
      .locator(".catalog-ui-candidate-entry")
      .evaluateAll((nodes) => nodes.map((node) => node.getAttribute("data-candidate-id"))))
      .filter(Boolean)
      .sort();
    assert.deepEqual(
      visibleCandidateIDs,
      (replacedPool?.products ?? []).map(({ candidateId }) => candidateId).sort(),
      "Research Again must render the durable REPLACE visible projection; reobserved products may remain visible",
    );
    await assertResearchTelemetryHidden(firstTarget);
    if ((replacedPool?.hiddenProducts ?? []).length > 0) {
      const expectedExpandedCount =
        (replacedPool.products?.length ?? 0) + replacedPool.hiddenProducts.length;
      const hiddenHydrationResponsePromise = page.waitForResponse((response) =>
        new URL(response.url()).pathname.endsWith("/catalog-research/hydrations") &&
        response.request().method() === "POST" &&
        response.request().postData()?.includes('"scope":"HIDDEN_TARGET"'),
      );
      await firstTarget.getByRole("button", { name: /숨김 해제/ }).click();
      const hiddenHydrationResponse = await hiddenHydrationResponsePromise;
      assert.equal(
        hiddenHydrationResponse.status(),
        200,
        await hiddenHydrationResponse.text(),
      );
      await page.waitForFunction(
        ({ targetId, count }) =>
          document.querySelectorAll(
            `.catalog-ui-target[data-target-id="${targetId}"] .catalog-ui-candidate-entry`,
          ).length === count,
        { targetId: firstTargetID, count: expectedExpandedCount },
      );
      assert.equal(
        await firstTarget.locator(".catalog-ui-candidate-entry").count(),
        expectedExpandedCount,
        "hidden history toggle must reveal every durable hidden Candidate",
      );
      await firstTarget.getByRole("button", { name: /다시 숨기기/ }).click();
    }

    const targetCountBeforeAdd = await page.locator(".catalog-ui-target").count();
    await page.getByRole("button", { name: "Target 추가 방식 선택" }).click();
    await page.locator('textarea[aria-label="조사 요청"]').fill("lightweight camping chair");
    await page.locator('textarea[aria-label="조사 요청"]').press("Enter");
    await page.waitForFunction(
      (count) => document.querySelectorAll(".catalog-ui-target").length > count,
      targetCountBeforeAdd,
      { timeout: 30000 },
    );
    const newlyRenderedTarget = page.locator(".catalog-ui-target").last();
    const addedTargetID = await newlyRenderedTarget.getAttribute("data-target-id");
    assert.ok(addedTargetID, "managed Add Target must expose its Target ID");
    const addedTarget = page.locator(
      `.catalog-ui-target[data-target-id="${addedTargetID}"]`,
    );
    const addedTargetTitle = (await addedTarget.locator("h2").innerText()).trim();
    const addTargetDirectMutationCount = liveCalls.filter((value) =>
      /POST .*\/targets\/[^/]+\/catalog-research\/expansions$/.test(value)
    ).length;
    await addedTarget.locator(".vt-candidate-card").first().waitFor({ timeout: 120000 });
    assert.equal(
      liveCalls.filter((value) =>
        /POST .*\/targets\/[^/]+\/catalog-research\/expansions$/.test(value)
      ).length,
      addTargetDirectMutationCount,
      "Add Target must automatically reach CandidatePool without Expand",
    );
    const addedWorkspaceResponse = await hydrateWhenNotBusy(
      page,
      "e5100000-0000-4000-8000-000000000002",
      addedTargetID,
    );
    assert.equal(addedWorkspaceResponse.status(), 200);
    const addedWorkspace = await addedWorkspaceResponse.json();
    const addedPool = (addedWorkspace.pools ?? []).find(
      ({ targetId }) => targetId === addedTargetID,
    );
    assert.equal(
      addedPool?.latestMode,
      "REPLACE",
      "the first Research Job for an added Target must replace its visible pool",
    );
    await assertResearchTelemetryHidden(addedTarget);
    await addedTarget.getByRole("button", { name: /Target 제거/ }).click();
    const removalDialog = page.getByRole("alertdialog", { name: "정말 이 Target을 제거하시겠습니까?" });
    await removalDialog.waitFor();
    assert.match(await removalDialog.innerText(), new RegExp(escapeRegex(addedTargetTitle)));
    await removalDialog.getByRole("button", { name: "제거" }).click();
    const removedTarget = page.locator(
      `.catalog-ui-target[data-target-id="${addedTargetID}"]`,
    );
    await removedTarget.waitFor({ state: "detached", timeout: 30000 });
    assert.equal(await removedTarget.count(), 0);

    await page.reload({ waitUntil: "networkidle" });
    await page.locator("[data-testid='phase8-curation-surface']").waitFor();

    await page.setViewportSize({ width: 390, height: 844 });
    const mobileClose = page.getByRole("button", { name: "사이드바 닫기" });
    await page.waitForTimeout(250);
    if (await mobileClose.isVisible()) {
      await mobileClose.click({ force: true, timeout: 2000 }).catch(() => undefined);
    }
    assert.equal(
      await page.locator(".catalog-ui-candidate-grid").first().getAttribute("data-row-count"),
      "2",
      "a single Target keeps the two-row rail contract on mobile",
    );
    const composerBox = await page.locator(".catalog-ui-focus-composer").boundingBox();
    assert.ok(composerBox, "composer must be visible");
    assert.ok(
      composerBox.y + composerBox.height <= 844,
      "sticky composer must remain inside the mobile viewport",
    );
    assert.deepEqual(pageErrors, [], `page errors: ${pageErrors.join(" | ")}`);

    if (artifactDir) {
      await fs.mkdir(artifactDir, { recursive: true });
      await page.screenshot({
        path: path.join(artifactDir, "phase8-curations-live-mobile.png"),
        fullPage: true,
      });
      await page.setViewportSize({ width: 1440, height: 960 });
      await page.screenshot({
        path: path.join(artifactDir, "phase8-curations-live-desktop.png"),
        fullPage: true,
      });
    }

    await page.goto(`${baseURL}/account`, { waitUntil: "networkidle" });
    await page.getByRole("heading", { name: "좋아요한 상품" }).waitFor();
    await page.locator(".product-ui-liked-products__grid")
      .getByText(selectedVariantTitle, { exact: false })
      .first()
      .waitFor();

    assert.deepEqual(pageErrors, [], `page errors: ${pageErrors.join(" | ")}`);

    console.log(
      "catalog /curations Firefox E2E: PASS (price None, persisted workspace/config/cart, likes profile, managed Add Target, real Shopify, replace/append/hide, responsive Target rail)",
    );
    console.log(JSON.stringify({
      schemaVersion: "vitlane.phase8-live-e2e-safe-evidence.v1",
      metrics: aggregateSafeMetricsEvidence(safeMetricsEvidence),
    }, null, 2));
  } finally {
    await page.unrouteAll({ behavior: "wait" }).catch(() => undefined);
    await context.close();
    await browser.close();
  }
}

// 카드 이미지는 상품 페이지 새 창 링크라 모달을 열지 않는다. 이미지는
// 정사각형(높이=카드 너비)이므로 그 바로 아래 본문 영역을 클릭해
// 모달 hit-area에 닿게 한다.
async function clickCandidateCardBody(card) {
  const box = await card.boundingBox();
  assert.ok(box, "candidate card must be visible to open its modal");
  await card.click({ position: { x: 24, y: box.width + 24 } });
}

async function assertCandidateCardCompactDensity(card, { touch = false } = {}) {
  const metrics = await card.evaluate((node) => {
    const media = node.querySelector(".vt-product-media");
    const body = node.querySelector(".vt-candidate-card__body");
    const evidence = node.querySelector(".vt-candidate-card__evidence");
    const action = node.querySelector(".vt-candidate-card__primary-action .vt-button");
    if (!media || !body || !evidence || !action) {
      throw new Error("Candidate compact-density elements must exist");
    }
    const cardRect = node.getBoundingClientRect();
    const mediaRect = media.getBoundingClientRect();
    const bodyStyle = getComputedStyle(body);
    return {
      cardWidth: cardRect.width,
      mediaWidth: mediaRect.width,
      mediaHeight: mediaRect.height,
      bodyPaddingTop: Number.parseFloat(bodyStyle.paddingTop),
      bodyPaddingBottom: Number.parseFloat(bodyStyle.paddingBottom),
      bodyGap: Number.parseFloat(bodyStyle.rowGap),
      evidenceHeight: evidence.getBoundingClientRect().height,
      actionHeight: action.getBoundingClientRect().height,
    };
  });
  assert.ok(
    metrics.cardWidth >= 223 && metrics.cardWidth <= 257,
    `Candidate width must stay within the compact 224–256px contract: ${metrics.cardWidth}`,
  );
  assert.ok(
    Math.abs(metrics.mediaWidth - metrics.cardWidth) <= 2,
    `Candidate media must fill the card width: ${JSON.stringify(metrics)}`,
  );
  assert.ok(
    Math.abs(metrics.mediaWidth - metrics.mediaHeight) <= 2,
    `Candidate media must remain square: ${JSON.stringify(metrics)}`,
  );
  const expectedPadding = touch
    ? metrics.bodyPaddingTop >= 7 && metrics.bodyPaddingTop <= 9 &&
      metrics.bodyPaddingBottom >= 7 && metrics.bodyPaddingBottom <= 9
    : metrics.bodyPaddingTop >= 11 && metrics.bodyPaddingTop <= 13 &&
      metrics.bodyPaddingBottom >= 11 && metrics.bodyPaddingBottom <= 13;
  assert.ok(
    expectedPadding,
    `Candidate body must use compact ${touch ? "8px" : "12px"} padding: ${JSON.stringify(metrics)}`,
  );
  assert.ok(
    metrics.bodyGap >= 7 && metrics.bodyGap <= 9,
    `Candidate body must use compact 8px gaps: ${JSON.stringify(metrics)}`,
  );
  assert.ok(
    touch
      ? metrics.evidenceHeight === 0
      : metrics.evidenceHeight >= 39 && metrics.evidenceHeight <= 41,
    `Candidate evidence must ${touch ? "collapse" : "reserve about 40px"}: ${JSON.stringify(metrics)}`,
  );
  if (touch) {
    assert.ok(
      metrics.actionHeight >= 43,
      `Candidate touch action must retain a 44px hit target: ${JSON.stringify(metrics)}`,
    );
  } else {
    assert.ok(
      metrics.actionHeight >= 39 && metrics.actionHeight <= 41,
      `Candidate desktop action must use the compact 40px height: ${JSON.stringify(metrics)}`,
    );
  }
}

async function hydrateWhenNotBusy(page, curationID, targetID) {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    const response = await page.request.post(
      `${baseURL}/api/v1/curations/${curationID}/catalog-research/hydrations`,
      { data: { scope: "VISIBLE_TARGET", targetId: targetID } },
    );
    if (response.status() !== 429) return response;
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  return page.request.post(
    `${baseURL}/api/v1/curations/${curationID}/catalog-research/hydrations`,
    { data: { scope: "VISIBLE_TARGET", targetId: targetID } },
  );
}

async function assertCandidateShopifyBadges(target) {
  const entries = target.locator(".catalog-ui-candidate-entry");
  const count = await entries.count();
  assert.ok(count > 0, "Shopify source evidence requires at least one Candidate");
  for (let index = 0; index < count; index += 1) {
    const entry = entries.nth(index);
    const card = entry.locator(":scope > .vt-candidate-card");
    const badge = card.locator('.candidate-source-badge[data-source="SHOPIFY"]');
    assert.equal(
      await badge.count(),
      1,
      "every Candidate must render exactly one Shopify source badge",
    );
    assert.equal(
      await card.evaluate((node) => {
        const title = node.querySelector(".vt-candidate-card__title");
        const source = node.querySelector(".vt-candidate-card__source");
        const price = node.querySelector(".vt-candidate-card__price");
        return title?.nextElementSibling === source &&
          Boolean(source?.compareDocumentPosition(price) & Node.DOCUMENT_POSITION_FOLLOWING);
      }),
      true,
      "Shopify badge must sit below the title and above the price",
    );
    await badge.getByText("Shopify", { exact: true }).waitFor();
    assert.equal(
      await badge.locator("svg").getAttribute("aria-hidden"),
      "true",
      "Shopify badge must retain its brand icon",
    );
    assert.equal(
      await entry.locator(":scope > .catalog-ui-search-platform").count(),
      0,
      "Candidate cards must not render a SearchPlatform block below the card",
    );
  }
}

async function assertResearchTelemetryHidden(target) {
  const text = await target.innerText();
  assert.equal(
    await target.locator(".phase8-target__provider-proof").count(),
    0,
    "Target must not render an internal provider telemetry strip",
  );
  assert.doesNotMatch(text, /SHOPIFY LIVE|\b(?:APPENDED|REPLACED)\b/);
  assert.doesNotMatch(text, /\b\d+\s+calls?\b/i);
  assert.doesNotMatch(text, /\b\d[\d,]*\s*ms\b/i);
}

function recordSafeLiveMetrics(evidence, callType, metrics) {
  assert.ok(metrics && typeof metrics === "object", `${callType} metrics must exist`);
  assert.equal(metrics.aiCallCount, 0, `${callType} must not call AI`);
  assert.ok(
    Number.isInteger(metrics.shopifyCallCount) && metrics.shopifyCallCount >= 1,
    `${callType} must report at least one Shopify call`,
  );
  assert.ok(
    Number.isInteger(metrics.durationMilliseconds) && metrics.durationMilliseconds >= 0,
    `${callType} duration must be a non-negative integer`,
  );
  assert.ok(
    Number.isInteger(metrics.localCallsUsed) && metrics.localCallsUsed >= metrics.shopifyCallCount,
    `${callType} local calls used must account for Shopify calls`,
  );
  assert.ok(
    Number.isInteger(metrics.localCallsRemaining) && metrics.localCallsRemaining >= 0,
    `${callType} local calls remaining must be non-negative`,
  );
  assert.ok(
    Number.isInteger(metrics.localRateLimit) && metrics.localRateLimit >= 1,
    `${callType} local rate limit must be positive`,
  );
  assert.equal(
    metrics.localCallsUsed + metrics.localCallsRemaining,
    metrics.localRateLimit,
    `${callType} rate usage and remaining slots must reconcile to the limit`,
  );
  assert.ok(
    Number.isInteger(metrics.localRateWindowSeconds) && metrics.localRateWindowSeconds >= 1,
    `${callType} local rate window must be positive`,
  );
  assert.equal(metrics.externalEffect, "CATALOG_READ_ONLY");
  assert.equal(
    metrics.providerCostStatus,
    "NOT_REPORTED_BY_PROVIDER",
    `${callType} must not misreport an unknown Shopify provider cost as zero`,
  );
  assert.equal(metrics.providerBillingCredential, false);

  evidence[callType].push({
    aiCallCount: metrics.aiCallCount,
    shopifyCallCount: metrics.shopifyCallCount,
    durationMilliseconds: metrics.durationMilliseconds,
    localCallsUsed: metrics.localCallsUsed,
    localCallsRemaining: metrics.localCallsRemaining,
    localRateLimit: metrics.localRateLimit,
    localRateWindowSeconds: metrics.localRateWindowSeconds,
    externalEffect: metrics.externalEffect,
    providerCostStatus: metrics.providerCostStatus,
  });
}

function aggregateSafeMetricsEvidence(evidence) {
  return Object.fromEntries(Object.entries(evidence).map(([callType, observations]) => {
    assert.ok(observations.length > 0, `${callType} safe metrics evidence must be observed`);
    const rateLimits = new Set(observations.map((metrics) => metrics.localRateLimit));
    const rateWindows = new Set(observations.map((metrics) => metrics.localRateWindowSeconds));
    assert.equal(rateLimits.size, 1, `${callType} rate limit must remain consistent`);
    assert.equal(rateWindows.size, 1, `${callType} rate window must remain consistent`);
    return [callType, {
      responses: observations.length,
      calls: {
        ai: observations.reduce((sum, metrics) => sum + metrics.aiCallCount, 0),
        shopify: observations.reduce((sum, metrics) => sum + metrics.shopifyCallCount, 0),
      },
      durationMilliseconds: observations.reduce(
        (sum, metrics) => sum + metrics.durationMilliseconds,
        0,
      ),
      rate: {
        localCallsUsedMaximum: Math.max(...observations.map((metrics) => metrics.localCallsUsed)),
        localCallsRemainingMinimum: Math.min(
          ...observations.map((metrics) => metrics.localCallsRemaining),
        ),
        localRateLimit: observations[0].localRateLimit,
        localRateWindowSeconds: observations[0].localRateWindowSeconds,
      },
      externalEffect: "CATALOG_READ_ONLY",
      providerCostStatus: "NOT_REPORTED_BY_PROVIDER",
    }];
  }));
}

function escapeRegex(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

run().catch((error) => {
  console.error("catalog /curations Firefox E2E: FAIL");
  console.error(error.stack ?? error);
  process.exitCode = 1;
});
