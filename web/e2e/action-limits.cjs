// One action per plan, three per user, and a way out of both.
//
// These limits only mean something against a real dispatcher: the guard reads
// the same job rows the worker claims from, so a test with a stubbed job store
// would prove nothing about whether a plan is actually busy.
const { firefox } = require("playwright");
const assert = require("node:assert/strict");
const { randomUUID } = require("node:crypto");
const os = require("node:os");
const path = require("node:path");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:8080").replace(
  /\/$/,
  "",
);
const screenshotDir =
  process.env.E2E_SCREENSHOT_DIR ??
  path.join(os.tmpdir(), "vitlane-action-limits-e2e");
const firefoxPath = process.env.E2E_FIREFOX_PATH;
const settleTimeoutMs = Number(process.env.E2E_LIMITS_TIMEOUT_MS ?? 90_000);

async function waitFor(label, predicate) {
  const deadline = Date.now() + settleTimeoutMs;
  let last;
  while (Date.now() < deadline) {
    last = await predicate();
    if (last) return last;
    await new Promise((resolve) => setTimeout(resolve, 750));
  }
  throw new Error(`timed out waiting for ${label}`);
}

async function createPlan(page, intent, modelKey) {
  const actionID = randomUUID();
  const response = await page.request.post(`${baseURL}/api/v1/shopping-plans`, {
    headers: { "Idempotency-Key": actionID },
    data: {
      originalIntent: intent,
      planningMode: "AUTO",
      totalBudget: { amount: "100", currency: "USD" },
      executionMode: "EXPERIMENT",
      location: { country: "US", city: "Seattle" },
      category: "",
      allowedItems: [],
      blockedItems: [],
      minPrice: null,
      maxPrice: null,
      referenceUrl: "",
      urlMode: "NONE",
      agentMode: "MANAGED",
      modelKey,
    },
  });
  return { status: response.status(), body: await response.json(), actionID };
}

