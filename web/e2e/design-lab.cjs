const { firefox } = require("playwright");
const { auditRadiusBoldBorders } = require("./shape-audit.cjs");
const crypto = require("node:crypto");
const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");

const webRoot = path.resolve(__dirname, "..");
const tokenSourcePath = path.join(
  webRoot,
  "src/shared/ui/design-system/tokens.source.json",
);
const outputDir = path.resolve(
  process.env.DESIGN_LAB_EVIDENCE_DIR ??
    path.join(os.tmpdir(), "vitlane-design-lab"),
);
const koreanFontPath = process.env.DESIGN_KOREAN_FONT;

const viewports = {
  desktop: { width: 1440, height: 1000 },
  tablet: { width: 768, height: 1024 },
  mobile: { width: 390, height: 844 },
};

function inspectPage() {
  const duplicateIDs = [
    ...document.querySelectorAll("[id]"),
  ].map((element) => element.id).filter(
    (id, index, ids) => ids.indexOf(id) !== index,
  );
  const buttonsWithoutName = [
    ...document.querySelectorAll("button"),
  ].filter(
    (button) =>
      !(button.textContent ?? "").trim() &&
      !button.getAttribute("aria-label"),
  ).length;
  const imagesNotDecoded = [
    ...document.querySelectorAll("img"),
  ].filter((image) => !image.complete || image.naturalWidth === 0).length;

  return {
    title: document.title,
    h1Count: document.querySelectorAll("h1").length,
    mainCount: document.querySelectorAll("main").length,
    duplicateIDs,
    buttonsWithoutName,
    imagesWithoutAlt: document.querySelectorAll("img:not([alt])").length,
    imagesNotDecoded,
    // ADR-0073 N7: standing notices per route are reported, not failed.
    notices: document.querySelectorAll(".vt-notice").length,
    innerWidth: window.innerWidth,
    scrollWidth: document.documentElement.scrollWidth,
    bodyScrollWidth: document.body.scrollWidth,
    scrollHeight: document.documentElement.scrollHeight,
  };
}

async function injectFont(page, koreanFontData) {
  if (!koreanFontData) return;
  await page.addStyleTag({
    content: `
      @font-face {
        font-family: "Pretendard Variable";
        src: url("data:font/woff2;base64,${koreanFontData}") format("woff2");
        font-style: normal;
        font-weight: 100 900;
      }
    `,
  });
  await page.evaluate(() => document.fonts.ready).catch(() => undefined);
}

async function waitForImages(page) {
  await page.evaluate(async () => {
    const images = [...document.images];
    images.forEach((image) => {
      image.loading = "eager";
    });
    await Promise.all(images.map((image) => image.decode()));
  });
}

function pageIssues(layout) {
  const issues = [];
  if (layout.h1Count !== 1) issues.push(`h1=${layout.h1Count}`);
  if (layout.mainCount !== 1) issues.push(`main=${layout.mainCount}`);
  if (layout.duplicateIDs.length > 0) {
    issues.push(`duplicate IDs=${layout.duplicateIDs.join(",")}`);
  }
  if (layout.buttonsWithoutName > 0) {
    issues.push(`unnamed buttons=${layout.buttonsWithoutName}`);
  }
  if (layout.imagesWithoutAlt > 0) {
    issues.push(`images without alt=${layout.imagesWithoutAlt}`);
  }
  if (layout.imagesNotDecoded > 0) {
    issues.push(`images not decoded=${layout.imagesNotDecoded}`);
  }
  if (
    layout.scrollWidth > layout.innerWidth ||
    layout.bodyScrollWidth > layout.innerWidth
  ) {
    issues.push(
      `horizontal overflow=${layout.scrollWidth}/${layout.bodyScrollWidth}/${layout.innerWidth}`,
    );
  }
  return issues;
}

