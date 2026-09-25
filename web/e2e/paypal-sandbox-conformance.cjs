// PayPal Sandbox conformance — 실 Sandbox 결제·환불 왕복 증적 (Phase 8 Step 3).
//
// 로컬 검수 환경 전용이다: buyer 자격(PAYPAL_SANDBOX_BUYER_*)이 없으면 즉시
// SKIP으로 끝나고, CI 스위트에는 연결하지 않는다. 서버에는 buyer 자격을 절대
// 주입하지 않으며 이 스크립트의 브라우저 컨텍스트에서만 쓴다.
//
// 검증 왕복: 큐레이션 → OrderSheet(PAYPAL_SANDBOX 발행) → 주문 gross 실 Sandbox
// AUTHORIZE → 운영자 조달 시작 시 exact-MO Capture → TEST 구매 증거·배송 완료 →
// whole-MO 환불 요청·승인 → sweeper의 exact Capture Refund 완료.
const assert = require("node:assert/strict");
const path = require("node:path");
const { firefox } = require("playwright");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:18080").replace(/\/$/, "");
const artifactDir = process.env.E2E_ARTIFACT_DIR ?? process.env.E2E_SCREENSHOT_DIR ?? "/screenshots";
const buyerEmail = (process.env.PAYPAL_SANDBOX_BUYER_EMAIL ?? "").trim();
const buyerPassword = (process.env.PAYPAL_SANDBOX_BUYER_PASSWORD ?? "").trim();
const timeoutMs = Number(process.env.E2E_PAYPAL_TIMEOUT_MS ?? 300_000);

