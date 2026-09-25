const { firefox, request } = require("playwright");
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");

const baseURL = (
  process.env.E2E_BASE_URL ?? "http://127.0.0.1:18080"
).replace(/\/$/, "");
const screenshotDir = process.env.E2E_SCREENSHOT_DIR ??
  path.join(os.tmpdir(), "vitlane-operator-api-usage");

// Firefox는 연속 페이지 이동이 진행 중인 폰트 fetch를 끊으면 NS_BINDING_ABORTED
// (status=2152398850)를 console error로 남긴다. 앱 결함이 아니므로 zero-console-error
// 단언에서 이 abort만 제외한다. 폰트 404 등 다른 실패 status는 계속 수집한다.
const isAbortedFontDownload = (text) =>
  text.includes("downloadable font: download failed") &&
  text.includes("status=2152398850");

async function run() {
  const browser = await firefox.launch({ headless: true });
  const errors = [];
  try {
    await fs.mkdir(screenshotDir, { recursive: true });

    const regularUser = await request.newContext({ baseURL });
    const regularLogin = await regularUser.post("/api/v1/dev/auth/session", {
      data: { profileKey: "empty-user" },
    });
    assert.equal(regularLogin.status(), 201, await regularLogin.text());
    const forbidden = await regularUser.get(
      "/api/v1/admin/managed-runner/usage?days=7",
    );
    assert.equal(forbidden.status(), 403, await forbidden.text());
    await regularUser.dispose();

    const reviewContext = await browser.newContext({
      locale: "ko-KR",
      viewport: { width: 1440, height: 1000 },
    });
    const reviewPage = await reviewContext.newPage();
    reviewPage.on("console", (message) => {
      if (message.type() === "error" && !isAbortedFontDownload(message.text())) {
        errors.push(message.text());
      }
    });
    reviewPage.on("pageerror", (error) => errors.push(error.message));
    await reviewPage.goto(`${baseURL}/admin/api-usage`, {
      waitUntil: "domcontentloaded",
    });
    const operatorProfile = reviewPage
      .locator(".catalog-ui-login__profile")
      .filter({ hasText: "빈 운영자 사용자" });
    await operatorProfile
      .getByRole("button", { name: "이 프로필로 시작" })
      .click();
    // 운영자 홈은 주문 처리다(운영정합 3차 #1).
    await reviewPage.waitForURL((url) => url.pathname === "/admin/agencyOrder");
    await reviewPage.locator(
      '.account-ui-section-tabs a[href="/admin/agencyOrder"][aria-current="page"]',
    ).waitFor();
    await reviewPage.goto(`${baseURL}/`, { waitUntil: "networkidle" });
    try {
      await reviewPage
        .getByRole("heading", { name: "무엇을 찾고 있나요?" })
        .waitFor();
    } catch (error) {
      throw new Error(
        `채팅 홈 진입 실패 (${reviewPage.url()}): ${
          await reviewPage.locator("body").innerText()
        }\n${errors.join("\n")}`,
        { cause: error },
      );
    }
    assert.equal(
      await reviewPage.getByText(
        "찾고 싶은 상품을 Curation으로 저장한 뒤, Planning에서 조사 항목과 다음 행동을 정합니다.",
        { exact: true },
      ).count(),
      0,
    );

    const settingsTrigger = reviewPage.getByRole('button', { name: '예산 설정', exact: true });
    await settingsTrigger.click();
    const autoSwitch = reviewPage.getByRole("switch", {name:"자동 큐레이션",exact:true});
    assert.equal(await autoSwitch.getAttribute("aria-checked"), "true");
    assert.equal(await reviewPage.locator("#curation-currency").count(), 0);
    await autoSwitch.click();
    const currency = reviewPage.locator('#curation-currency');
    assert.equal(await currency.inputValue(), 'KRW');
    assert.deepEqual(await currency.locator('option').evaluateAll(options=>options.map(option=>option.value).sort()), ['KRW','USD']);
    assert.equal(await reviewPage.locator('#purchase-environment, [aria-label="상품 항목 구성"]').count(), 0);
    assert.equal(await reviewPage.locator('[data-slot="dialog-overlay"]').count(), 0);
    await reviewPage.locator('.init-settings').getByRole('button', { name: '제한 없음', exact:true }).waitFor();
    await reviewPage.screenshot({
      path: path.join(screenshotDir, "home-settings-firefox.png"),
      fullPage: true,
    });

    await reviewPage.keyboard.press("Escape");
    await reviewPage.getByRole("link", { name: "운영자" }).click();
    await reviewPage.waitForURL(`${baseURL}/admin/agencyOrder`);
    const activeOpsLink = reviewPage.locator(
      '.account-ui-section-tabs a[href="/admin/agencyOrder"][aria-current="page"]',
    );
    await activeOpsLink.waitFor();
    assert.equal(await activeOpsLink.count(), 1);

    await reviewPage.getByRole("button", { name: "프로필 메뉴" }).click();
    const profileMenu = reviewPage.locator(
      '[data-slot="dropdown-menu-content"]',
    );
    await profileMenu.waitFor();
    assert.equal(await profileMenu.getAttribute("data-side"), "top");
    await reviewPage
      .locator('[data-slot="dropdown-menu-item"]')
      .filter({ hasText: "화면 설정" })
      .click();
    await reviewPage.getByLabel("화면 설정").waitFor();

    const sidebar = reviewPage.locator(".shell-product-sidebar");
    const neutralSidebarColor = await assertColorToken(
      sidebar,
      "backgroundColor",
      "--vt-shell-sidebar-background",
    );
    await reviewPage.locator(
      '[data-slot="toggle-group-item"][aria-label="Water 강조색"]',
    ).click();
    const blueSidebarColor = await assertColorToken(
      sidebar,
      "backgroundColor",
      "--vt-shell-sidebar-background",
    );
    assert.equal(blueSidebarColor, neutralSidebarColor);
    await reviewPage.locator(
      '[data-slot="toggle-group-item"][aria-label="Ink 강조색"]',
    ).click();
    assert.equal(
      await assertColorToken(
        sidebar,
        "backgroundColor",
        "--vt-shell-sidebar-background",
      ),
      neutralSidebarColor,
    );
    await reviewPage.screenshot({
      path: path.join(
        screenshotDir,
        "operator-appearance-black-firefox.png",
      ),
      fullPage: true,
    });

    await reviewPage.locator(
      '[data-slot="toggle-group-item"][aria-label="다크 모드"]',
    ).click();
    const darkCanvasColor = await assertColorToken(
      reviewPage.locator("body"),
      "backgroundColor",
      "--vt-semantic-color-surface-canvas",
    );
    assert.equal(
      await assertColorToken(
        sidebar,
        "backgroundColor",
        "--vt-shell-sidebar-background",
      ),
      darkCanvasColor,
    );
    const sidebarBrand = sidebar.locator(".vt-brand-mark--inverted");
    const neutralBrandColor = await assertColorToken(
      sidebarBrand,
      "color",
      "--vt-foundation-color-clear",
    );

    await reviewPage.locator(
      '[data-slot="toggle-group-item"][aria-label="Water 강조색"]',
    ).click();
    assert.equal(
      await assertColorToken(
        reviewPage.locator("body"),
        "backgroundColor",
        "--vt-semantic-color-surface-canvas",
      ),
      darkCanvasColor,
    );
    assert.equal(
      await assertColorToken(
        sidebar,
        "backgroundColor",
        "--vt-shell-sidebar-background",
      ),
      darkCanvasColor,
    );
    const blueBrandColor = await assertColorToken(
      sidebarBrand,
      "color",
      "--vt-foundation-color-clear",
    );
    assert.equal(blueBrandColor, neutralBrandColor);
    await reviewPage.screenshot({
      path: path.join(
        screenshotDir,
        "operator-appearance-dark-firefox.png",
      ),
      fullPage: true,
    });
    await reviewContext.close();

    const context = await browser.newContext({
      locale: "ko-KR",
      viewport: { width: 1440, height: 1100 },
    });
    const page = await context.newPage();
    page.on("console", (message) => {
      if (message.type() === "error" && !isAbortedFontDownload(message.text())) {
        errors.push(message.text());
      }
    });
    page.on("pageerror", (error) => errors.push(error.message));

    const login = await page.request.post(
      `${baseURL}/api/v1/dev/auth/session`,
      { data: { profileKey: "empty-operator" } },
    );
    assert.equal(login.status(), 201, await login.text());

    const usageResponse = page.waitForResponse((response) =>
      response.url().endsWith("/api/v1/admin/managed-runner/usage?days=30") &&
      response.status() === 200
    );
    await page.goto(`${baseURL}/admin/api-usage`, {
      waitUntil: "domcontentloaded",
    });
    const response = await usageResponse;
    const usage = await response.json();
    assert.equal(usage.timezone, "UTC");
    assert.equal(usage.days.length, 30);
    assert.ok(usage.totals.totalTokens > 0);
    assert.equal(
      response.headers()["cache-control"],
      "no-store",
    );

    await page.getByRole("heading", { name: "API 사용량", exact: true }).waitFor();
    assert.equal(
      await page.locator('a[href="/admin/api-usage"][aria-current="page"]').count(),
      1,
    );
    assert.equal(
      await page.getByRole("img", { name: "일별 API 토큰과 비용 추이" }).count(),
      1,
    );
    assert.equal(await page.locator(".product-ui-api-usage__ledger tbody tr").count(), 30);
    for (const label of [
      "오늘 토큰",
      "오늘 정산 비용",
      "오늘 예산 점유",
      "30일 API 호출",
    ]) {
      assert.equal(await page.getByText(label, { exact: true }).count(), 1);
    }
    await page.screenshot({
      path: path.join(screenshotDir, "operator-api-usage-firefox.png"),
      fullPage: true,
    });

    const sevenDayResponse = page.waitForResponse((nextResponse) =>
      nextResponse.url().endsWith(
        "/api/v1/admin/managed-runner/usage?days=7",
      )
    );
    const sevenDayToggle = page.locator(
      '[data-slot="toggle-group-item"][aria-label="7일 조회"]',
    );
    assert.equal(await sevenDayToggle.count(), 1);
    await sevenDayToggle.click();
    const sevenDayResult = await sevenDayResponse;
    assert.equal(
      sevenDayResult.status(),
      200,
      await sevenDayResult.text(),
    );
    assert.equal((await sevenDayResult.json()).days.length, 7);
    await page.getByText("7일 API 호출", { exact: true }).waitFor();
    assert.equal(await page.locator(".product-ui-api-usage__ledger tbody tr").count(), 7);
    assert.deepEqual(errors, []);

    await context.close();
    console.log(JSON.stringify({
      result: "PASS",
      suite: "operator-api-usage",
      days: usage.days.length,
      screenshot: path.join(
        screenshotDir,
        "operator-api-usage-firefox.png",
      ),
    }));
  } finally {
    await browser.close();
  }
}

async function assertColorToken(locator, property, token) {
  const colors = await locator.evaluate(
    (element, input) => {
      const probe = document.createElement("div");
      probe.style[input.property] = `var(${input.token})`;
      document.body.append(probe);
      const actual = getComputedStyle(element)[input.property];
      const expected = getComputedStyle(probe)[input.property];
      probe.remove();
      return { actual, expected };
    },
    { property, token },
  );
  assert.equal(colors.actual, colors.expected);
  return colors.actual;
}

run().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
