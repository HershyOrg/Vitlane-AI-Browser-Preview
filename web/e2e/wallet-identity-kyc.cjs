const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { firefox } = require("playwright");

const baseURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:8080";
const screenshotDir = process.env.E2E_SCREENSHOT_DIR ?? "/tmp";

async function main() {
  fs.mkdirSync(screenshotDir, { recursive: true });
  const browser = await firefox.launch({ headless: true });
  const context = await browser.newContext({
    baseURL,
    locale: "ko-KR",
    viewport: { width: 1440, height: 1000 },
  });
  let page = await context.newPage();
  const failures = [];
  let agentConnectionRemovalRequests = 0;

  const observePage = (observedPage) => {
    observedPage.on("console", (message) => {
      if (message.type() === "error") failures.push(`console: ${message.text()}`);
    });
    observedPage.on("pageerror", (error) => (
      failures.push(`page: ${error.message}`)
    ));
    observedPage.on("request", (request) => {
      if (
        request.method() === "POST"
        && /\/api\/v1\/agent-connections\/[^/]+\/revoke$/.test(
          new URL(request.url()).pathname,
        )
      ) {
        agentConnectionRemovalRequests += 1;
      }
    });
  };
  observePage(page);

  try {
    const login = await context.request.post(
      `${baseURL}/api/v1/dev/auth/session`,
      { data: { profileKey: "empty-user" } },
    );
    assert.equal(login.status(), 201, await login.text());

    await page.goto(`${baseURL}/account`, { waitUntil: "networkidle" });
    await page.getByRole("heading", { name: "계정", exact: true }).waitFor();
    const registerButton = page.getByRole("button", {
      name: "지갑 등록",
      exact: true,
    });
    await registerButton.waitFor();

    await page.evaluate(() => {
      window.__vitlaneReviewWallet.setChainId("0x1");
      window.__vitlaneReviewWallet.rejectNext(
        "wallet_switchEthereumChain",
        4001,
      );
    });
    await registerButton.click();
    await page.getByText("지갑 네트워크 전환이 취소되었습니다", {
      exact: false,
    }).waitFor();
    assert.equal((await overview(context)).wallets.length, 0);

    let committedRegistration;
    let completionReplayURL;
    let completionReplayBody;
    let resolveCommitted;
    const committed = new Promise((resolve) => {
      resolveCommitted = resolve;
    });
    await page.route(
      "**/api/v1/account/wallet-registration-attempts/*/complete",
      async (route) => {
        const response = await route.fetch();
        const responseText = await response.text();
        assert.equal(response.status(), 200, responseText);
        committedRegistration = JSON.parse(responseText);
        completionReplayURL = route.request().url();
        completionReplayBody = route.request().postDataJSON();
        await route.abort("failed");
        resolveCommitted();
      },
      { times: 1 },
    );
    await registerButton.click();
    await withTimeout(
      committed,
      30_000,
      "지갑 등록 complete 응답을 중단한 뒤 commit을 관찰하지 못했습니다.",
    );
    const firstWallet = committedRegistration.latestWallet.wallet;
    assert.equal(firstWallet.registrationStatus, "REGISTERED");
    assert.equal(committedRegistration.latestWallet.ownership.status, "VALID");

    const replayResponse = await context.request.post(completionReplayURL, {
      data: completionReplayBody,
    });
    const replayText = await replayResponse.text();
    assert.equal(replayResponse.status(), 200, replayText);
    assert.equal(JSON.parse(replayText).replay, true);

    await page.reload({ waitUntil: "networkidle" });
    assert.equal((await overview(context)).wallets.length, 1);
    assert.equal(
      await page.getByRole("button", { name: "지갑 등록", exact: true }).count(),
      0,
      "현재 Wallet이 있으면 두 번째 Wallet 등록 action을 숨겨야 한다.",
    );

    const secondAddress = await page.evaluate(
      () => window.__vitlaneReviewWallet.accounts[1],
    );
    const rejectedSecond = await context.request.post(
      `${baseURL}/api/v1/account/wallet-registration-attempts`,
      {
        data: {
          address: secondAddress,
          chainId: "eip155:91342",
          clientOperationId: "reject-second-current-wallet:e2e",
        },
      },
    );
    const rejectedSecondText = await rejectedSecond.text();
    assert.equal(rejectedSecond.status(), 409, rejectedSecondText);
    assert.equal(
      JSON.parse(rejectedSecondText).error.code,
      "WALLET_ALREADY_REGISTERED",
    );

    let currentWalletRow = page.locator(".account-ui-wallet-row").first();
    const interruptedKYC = await startMockKYC(page, currentWalletRow);
    assert.equal(interruptedKYC.walletId, firstWallet.id);
    assert.equal(interruptedKYC.state, "PENDING_PROVIDER");

    const deregistrationURL =
      `${baseURL}/api/v1/account/wallets/${firstWallet.id}/deregister`;
    const { committed: deregistrationCommitted } =
      await abortCommittedResponseOnce(page, deregistrationURL, 204);
    page.once("dialog", (dialog) => dialog.accept());
    await currentWalletRow
      .getByRole("button", { name: "지갑 등록 해제" })
      .click();
    await withTimeout(
      deregistrationCommitted,
      30_000,
      "지갑 등록 해제 응답을 중단한 뒤 commit을 관찰하지 못했습니다.",
    );
    assert.equal((await overview(context)).wallets.length, 0);

    const deregistrationReplay = await context.request.post(
      deregistrationURL,
      { data: {} },
    );
    assert.equal(
      deregistrationReplay.status(),
      204,
      await deregistrationReplay.text(),
    );
    const stoppedKYC = await context.request.post(
      `${baseURL}/api/v1/account/kyc-cases/${interruptedKYC.id}/check`,
      {
        data: {
          clientOperationId: "wallet-deregistered-kyc-check:e2e",
        },
      },
    );
    const stoppedKYCText = await stoppedKYC.text();
    assert.equal(stoppedKYC.status(), 409, stoppedKYCText);
    assert.equal(
      JSON.parse(stoppedKYCText).error.code,
      "KYC_VERIFICATION_STATE_INVALID",
    );
    assert.equal(agentConnectionRemovalRequests, 0);

    await page.reload({ waitUntil: "networkidle" });
    await page.getByText("아직 등록된 지갑이 없습니다", {
      exact: true,
    }).waitFor();
    await page.screenshot({
      path: path.join(screenshotDir, "wallet-deregistered-firefox.png"),
      fullPage: true,
    });

    await page.close();
    page = await context.newPage();
    observePage(page);
    await page.goto(`${baseURL}/account`, { waitUntil: "networkidle" });
    await page.getByRole("heading", { name: "계정", exact: true }).waitFor();
    const reregisteredResponse = page.waitForResponse((response) => (
      response.request().method() === "POST"
      && response.url().endsWith("/complete")
    ));
    await page.getByRole("button", {
      name: "지갑 등록",
      exact: true,
    }).click();
    const reregistered = await reregisteredResponse;
    assert.equal(reregistered.status(), 200, await reregistered.text());
    const restoredOverview = await overview(context);
    assert.equal(restoredOverview.wallets.length, 1);
    assert.equal(
      restoredOverview.wallets[0].wallet.id,
      firstWallet.id,
      "같은 canonical account 재등록은 같은 Wallet ID를 복구해야 한다.",
    );

    currentWalletRow = page.locator(".account-ui-wallet-row").first();
    await currentWalletRow
      .getByRole("button", { name: "신원 확인 시작" })
      .waitFor();
    await completeMockKYC(page, currentWalletRow);
    await page.screenshot({
      path: path.join(
        screenshotDir,
        "wallet-identity-kyc-registration-firefox.png",
      ),
      fullPage: true,
    });

    page.once("dialog", (dialog) => dialog.accept());
    const cleanDeregister = page.waitForResponse((response) => (
      response.request().method() === "POST"
      && response.url().endsWith(`/wallets/${firstWallet.id}/deregister`)
    ));
    await currentWalletRow
      .getByRole("button", { name: "지갑 등록 해제" })
      .click();
    assert.equal((await cleanDeregister).status(), 204);
    await page.evaluate(() => {
      window.__vitlaneReviewWallet.setAccount(1);
    });
    const replacementRegisterButton = page.getByRole("button", {
      name: "지갑 등록",
      exact: true,
    });
    await replacementRegisterButton.waitFor();
    const replacementRegistration = page.waitForResponse((response) => (
      response.request().method() === "POST"
      && response.url().endsWith("/complete")
    ));
    await replacementRegisterButton.click();
    const replacementResponse = await replacementRegistration;
    const replacementText = await replacementResponse.text();
    assert.equal(replacementResponse.status(), 200, replacementText);
    const replacementBody = JSON.parse(replacementText);
    const replacementOverview = await overview(context);
    assert.equal(replacementOverview.wallets.length, 1);
    assert.equal(
      replacementOverview.wallets[0].wallet.id,
      replacementBody.latestWallet.wallet.id,
    );
    assert.notEqual(replacementOverview.wallets[0].wallet.id, firstWallet.id);
    assert.equal(replacementOverview.wallets[0].wallet.isDefault, true);

    assert.deepEqual(failures, []);
    console.log(JSON.stringify({
      result: "PASS",
      suite: "single-wallet-kyc",
      screenshots: screenshotDir,
      checks: [
        "one current Wallet is exposed and a second registration is rejected",
        "committed registration and deregistration response loss replay idempotently",
        "deregistration cancels in-flight KYC without removing Agent Connections",
        "same canonical account reuses its historical Wallet ID",
        "replacement requires explicit deregistration before registration",
      ],
    }, null, 2));
  } finally {
    await browser.close();
  }
}