async function waitFor(label, predicate, timeout = timeoutMs) {
  const deadline = Date.now() + timeout;
  let last;
  while (Date.now() < deadline) {
    last = await predicate();
    if (last) return last;
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  throw new Error(`timed out waiting for ${label}; last=${JSON.stringify(last)}`);
}

async function clickCandidateCardBody(card) {
  const box = await card.boundingBox();
  assert.ok(box, "candidate card must be visible to open its modal");
  await card.click({ position: { x: 24, y: box.width + 24 } });
}

// Sandbox 승인 화면은 배포에 따라 셀렉터가 흔들린다 — 이메일/비밀번호 2단계
// 로그인과 단일 폼 로그인, 승인 버튼 후보들을 순서대로 시도한다.
async function approveOnPayPal(page, shot) {
  await page.waitForURL(/sandbox\.paypal\.com/, { timeout: timeoutMs });
  await shot("paypal-approval-entry");
  const email = page.locator("#email");
  if (await email.isVisible().catch(() => false)) {
    await email.fill(buyerEmail);
    const next = page.locator("#btnNext");
    if (await next.isVisible().catch(() => false)) await next.click();
  }
  const password = page.locator("#password");
  await password.waitFor({ timeout: 60_000 });
  await password.fill(buyerPassword);
  await page.locator("#btnLogin").click();
  await shot("paypal-logged-in");
  // 결제 승인 버튼 후보를 화면 갱신을 기다리며 반복 시도한다.
  const submitCandidates = [
    '[data-testid="submit-button-initial"]',
    "#payment-submit-btn",
    'button[data-id="payment-submit-btn"]',
  ];
  await waitFor("PayPal approval submit", async () => {
    for (const selector of submitCandidates) {
      const button = page.locator(selector).first();
      if (await button.isVisible().catch(() => false)) {
        await button.click().catch(() => undefined);
        return true;
      }
    }
    // 이미 승인 후 redirect가 시작됐다면 그대로 통과한다.
    return !/sandbox\.paypal\.com/.test(page.url());
  }, 120_000);
}

async function run() {
  if (!buyerEmail || !buyerPassword) {
    console.log("PayPal Sandbox conformance: buyer 자격 미설정 — SKIP");
    return;
  }
  const browser = await firefox.launch({ headless: true });
  const context = await browser.newContext({
    locale: "ko-KR",
    viewport: { width: 1440, height: 960 },
  });
  let operatorContext;
  const page = await context.newPage();
  let shotIndex = 0;
  const shot = async (name) => {
    if (!artifactDir) return;
    shotIndex += 1;
    await page.screenshot({
      path: path.join(artifactDir, `paypal-conformance-${String(shotIndex).padStart(2, "0")}-${name}.png`),
      fullPage: false,
    }).catch(() => undefined);
  };
  try {
    const session = await page.request.post(`${baseURL}/api/v1/dev/auth/session`, {
      data: { profileKey: "empty-user" },
    });
    assert.equal(session.status(), 201, await session.text());
    await page.goto(`${baseURL}/account`, { waitUntil: "networkidle" });
    await page.getByLabel("배송지 이름").fill("PayPal Conformance");
    await page.getByLabel("수령인").fill("Sandbox Buyer");
    await page.getByLabel("주소 1").fill("500 Conformance Ave");
    await page.getByLabel("도시").fill("Seattle");
    await page.getByLabel("주/도").fill("WA");
    await page.getByLabel("우편번호").fill("98101");
    await page.getByLabel("국가 코드").fill("US");
    await page.getByLabel("전화번호").fill("+12065550111");
    await page.getByRole("button", { name: "배송지 저장" }).click();
    await page.getByText("PayPal Conformance · 기본", { exact: true }).waitFor({ timeout: 30_000 });

    await page.goto(`${baseURL}/`, { waitUntil: "networkidle" });
    await page.locator("#curation-intent").fill("one US domestic camping mug");
    await page.locator(".shell-intent-composer__submit").click();
    await page.waitForURL(/\/curations\/[0-9a-f-]+$/i, { timeout: 60_000 });
    const card = page.locator(".catalog-ui-target .vt-candidate-card").first();
    await card.waitFor({ timeout: timeoutMs });
    await clickCandidateCardBody(card);
    const candidateModal = page.locator(".catalog-ui-candidate-modal");
    await candidateModal.locator(".catalog-ui-variant-row").first().waitFor();
    await candidateModal.getByRole("button", { name: "장바구니 담기" }).click();
    await page.getByRole("button", { name: /장바구니 1/ }).click();
    await page.getByRole("button", { name: "주문하기" }).waitFor();
    await page.getByRole("button", { name: "주문하기" }).click();
    await page.waitForURL(/\/curations\/[^/]+\/order-sheet\?cartVersion=\d+$/);
    await page.getByRole("heading", { name: "주문 내용을 마지막으로 확인해 주세요." }).waitFor();
    await page.getByRole("button", { name: "이 배송지로 배송 옵션 확인" }).click();
    await page.getByRole("button", { name: "주문하기", exact: true }).waitFor();
    await shot("order-sheet-ready");

    await page.getByRole("checkbox", {
      name: "표시된 금액과 조건 범위에서 Vitlane 운영자가 해당 상품을 구매하도록 승인합니다.",
    }).click();
    await page.getByRole("checkbox", {
      name: "merchant 주문에 필요한 배송정보 전달을 승인합니다. Vitlane은 merchant 비밀번호나 MFA 코드를 요구하지 않습니다.",
    }).click();

    // PayPal rail 선택 → preflight 반영 → 발행. capability는 READY여야 한다.
    await page.getByRole("radio", { name: "PayPal" }).click();
    await page.getByRole("button", { name: "주문하기", exact: true }).click();
    const issueButton = page.getByRole("button", { name: "확정 주문서 작성하고 PayPal로 결제" });
    const confirmChangedTotalButton = page.getByRole("button", {
      name: /^\$[\d,.]+로 주문 확정$/,
    });
    await waitFor("PayPal order-sheet preflight confirmation", async () =>
      await issueButton.isVisible().catch(() => false)
        || await confirmChangedTotalButton.isVisible().catch(() => false),
      60_000,
    );
    await shot("order-sheet-paypal-selected");
    if (await confirmChangedTotalButton.isVisible().catch(() => false)) {
      await confirmChangedTotalButton.click();
    } else {
      await issueButton.click();
    }
    await page.waitForURL(/\/agencyOrder\/[^/]+\/payment$/, { timeout: 60_000 });
    const agencyOrderID = new URL(page.url()).pathname.split("/").at(-2);
    assert.ok(agencyOrderID);
    await shot("payment-page-issued");

    // 결제 시작 → 실 Sandbox 승인 → 복귀 → 서버 AUTHORIZE 확정 폴링.
    await page.getByRole("button", { name: "PayPal에서 승인" }).click();
    await approveOnPayPal(page, shot);
    // 웹 라우트는 kebab-case(/agency-order/…)로 복귀한다.
    await page.waitForURL(new RegExp(`/agenc(yOrder|y-order)/${agencyOrderID}/payment`), { timeout: timeoutMs });
    await shot("returned-from-paypal");
    const authorized = await waitFor("PayPal authorization confirmed", async () => {
      const response = await page.request.get(
        `${baseURL}/api/v1/agencyOrder/${agencyOrderID}/paypal`,
      );
      if (response.status() !== 200) return null;
      const body = await response.json();
      return body.payment.state === "AUTHORIZED" ? body : null;
    });
    assert.ok(authorized.attempt.paypalOrderId, "PayPal Order id must be recorded");
    // 페이지 자체 폴링 주기와 경합하지 않도록 서버 확정 후 뷰를 재구성한다.
    await page.reload({ waitUntil: "networkidle" });
    await page.getByText("PayPal 승인이 확정되었습니다.", { exact: false }).waitFor({ timeout: 60_000 });
    await shot("authorization-confirmed");

    // 운영자: 완료 체크(PLACED) → 배송 일괄 처리 → terminal COMPLETED.
    operatorContext = await browser.newContext({ locale: "ko-KR" });
    const operator = await operatorContext.newPage();
    const operatorSession = await operator.request.post(
      `${baseURL}/api/v1/dev/auth/session`,
      { data: { profileKey: "empty-operator" } },
    );
    assert.equal(operatorSession.status(), 201, await operatorSession.text());
    const queueItem = await waitFor("procurement task QUEUED", async () => {
      const response = await operator.request.get(`${baseURL}/api/v1/admin/procurement/queue`);
      if (response.status() !== 200) return null;
      const body = await response.json();
      return body.items.find(
        (item) => item.task.agencyOrderId === agencyOrderID
          && item.task.state === "QUEUED",
      ) ?? null;
    });
    const unitProduct = queueItem.agencyOrder.lines.find(
      (line) => line.shopDomain === queueItem.merchantOrder.shopDomain,
    ).productTitle;
    await operator.goto(`${baseURL}/admin/agencyOrder`, { waitUntil: "networkidle" });
    // 기본은 내 담당·조달이다(운영정합 2차 P5) — 새 주문은 미할당에서 집는다.
    await operator.getByRole("button", { name: /미할당 \d/ }).click();
    const readyRow = operator.getByRole("button", { name: new RegExp(unitProduct) });
    await readyRow.waitFor({ timeout: 30_000 });
    await readyRow.click();
    await operator.getByRole("button", { name: "내가 맡기" }).click();
    // 담당하면 미할당 범위를 떠난다 — 내 담당 범위에서 이어간다(ADR-0057 §6).
    await operator.getByRole("button", { name: /내 담당 1/ }).click();
    const runningRow = operator.getByRole("button", { name: new RegExp(unitProduct) });
    await runningRow.waitFor({ timeout: 30_000 });
    await runningRow.click();
    // 열람 사유는 프리필되지 않는다(2차 P6).
    await operator.getByLabel("배송정보 열람 사유").fill("PayPal conformance 배송지 확인");
    await operator.getByRole("button", { name: "배송정보 감사 열람" }).click();
    assert.equal(
      await operator.getByLabel("판단", { exact: true }).inputValue(),
      "WITHIN_AUTHORIZATION",
    );
    assert.equal(await operator.getByLabel("관찰한 merchant 조건").count(), 0);
    await operator.getByRole("button", { name: "주문 처리 시작" }).click();
    // PayPal Sandbox도 Live와 같은 구매 증거 shape를 TEST evidence로 남긴 뒤
    // PLACED가 된다. 이후 같은 카드가 배송 stage로 넘어간다.
    await operator.getByLabel("TEST merchant 주문 참조").fill(`TEST-${queueItem.task.id}`);
    await operator.getByLabel("영수증 안전 참조").fill(`receipt:${queueItem.task.id}`);
    await operator.getByLabel("TEST 기입액(cent)").fill(String(
      queueItem.merchantOrder.checkoutSnapshot.authoritativeTotal.amountMinor,
    ));
    await operator.getByLabel("구매 증거 해시").fill(
      "sha256:paypal-sandbox-conformance-evidence",
    );
    await operator.getByLabel("구매 관찰 시각").fill("2026-01-15T12:00");
    await operator.getByRole("button", { name: "TEST 구매 증거 기록" }).click();
    await operator.getByRole("button", { name: /^배송 \d/ }).click();
    const placedRow = operator.getByRole("button", { name: new RegExp(unitProduct) });
    await placedRow.waitFor({ timeout: 30_000 });
    await placedRow.click();
    await operator.getByRole("button", { name: /남은 단계 일괄 처리했다 치기/ }).click();
    const completed = await waitFor("terminal COMPLETED", async () => {
      const response = await page.request.get(`${baseURL}/api/v1/agencyOrder/${agencyOrderID}`);
      if (response.status() !== 200) return null;
      const projection = (await response.json()).agencyOrder;
      return projection.process?.state === "TERMINAL"
        && ["COMPLETED_ALL", "COMPLETED_PARTIAL"].includes(projection.process?.terminalReason)
        ? projection
        : null;
    });
    const completedMO = completed.merchantOrders.find(
      (merchantOrder) => merchantOrder.id === queueItem.merchantOrder.id,
    );
    assert.equal(completedMO?.fundingState, "ACTIVE");
    assert.ok(completed.payment?.captureId, "MO Capture id must be projected after Procurement starts");
    await shot("terminal-completed");

    // 사용자: 구매 뒤 정당 사유로 이 Shop의 whole-MO 환불을 요청한다.
    await page.goto(`${baseURL}/agencyOrder/${agencyOrderID}`, { waitUntil: "networkidle" });
    await page.getByLabel(`${queueItem.merchantOrder.shopDomain} 환불 사유`)
      .selectOption("OTHER_SERVICE_FAULT");
    await page.getByLabel("이 Shop 결제 단위에 무슨 일이 있었나요?")
      .fill("Sandbox conformance 검증 중 확인한 서비스 과실로 MO 전체 환불을 요청합니다.");
    await page.getByRole("button", { name: /MO 전체 환불 요청/ }).click();
    await page.getByText("이 Shop 결제 단위 전체의 환불 심사를 요청했습니다.", { exact: false })
      .waitFor({ timeout: 30_000 });
    await shot("refund-requested");

    // 운영자: 고객 공개 근거를 남겨 whole-MO 승인 → sweeper가 해당 Capture의
    // exact gross를 실 PayPal Sandbox Refund API로 반환한다.
    await operator.goto(`${baseURL}/admin/agencyOrder/exceptions`, { waitUntil: "networkidle" });
    await operator.getByLabel("고객 공개 근거").fill(
      "배송 완료 뒤 접수된 서비스 과실 근거를 확인하여 이 MerchantOrder 전체 환불을 승인합니다.",
    );
    await operator.getByRole("button", { name: /MO 전체 환불 승인/ }).click();
    const refunded = await waitFor("whole-MO refund SUCCEEDED", async () => {
      const response = await page.request.get(`${baseURL}/api/v1/agencyOrder/${agencyOrderID}`);
      if (response.status() !== 200) return null;
      const projection = (await response.json()).agencyOrder;
      const merchantOrder = projection.merchantOrders.find(
        (item) => item.id === queueItem.merchantOrder.id,
      );
      const request = (projection.refundRequests ?? []).find(
        (item) => item.merchantOrderId === queueItem.merchantOrder.id,
      );
      return merchantOrder?.compensationState === "SUCCEEDED"
        && request?.state === "RESOLVED" && request.decision === "APPROVED"
        && (projection.payment?.refundedTotalCent ?? 0) > 0
        ? projection
        : null;
    });
    await page.reload({ waitUntil: "networkidle" });
    await shot("refund-completed");
    const approvedRequest = refunded.refundRequests.find(
      (request) => request.merchantOrderId === queueItem.merchantOrder.id,
    );
    assert.equal(
      refunded.payment.refundedTotalCent,
      approvedRequest.requestedGrossAmount.amountMinor,
      "PayPal refund must equal the immutable whole-MO gross",
    );
    assert.equal(
      refunded.payment.refundedTotalCent,
      completedMO.customerGrossAmount.amountMinor,
      "PayPal refund must equal the captured MerchantOrder gross",
    );

    console.log("PayPal Sandbox conformance: PASS");
    console.log(JSON.stringify({
      agencyOrderID,
      paypalOrderID: authorized.attempt.paypalOrderId,
      captureID: completed.payment.captureId,
      refundedTotalCent: refunded.payment.refundedTotalCent,
      refundRequests: (refunded.refundRequests ?? []).map(({ state, decision, requestedGrossAmount }) => ({
        state,
        decision,
        requestedGrossAmount,
      })),
    }, null, 2));
  } finally {
    await operatorContext?.close().catch(() => undefined);
    await context.close().catch(() => undefined);
    await browser.close().catch(() => undefined);
  }
}

run().then(
  () => process.exit(0),
  (error) => {
    console.error("PayPal Sandbox conformance: FAIL");
    console.error(error);
    process.exit(1);
  },
);