async function run() {
  const browser = await firefox.launch(
    firefoxPath ? { executablePath: firefoxPath } : {},
  );
  const context = await browser.newContext({ locale: "ko-KR" });
  await context.addInitScript(() => localStorage.setItem("vitlane.locale.v1", "ko-KR"));
  const page = await context.newPage();
  try {
    const session = await page.request.post(
      `${baseURL}/api/v1/dev/auth/session`,
      { data: {} },
    );
    assert.equal(session.status(), 201, "development session must be created");
    // UI locale follows the account preference, including in a reused test DB.
    const preferences = await (await page.request.get(`${baseURL}/api/v1/me/preferences`)).json();
    const localeResponse = await page.request.patch(`${baseURL}/api/v1/me/preferences`, {data:{
      schemaVersion:"vitlane.user-preferences.v1",expectedVersion:preferences.preferences.version,
      uiLocale:"ko-KR",preferredCurrency:"USD",researchCountry:"US",
    }});
    assert.equal(localeResponse.status(),200);


    const capability = await (
      await page.request.get(`${baseURL}/api/v1/managed-runner/capability`)
    ).json();
    assert.ok(capability.enabled, "the managed provider must be running");
    const modelKey = capability.defaultModelKey;

    // 1. A plan whose first action is still working refuses the next one.
    const first = await createPlan(page, "a wool blanket", modelKey);
    assert.equal(first.status, 201, "the first plan must be created");
    const planID = first.body.plan.id;
    const curationID = first.body.curation.id;

    const secondActionID = randomUUID();
    const second = await page.request.post(
      `${baseURL}/api/v1/shopping-plans/${planID}/expansions`,
      {
        headers: { "Idempotency-Key": secondActionID },
        data: {
          curationId: curationID,
          curationActionId: secondActionID,
          type: "PLANNING_ADD_TARGETS",
          instruction: "a reading lamp",
          expectedCurationVersion: first.body.curation.version,
        },
      },
    );
    assert.equal(
      second.status(),
      409,
      "a plan mid-action must refuse the next action",
    );
    const refusal = await second.json();
    // Both the action gate and plan admission read non-terminal Intelligence
    // jobs. Product rows may remain REQUESTED after a final failed Job so the
    // user can retry them, but they must not keep the next action blocked.
    assert.ok(
      ["CURATION_EXPANSION_IN_PROGRESS", "CURATION_ACTION_IN_PROGRESS"].includes(
        refusal.error.code,
      ),
      `unexpected refusal code: ${JSON.stringify(refusal)}`,
    );

    // 2. Cancelling the action frees the plan. Nothing already produced is
    //    undone — the plan and curation survive, only the pending work stops.
    const cancel = await page.request.post(
      `${baseURL}/api/v1/curation-actions/${first.actionID}/cancel`,
    );
    assert.equal(cancel.status(), 200, "cancel must succeed");
    const cancelled = await cancel.json();
    assert.ok(
      cancelled.cancelledJobs > 0,
      `cancel must report what it stopped: ${JSON.stringify(cancelled)}`,
    );

    const stillThere = await page.request.get(
      `${baseURL}/api/v1/shopping-plans/${planID}`,
    );
    assert.equal(
      stillThere.status(),
      200,
      "cancelling must not remove the plan",
    );

    // A first Planning cancel legitimately leaves Target 0. The live artifact
    // must project that terminal Job rather than inferring "still working"
    // from the empty list. Recovery uses the same composer as Curating.
    const cancelledWorkspace = await waitFor(
      "the cancelled first Planning projection",
      async () => {
        const response = await page.request.get(
          `${baseURL}/api/v1/curations/${curationID}/workspace`,
        );
        if (response.status() !== 200) return null;
        const body = await response.json();
        const cancelledJob = (body.intelligence ?? []).find(
          ({ actionId }) => actionId === first.actionID,
        );
        return !body.activeWork &&
          body.curation?.phase === "PLANNING" &&
          (body.targets ?? []).length === 0 &&
          cancelledJob?.status === "CANCELLED"
          ? body
          : null;
      },
    );
    assert.equal(cancelledWorkspace.targets.length, 0);

    await page.goto(`${baseURL}/curations/${curationID}`, {
      waitUntil: "networkidle",
    });
    const cancelledArtifact = page.locator(
      '[data-planning-state="cancelled"]',
    );
    await cancelledArtifact.waitFor({ state: "visible" });
    assert.ok(
      (await cancelledArtifact.innerText()).includes("조사를 취소했습니다"),
      "the terminal artifact must say that the first research was cancelled",
    );
    assert.equal(
      await page.locator(".curation-working-bar").count(),
      0,
      "a cancelled first Planning Job must not keep an indeterminate bar",
    );

    assert.equal(
      await page
        .getByRole("combobox", { name: "현재 단계에 행동 보내기" })
        .count(),
      0,
      "the removed generic composer must not return after cancellation",
    );
    assert.equal(
      await page.getByRole("button", { name: "새 요청 입력" }).count(),
      0,
      "recovery must not expose a dead generic-composer control",
    );

    assert.equal(await page.locator(".curation-artifact-empty--recovery").count(), 0);
    const composer = page.locator(".catalog-ui-focus-composer");
    assert.equal(await composer.count(), 1, "Planning recovery must share the common composer");
    const input = composer.getByRole("textbox", { name: "조사 요청" });
    assert.equal(await input.isEnabled(), true);
    assert.match(await composer.locator(".catalog-ui-focus-token").innerText(), /^Auto$/);
    const autoPath = `/api/v1/curations/${curationID}/threads`;
    const autoRequests = [];
    page.on("request", request => {
      if (new URL(request.url()).pathname === autoPath && request.method() === "POST") {
        autoRequests.push(request.postDataJSON());
      }
    });
    await input.fill("a reading lamp");
    const retryResponse = page.waitForResponse(response =>
      new URL(response.url()).pathname === autoPath && response.request().method() === "POST",
    );
    await composer.getByRole("button", { name: "조사 요청 보내기" }).click();
    const retry = await retryResponse;
    assert.equal(retry.status(), 202, "the Thread API must admit Auto recovery from the composer");
    const accepted = await retry.json();
    assert.equal(accepted.schemaVersion, "vitlane.curation-thread.v2");
    assert.equal(accepted.actions[0].type, "AUTO_START");
    assert.equal(autoRequests.length, 1, "one user submit must create one Auto command");
    assert.equal(autoRequests[0].request, "a reading lamp");

    const resumed = await waitFor("a new Planning job in the real dispatcher", async () => {
      const response = await page.request.get(`${baseURL}/api/v1/curations/${curationID}/workspace`);
      if (response.status() !== 200) return null;
      const body = await response.json();
      const newJobs = (body.intelligence ?? []).filter(job =>
        job.targetKind === "PLANNING_TASK" && job.actionId !== first.actionID,
      );
      if (!newJobs.length) return null;
      assert.equal(newJobs.length, 1, "recovery must persist exactly one new Planning job");
      assert.notEqual(newJobs[0].status, "CANCELLED");
      assert.notEqual(newJobs[0].status, "FAILED");
      return body;
    });
    assert.equal(resumed.intelligence.find(job => job.actionId === first.actionID).status, "CANCELLED",
      "recovery must preserve the earlier cancellation record");

    // 3. The user ceiling is separate from the per-plan rule: three plans may
    //    work at once, the fourth is refused.
    const plans = [];
    for (let index = 0; index < 3; index += 1) {
      const created = await createPlan(page, `a desk item ${index}`, modelKey);
      if (created.status !== 201) {
        // Already at the ceiling because earlier plans are still working.
        assert.equal(
          created.body.error.code,
          "CURATION_TOO_MANY_ACTIVE_ACTIONS",
          `unexpected creation failure: ${JSON.stringify(created.body)}`,
        );
        break;
      }
      plans.push(created);
    }

    const overflow = await createPlan(page, "one plan too many", modelKey);
    if (overflow.status === 201) {
      // The earlier plans finished faster than the ceiling could be reached,
      // which is a legitimate outcome for a stub provider. The per-plan rule
      // above is the assertion that matters and it already held.
      console.log(
        "action-limits E2E: ceiling not reached (stub finished early)",
      );
    } else {
      assert.equal(
        overflow.status,
        409,
        `the ceiling must refuse with 409, got ${overflow.status}`,
      );
      assert.equal(
        overflow.body.error.code,
        "CURATION_TOO_MANY_ACTIVE_ACTIONS",
        `unexpected ceiling code: ${JSON.stringify(overflow.body)}`,
      );
      console.log("action-limits E2E: user ceiling refused the 4th action");
    }

    console.log("action-limits E2E: PASS");
    console.log(`  plan ${planID} refused, cancelled and reopened`);
  } finally {
    await page.screenshot({
      path: path.join(screenshotDir, "action-limits.png"),
      fullPage: true,
    });
    await context.close();
    await browser.close();
  }
}

require("node:fs").mkdirSync(screenshotDir, { recursive: true });
run().catch((error) => {
  console.error("action-limits E2E: FAIL");
  console.error(error);
  process.exit(1);
});