async function startMockKYC(page, walletRow) {
  const startResponse = page.waitForResponse((response) => (
    response.request().method() === "POST"
    && /\/api\/v1\/account\/wallets\/[^/]+\/kyc-cases$/.test(
      new URL(response.url()).pathname,
    )
  ));
  await walletRow
    .getByRole("button", { name: "신원 확인 시작" })
    .click();
  const response = await startResponse;
  const responseText = await response.text();
  assert.equal(response.status(), 201, responseText);
  const result = JSON.parse(responseText);
  await walletRow
    .getByRole("button", { name: "신원 확인 결과" })
    .waitFor();
  return result.kycCase;
}

async function completeMockKYC(page, walletRow) {
  const started = await startMockKYC(page, walletRow);
  const checkResponse = page.waitForResponse((response) => (
    response.request().method() === "POST"
    && response.url().includes(`/api/v1/account/kyc-cases/${started.id}/check`)
  ));
  await walletRow
    .getByRole("button", { name: "신원 확인 결과" })
    .click();
  const response = await checkResponse;
  const responseText = await response.text();
  assert.equal(response.status(), 200, responseText);
  const result = JSON.parse(responseText);
  assert.equal(result.kycCase.state, "VERIFIED");
  assert.equal(result.credential.externalEffect, "SIMULATED");
  assert.equal(result.observation.status, "VALID");
  return result;
}

async function overview(context) {
  const response = await context.request.get(
    `${baseURL}/api/v1/account/overview`,
  );
  const responseText = await response.text();
  assert.equal(response.status(), 200, responseText);
  return JSON.parse(responseText).account;
}

async function abortCommittedResponseOnce(page, url, expectedStatus) {
  let resolveCommitted;
  let rejectCommitted;
  const committed = new Promise((resolve, reject) => {
    resolveCommitted = resolve;
    rejectCommitted = reject;
  });
  await page.route(url, async (route) => {
    try {
      const response = await route.fetch();
      const responseText = await response.text();
      assert.equal(response.status(), expectedStatus, responseText);
      await route.abort("failed");
      resolveCommitted(response);
    } catch (error) {
      rejectCommitted(error);
      await route.abort("failed").catch(() => {});
    }
  }, { times: 1 });
  return { committed };
}

async function withTimeout(promise, timeoutMs, message) {
  let timeout;
  try {
    return await Promise.race([
      promise,
      new Promise((_, reject) => {
        timeout = setTimeout(() => reject(new Error(message)), timeoutMs);
      }),
    ]);
  } finally {
    clearTimeout(timeout);
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
