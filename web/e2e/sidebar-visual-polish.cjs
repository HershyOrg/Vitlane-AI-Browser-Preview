const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { firefox } = require("playwright");

const baseURL = (process.env.E2E_BASE_URL ?? "http://127.0.0.1:4179").replace(/\/$/, "");
const curationPath = "/curations/e5100000-0000-4000-8000-000000000002";
const evidenceDir = process.env.SIDEBAR_EVIDENCE_DIR ?? "/tmp/vitlane-sidebar-visual";

async function run() {
  fs.mkdirSync(evidenceDir, { recursive: true });
  const browser = await firefox.launch({ headless: true });
  const context = await browser.newContext({
    locale: "ko-KR",
    viewport: { width: 1440, height: 960 },
  });
  const page = await context.newPage();
  const pageErrors = [];
  const preferenceStatuses = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (new URL(response.url()).pathname === "/api/v1/me/preferences") {
      preferenceStatuses.push(response.status());
    }
  });
  await page.addInitScript(() => {
    window.localStorage.setItem(
      "vitlane.appearance.v2",
      JSON.stringify({ theme: "dark", accent: "blue" }),
    );
  });

  try {
    await page.goto(
      `${baseURL}/login?returnTo=${encodeURIComponent(curationPath)}`,
      { waitUntil: "networkidle" },
    );
    await page.waitForURL(`**${curationPath}`);

    const sidebar = page.locator(".shell-product-sidebar");
    const primaryLink = page.locator(".shell-product-sidebar__primary-link").first();
    const currentCuration = page.locator(".shell-product-sidebar__list li.is-current");
    const currentCurationLink = currentCuration.locator(".shell-product-sidebar__curation");
    const profileTrigger = page.getByRole("button", { name: "프로필 메뉴" });
    await profileTrigger.waitFor();
    assert.ok(preferenceStatuses.includes(200));
    assert.equal(await page.locator(".shell-preferences-alert").count(), 0);

    const expanded = await sidebar.evaluate((element) => {
      const header = element.querySelector(".shell-product-sidebar__header");
      const brandSymbol = element.querySelector(".shell-product-sidebar__brand .vt-brand-mark__symbol");
      const brandSymbolRect = brandSymbol?.getBoundingClientRect();
      const avatar = element.querySelector(".shell-user-menu__avatar");
      const avatarRect = avatar?.getBoundingClientRect();
      const primaryIcon = element.querySelector(".shell-product-sidebar__primary-icon");
      const brandName = element.querySelector(".shell-product-sidebar__brand .vt-brand-mark__name");
      const brandNameRect = brandName?.getBoundingClientRect();
      const primaryIconRect = primaryIcon?.getBoundingClientRect();
      const historyTitle = element.querySelector(".shell-product-sidebar__title");
      return {
        brandSymbolDisplay: brandSymbol ? getComputedStyle(brandSymbol).display : null,
        brandSymbolLeft: brandSymbolRect?.left,
        brandSymbolWidth: brandSymbolRect?.width,
        brandName: brandName?.textContent?.trim(),
        brandWeight: brandName
          ? getComputedStyle(brandName).fontWeight
          : null,
        brandSize: brandName
          ? getComputedStyle(brandName).fontSize
          : null,
        brandNameLeft: brandNameRect?.left,
        primaryIconLeft: primaryIconRect?.left,
        historyTitle: historyTitle?.textContent?.trim().replace(/\s+/g, " "),
        historyEyebrowCount: historyTitle?.querySelectorAll("p").length,
        historyBadgeCount: historyTitle?.querySelectorAll("small").length,
        headerBorderBottomWidth: header ? getComputedStyle(header).borderBottomWidth : null,
        profileChevronCount: element.querySelectorAll(".phase6-sidebar-profile__chevron").length,
        avatarWidth: avatarRect?.width,
        avatarHeight: avatarRect?.height,
        avatarRadius: avatar ? getComputedStyle(avatar).borderRadius : null,
        iconStrokeWidth: primaryIcon ? getComputedStyle(primaryIcon).strokeWidth : null,
      };
    });

    assert.notEqual(expanded.brandSymbolDisplay, "none");
    assert.ok(expanded.brandSymbolWidth > 0);
    assert.equal(expanded.brandName, "Vitlane");
    assert.equal(expanded.brandWeight, "400");
    assert.equal(expanded.brandSize, "20px");
    assert.ok(
      Math.abs(expanded.brandSymbolLeft - expanded.primaryIconLeft) < 0.5,
      `brandSymbolLeft=${expanded.brandSymbolLeft}, primaryIconLeft=${expanded.primaryIconLeft}`,
    );
    assert.equal(expanded.historyTitle, "Curations 1");
    assert.equal(expanded.historyEyebrowCount, 0);
    assert.equal(expanded.historyBadgeCount, 0);
    assert.equal(expanded.headerBorderBottomWidth, "0px");
    assert.equal(expanded.profileChevronCount, 0);
    assert.ok(Math.abs(expanded.avatarWidth - expanded.avatarHeight) < 0.5);
    assert.equal(expanded.avatarRadius, "50%");
    assert.equal(expanded.iconStrokeWidth, "1.35px");

    const currentStyle = await currentCuration.evaluate((element) => {
      const style = getComputedStyle(element);
      return {
        backgroundColor: style.backgroundColor,
        borderInlineStartColor: style.borderInlineStartColor,
        borderInlineStartWidth: style.borderInlineStartWidth,
      };
    });
    assert.equal(currentStyle.backgroundColor, "rgba(0, 0, 0, 0)");
    assert.notEqual(currentStyle.borderInlineStartColor, "rgba(0, 0, 0, 0)");
    assert.notEqual(currentStyle.borderInlineStartWidth, "0px");

    await currentCuration.hover();
    const curationHoverStyle = await currentCuration.evaluate((element) => {
      const style = getComputedStyle(element);
      return {
        backgroundColor: style.backgroundColor,
        borderInlineStartColor: style.borderInlineStartColor,
        borderInlineStartWidth: style.borderInlineStartWidth,
      };
    });
    assert.equal(curationHoverStyle.backgroundColor, "rgba(0, 0, 0, 0)");
    assert.equal(
      curationHoverStyle.borderInlineStartColor,
      currentStyle.borderInlineStartColor,
    );
    assert.notEqual(curationHoverStyle.borderInlineStartWidth, "0px");

    await page.mouse.move(1000, 400);
    await currentCurationLink.focus();
    const curationFocusStyle = await currentCurationLink.evaluate((element) => {
      const style = getComputedStyle(element);
      const parentStyle = getComputedStyle(element.parentElement);
      return {
        backgroundColor: parentStyle.backgroundColor,
        outlineStyle: style.outlineStyle,
        outlineWidth: style.outlineWidth,
      };
    });
    assert.equal(curationFocusStyle.backgroundColor, "rgba(0, 0, 0, 0)");
    assert.equal(curationFocusStyle.outlineStyle, "solid");
    assert.notEqual(curationFocusStyle.outlineWidth, "0px");

    await primaryLink.hover();
    await page.waitForTimeout(250);
    const hoverStyle = await primaryLink.evaluate((element) => {
      const style = getComputedStyle(element);
      return {
        backgroundColor: style.backgroundColor,
        borderColor: style.borderColor,
        boxShadow: style.boxShadow,
        color: style.color,
        outlineStyle: style.outlineStyle,
      };
    });
    const idleColor = await page.locator(".shell-product-sidebar__primary").evaluate(
      (element) => getComputedStyle(element).color,
    );
    assert.notEqual(hoverStyle.backgroundColor, "rgba(0, 0, 0, 0)");
    assert.match(hoverStyle.borderColor, /rgba\([^)]*, 0\)|transparent/);
    assert.equal(hoverStyle.boxShadow, "none");
    assert.equal(hoverStyle.color, idleColor);
    assert.equal(hoverStyle.outlineStyle, "none");

    await page.mouse.move(1000, 400);
    await primaryLink.focus();
    await page.waitForTimeout(250);
    const focusStyle = await primaryLink.evaluate((element) => {
      const style = getComputedStyle(element);
      return {
        backgroundColor: style.backgroundColor,
        borderColor: style.borderColor,
        boxShadow: style.boxShadow,
        color: style.color,
        outlineStyle: style.outlineStyle,
      };
    });
    assert.deepEqual(focusStyle, hoverStyle);

    await page.screenshot({
      path: path.join(evidenceDir, "sidebar-expanded.png"),
      fullPage: false,
    });

    await page.getByRole("button", { name: "사이드바 접기" }).click();
    await assertCollapsed(page, sidebar);
    await page.screenshot({
      path: path.join(evidenceDir, "sidebar-collapsed.png"),
      fullPage: false,
    });

    assert.deepEqual(pageErrors, []);
    console.log(JSON.stringify({ expanded, currentStyle, curationHoverStyle, curationFocusStyle, hoverStyle, focusStyle, preferenceStatuses, evidenceDir }, null, 2));
  } finally {
    await browser.close();
  }
}

async function assertCollapsed(page, sidebar) {
  await page.waitForFunction(() =>
    document.querySelector(".shell-product-sidebar")?.classList.contains("is-collapsed"),
  );
  assert.equal(await sidebar.locator(".shell-product-sidebar__history").count(), 0);
  assert.equal(await sidebar.locator(".phase6-product-sidebar__index").count(), 0);
  assert.equal(await sidebar.getByText("작업 공간 큐레이션", { exact: true }).count(), 0);
  const primaryTops = await sidebar
    .locator(".shell-product-sidebar__primary-link")
    .evaluateAll((links) => links.map((link) => link.getBoundingClientRect().top));
  assert.ok(primaryTops.length >= 2);
  assert.ok(primaryTops[1] - primaryTops[0] <= 60);
}

run().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
