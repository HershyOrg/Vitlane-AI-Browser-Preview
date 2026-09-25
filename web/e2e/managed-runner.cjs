// ADR-0032 MANAGED path, end to end through the real browser.
//
// This is the replacement for the external-agent E2E: that suite could only
// pass by simulating OAuth and MCP in the harness, so the actual execution
// subject was never exercised. Here the Server owns the workflow, so a single
// user action drives the whole flow and every assertion is about state the
// Server really produced.
const { chromium, firefox } = require("playwright");
const assert = require("node:assert/strict");
const { randomUUID } = require("node:crypto");
const os = require("node:os");
const path = require("node:path");
const { openResults } = require("./support/open-results.cjs");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:8080").replace(
  /\/$/,
  "",
);
const screenshotDir =
  process.env.E2E_SCREENSHOT_DIR ??
  path.join(os.tmpdir(), "vitlane-managed-runner-e2e");
const firefoxPath = process.env.E2E_FIREFOX_PATH;
const browserName = process.env.E2E_BROWSER === "chromium" ? "chromium" : "firefox";
const browserPath = browserName === "chromium"
  ? process.env.E2E_CHROMIUM_PATH
  : firefoxPath;

// The runner polls every 2s and each step is a network call, so allow real
// time rather than making the assertions flaky.
const settleTimeoutMs = Number(process.env.E2E_MANAGED_TIMEOUT_MS ?? 90_000);

async function waitFor(label, predicate) {
  const deadline = Date.now() + settleTimeoutMs;
  let last;
  while (Date.now() < deadline) {
    last = await predicate();
    if (last) return last;
    await new Promise((resolve) => setTimeout(resolve, 1_500));
  }
  throw new Error(`timed out waiting for ${label}`);
}

async function hydrateCatalogResearch(page, curationID, targetID, label) {
  return waitFor(label, async () => {
    const response = await page.request.post(
      `${baseURL}/api/v1/curations/${curationID}/catalog-research/hydrations`,
      { data: { scope: "VISIBLE_TARGET", targetId: targetID } },
    );
    if (response.status() === 429) return null;
    assert.equal(response.status(), 200);
    return response.json();
  });
}

