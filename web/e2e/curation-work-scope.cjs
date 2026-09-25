// One route serves every curation, so the workspace page is reused when only
// `:curationId` changes and every piece of its state survives the move. A held
// agent work reference then rendered its recovery panel over curations it had
// nothing to do with, so this suite proves a settled curation never shows a
// foreign work panel.
//
// ADR-0038 retired the external Agent beta, so the second half of this suite
// pins the retirement contract itself: a new EXTERNAL plan is rejected with
// 409 EXTERNAL_AGENT_RETIRED and creates nothing. The old cancel-follows-you
// journey needed an open EXTERNAL curation, which can no longer be created by
// design; the panel data source itself moves to the workspace projection in
// the Intelligence workstream.
const { firefox } = require("playwright");
const assert = require("node:assert/strict");
const { randomUUID } = require("node:crypto");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:8080").replace(
  /\/$/,
  "",
);
const settleTimeoutMs = Number(process.env.E2E_SCOPE_TIMEOUT_MS ?? 120_000);

async function waitFor(label, predicate) {
  const deadline = Date.now() + settleTimeoutMs;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 1_000));
  }
  throw new Error(`timed out waiting for ${label}`);
}

async function createCuration(request, intent, modelKey) {
  const response = await request.post(`${baseURL}/api/v1/shopping-plans`, {
    headers: { "Idempotency-Key": randomUUID() },
    data: {
      originalIntent: intent,
      planningMode: "SINGLE",
      totalBudget: { amount: "100", currency: "USD" },
      executionMode: "EXPERIMENT",
      location: { country: "US", city: "Seattle" },
      urlMode: "NONE",
      agentMode: "MANAGED",
      modelKey,
    },
  });
  assert.equal(response.status(), 201, await response.text());
  return (await response.json()).curation.id;
}

async function activeWorkOf(request, curationID) {
  const response = await request.get(
    `${baseURL}/api/v1/curations/${curationID}/workspace`,
  );
  assert.equal(response.status(), 200);
  return (await response.json()).activeWork ?? null;
}

// A request Thread runs product planning and research as separate Actions, so
// there is a moment between them where no Job is active yet the curation is
// still working. Waiting on `activeWork` alone can settle in that gap and then
// watch research start underneath the assertions below.
async function isSettled(request, curationID) {
  if ((await activeWorkOf(request, curationID)) !== null) return false;
  const response = await request.get(
    `${baseURL}/api/v1/curations/${curationID}/threads`,
  );
  assert.equal(response.status(), 200);
  return ((await response.json()).threads ?? []).every(
    (thread) =>
      !["INTERPRETING", "WAITING_SELECTION", "RUNNING"].includes(thread.status),
  );
}

async function run() {
  const browser = await firefox.launch();
  const context = await browser.newContext({ locale: "ko-KR" });
  const page = await context.newPage();

  try {
    const session = await page.request.post(
      `${baseURL}/api/v1/dev/auth/session`,
      { data: {} },
    );
    assert.equal(session.status(), 201, "development session must be created");

    // Runs to completion on the Server, so it ends with no work of its own and
    // nothing to overwrite a leaked reference.
    const settledCuration = await createCuration(
      page.request,
      "내부 러너 큐레이션: 휴대용 충전기를 찾아줘",
      "gpt-5-nano",
    );
    // A second managed curation exercises the same route with different state
    // so the in-app navigation below actually swaps `:curationId`.
    const secondCuration = await createCuration(
      page.request,
      "내부 러너 큐레이션 2: 사무용 의자를 찾아줘",
      "gpt-5-nano",
    );

    // The retired external path must reject new work without creating rows.
    const retired = await page.request.post(
      `${baseURL}/api/v1/shopping-plans`,
      {
        headers: { "Idempotency-Key": randomUUID() },
        data: {
          originalIntent: "외부 Agent 큐레이션: 사무용 의자를 찾아줘",
          planningMode: "SINGLE",
          totalBudget: { amount: "100", currency: "USD" },
          executionMode: "EXPERIMENT",
          location: { country: "US", city: "Seattle" },
          urlMode: "NONE",
          agentMode: "EXTERNAL",
        },
      },
    );
    assert.equal(retired.status(), 409, await retired.text());
    const retiredBody = await retired.json();
    assert.equal(retiredBody.error?.code, "EXTERNAL_AGENT_RETIRED");

    await waitFor("both managed curations to settle", async () =>
      (await isSettled(page.request, settledCuration)) &&
      (await isSettled(page.request, secondCuration)),
    );

    await page.goto(`${baseURL}/curations/${secondCuration}`, {
      waitUntil: "networkidle",
    });
    const panel = page.locator(".curation-agent-work");

    // In-app navigation is the move that used to carry the panel across. A
    // reload would remount the page and hide the defect.
    await page
      .getByRole("link", { name: /내부 러너 큐레이션:/ })
      .first()
      .click();
    await page.waitForURL(`**/curations/${settledCuration}`);
    // The destination renders its loading state first, which hides the panel
    // slot entirely. Asserting then would pass without proving anything, so
    // wait until the destination curation's first user Bubble is on screen.
    // Thread reports sit in the conversation; product headings may repeat the
    // request, so select the actual user message.
    await page
      .locator(".curation-thread-report .curation-thread__request p")
      .filter({ hasText: /^내부 러너 큐레이션: 휴대용 충전기를 찾아줘$/ })
      .waitFor({ state: "visible" });
    await page.waitForLoadState("networkidle");

    const leaked = (await panel.allInnerTexts()).join(" ");
    assert.ok(
      !leaked.includes("요청을 취소했습니다"),
      `another curation's state must not appear here: ${leaked.slice(0, 120)}`,
    );
    assert.equal(
      await activeWorkOf(page.request, settledCuration),
      null,
      "the settled curation must still report no active work",
    );

    console.log("curation-work-scope E2E: PASS");
    console.log("  EXTERNAL plan creation rejected with EXTERNAL_AGENT_RETIRED");
    console.log(`  settled ${settledCuration} showed no foreign panel`);
  } finally {
    await context.close();
    await browser.close();
  }
}

run().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
