const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { firefox } = require("playwright");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:18080").replace(/\/$/, "");
const rpcURL = (process.env.E2E_RPC_URL ?? "http://127.0.0.1:8545").replace(/\/$/, "");
const artifactDir = process.env.E2E_ARTIFACT_DIR ?? process.env.E2E_SCREENSHOT_DIR;
const timeoutMs = Number(process.env.E2E_AGENCY_ORDER_TIMEOUT_MS ?? 180_000);

async function waitFor(label, predicate, timeout = timeoutMs) {
  const deadline = Date.now() + timeout;
  let last;
  while (Date.now() < deadline) {
    last = await predicate();
    if (last) return last;
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw new Error(`timed out waiting for ${label}; last=${JSON.stringify(last)}`);
}

// 카드 이미지는 상품 페이지 새 창 링크라 모달을 열지 않는다. 이미지는
// 정사각형(높이=카드 너비)이므로 그 바로 아래 본문 영역을 클릭해
// 모달 hit-area에 닿게 한다.
async function clickCandidateCardBody(card) {
  const box = await card.boundingBox();
  assert.ok(box, "candidate card must be visible to open its modal");
  await card.click({ position: { x: 24, y: box.width + 24 } });
}

async function run() {
  const { createPublicClient, http, parseAbi } = await import("viem");
  const chain = createPublicClient({ transport: http(rpcURL) });
  const tokenABI = parseAbi([
    "function allowance(address owner, address spender) view returns (uint256)",
  ]);
  const browser = await firefox.launch({ headless: true });
  const context = await browser.newContext({
    locale: "ko-KR",
    viewport: { width: 1440, height: 960 },
  });
  let operatorContext;
  let operator;
  let operatorTaskID;
  const operatorPageErrors = [];
  const page = await context.newPage();
  const agencyCalls = [];
  const pageErrors = [];
  const expansionCalls = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("request", (request) => {
    const pathname = new URL(request.url()).pathname;
    if (/order-sheets|agencyOrder/.test(pathname)) {
      agencyCalls.push(`${request.method()} ${pathname}`);
    }
    if (/\/targets\/[^/]+\/catalog-research\/expansions$/.test(pathname)) {
      expansionCalls.push(`${request.method()} ${pathname}`);
    }
  });
  page.on("response", (response) => {
    const pathname = new URL(response.url()).pathname;
    if (/order-sheets|agencyOrder/.test(pathname)) {
      agencyCalls.push(`${response.status()} ${response.request().method()} ${pathname}`);
    }
  });
  // 지원 대화 발신 실패는 위젯 폴이 에러 문구를 곧 덮어써 화면에 남지 않는다 —
  // 응답 원문을 진단 로그로 보존한다.
  const supportCalls = [];
  page.on("requestfailed", (request) => {
    const pathname = new URL(request.url()).pathname;
    if (!pathname.startsWith("/api/v1/support/")) return;
    supportCalls.push(
      `REQUEST-FAILED ${request.method()} ${pathname} ${request.failure()?.errorText ?? ""}`,
    );
  });
  page.on("response", (response) => {
    const pathname = new URL(response.url()).pathname;
    if (!pathname.startsWith("/api/v1/support/")) return;
    const method = response.request().method();
    if (method === "GET" && response.status() === 200) return;
    response.text().then(
      (body) => supportCalls.push(
        `${response.status()} ${method} ${pathname} ${body.slice(0, 300)}`,
      ),
      () => supportCalls.push(`${response.status()} ${method} ${pathname}`),
    );
  });
  let capabilityBootstrapFailures = 0;
  let cartBootstrapFailures = 0;
  await page.route("**/api/v1/auth/capabilities", async (route) => {
    if (capabilityBootstrapFailures === 0) {
      capabilityBootstrapFailures += 1;
      await route.abort("connectionfailed");
      return;
    }
    await route.continue();
  });
  await page.route("**/api/v1/curations/*/cart", async (route) => {
    if (
      route.request().method() === "GET" &&
      cartBootstrapFailures === 0
    ) {
      cartBootstrapFailures += 1;
      await route.abort("connectionfailed");
      return;
    }
    await route.continue();
  });

  try {
    const session = await page.request.post(`${baseURL}/api/v1/dev/auth/session`, {
      data: { profileKey: "empty-user" },
    });
    assert.equal(session.status(), 201, await session.text());
    await page.goto(`${baseURL}/account`, { waitUntil: "networkidle" });
    await page.getByLabel("배송지 이름").fill("AgencyOrder E2E");
    await page.getByLabel("수령인").fill("Test Buyer");
    await page.getByLabel("주소 1").fill("123 Test Street");
    await page.getByLabel("도시").fill("Seattle");
    await page.getByLabel("주/도").fill("WA");
    await page.getByLabel("우편번호").fill("98101");
    await page.getByLabel("국가 코드").fill("US");
    await page.getByLabel("전화번호").fill("+12065550100");
    await page.getByRole("button", { name: "배송지 저장" }).click();
    await page.getByText("AgencyOrder E2E · 기본", { exact: true }).waitFor({ timeout: 30_000 });
    const accountBeforeOrderResponse = await page.request.get(`${baseURL}/api/v1/account/overview`);
    assert.equal(accountBeforeOrderResponse.status(), 200, await accountBeforeOrderResponse.text());
    const accountDefaultShippingProfileID = (await accountBeforeOrderResponse.json())
      .account.shippingProfiles.find(({ isDefault }) => isDefault)?.id;
    assert.ok(accountDefaultShippingProfileID);

    // One browser action must drive Managed planning and initial Candidate
    // research. No Expand request is allowed to repair an empty first pool.
    await page.goto(`${baseURL}/`, { waitUntil: "networkidle" });
    assert.equal(
      await page.evaluate(() => Boolean(window.__vitlaneReviewWallet && window.ethereum)),
      true,
      "local review wallet must recover from one capabilities bootstrap failure",
    );
    // This existing Shopify/AgencyOrder journey explicitly selects its US market.
    await page.getByRole('button', { name: '조사·보기 설정', exact: true }).click();
    await page.locator("#shipping-country").selectOption("US");
    await page.getByLabel('표시 통화', { exact: true }).selectOption('USD');
    await page.keyboard.press('Escape');
    await page.waitForFunction(() => !document.querySelector('#curation-intent')?.disabled);
    await page.locator("#curation-intent").fill("one US domestic camping chair");
    await page.locator(".shell-intent-composer__submit").click();
    await page.waitForURL(/\/curations\/[0-9a-f-]+$/i, { timeout: 30_000 });
    const curationID = new URL(page.url()).pathname.split("/").pop();
    assert.ok(curationID);
    // The response shows one representative for the product (ADR-0086). The
    // product group's own surface, with every Candidate card, is a sheet.
    const result = page.locator("[data-result-target]").first();
    await result.locator(".curation-result__title").waitFor({ timeout: timeoutMs });
    assert.equal(await page.locator(".curation-target-sheet").count(), 0, "the conversation holds no board");
    await result.locator(".curation-result__more").click();
    const card = page.locator(".curation-target-sheet .vt-candidate-card").first();
    await card.waitFor({ timeout: timeoutMs });
    assert.deepEqual(expansionCalls, [], "initial Candidate must not require Expand");

    const workspaceResponse = await page.request.get(
      `${baseURL}/api/v1/curations/${curationID}/workspace`,
    );
    assert.equal(workspaceResponse.status(), 200, await workspaceResponse.text());
    const workspace = await workspaceResponse.json();
    assert.equal(workspace.curation.phase, "CURATING");
    assert.ok((workspace.targets ?? []).length >= 1);
    assert.ok((workspace.intelligence ?? []).some(
      ({ targetKind, status }) => targetKind === "RESEARCH_ROUND" && status === "SUCCEEDED",
    ));

    await clickCandidateCardBody(card);
    const candidateModal = page.locator(".catalog-ui-candidate-modal");
    await candidateModal.locator(".catalog-ui-variant-row").first().waitFor();
    await candidateModal.getByRole("button", { name: "장바구니 담기" }).click();
    assert.equal(capabilityBootstrapFailures, 1);
    assert.equal(cartBootstrapFailures, 1);
    // Adding to the cart returns to the sheet; the cart itself is in the composer behind it.
    await candidateModal.waitFor({ state: "detached" });
    await page.locator(".curation-target-sheet").getByRole("button", { name: "닫기", exact: true }).click();
    await page.locator(".curation-target-sheet").waitFor({ state: "detached" });
    assert.equal(
      await result.locator(".curation-result__why").innerText(), "담은 상품",
      "the carted product stands for its product group in the conversation",
    );
    await page.getByRole("button", { name: "장바구니 (1)", exact: true }).click();
    await page.getByRole("button", { name: "주문하기" }).waitFor();
    await page.getByRole("button", { name: "주문하기" }).click();
    await page.waitForURL(/\/curations\/[^/]+\/order-sheet\?cartVersion=\d+$/);
    await page.getByRole("heading", { name: "주문 내용을 마지막으로 확인해 주세요." }).waitFor();

    // The account profile is a one-way form default. OrderSheet owns the
    // exact shipping address and must not mutate Account unless explicitly
    // opted in.
    await page.getByLabel("주소 1").waitFor();
    assert.equal(await page.getByLabel("주소 1").inputValue(), "123 Test Street");
    await page.getByLabel("주소 2").fill("Suite Order Only");
    assert.equal(await page.getByRole("checkbox", {
      name: "이 배송지를 계정의 기본 배송지로도 저장",
    }).getAttribute("data-state"), "unchecked");
    await page.getByRole("button", { name: "이 배송지로 배송 옵션 확인" }).click();
    await page.getByRole("button", { name: "주문하기", exact: true }).waitFor();
    const revealAccountDefault = await page.request.post(
      `${baseURL}/api/v1/account/shipping-profiles/${accountDefaultShippingProfileID}/reveal`,
      { data: {} },
    );
    assert.equal(revealAccountDefault.status(), 200, await revealAccountDefault.text());
    assert.equal((await revealAccountDefault.json()).address.addressLine2 ?? "", "");

    // ProcurementAuthorization is part of order issuance now. The browser
    // journey must exercise the same explicit customer consent as production;
    // otherwise every payment-rail CTA correctly remains disabled.
    await page.getByRole("checkbox", {
      name: "표시된 금액과 조건 범위에서 Vitlane 운영자가 해당 상품을 구매하도록 승인합니다.",
    }).click();
    await page.getByRole("checkbox", {
      name: "merchant 주문에 필요한 배송정보 전달을 승인합니다. Vitlane은 merchant 비밀번호나 MFA 코드를 요구하지 않습니다.",
    }).click();

    assert.equal(
      await page.locator('.agency-order-payment-options [role="radio"][aria-checked="true"]').count(),
      0,
      "payment rail must not be selected by default",
    );
    if (process.env.E2E_PAYPAL_ENABLED !== "true") {
      const mutationsBeforePayPal = agencyCalls.filter((value) =>
        /^(POST|PUT|DELETE) /.test(value),
      ).length;
      assert.equal(
        await page.getByRole("radio", { name: "PayPal Live", exact: true }).isDisabled(),
        true,
        "PayPal Live must be disabled when its capability is unavailable",
      );
      assert.equal(
        await page.getByRole("radio", { name: "PayPal Sandbox", exact: true }).isDisabled(),
        true,
        "PayPal Sandbox must be disabled when its capability is unavailable",
      );
      assert.equal(
        agencyCalls.filter((value) => /^(POST|PUT|DELETE) /.test(value)).length,
        mutationsBeforePayPal,
        "disabled PayPal rails must issue zero requests",
      );
    }
    // This terminal journey exercises tVITUSD. PayPal Sandbox has an
    // independent conformance E2E, while PayPal Live stays activation-gated.
    await page.getByRole("radio", { name: "tVITUSD", exact: true }).click();
    await page.getByRole("button", { name: "주문하기", exact: true }).click();
    const issueOrderButton = page.getByRole("button", {
      name: "확정 주문서 작성하고 tVITUSD로 결제",
    });
    const confirmChangedTotalButton = page.getByRole("button", {
      name: /^\$[\d,.]+로 주문 확정$/,
    });
    // A first preflight can either preserve the amount already displayed or
    // produce a newly confirmed merchant total. The latter intentionally asks
    // for one more click before issuing the AgencyOrder.
    await waitFor("order-sheet preflight confirmation", async () =>
      await issueOrderButton.isVisible().catch(() => false)
        || await confirmChangedTotalButton.isVisible().catch(() => false),
      30_000,
    );
    if (await confirmChangedTotalButton.isVisible().catch(() => false)) {
      await confirmChangedTotalButton.click();
    } else {
      await issueOrderButton.click();
    }
    await page.waitForURL(/\/agencyOrder\/[^/]+\/payment$/);
    const agencyOrderID = new URL(page.url()).pathname.split("/").at(-2);
    assert.ok(agencyOrderID);
    await page.getByRole("heading", { name: "주문서가 안전하게 발행되었습니다." }).waitFor();

    const configResponse = await page.request.get(`${baseURL}/api/v1/settlement/config`);
    assert.equal(configResponse.status(), 200, await configResponse.text());
    const settlementConfig = (await configResponse.json()).settlement;
    await page.evaluate(() => window.__vitlaneReviewWallet?.setAccount(1));
    const payer = await page.evaluate(() => window.__vitlaneReviewWallet?.activeAccount());
    assert.ok(payer);
    await page.getByRole("button", { name: "결제 지갑 인증" }).click();
    const confirmPaymentTerms = page.getByRole("button", { name: "주문 금액으로 결제 조건 확정" });
    await confirmPaymentTerms.waitFor({ timeout: 20_000 });

    // A fresh TEST wallet has no tVITUSD. The payment page must open the
    // existing Faucet only for that insufficient-balance case and must not
    // allow payment-term sealing until the receipt-backed balance refreshes.
    const assets = page.getByRole("region", { name: "TEST 자산" });
    await waitFor("pre-payment tVITUSD check", async () =>
      await confirmPaymentTerms.isEnabled()
        || await assets.isVisible().catch(() => false),
    );
    const insufficientBalance = await assets.isVisible().catch(() => false);
    if (insufficientBalance) {
      assert.equal(await confirmPaymentTerms.isDisabled(), true);
      await page.getByRole("button", { name: "패널 닫기" }).click();
    }
    await page.getByLabel("tVITUSD는 실제 가치가 없는 TEST 자산임을 확인했습니다.").check();
    await page.getByLabel("이 결제가 실제 판매처 주문 완료나 법적 판매를 의미하지 않음을 확인했습니다.").check();
    if (insufficientBalance) {
      await page.getByRole("button", { name: "tVITUSD Faucet 열기" }).click();
      const claim = assets.getByRole("button", { name: "tVITUSD 받기" });
      await waitFor("claim action to become enabled", async () => await claim.isEnabled());
      await claim.click();
      await assets.getByText("TEST 자산을 받았습니다", { exact: true }).waitFor({ timeout: 30_000 });
      await page.getByRole("button", { name: "패널 닫기" }).click();
    }
    await waitFor("payment terms to become enabled after balance refresh", async () =>
      await confirmPaymentTerms.isEnabled(),
    );
    const authorizationResponsePromise = page.waitForResponse(
      (response) => response.request().method() === "POST"
        && /\/agencyOrder\/[^/]+\/settlement-authorizations$/.test(new URL(response.url()).pathname),
    );
    await confirmPaymentTerms.click();
    let authorizationResponse = await authorizationResponsePromise;
    const authorization = await waitFor("PaymentInstruction Owner confirmation", async () => {
      if (authorizationResponse.status() === 201) return authorizationResponse.json();
      assert.equal(authorizationResponse.status(), 202, await authorizationResponse.text());
      const pending = await authorizationResponse.json();
      assert.equal(pending.schemaVersion, "vitlane.payment-instruction-confirmation.v1");
      assert.equal(pending.outcome, "WAITING");
      assert.equal("authorization" in pending, false, "no signature before Instruction confirmation");
      const checkConfirmation = page.getByRole("button", { name: "주문 확인 상태 조회", exact: true });
      await checkConfirmation.waitFor();
      await waitFor("same approval can be checked again", () => checkConfirmation.isEnabled(), 10_000);
      const next = page.waitForResponse((response) => response.request().method() === "POST"
        && /\/agencyOrder\/[^/]+\/settlement-authorizations$/.test(new URL(response.url()).pathname));
      await checkConfirmation.click();
      authorizationResponse = await next;
      return authorizationResponse.status() === 201 ? authorizationResponse.json() : null;
    });
    // ADR-0054: settlement source는 AgencyOrder 하나 — legacy discriminator 없음.
    assert.equal(authorization.agencyOrderId, agencyOrderID);
    assert.equal("sourceKind" in authorization, false);
    assert.equal("purchaseId" in authorization, false);
    // v2 (ADR-0050): 승인·지불 총액 = passThrough + fee.
    const amount = BigInt(authorization.authorization.passThroughAmount)
      + BigInt(authorization.authorization.feeAmount);
    const token = authorization.authorization.token;
    const settlementAddress = authorization.domain.verifyingContract;
    assert.equal(
      await chain.readContract({
        address: token,
        abi: tokenABI,
        functionName: "allowance",
        args: [payer, settlementAddress],
      }),
      0n,
      "AgencyOrder E2E must begin with zero allowance",
    );

    await waitFor("AgencyOrder balance refresh", async () =>
      await page.getByRole("button", { name: "지갑에서 정확한 tVITUSD 사용 승인" }).isEnabled(),
    );

    const approveResponsePromise = page.waitForResponse(
      (response) => response.request().method() === "POST"
        && /\/agencyOrder\/[^/]+\/wallet-transactions$/.test(new URL(response.url()).pathname),
    );
    await page.getByRole("button", { name: "지갑에서 정확한 tVITUSD 사용 승인" }).click();
    const approveResponse = await approveResponsePromise;
    assert.equal(approveResponse.status(), 204, await approveResponse.text());
    await page.getByRole("button", { name: "tVITUSD로 결제" }).waitFor({ timeout: 30_000 });
    assert.equal(
      await chain.readContract({
        address: token,
        abi: tokenABI,
        functionName: "allowance",
        args: [payer, settlementAddress],
      }),
      amount,
      "approval must equal the immutable payment amount",
    );

    const payRequestPromise = page.waitForRequest(
      (request) => request.method() === "POST"
        && /\/agencyOrder\/[^/]+\/settlement-transactions$/.test(new URL(request.url()).pathname),
    );
    const payResponsePromise = page.waitForResponse(
      (response) => response.request().method() === "POST"
        && /\/agencyOrder\/[^/]+\/settlement-transactions$/.test(new URL(response.url()).pathname),
    );
    await page.getByRole("button", { name: "tVITUSD로 결제" }).click();
    const payRequest = await payRequestPromise;
    const payResponse = await payResponsePromise;
    assert.equal(payResponse.status(), 204, await payResponse.text());
    const payTxHash = JSON.parse(payRequest.postData()).txHash;
    const receipt = await chain.waitForTransactionReceipt({ hash: payTxHash });
    assert.equal(receipt.status, "success");
    assert.ok(receipt.logs.length >= 1, "pay receipt must contain the canonical settlement event");

    const settlementStates = [];
    const finalized = await waitFor("AgencyOrder payment finality", async () => {
      const response = await page.request.get(
        `${baseURL}/api/v1/agencyOrder/${agencyOrderID}/settlement`,
      );
      if (response.status() !== 200) return null;
      const body = await response.json();
      settlementStates.push(body.payment.state);
      return body.payment.state === "FINALIZED" ? body.payment : null;
    });
    assert.equal(finalized.agencyOrderId, agencyOrderID);
    assert.equal("sourceKind" in finalized, false);
    assert.equal(finalized.payTxHash.toLowerCase(), payTxHash.toLowerCase());
    assert.ok(Number.isInteger(finalized.safeBlock), "SAFE transition must be persisted");
    assert.ok(Number.isInteger(finalized.finalizedBlock), "FINALIZED block must be persisted");
    assert.equal("purchaseId" in finalized, false);
    await page.getByText("결제가 finality에 도달했습니다.", { exact: true }).waitFor({ timeout: 30_000 });

    await page.reload({ waitUntil: "networkidle" });
    await page.getByText("결제가 finality에 도달했습니다.", { exact: true }).waitFor({ timeout: 30_000 });
    const reloaded = await page.request.get(
      `${baseURL}/api/v1/agencyOrder/${agencyOrderID}/settlement`,
    );
    const reloadedPayment = (await reloaded.json()).payment;
    assert.equal(reloadedPayment.state, "FINALIZED");
    assert.equal(reloadedPayment.payTxHash.toLowerCase(), payTxHash.toLowerCase());
    // 조달 root(MerchantOrder)는 accepted receipt를 소비한 Procurement plan이
    // 만든다(Step 4B). Settlement finality와 이 worker tick은 별도 transaction이므로
    // 둘을 같은 즉시 read로 단언하지 않고 projection이 함께 수렴할 때까지 기다린다.
    const finalizedProjection = await waitFor(
      "Procurement activation and plan roots",
      async () => {
        const response = await page.request.get(`${baseURL}/api/v1/agencyOrder/${agencyOrderID}`);
        if (response.status() !== 200) return null;
        const projection = (await response.json()).agencyOrder;
        return projection.process.state === "PROCUREMENT_IN_PROGRESS"
          && projection.merchantOrders.length === 1
          ? projection
          : null;
      },
    );
    assert.equal(finalizedProjection.agencyOrder.status, "ISSUED");
    assert.equal(finalizedProjection.process.state, "PROCUREMENT_IN_PROGRESS");

    // The operator UI is the Procurement work surface. Assignment, audited
    // reveal and completion must all be performed through the same screen a
    // human operator uses.
    operatorContext = await browser.newContext({ locale: "ko-KR" });
    operator = await operatorContext.newPage();
    operator.on("pageerror", (error) => operatorPageErrors.push(error.stack || error.message));
    const operatorSession = await operator.request.post(
      `${baseURL}/api/v1/dev/auth/session`,
      { data: { profileKey: "empty-operator" } },
    );
    assert.equal(operatorSession.status(), 201, await operatorSession.text());
    const queueProjection = await waitFor("Procurement task intake", async () => {
      const response = await operator.request.get(`${baseURL}/api/v1/admin/procurement/queue`);
      if (response.status() !== 200) return null;
      const body = await response.json();
      return body.items.find(
        (item) => item.task.agencyOrderId === agencyOrderID
          && item.task.state === "QUEUED",
      ) ?? null;
    });
    const taskID = queueProjection.task.id;
    operatorTaskID = taskID;
    assert.equal(queueProjection.merchantOrder.executionMode, "SIMULATED_NO_EFFECT");
    assert.equal(queueProjection.funding.rail, "GIWA");
    assert.equal(queueProjection.funding.state, "AVAILABLE");
    const unifiedWorkItems = await operator.request.get(
      `${baseURL}/api/v1/admin/ordering/work-items`,
    );
    assert.equal(
      unifiedWorkItems.status(),
      200,
      `unified operator work surface failed: ${await unifiedWorkItems.text()}`,
    );
    const unifiedBody = await unifiedWorkItems.json();
    assert.ok(
      unifiedBody.items.some(
        (item) => item.kind === "PROCUREMENT_EXECUTION" && item.id === taskID,
      ),
      "unified operator work surface must include the accepted Procurement task",
    );
    const operatorProduct = queueProjection.agencyOrder.lines.find(
      (line) => line.shopDomain === queueProjection.merchantOrder.shopDomain,
    ).productTitle;
    await operator.goto(`${baseURL}/admin/agencyOrder`, { waitUntil: "networkidle" });
    assert.equal(new URL(operator.url()).pathname, "/admin/agencyOrder");
    const assignmentScope = operator.getByRole("group", { name: "담당 범위" });
    const assignmentCount = async (label) => {
      const text = await assignmentScope.getByRole(
        "button", { name: new RegExp(`^${label} \\d+$`) },
      ).innerText();
      return Number(text.match(/(\d+)$/)?.[1]);
    };
    const [mineCount, availableCount, otherCount, allCount] = await Promise.all([
      assignmentCount("내 담당"), assignmentCount("담당 가능"),
      assignmentCount("다른 담당"), assignmentCount("전체"),
    ]);
    const assignmentSummary = await operator.locator(".order-ui-page-header-summary").innerText();
    const completedCount = Number(assignmentSummary.match(/담당 완료\s+(\d+)/)?.[1]);
    const activeCount = Number(assignmentSummary.match(/활성 전체\s+(\d+)/)?.[1]);
    assert.equal(mineCount + availableCount + otherCount, activeCount,
      "active assignment scopes must partition active Procurement tasks");
    assert.equal(activeCount + completedCount, allCount,
      "active and completed assignment scopes must partition all Procurement tasks");
    // 기본은 내 담당·조달이다. 새 주문과 lease 만료 주문은 담당 가능에서 집는다.
    await operator.getByRole("button", { name: /담당 가능 \d/ }).click();
    const readyRow = operator.getByRole("button", { name: new RegExp(operatorProduct) });
    try {
      await readyRow.waitFor({ timeout: 30_000 });
    } catch (cause) {
      const operatorText = await operator.locator("body").innerText();
      const unifiedTask = unifiedBody.items.find((item) => item.id === taskID);
      throw new Error(
        `accepted Procurement task was absent from the operator UI\n` +
        `url=${operator.url()}\npageErrors=${JSON.stringify(operatorPageErrors)}\n` +
        `unified=${JSON.stringify(unifiedTask)}\nui=${operatorText}`,
        { cause },
      );
    }
    await readyRow.click();
    await operator.getByRole("button", { name: "내가 맡기" }).click();
    // 담당하면 담당 가능 범위를 떠난다 — 내 담당 범위에서 이어간다(ADR-0057 §6).
    await operator.getByRole("button", { name: /내 담당 1/ }).click();
    const runningRow = operator.getByRole("button", { name: new RegExp(operatorProduct) });
    await runningRow.waitFor({ timeout: 30_000 });
    await runningRow.click();
    // 열람 사유는 프리필되지 않는다(2차 P6) — 감사 기록용으로 직접 쓴다.
    await operator.getByLabel("배송정보 열람 사유").fill("E2E 구매대행 배송지 확인 여정");
    await operator.getByRole("button", { name: /배송정보 감사 열람/ }).click();
    await operator.getByText(/Test Buyer/).waitFor({ timeout: 30_000 });
    assert.equal(await operator.getByLabel("배송정보 열람 사유").count(), 0,
      "successful reveal must replace the reason form with a completed audit state");
    await operator.getByText("배송정보 감사 열람 완료", { exact: true }).waitFor();
    // 실제 운영자 입력과 같은 브라우저 경로에서 중대한 조건의 세 필드를 채우면
    // 요청 버튼이 열리고 POST가 발생하는지 확인한다. 이 본 시나리오는 이후
    // 정상 구매를 계속해야 하므로 요청 endpoint만 성공 응답으로 대체한다.
    await operator.getByLabel("판단", { exact: true }).selectOption("MATERIAL_NEW_CONDITION");
    await operator.getByLabel("관찰한 merchant 조건").fill("판매처가 새 배송 조건을 요구함");
    await operator.getByLabel("고객 공개 배경").fill("새 배송 조건 확인 후에만 구매를 진행합니다.");
    await operator.getByLabel("고객 질문").fill("새 배송 조건으로 진행할까요?");
    const requestButton = operator.getByRole("button", { name: "Messages로 요청 보내기" });
    assert.equal(await requestButton.isEnabled(), true,
      "valid material-condition fields must enable the Messages request button");
    const materialRequestBodies = [];
    const customerRequestPath = `/api/v1/admin/procurement/tasks/${taskID}/customer-requests`;
    await operator.route(`**${customerRequestPath}`, async (route) => {
      const materialRequestBody = route.request().postDataJSON();
      materialRequestBodies.push(materialRequestBody);
      const requestRound = materialRequestBodies.length;
      await route.fulfill({
        status: 202,
        contentType: "application/json",
        body: JSON.stringify({
          schemaVersion: "vitlane.order-process-receipt.v1",
          agencyOrderId: agencyOrderID, merchantOrderId: queueProjection.merchantOrder.id,
          requestId: `e2e-intercepted-request-${requestRound}`,
          flowId: `e2e-intercepted-flow-${requestRound}`,
          kind: "CUSTOMER_QUESTION", outcome: "COMPLETED",
          guidance: { reasonCode: "OWNER_INPUT_APPLIED", customerAction: "WAIT", operatorAction: "WAIT" },
        }),
      });
    });
    // The customer has answered the first request. The next review fetch must
    // restore the material-condition form so the operator can immediately send
    // another request (or choose a different decision) instead of falling back
    // to the within-authorization action.
    const manualReviewPath = `/api/v1/admin/procurement/tasks/${taskID}/manual-review`;
    await operator.route(`**${manualReviewPath}`, async (route) => {
      const requestRound = materialRequestBodies.length;
      const materialRequestBody = materialRequestBodies.at(-1);
      assert.ok(materialRequestBody, "manual review interception requires a preceding request");
      const materialDecisionID = `e2e-intercepted-material-decision-${requestRound}`;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          schemaVersion: "vitlane.procurement-manual-review.v1",
          decisions: [{
            id: materialDecisionID, merchantOrderId: queueProjection.merchantOrder.id,
            agencyOrderId: agencyOrderID, taskId: taskID,
            decision: "MATERIAL_NEW_CONDITION",
            publicRationale: materialRequestBody.publicContext,
            observedCondition: materialRequestBody.observedCondition,
            evidenceSource: materialRequestBody.evidenceSource,
            evidenceHash: "a".repeat(64), authorizationHash: "b".repeat(64),
            executionProfileHash: "c".repeat(64),
            observedAt: new Date().toISOString(), createdAt: new Date().toISOString(),
          }],
          customerRequests: [{
            id: `e2e-intercepted-request-${requestRound}`,
            merchantOrderId: queueProjection.merchantOrder.id,
            agencyOrderId: agencyOrderID, sourceDecisionId: materialDecisionID,
            kind: "INFORMATION", prompt: materialRequestBody.prompt,
            responseType: "TEXT", responseOptions: [],
            publicContext: materialRequestBody.publicContext, state: "ANSWERED",
            response: { text: requestRound === 1
              ? "새 배송 조건으로 진행해 주세요."
              : "추가 질문에도 답변했습니다." },
            requestedAt: new Date().toISOString(), dueAt: new Date(Date.now() + 86_400_000).toISOString(),
            resolvedAt: new Date().toISOString(), version: 2,
          }],
        }),
      });
    });
    const materialRequestResponsePromise = operator.waitForResponse((response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname === customerRequestPath,
    );
    await requestButton.click();
    const materialRequestResponse = await materialRequestResponsePromise;
    assert.equal(materialRequestResponse.status(), 202, await materialRequestResponse.text());
    assert.equal(materialRequestBodies[0].observedCondition, "판매처가 새 배송 조건을 요구함");
    assert.equal(materialRequestBodies[0].prompt, "새 배송 조건으로 진행할까요?");
    await waitFor("answered material request restored for re-decision", async () =>
      await operator.getByLabel("판단", { exact: true }).inputValue() === "MATERIAL_NEW_CONDITION"
        ? true
        : null,
    );
    assert.equal(await operator.getByLabel("관찰한 merchant 조건").inputValue(),
      "판매처가 새 배송 조건을 요구함");
    assert.equal(await operator.getByLabel("고객 질문").inputValue(),
      "새 배송 조건으로 진행할까요?");
    await operator.getByText("새 배송 조건으로 진행해 주세요.").waitFor();
    const followUpButton = operator.getByRole("button", { name: "Messages로 재요청 보내기" });
    // Manual-review refresh can paint the answer before useOperatorSurface.run
    // finishes reloading the work list and clears its busy flag. Assert the
    // usable action with a bounded wait, after the answered form is visible.
    await waitFor("answered request enables a sequential follow-up request", () =>
      followUpButton.isEnabled(), 10_000);
    await operator.getByLabel("고객 질문").fill("추가 배송 조건도 확인해 주시겠어요?");
    const followUpResponsePromise = operator.waitForResponse((response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname === customerRequestPath,
    );
    await followUpButton.click();
    const followUpResponse = await followUpResponsePromise;
    assert.equal(followUpResponse.status(), 202, await followUpResponse.text());
    await waitFor("second answered request restored for re-decision", async () =>
      await operator.getByLabel("고객 질문").inputValue() === "추가 배송 조건도 확인해 주시겠어요?"
        ? true
        : null,
    );
    assert.equal(materialRequestBodies.length, 2);
    assert.equal(materialRequestBodies[1].prompt, "추가 배송 조건도 확인해 주시겠어요?");
    await operator.getByText("추가 질문에도 답변했습니다.").waitFor();
    await waitFor("second answer enables the same explicit re-decision action", () =>
      followUpButton.isEnabled(), 10_000);
    await operator.unroute(`**${customerRequestPath}`);
    await operator.unroute(`**${manualReviewPath}`);
    await operator.getByLabel("판단", { exact: true }).selectOption("WITHIN_AUTHORIZATION");
    // 고객 안내 왕복(ADR-0059): 안내는 주문 첨부 채팅 메시지로 고객 지원
    // 대화에 쌓인다. 한글 본문 발송이 성공해야 한다(헤더 Latin1 회귀 방지 —
    // 멱등 키는 UUID, 본문은 JSON).
    await operator.getByLabel("고객 안내 보내기").fill("판매처 확인이 진행 중입니다 — E2E 안내");
    const noticeResponsePromise = operator.waitForResponse((response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname ===
        `/api/v1/admin/support/orders/${agencyOrderID}/messages`,
    );
    await operator.getByRole("button", { name: "안내 발송" }).click();
    const noticeResponse = await noticeResponsePromise;
    assert.equal(noticeResponse.status(), 201, await noticeResponse.text());
    // 승인 범위 일치는 판단 폼을 더 요구하지 않는다. 주문 처리 시작 뒤에는
    // Live와 같은 구매 증거 shape를 TEST evidence로 남겨야 완료할 수 있다.
    assert.equal(
      await operator.getByLabel("판단", { exact: true }).inputValue(),
      "WITHIN_AUTHORIZATION",
    );
    assert.equal(await operator.getByLabel("관찰한 merchant 조건").count(), 0);
    const effectResponsePromise = operator.waitForResponse((response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname ===
        `/api/v1/admin/procurement/tasks/${taskID}/merchant-effect`,
    );
    await operator.getByRole("button", { name: "주문 처리 시작" }).click();
    const effectResponse = await effectResponsePromise;
    const effectResponseText = await effectResponse.text();
    assert.equal(effectResponse.status(), 202, effectResponseText);
    const effectBody = JSON.parse(effectResponseText);
    assert.equal(effectBody.schemaVersion, "vitlane.order-process-receipt.v1");
    assert.ok(["RECEIVED", "ACCEPTED", "WAITING"].includes(effectBody.outcome), JSON.stringify(effectBody));
    assert.equal(effectBody.kind, "PURCHASE");
    assert.equal(effectBody.agencyOrderId, agencyOrderID);
    assert.equal(effectBody.merchantOrderId, queueProjection.merchantOrder.id);
    assert.ok(effectBody.requestId && effectBody.flowId, "purchase receipt must identify its request and flow");
    // Acceptance reserves execution; wait for the real Owner funding and merchant
    // permission before the operator records any external purchase evidence.
    const fundedItem = await waitFor("merchant effect funding activation", async () => {
      const response = await operator.request.get(`${baseURL}/api/v1/admin/procurement/queue`);
      if (response.status() !== 200) return null;
      const item = (await response.json()).items.find(
        (candidate) => candidate.task.id === taskID,
      );
      return item?.task.state === "IN_PROGRESS" &&
        item.merchantOrder.state === "PLACEMENT_PENDING" &&
        item.funding?.state === "ACTIVE"
        ? item
        : null;
    });
    assert.equal(fundedItem.funding.rail, "GIWA");
    const pendingReceipt = await waitFor("purchase waiting for merchant evidence", async () => {
      const response = await page.request.get(`${baseURL}/api/v1/agencyOrder/${agencyOrderID}/process-requests`);
      assert.equal(response.status(), 200, await response.text());
      const receipt = (await response.json()).requests.find(
        (candidate) => candidate.requestId === effectBody.requestId,
      );
      return receipt?.outcome === "WAITING" && receipt.guidance.reasonCode === "MERCHANT_RESULT_REQUIRED"
        ? receipt
        : null;
    });
    assert.equal(pendingReceipt.flowId, effectBody.flowId);
    assert.equal(pendingReceipt.merchantOrderId, queueProjection.merchantOrder.id);
    // The authoritative Task/MO transition resets only the stage-local UI.
    // The row stays open, while question/decision state must not leak into the
    // placement-evidence step, which initializes with its own empty fields.
    const transitionedRow = operator.getByRole("button", { name: new RegExp(operatorProduct) });
    await transitionedRow.waitFor({ timeout: 30_000 });
    assert.equal(await transitionedRow.getAttribute("aria-expanded"), "true",
      "the current Task row must remain open across a process-state transition");
    await operator.getByText("TEST 구매 증거", { exact: true }).waitFor();
    assert.equal(await operator.getByLabel("판단", { exact: true }).count(), 0);
    assert.equal(await operator.getByText("고객 답변 완료 — 다시 판단하세요", { exact: true }).count(), 0);
    assert.equal(await operator.getByLabel("TEST merchant 주문 참조").inputValue(), "");
    assert.equal(await operator.getByLabel("영수증 안전 참조").inputValue(), "");
    assert.equal(await operator.getByLabel("결제 금액").inputValue(), "UNCHANGED");
    assert.equal(await operator.getByLabel("TEST 기입액(USD)").count(), 0,
      "unchanged placement must not ask the operator to re-enter the approved amount");
    await operator.getByLabel("TEST merchant 주문 참조").fill("TEST-STATE-RESET");
    await operator.getByLabel("영수증 안전 참조").fill("receipt:state-reset");
    assert.equal(await operator.getByRole("button", { name: "TEST 구매 증거 기록" }).isEnabled(), true,
      "approved amount plus fresh evidence fields must enable independently after the state transition");
    // Reload clears every React-local PII value. The persisted reveal audit and
    // server action remain authoritative, and PLACEMENT_PENDING must keep the
    // original decision rather than rendering it as "unavailable".
    await operator.reload({ waitUntil: "networkidle" });
    const pendingRow = operator.getByRole("button", { name: new RegExp(operatorProduct) });
    await pendingRow.waitFor({ timeout: 30_000 });
    await pendingRow.click();
    assert.equal(await operator.getByLabel("판단", { exact: true }).count(), 0);
    assert.equal(
      (await operator.locator(".agency-order-operator__manual-review header .vt-chip").innerText()).trim(),
      "승인 범위 내",
    );
    assert.equal(await operator.getByRole("heading", { name: "구매 실패·환불 처리" }).count(), 0);
    assert.equal(await operator.getByLabel("고객 공개 근거").count(), 0);
    await operator.getByRole("button", { name: "구매 실패 처리 시작" }).waitFor();
    await operator.getByLabel("TEST merchant 주문 참조").fill(`TEST-${taskID}`);
    await operator.getByLabel("영수증 안전 참조").fill(`receipt:${taskID}`);
		assert.equal(await operator.getByLabel("결제 금액").inputValue(), "UNCHANGED");
		await operator.getByLabel("결제 금액").selectOption("CHANGED");
		await operator.getByLabel("TEST 기입액(USD)").fill((
			(queueProjection.merchantOrder.checkoutSnapshot.authoritativeTotal.amountMinor - 1) / 100
		).toFixed(2));
		assert.equal(await operator.getByLabel("구매 증거 해시").count(), 0);
    assert.equal(await operator.getByLabel("구매 관찰 시각").count(), 0);
    const recordTestEvidence = operator.getByRole("button", { name: "TEST 구매 증거 기록" });
    assert.equal(await recordTestEvidence.isEnabled(), true,
      "lower actual spend must remain recordable after local address state is cleared");
    await recordTestEvidence.click({ timeout: 30_000 });

    const acceptedItem = await waitFor("operator UI placement", async () => {
      const response = await operator.request.get(`${baseURL}/api/v1/admin/procurement/queue`);
      const body = await response.json();
      return body.items.find(
        (item) => item.task.id === taskID && item.task.state === "SUCCEEDED",
      ) ?? null;
    });
    await waitFor("purchase receipt completed by merchant fact", async () => {
      const response = await page.request.get(`${baseURL}/api/v1/agencyOrder/${agencyOrderID}/process-requests`);
      assert.equal(response.status(), 200, await response.text());
      const receipt = (await response.json()).requests.find(
        (candidate) => candidate.requestId === effectBody.requestId,
      );
      return receipt?.outcome === "COMPLETED" && receipt.flowId === effectBody.flowId
        && receipt.guidance.reasonCode === "MERCHANT_PLACED";
    });
    assert.equal(acceptedItem.merchantOrder.state, "PLACED");
    assert.equal(acceptedItem.merchantOrder.externalOrderRef, `TEST-${taskID}`);
    assert.equal(
      acceptedItem.merchantOrder.placementEvidence?.kind,
      "SANDBOX_TEST_EVIDENCE",
    );
    assert.ok(
      !Number.isNaN(Date.parse(acceptedItem.merchantOrder.placementEvidence?.observedAt ?? "")),
      "server must record the placement observation time automatically",
    );
		assert.match(
			acceptedItem.merchantOrder.placementEvidence?.evidenceHash ?? "",
			/^sha256:[0-9a-f]{64}$/,
			"server must generate the canonical SHA-256 placement evidence hash",
		);

    // 배송 단계도 동일 워크플로다: 내 담당 범위의 같은 카드가 배송 stage로
    // 넘어가며 서버가 현재 exact-MO에 허용한 다음 행동만 하나씩 실행한다.
    // Web이 로컬 전이표나 "남은 단계 일괄 처리" 우회를 소유하면 실패다.
    await operator.getByRole("button", { name: /^배송 \d/ }).click();
    const placedRow = operator.getByRole("button", { name: new RegExp(operatorProduct) });
    await placedRow.waitFor({ timeout: 30_000 });
    await placedRow.click();
    await operator.getByLabel("운송사").fill("SANDBOX");
    await operator.getByLabel("운송장 번호").fill(`SBX-${taskID}`);
    await operator.getByRole("button", { name: "운송장 등록", exact: true }).click();
    const inTransitButton = operator.getByRole("button", { name: "배송중 처리" });
    await inTransitButton.waitFor({ timeout: 30_000 });
    await inTransitButton.click();
    const normalReceiptButton = operator.getByRole("button", { name: "정상 수령 기록" });
    await normalReceiptButton.waitFor({ timeout: 30_000 });
    await normalReceiptButton.click();
    await waitFor("sandbox delivered confirmation", async () => {
      const response = await operator.request.get(
        `${baseURL}/api/v1/admin/logistics/agencyOrder/${agencyOrderID}/shipments`,
      );
      if (response.status() !== 200) return null;
      const body = await response.json();
      const view = body.shipments.find(
        (item) => item.shipment.merchantOrderId === acceptedItem.merchantOrder.id,
      );
      return view && view.shipment.state === "DELIVERED"
        && view.units.every((unit) => unit.fulfillment === "DELIVERED_EXPECTED")
        ? view
        : null;
    });

    const terminalStates = [];
    const terminalProjection = await waitFor("AgencyOrder COMPLETE finality", async () => {
      const response = await page.request.get(
        `${baseURL}/api/v1/agencyOrder/${agencyOrderID}`,
      );
      if (response.status() !== 200) return null;
      const projection = (await response.json()).agencyOrder;
      terminalStates.push({
        payment: projection.payment?.state,
        process: projection.process.state,
        receipt: Boolean(projection.receipt),
      });
      return projection.payment?.state === "COMPLETED"
        && projection.process.state === "TERMINAL"
        && projection.process.terminalReason === "COMPLETED_ALL"
        && projection.receipt
        ? projection
        : null;
    });
    assert.equal(terminalProjection.receipt.kind, "TEST");
    assert.equal(terminalProjection.receipt.legalSale, false);
    assert.equal(terminalProjection.receipt.terminalState, "COMPLETED_ALL");
    assert.equal(
      terminalProjection.receipt.terminalTxHash.toLowerCase(),
      terminalProjection.payment.completeTxHash.toLowerCase(),
    );

    await page.goto(`${baseURL}/agencyOrder/${agencyOrderID}`, { waitUntil: "networkidle" });
    await page.getByText("TEST 처리 완료", { exact: true }).waitFor({ timeout: 30_000 });
    // 단일 rail·물류 어휘(ADR-0057) — 체인 lane 대신 결제 한 줄 요약과
    // 패키지·unit 결과가 종결을 말한다.
    // Still Water (PR E): 상태 칩은 raw enum이 아니라 문장체 문구다(stateChipCopy).
    await page.getByText("tVITUSD · 완료", { exact: true }).waitFor();
    await page.getByText(/현재 단계 · 정상 배송 완료/).first().waitFor();
    await page.getByText(/— 정상 수령$/).first().waitFor();
    // 주문별 결제 모드 배지(2차 P0): 이 주문의 economicEffect 기록이 근거다.
    await page.getByText("테스트 결제 · 실제 청구 없음 (TESTNET)", { exact: true }).first().waitFor();
    // 운영자 안내가 고객 지원 대화에 주문 첨부 메시지로 도착한다(ADR-0059).
    // 고객 위젯 UI는 후속 PR이 열며, 여기서는 대화 계약으로 왕복을 검증한다.
    const orderMessage = await waitFor("support order message", async () => {
      const response = await page.request.get(`${baseURL}/api/v1/support/messages`);
      if (response.status() !== 200) return null;
      const body = await response.json();
      return body.messages.find((message) =>
        message.body.includes("판매처 확인이 진행 중입니다 — E2E 안내"),
      ) ?? null;
    });
    assert.equal(orderMessage.author, "OPERATOR");
    assert.equal(orderMessage.agencyOrderId, agencyOrderID);

    // 고객이 지원 대화로 문의를 보내면 운영자 고객 대화 콘솔의 답변 대기
    // 기본 탭에 뜨고, 한국어 답변이 같은 대화로 돌아온다.
    const customerSend = await page.request.post(`${baseURL}/api/v1/support/messages`, {
      headers: { "Idempotency-Key": `e2e-support-${Date.now()}` },
      data: { body: "배송 관련해서 문의드립니다 — E2E 고객 문의" },
    });
    assert.equal(customerSend.status(), 201, await customerSend.text());
    await operator.goto(`${baseURL}/admin/support`, { waitUntil: "networkidle" });
    await operator.getByText(/배송 관련해서 문의드립니다 — E2E 고객 문의/).first().waitFor({ timeout: 30_000 });
    await operator.getByText(/배송 관련해서 문의드립니다 — E2E 고객 문의/).first().click();
    await operator.getByLabel("답변 보내기").fill("확인해 보고 바로 안내드릴게요 — E2E 답변");
    await operator.getByRole("button", { name: "답변 발송" }).click();
    await operator.getByTestId("support-thread-log")
      .getByText(/확인해 보고 바로 안내드릴게요 — E2E 답변/).waitFor({ timeout: 30_000 });
    await waitFor("customer unread reply", async () => {
      const summary = await page.request.get(`${baseURL}/api/v1/support/summary`);
      if (summary.status() !== 200) return null;
      const { unread } = await summary.json();
      return unread >= 2 ? unread : null; // 주문 안내 + 운영자 답변
    });

    // 고객 "메시지" 위젯(ADR-0059/0063): 프로필 메뉴 맨 위 항목으로 열고,
    // 주문 첨부 안내는 구조화 참조 칩으로만 주문에 링크된다(본문 파싱 없음).
    await page.getByLabel("프로필 메뉴").click();
    await page.getByText("메시지", { exact: true }).click();
    const chatLog = page.getByTestId("support-chat-log");
    await chatLog.getByText(/판매처 확인이 진행 중입니다 — E2E 안내/).waitFor({ timeout: 30_000 });
    await chatLog.getByText(/확인해 보고 바로 안내드릴게요 — E2E 답변/).waitFor();
    // 열람과 함께 읽음 워터마크가 찍혀 안 읽음 뱃지가 사라진다.
    await waitFor("support read watermark", async () => {
      const summary = await page.request.get(`${baseURL}/api/v1/support/summary`);
      if (summary.status() !== 200) return null;
      const { unread } = await summary.json();
      return unread === 0 ? true : null;
    });
    // 주문 링크 칩 → 해당 주문 추적 화면으로 이동한다.
    await page.getByRole("link", { name: "관련 주문 보기" }).first().click();
    await waitFor("order chip navigation", async () =>
      new URL(page.url()).pathname === `/agencyOrder/${agencyOrderID}` ? true : null);
    await page.getByText("TEST 처리 완료", { exact: true }).waitFor({ timeout: 30_000 });
    // 대칭 첨부(ADR-0059 §5): 고객도 자기 주문을 구조화 참조로 첨부해
    // 문의한다 — 소유권 검증은 서버가 한다.
    await page.getByLabel("주문 첨부").click();
    await page.getByTestId("support-attach-picker").waitFor({ timeout: 30_000 });
    await page.getByTestId("support-attach-picker").locator("button").first().click();
    await page.getByText(/주문 첨부 · /).waitFor();
    await page.getByLabel("메시지 입력").fill("이 주문 관련해서 추가 문의드립니다 — E2E 고객 첨부");
    await page.getByLabel("메시지 보내기").click();
    await chatLog.getByText(/E2E 고객 첨부/).waitFor({ timeout: 30_000 });
    // 운영자 스레드에도 주문 첨부 배지와 함께 도착한다(5s 폴).
    await operator.getByTestId("support-thread-log")
      .getByText(/E2E 고객 첨부/).waitFor({ timeout: 30_000 });
    await operator.getByTestId("support-thread-log")
      .getByText(/주문 첨부 · /).first().waitFor();
    // 답변 완료로 표시(ADR-0062): 고객에게 메시지를 추가로 보내지 않고 exact
    // 마지막 고객 메시지만 운영 완료로 닫아 답변 대기 목록에서 제거한다.
    await operator.getByRole("button", { name: "답변 완료로 표시" }).click();
    await waitFor("support no-reply resolution", async () => {
      const response = await operator.request.get(
        `${baseURL}/api/v1/admin/support/conversations?view=AWAITING`,
      );
      if (response.status() !== 200) return null;
      const body = await response.json();
      return body.conversations.some((conversation) =>
        conversation.lastMessage.body.includes("E2E 고객 첨부"),
      ) ? null : true;
    });
    await operator.getByTestId("support-thread-log")
      .getByText(/E2E 고객 첨부/).waitFor();
    // 위젯을 명시적으로 닫아 이후 단계의 클릭을 가리지 않게 한다.
    await page.getByLabel("메시지 닫기").click();

    await page.getByRole("button", { name: "주문 시 배송정보 보기" }).click();
    await page.getByText("Test Buyer", { exact: true }).waitFor();
    await page.reload({ waitUntil: "networkidle" });
    await page.getByText("TEST 처리 완료", { exact: true }).waitFor({ timeout: 30_000 });
    await page.goto(`${baseURL}/agencyOrder`, { waitUntil: "networkidle" });
    await page.getByRole("button", { name: /완료 내역 1/ }).click();
    await page.getByRole("button", { name: new RegExp(operatorProduct) }).waitFor();

    const curationAfterOrder = await page.request.get(
      `${baseURL}/api/v1/curations/${curationID}/workspace`,
    );
    assert.equal(curationAfterOrder.status(), 200, await curationAfterOrder.text());
    assert.ok((await curationAfterOrder.json()).agencyOrderTrace.some(
      (item) => item.agencyOrderId === agencyOrderID,
    ));

    assert.ok(agencyCalls.some((value) => /^POST .*\/order-sheets$/.test(value)), agencyCalls.join("\n"));
    assert.ok(agencyCalls.some((value) => /^PUT .*\/order-sheets\/[^/]+\/shipping-address$/.test(value)), agencyCalls.join("\n"));
    assert.ok(agencyCalls.some((value) => value.endsWith("/preflight")), agencyCalls.join("\n"));
    assert.ok(agencyCalls.some((value) => value.endsWith("/issue")), agencyCalls.join("\n"));
    assert.equal(agencyCalls.some((value) => value.includes("complete_checkout")), false);
    assert.deepEqual(pageErrors, []);

    if (artifactDir) {
      await fs.mkdir(artifactDir, { recursive: true });
      await page.screenshot({
        path: path.join(artifactDir, "phase8-agency-order-finalized-firefox.png"),
        fullPage: true,
      });
    }
    console.log("Phase 8 AgencyOrder terminal Firefox E2E: PASS");
    console.log(JSON.stringify({
      curationID,
      agencyOrderID,
      payTxHash,
      receiptBlock: receipt.blockNumber.toString(),
      settlementStates: [...new Set(settlementStates)],
      terminalStates,
      agencyCalls,
    }, null, 2));
  } catch (error) {
    let operatorDiagnostics;
    if (operator) {
      let queueItem;
      if (operatorTaskID) {
        const response = await operator.request.get(
          `${baseURL}/api/v1/admin/procurement/queue`,
        ).catch(() => undefined);
        if (response?.status() === 200) {
          queueItem = (await response.json()).items.find(
            (item) => item.task.id === operatorTaskID,
          );
        }
      }
      operatorDiagnostics = {
        url: operator.url(),
        alerts: await operator.locator(
          ".agency-order-operator > [role='alert']",
        ).allInnerTexts().catch(() => []),
        pageErrors: operatorPageErrors,
        queueItem,
      };
    }
    if (artifactDir) {
      await fs.mkdir(artifactDir, { recursive: true });
      await page.screenshot({
        path: path.join(artifactDir, "phase8-agency-order-failure-firefox.png"),
        fullPage: true,
      });
    }
    console.error(JSON.stringify({
      url: page.url(), agencyCalls, expansionCalls, supportCalls, pageErrors,
      operator: operatorDiagnostics,
    }, null, 2));
    throw error;
  } finally {
    if (operatorContext) await operatorContext.close();
    await context.close();
    await browser.close();
  }
}

run().catch((error) => {
  console.error("Phase 8 AgencyOrder terminal Firefox E2E: FAIL");
  console.error(error.stack ?? error);
  process.exitCode = 1;
});