async function run() {
  const browser = await (browserName === "chromium" ? chromium : firefox).launch(
    browserPath ? { executablePath: browserPath } : {},
  );
  const context = await browser.newContext({ locale: "ko-KR" });
  const page = await context.newPage();
  const consoleErrors = [];
  const failedResponses = [];
  const browserCatalogMutations = [];
  const browserProviderReads = [];
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  page.on("response", (response) => {
    if (response.status() >= 400) {
      failedResponses.push(
        `${response.status()} ${response.request().method()} ${new URL(response.url()).pathname}`,
      );
    }
  });
  context.on("request", (request) => {
    const pathname = new URL(request.url()).pathname;
    if (
      request.method() === "POST" &&
      /\/targets\/[^/]+\/catalog-research\/expansions$/.test(pathname)
    ) {
      browserCatalogMutations.push(request.url());
    }
    if (
      /\/catalog-research\/candidates\/[^/]+\/variant-pages$/.test(pathname) ||
      pathname.endsWith("/prepare-agency-order") ||
      /\/targets\/[^/]+\/catalog-research\/expansions$/.test(pathname)
    ) {
      browserProviderReads.push(`${request.method()} ${pathname}`);
    }
  });

  try {
    // A development session stands in for Google login; the managed path is
    // what this suite is about, not authentication.
    const session = await page.request.post(
      `${baseURL}/api/v1/dev/auth/session`,
      { data: {} },
    );
    assert.equal(session.status(), 201, "development session must be created");

    const capabilityResponse = await page.request.get(
      `${baseURL}/api/v1/managed-runner/capability`,
    );
    assert.equal(capabilityResponse.status(), 200);
    const capability = await capabilityResponse.json();
    assert.equal(
      capability.enabled,
      true,
      "MANAGED E2E requires MANAGED_RUNNER_ENABLED",
    );
    assert.ok(
      capability.models.length > 0,
      "capability must offer at least one model",
    );

    await page.goto(`${baseURL}/`, { waitUntil: "networkidle" });

    // Managed is the only owner; Auto and the visible model selector need no legacy toggles.
    assert.equal(await page.locator('.shell-agent-mode__toggle, #external-agent-mode').count(), 0);
    const modelSelect = page.locator('.init-composer__model select');
    await modelSelect.selectOption(capability.defaultModelKey);
    await page.getByRole('button', { name: '조사·보기 설정', exact: true }).click();
    await page.locator('#shipping-country').selectOption('US');
    await page.getByLabel('표시 통화', { exact: true }).selectOption('USD');
    await page.keyboard.press('Escape');
    await page.waitForFunction(() => !document.querySelector('#curation-intent')?.disabled);

    // 4. One user action starts everything.
    await page
      .locator("#curation-intent")
      .fill("a ceramic coffee mug and a stainless steel water bottle");
    await page.locator(".shell-intent-composer__submit").click();
    await page.waitForURL(/\/curations\//, { timeout: 30_000 });
    const curationID = new URL(page.url()).pathname.split("/").pop();
    assert.ok(curationID, "submission must land on a curation");

    // 4a. The page must reach the finished state on its own. Managed work
    //     continues after the submitting action returns and the transition to
    //     CURATING creates new research jobs, so a page that only renders its
    //     first response looks permanently stuck on the planning step. This
    //     assertion deliberately never reloads.
    // The local stub can complete before the browser's first CURATING paint,
    // so a transient working bar is not a reliable completion oracle. The
    // durable Target and the control-plane Job/Step assertions below are.
    // The finished state is the product's result inside the response
    // (ADR-0086); its own section stays folded until the reader opens it.
    await page
      .locator('[data-testid="phase8-curation-surface"] [data-result-target], .curation-response__results [data-result-target]')
      .first()
      .waitFor({ state: "visible", timeout: settleTimeoutMs });

    // 5. The Server drives planning, the transition, and research with no
    //    further user action. This is the whole point of the managed path.
    const workspace = await waitFor("curation to reach CURATING", async () => {
      const response = await page.request.get(
        `${baseURL}/api/v1/curations/${curationID}/workspace`,
      );
      if (response.status() !== 200) return null;
      const body = await response.json();
      return body.curation?.phase === "CURATING" ? body : null;
    });
    assert.equal(workspace.curation.phase, "CURATING");
    assert.ok(
      (workspace.targets ?? []).length >= 1,
      "planning must have produced at least one target",
    );

    // 6. Research finishes on its own. NO_RESULTS is an acceptable outcome:
    //    the catalog may legitimately have nothing, and inventing products
    //    would be the real failure.
    const planID = workspace.plan?.id;
    assert.ok(planID, "workspace must expose its plan");
    const settled = await waitFor("research rounds to settle", async () => {
      const response = await page.request.get(
        `${baseURL}/api/v1/shopping-plans/${planID}`,
      );
      if (response.status() !== 200) return null;
      const body = await response.json();
      const sessions = body.sessions ?? [];
      if (sessions.length === 0) return null;
      // REVIEWING means the round produced a result. RESEARCHING means the
      // runner is still working, which is the state this poll waits out.
      return sessions.every(({ status }) => status === "REVIEWING")
        ? body
        : null;
    });
    assert.ok(settled.sessions.length >= 1);

    // 6a. The same asynchronous worker completion must have populated the
    //     Phase 8 CandidatePool. The browser has not issued Expand or a direct
    //     search command: one intent submission must reach Target + Candidate.
    const research = await page.request.get(
      `${baseURL}/api/v1/curations/${curationID}/workspace`,
    );
    assert.equal(research.status(), 200);
    const workspaceBody = await research.json();
    const groups = workspaceBody.research?.groups ?? [];
    assert.ok(groups.length >= 1, "research must project at least one group");
    for (const group of groups) {
      assert.equal(
        Object.hasOwn(group, "candidates"),
        false,
        "the control-plane projection must not expose Candidate data-plane rows",
      );
      assert.equal(
        Object.hasOwn(group, "submission"),
        false,
        "the control-plane projection must not expose submission data-plane rows",
      );
    }
    // Leave the mounted surface before making a direct diagnostic hydration.
    // Running both at once would make the E2E itself create a second Shopify
    // operation and exercise the BUSY guard instead of the user flow.
    await page.goto(`${baseURL}/`, { waitUntil: "networkidle" });
    const catalogWorkspace = await hydrateCatalogResearch(
      page,
      curationID,
      groups[0].session.planTargetId,
      "initial Phase 8 workspace hydration",
    );
    assert.equal(
      catalogWorkspace.schemaVersion,
      "vitlane.catalog-research-hydration.v3",
    );
    const candidates = (catalogWorkspace.pools ?? []).flatMap(
      ({ products }) => products ?? [],
    );
    assert.ok(
      candidates.length >= 1,
      "one managed request must populate the Phase 8 CandidatePool",
    );
    for (const candidate of candidates) {
      assert.ok(
        candidate.candidateId,
        "Candidate identity must be bound to the durable Phase 8 row",
      );
      assert.equal(candidate.source, "SHOPIFY", "the managed catalog stub exposes Shopify");
      assert.equal(candidate.sourceProductRef?.source, "SHOPIFY");
      assert.ok(candidate.sourceProductRef.productId,
        "the common source reference must retain the provider product identity");
      assert.notEqual(candidate.sourceProductRef.productId, candidate.candidateId,
        "the persisted Candidate ID must not overwrite the provider product identity");
      assert.equal(candidate.purchaseRoute, "VITLANE_CHECKOUT");
      assert.equal(Object.hasOwn(candidate, "providerProductId"), false,
        "the public contract must expose candidateId, not the internal field name");
      assert.equal(
        candidate.hydration?.status,
        "READY",
        "resolved Candidate hydration must be explicit",
      );
      assert.ok(candidate.title, "candidate must carry the fresh observed name");
      assert.ok(
        candidate.locator?.productUrl?.startsWith("https://") ||
          (candidate.locator?.variantId && candidate.locator?.sellerDomain),
        "candidate must carry one server-verified locator",
      );
      assert.equal(
        candidate.currency,
        "USD",
        "a non-USD product must not survive the currency filter",
      );
      assert.ok(
        candidate.intentPoint,
        "managed ranking must contribute an assessment, not product facts",
      );
    }

    // 6b. Ranking is bounded and free of duplicates: an unbounded or repeating
    //     list would mean the offered-observation filter is not doing its job.
    const seen = new Set(
      candidates.map(({ candidateId }) => candidateId),
    );
    assert.equal(
      seen.size,
      candidates.length,
      "the same candidate must not appear twice",
    );
    assert.ok(
      candidates.length <= 50,
      "CandidatePool must respect its hard maximum",
    );

    // 6c. Progress is observable from the workspace projection — ADR-0038
    //     folded it in so the browser polls one endpoint — and carries only
    //     closed labels.
    const jobs = workspaceBody.intelligence ?? [];
    assert.ok(jobs.length >= 1, "the workspace must project its jobs");
    assert.ok(
      jobs.every(({ provider }) => provider === "MANAGED"),
      "every job must name the provider that ran it",
    );
    const steps = jobs.flatMap(({ steps }) => steps ?? []);
    assert.ok(steps.length >= 1, "a finished job must have recorded steps");
    for (const step of steps) {
      assert.ok(
        ["INTERPRETING", "SEARCHING_CATALOG", "RANKING", "SUBMITTING"].includes(
          step.kind,
        ),
        `unexpected progress label ${step.kind}`,
      );
      assert.ok(
        !JSON.stringify(step).includes("mug"),
        "progress must not echo intent text",
      );
    }

    // The lifecycle projection still binds each settled Round to exactly one
    // IntelligenceJob; only the old data-plane submission was removed.
    const initialResearchJobs = [];
    for (const group of groups) {
      const job = jobs.find(
        ({ targetKind, targetId }) =>
          targetKind === "RESEARCH_ROUND" && targetId === group.round?.id,
      );
      assert.ok(job, "each settled Round must retain its IntelligenceJob");
      initialResearchJobs.push(job.jobId);
    }
    assert.equal(
      new Set(initialResearchJobs).size,
      groups.length,
      "each initial Target must have exactly one distinct Research Job",
    );
    assert.deepEqual(
      browserCatalogMutations,
      [],
      "initial Candidates must not require a browser-side Expand command",
    );

    // 6d. CartView is a durable, fallible selection draft. The managed CI
    //     catalog stub deliberately has no Storefront Variant authority; its
    //     observed preview Variant must still remain freely draftable without
    //     inventing a remote Storefront response or making another provider call.
    await page.goto(`${baseURL}/curations/${curationID}`, {
      waitUntil: "networkidle",
    });
    const candidateID = candidates[0].candidateId;
    await page.locator("[data-result-target]").first().waitFor({ timeout: settleTimeoutMs });
    await openResults(page);
    const candidateEntry = page.locator(
      `.catalog-ui-candidate-entry[data-candidate-id="${candidateID}"]`,
    );
    await candidateEntry.waitFor({ timeout: settleTimeoutMs });
    await candidateEntry
      .locator(".vt-candidate-card:not(.is-loading)")
      .waitFor({ timeout: settleTimeoutMs });
    // The product title is intentionally a real outbound link layered over
    // the card action. Exercise the accessible card action itself rather than
    // clicking through that link's hit area.
    await candidateEntry.locator(".vt-candidate-card__hit-area").press("Enter");
    const modal = page.locator(".catalog-ui-candidate-modal");
    await modal.getByRole("heading", { name: "옵션", exact: true }).waitFor();
    const firstVariant = modal.locator(".catalog-ui-variant-row").first();
    await firstVariant.waitFor();
    await firstVariant.click();

    const providerReadsBeforeCart = browserProviderReads.length;
    const cartMutationPromise = page.waitForResponse((response) =>
      new URL(response.url()).pathname.endsWith("/cart") &&
      response.request().method() === "PUT",
    );
    await modal.getByRole("button", { name: "장바구니 담기" }).click();
    const cartMutation = await cartMutationPromise;
    assert.equal(cartMutation.status(), 200, await cartMutation.text());
    assert.equal(
      browserProviderReads.length,
      providerReadsBeforeCart,
      "CartView mutation must issue zero Shopify/provider reads",
    );
    await candidateEntry
      .getByRole("button", { name: "장바구니 제거" })
      .waitFor();

    const reloadPage = await context.newPage();
    try {
      await reloadPage.goto(`${baseURL}/curations/${curationID}`, {
        waitUntil: "networkidle",
      });
      await reloadPage
        .getByRole("button", { name: "장바구니 (1)", exact: true })
        .waitFor({ timeout: settleTimeoutMs });
      await openResults(reloadPage);
      const reloadedEntry = reloadPage.locator(
        `.catalog-ui-candidate-entry[data-candidate-id="${candidateID}"]`,
      );
      await reloadedEntry
        .getByRole("button", { name: "장바구니 제거" })
        .waitFor();
    } finally {
      await reloadPage.close();
    }

    // The remaining assertions drive the control plane through API commands.
    // Unmount the Curation view so its automatic display hydration does not
    // race the explicit evidence reads below.
    await page.goto(`${baseURL}/`, { waitUntil: "networkidle" });

    // 6e. Natural-language Research Again remains asynchronous: it creates a
    //     new Round/Job and appends new identities only after the Job succeeds.
    const feedbackGroup = groups[0];
    const feedbackPool = (catalogWorkspace.pools ?? []).find(
      ({ targetId }) => targetId === feedbackGroup.session.planTargetId,
    );
    assert.ok(feedbackPool, "the feedback Target must have an initial pool");
    const criteriaResponse = await page.request.get(`${baseURL}/api/v1/curations/${curationID}/targets/${feedbackGroup.session.planTargetId}/criteria`);
    assert.equal(criteriaResponse.status(), 200);
    const feedbackCriteria = await criteriaResponse.json();
    const frozenAssessments = new Map(feedbackPool.products.map(p => [p.candidateId, p.axisAssessment]));
    const feedbackActionID = randomUUID();
    const researchAgain = await page.request.post(
      `${baseURL}/api/v1/shopping-sessions/${feedbackGroup.session.id}/research-again`,
      {
        headers: { "Idempotency-Key": feedbackActionID },
        data: {
          curationId: curationID,
          targetId: feedbackGroup.session.planTargetId,
          curationActionId: feedbackActionID,
          schemaVersion: "vitlane.research-again.v2",
          expectedCriteriaVersion: feedbackCriteria?.version ?? 0,
          feedback: "find a lighter alternative with a taller back",
          expectedCurationVersion: workspaceBody.curation.version,
          expectedSessionVersion: feedbackGroup.session.version,
        },
      },
    );
    if (researchAgain.status() !== 202) {
      throw new Error(
        `feedback re-research must start: ${researchAgain.status()} ${await researchAgain.text()}`,
      );
    }
    const researchAgainBody = await researchAgain.json();
    assert.equal(researchAgainBody.schemaVersion, 'vitlane.curation-thread.v2');
    await waitFor('feedback request thread to finish', async()=>{
      const r=await page.request.get(`${baseURL}/api/v1/curations/${curationID}/threads`);
      const t=(await r.json()).threads.find(t=>t.id===researchAgainBody.id);
      if(t?.status==='FAILED')throw Error(JSON.stringify(t));
      return t?.status==='SUCCEEDED'?t:null;
    });
    const afterReresearch = await waitFor('feedback re-research job and thread to settle', async()=>{
      const r=await page.request.get(`${baseURL}/api/v1/curations/${curationID}/workspace`);
      const w=await r.json(); const group=w.research?.groups.find(g=>g.session.id===feedbackGroup.session.id);
      const job=w.intelligence?.find(j=>j.targetKind==='RESEARCH_ROUND' && j.targetId===group?.round?.id);
      return group?.round?.id!==feedbackGroup.round?.id && job?.status==='SUCCEEDED' && !w.activeWork?w:null;
    });
    const feedbackCatalog = await hydrateCatalogResearch(
      page,
      curationID,
      feedbackGroup.session.planTargetId,
      "feedback Phase 8 workspace hydration",
    );
    const appendedPool = (feedbackCatalog.pools ?? []).find(
      ({ targetId }) => targetId === feedbackGroup.session.planTargetId,
    );
    assert.ok(appendedPool.version > feedbackPool.version);
    assert.equal(appendedPool.latestMode, "APPEND");
    for (const [id, assessment] of frozenAssessments) assert.deepEqual(appendedPool.products.find(p => p.candidateId === id)?.axisAssessment, assessment);
    assert.ok(appendedPool.products.length >= 1);

    // 6f. Adding a Target while already curating must run planning and research
    //     back to back, exactly as the first pass did. Without the continuation
    //     the new Target sits READY forever, waiting for an action the managed
    //     path is supposed to have removed.
    const beforeSessions = settled.sessions.length;
    const addActionID = randomUUID();
    const addAction = await page.request.post(
      `${baseURL}/api/v1/shopping-plans/${planID}/expansions`,
      {
        headers: { "Idempotency-Key": addActionID },
        data: {
          curationId: curationID,
          curationActionId: addActionID,
          type: "CURATION_ADD_TARGETS",
          instruction: "insulated lunch box",
          expectedCurationVersion: afterReresearch.curation.version,
        },
      },
    );
    if (addAction.status() >= 200 && addAction.status() < 300) {
      const grown = await waitFor("the added Target to be researched", async () => {
        const response = await page.request.get(
          `${baseURL}/api/v1/shopping-plans/${planID}`,
        );
        if (response.status() !== 200) return null;
        const body = await response.json();
        const sessions = body.sessions ?? [];
        if (sessions.length <= beforeSessions) return null;
        // Every session settled means the new one was researched too, not just
        // created. A READY session here is the exact regression this guards.
        return sessions.every(({ status }) => status === "REVIEWING")
          ? body
          : null;
      });
      assert.ok(
        grown.sessions.length > beforeSessions,
        "adding a Target must create a session",
      );
      for (const session of grown.sessions) {
        assert.notEqual(
          session.status,
          "READY",
          "an added Target must not be left waiting for research",
        );
      }
      const grownWorkspaceResponse = await page.request.get(
        `${baseURL}/api/v1/curations/${curationID}/workspace`,
      );
      assert.equal(grownWorkspaceResponse.status(), 200);
      const grownWorkspace = await grownWorkspaceResponse.json();
      const addedSession = grown.sessions.find(
        ({ id }) => !(settled.sessions ?? []).some((before) => before.id === id),
      );
      assert.ok(addedSession, "the new Target must own a new session");
      const addedGroup = (grownWorkspace.research?.groups ?? []).find(
        ({ session }) => session.id === addedSession.id,
      );
      const addedJobs = (grownWorkspace.intelligence ?? []).filter(
        ({ targetKind, targetId }) =>
          targetKind === "RESEARCH_ROUND" && targetId === addedGroup?.round?.id,
      );
      assert.equal(
        addedJobs.length,
        1,
        "Add Target continuation must create exactly one Research Job",
      );
      const grownCatalog = await hydrateCatalogResearch(
        page,
        curationID,
        addedSession.planTargetId,
        "added Target Phase 8 workspace hydration",
      );
      const addedPool = (grownCatalog.pools ?? []).find(
        ({ targetId }) => targetId === addedSession.planTargetId,
      );
      assert.ok(
        addedPool?.products?.length >= 1,
        "Add Target must reach CandidatePool without a second user action",
      );
      console.log(
        `  sessions after add: ${grown.sessions.length} (was ${beforeSessions})`,
      );
    } else {
      throw new Error(
        `CURATION_ADD_TARGETS returned ${addAction.status()}`,
      );
    }

    // 7. No Codex affordance anywhere on the managed path.
    const bodyText = (await page.locator("body").innerText()).toString();
    assert.ok(
      !bodyText.includes("Codex"),
      "the managed path must not mention Codex",
    );
    for (const forbidden of ["OPEN_CODEX", "REASSIGN"]) {
      assert.equal(
        await page.locator(`text=${forbidden}`).count(),
        0,
        `${forbidden} is not an action a managed user can take`,
      );
    }

    // 8. Usage is attributed to the user and every reservation is released.
    const usageResponse = await page.request.get(
      `${baseURL}/api/v1/managed-runner/usage`,
    );
    assert.equal(usageResponse.status(), 200);
    const usage = await usageResponse.json();
    assert.equal(usage.enabled, true);
    assert.ok(
      usage.userSpentMicros > 0,
      "running managed work must record spend against the user",
    );
    assert.ok(
      usage.userSpentMicros < usage.userLimitMicros,
      "one curation must not exhaust the daily cap",
    );

    await page.screenshot({
      path: path.join(screenshotDir, "managed-runner-curating.png"),
      fullPage: true,
    });

    assert.deepEqual(
      consoleErrors,
      [],
      `no console errors during the flow; failed responses: ${failedResponses.join(", ")}`,
    );
    console.log("managed-runner E2E: PASS");
    console.log(`  curation ${curationID} reached CURATING`);
    console.log(`  targets: ${(workspace.targets ?? []).length}`);
    console.log(`  sessions settled: ${settled.sessions.length}`);
    console.log(`  candidates: ${candidates.length}`);
    console.log("  cart: 1 durable draft restored after reload; mutation Shopify calls 0");
    console.log(`  user spend: ${usage.userSpentMicros} micros`);
  } finally {
    await context.close();
    await browser.close();
  }
}

run().catch((error) => {
  console.error("managed-runner E2E: FAIL");
  console.error(error);
  process.exitCode = 1;
});