async function main() {
  await fs.mkdir(outputDir, { recursive: true });
  const tokenSourceHash = `sha256:${crypto
    .createHash("sha256")
    .update(await fs.readFile(tokenSourcePath))
    .digest("hex")}`;
  const koreanFontData = koreanFontPath
    ? (await fs.readFile(koreanFontPath)).toString("base64")
    : null;
  const { createServer } = await import("vite");
  const vite = await createServer({
    root: webRoot,
    configFile: path.join(webRoot, "vite.config.ts"),
    clearScreen: false,
    logLevel: "error",
    server: {
      host: "127.0.0.1",
      port: 4177,
      strictPort: false,
    },
  });
  await vite.listen();
  const baseURL = vite.resolvedUrls?.local?.[0]?.replace(/\/$/, "");
  if (!baseURL) throw new Error("Vite local URL을 확인하지 못했습니다.");

  const browser = await firefox.launch({ headless: true });
  const failures = [];
  const noticeBudgetWarnings = [];
  // N9: computed-style audit of every fixture and overview capture.
  const radiusBoldBorderViolations = [];
  const overviewResults = [];
  const fixtureResults = [];

  try {
    for (const [viewportName, viewport] of Object.entries(viewports)) {
      const context = await browser.newContext({
        viewport,
        locale: "ko-KR",
        colorScheme: "light",
        reducedMotion: "reduce",
      });
      const page = await context.newPage();
      const consoleErrors = [];
      const pageErrors = [];
      page.on("console", (message) => {
        if (message.type() === "error") consoleErrors.push(message.text());
      });
      page.on("pageerror", (error) => pageErrors.push(error.message));

      await page.goto(`${baseURL}/design-lab.html`, {
        waitUntil: "networkidle",
        timeout: 20_000,
      });
      await injectFont(page, koreanFontData);
      await waitForImages(page);
      const productImages = page.locator(".vt-product-media img");
      for (let index = 0; index < (await productImages.count()); index += 1) {
        const productImage = productImages.nth(index);
        await productImage.scrollIntoViewIfNeeded();
        await productImage.evaluate(async (image) => {
          if (!image.complete) {
            await new Promise((resolve, reject) => {
              image.addEventListener("load", resolve, { once: true });
              image.addEventListener(
                "error",
                () => reject(new Error("product image request failed")),
                { once: true },
              );
            });
          }
        });
        const imageStatus = await productImage.evaluate((image) => ({
          complete: image.complete,
          currentSrc: image.currentSrc,
          naturalHeight: image.naturalHeight,
          naturalWidth: image.naturalWidth,
        }));
        if (
          !imageStatus.complete ||
          imageStatus.naturalWidth === 0 ||
          imageStatus.naturalHeight === 0
        ) {
          throw new Error(
            `Design Lab product image did not decode: ${JSON.stringify(imageStatus)}`,
          );
        }
      }
      const layout = await page.evaluate(inspectPage);
      for (const offender of await page.evaluate(auditRadiusBoldBorders)) {
        radiusBoldBorderViolations.push(`overview ${viewportName}: ${offender}`);
      }
      await page.evaluate(() => window.scrollTo(0, 0));
      const screenshot = `design-lab-${viewportName}.png`;
      await page.screenshot({
        path: path.join(outputDir, screenshot),
        fullPage: true,
      });

      const overviewIssues = [
        ...pageIssues(layout),
        ...consoleErrors.map((error) => `console: ${error}`),
        ...pageErrors.map((error) => `page: ${error}`),
      ];

      if (viewportName === "desktop") {
        await page.keyboard.press("Tab");
        const focus = await page.evaluate(() => {
          const active = document.activeElement;
          if (!(active instanceof HTMLElement)) return null;
          const style = getComputedStyle(active);
          return {
            tag: active.tagName,
            text: active.textContent?.trim() ?? "",
            outlineStyle: style.outlineStyle,
            outlineWidth: style.outlineWidth,
          };
        });
        if (
          !focus ||
          focus.outlineStyle === "none" ||
          focus.outlineWidth === "0px"
        ) {
          overviewIssues.push("keyboard focus is not visibly outlined");
        }

        const transitionDuration = await page
          .locator(".vt-button")
          .first()
          .evaluate((button) => getComputedStyle(button).transitionDuration);
        if (transitionDuration !== "0s") {
          overviewIssues.push(
            `reduced motion transition=${transitionDuration}`,
          );
        }

        const firstDetails = page.locator(".vt-candidate-card__details").first();
        await firstDetails
          .locator(":scope > .vt-disclosure__trigger")
          .click();
        const opened = await firstDetails.getAttribute("data-state") === "open";
        if (!opened) overviewIssues.push("CandidateCard disclosure did not open");

        const edgeStates = page.locator(".lab-component-states");
        await edgeStates
          .locator(":scope > .vt-disclosure__trigger")
          .click();
        const edgeStatesOpened =
          await edgeStates.getAttribute("data-state") === "open";
        if (!edgeStatesOpened) {
          overviewIssues.push("CandidateCard edge state catalog did not open");
        }
      }

      if (overviewIssues.length > 0) {
        failures.push(
          `overview:${viewportName}: ${overviewIssues.join("; ")}`,
        );
      }
      overviewResults.push({
        viewport: viewportName,
        screenshot,
        koreanFontInjected: Boolean(koreanFontData),
        layout,
        consoleErrors,
        pageErrors,
      });
      await context.close();
    }

    const context = await browser.newContext({
      viewport: viewports.mobile,
      locale: "ko-KR",
      colorScheme: "light",
      reducedMotion: "reduce",
    });
    const page = await context.newPage();
    let consoleErrors = [];
    let pageErrors = [];
    page.on("console", (message) => {
      if (message.type() === "error") consoleErrors.push(message.text());
    });
    page.on("pageerror", (error) => pageErrors.push(error.message));

    await page.goto(`${baseURL}/design-lab.html#screens`, {
      waitUntil: "networkidle",
      timeout: 20_000,
    });
    const fixtureTargets = await page.evaluate(() =>
      [...document.querySelectorAll(".lab-state-links a")].map((link) => {
        const url = new URL(link.href);
        return {
          screen: url.searchParams.get("screen"),
          state: url.searchParams.get("state"),
          url: url.href,
        };
      }),
    );
    // PlanReview and standalone Session screens were removed by the hard
    // cutover, and ADR-0026 dropped the standalone Cart screen family with its
    // open, partial and post-purchase return states: CartView is now a
    // projection of active Selections inside Curation rather than its own
    // screen. Curation owns the three explicit interaction states.
    if (fixtureTargets.length !== 61) {
      failures.push(
        `fixture catalog count=${fixtureTargets.length}, expected=61`,
      );
    }

    for (const target of fixtureTargets) {
      const { screen, state, url } = target;
      consoleErrors = [];
      pageErrors = [];
      await page.goto(url, {
        waitUntil: "networkidle",
        timeout: 20_000,
      });
      await injectFont(page, koreanFontData);
      await waitForImages(page);
      const layout = await page.evaluate(inspectPage);
      for (const offender of await page.evaluate(auditRadiusBoldBorders)) {
        radiusBoldBorderViolations.push(`${screen}:${state}: ${offender}`);
      }
      const fixture = await page
        .locator("[data-fixture]")
        .getAttribute("data-fixture");
      const issues = [
        ...pageIssues(layout),
        ...consoleErrors.map((error) => `console: ${error}`),
        ...pageErrors.map((error) => `page: ${error}`),
      ];
      if (fixture !== `${screen}:${state}`) {
        issues.push(`fixture resolved as ${fixture}`);
      }
      const primaryButtons = await page
        .locator(".vt-button--primary")
        .evaluateAll((buttons) => ({
          count: buttons.length,
          outsideCandidateCard: buttons.filter(
            (button) => !button.closest(".vt-candidate-card"),
          ).length,
          candidateCardsWithMultiplePrimaryActions: [
            ...document.querySelectorAll(".vt-candidate-card"),
          ].filter(
            (card) => card.querySelectorAll(".vt-button--primary").length > 1,
          ).length,
        }));
      if (screen === "research") {
        const allowedOutsideCandidatePrimary = state === "expanded" ? 1 : 0;
        if (
          primaryButtons.outsideCandidateCard >
            allowedOutsideCandidatePrimary ||
          primaryButtons.candidateCardsWithMultiplePrimaryActions > 0
        ) {
          issues.push(
            `research primary buttons=${primaryButtons.count}, outside candidate=${primaryButtons.outsideCandidateCard}, multi-primary cards=${primaryButtons.candidateCardsWithMultiplePrimaryActions}`,
          );
        }
      } else if (primaryButtons.count > 1) {
        issues.push(`primary buttons=${primaryButtons.count}`);
      }
      if (state !== "default") {
        if (["collapsed", "expanded", "researching", "return"].includes(state)) {
          const renderedScenario = await page
            .locator("[data-scenario-state]")
            .getAttribute("data-scenario-state");
          if (renderedScenario !== state) {
            issues.push(`scenario resolved as ${renderedScenario}`);
          }
        } else {
          const renderedState = await page
            .locator("[data-fixture] > main > [data-state]")
            .getAttribute("data-state");
          if (renderedState !== state) {
            issues.push(`state resolved as ${renderedState}`);
          }
        }
      }
      if (screen === "research" && state === "collapsed") {
        const addButton = page.getByRole("button", {
          name: "조사할 상품이나 항목 추가",
        });
        if ((await addButton.count()) !== 1) {
          issues.push("collapsed Curation + control is missing");
        }
      }
      if (screen === "research" && state === "expanded") {
        const expansionInput = page.getByPlaceholder(
          "추가로 조사할 상품이나 항목을 입력하세요",
        );
        if (
          (await expansionInput.count()) !== 1 ||
          (await page.getByRole("button", { name: "새 항목 조사" }).count()) !==
            1
        ) {
          issues.push("expanded Curation composer is incomplete");
        }
      }
      if (screen === "research" && state === "researching") {
        if (
          (await page
            .locator(".vt-feedback-state.is-loading")
            .filter({ hasText: "Agent가 조사 중" })
            .count()) !== 1
        ) {
          issues.push("Curation researching state is not dynamic");
        }
      }
      if (
        screen === "research" &&
        ["collapsed", "expanded", "researching"].includes(state) &&
        (await page
          .getByRole("button", { name: /해당 상품군 제거$/ })
          .count()) !== 1
      ) {
        issues.push("explicit Target removal control is missing");
      }
      if (screen === "cart" && state === "return") {
        const returnButton = page.getByRole("button", {
          name: "장바구니로 돌아가기",
        });
        if (
          (await returnButton.count()) !== 1 ||
          !(await returnButton.first().evaluate((button) =>
            button.classList.contains("vt-button--primary"),
          ))
        ) {
          issues.push("post-purchase Cart return is not a primary action");
        }
      }
      const capturesIntentShoppingState =
        (screen === "research" &&
          ["collapsed", "expanded", "researching"].includes(state)) ||
        screen === "cart";
      const screenshot = capturesIntentShoppingState
        ? `design-lab-fixture-${screen}-${state}-mobile.png`
        : null;
      if (screenshot) {
        await page.screenshot({
          path: path.join(outputDir, screenshot),
          fullPage: true,
        });
      }
      if (issues.length > 0) {
        failures.push(`${screen}:${state}: ${issues.join("; ")}`);
      }
      if (state === "default" && layout.notices >= 2) {
        noticeBudgetWarnings.push(`${screen}: ${layout.notices} notices`);
      }
      fixtureResults.push({
        screen,
        state,
        layout,
        consoleErrors: [...consoleErrors],
        pageErrors: [...pageErrors],
        screenshot,
      });
    }
    await context.close();
  } finally {
    await browser.close();
    await vite.close();
  }

  const manifest = {
    generatedAt: new Date().toISOString(),
    browser: "firefox",
    source: "web/design-lab.html",
    tokenSourceHash,
    overviewResults,
    fixtureCount: fixtureResults.length,
    fixtureResults,
    noticeBudgetWarnings,
    radiusBoldBorderViolations,
    failures,
  };
  await fs.writeFile(
    path.join(outputDir, "manifest.json"),
    `${JSON.stringify(manifest, null, 2)}\n`,
  );
  process.stdout.write(
    `Design Lab: ${overviewResults.length} viewports, ` +
      `${fixtureResults.length} fixtures, ${failures.length} failures, ` +
      `${noticeBudgetWarnings.length} notice budget warnings (N7), ` +
      `${radiusBoldBorderViolations.length} radius+bold-border violations (N9)\n`,
  );
  if (radiusBoldBorderViolations.length > 0) {
    failures.push(...radiusBoldBorderViolations.map((entry) => `N9 ${entry}`));
  }

  if (failures.length > 0) {
    process.stderr.write(`${failures.map((failure) => `- ${failure}`).join("\n")}\n`);
    process.exitCode = 1;
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
