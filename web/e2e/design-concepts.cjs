const { firefox } = require("playwright");
const fs = require("node:fs/promises");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const sourcePath = path.resolve(
  __dirname,
  "../../docs/design/concepts/phase-3-concept-explorer.html",
);
const outputDir = path.resolve(
  process.env.DESIGN_CONCEPTS_DIR ??
    "../docs/design/concepts/evidence",
);
const koreanFontPath = process.env.DESIGN_KOREAN_FONT;

const themes = ["lane", "ledger", "relay"];
const desktopScreens = ["marketing", "home", "plan", "research", "checkout"];
const mobileScreens = ["marketing", "research", "checkout"];
const targets = themes.flatMap((theme) => [
  ...desktopScreens.map((screen) => ({
    theme,
    screen,
    viewport: { name: "desktop", width: 1440, height: 1000 },
  })),
  ...mobileScreens.map((screen) => ({
    theme,
    screen,
    viewport: { name: "mobile", width: 390, height: 844 },
  })),
]);

async function main() {
  await fs.mkdir(outputDir, { recursive: true });
  const koreanFontData = koreanFontPath
    ? (await fs.readFile(koreanFontPath)).toString("base64")
    : null;
  const browser = await firefox.launch({ headless: true });
  const results = [];
  const motion = {};

  try {
    for (const target of targets) {
      const context = await browser.newContext({
        viewport: {
          width: target.viewport.width,
          height: target.viewport.height,
        },
        locale: "ko-KR",
        reducedMotion: "reduce",
      });
      const page = await context.newPage();
      const consoleErrors = [];
      const pageErrors = [];

      page.on("console", (message) => {
        if (message.type() === "error") consoleErrors.push(message.text());
      });
      page.on("pageerror", (error) => pageErrors.push(error.message));

      const url = new URL(pathToFileURL(sourcePath).href);
      url.searchParams.set("theme", target.theme);
      url.searchParams.set("screen", target.screen);
      await page.goto(url.href, { waitUntil: "load", timeout: 20_000 });
      if (koreanFontData) {
        await page.addStyleTag({
          content: `
            @font-face {
              font-family: "Vitlane Korean";
              src: url("data:font/woff2;base64,${koreanFontData}") format("woff2");
              font-style: normal;
              font-weight: 100 900;
            }
          `,
        });
      }
      await page.evaluate(() => document.fonts.ready).catch(() => undefined);

      const layout = await page.evaluate(() => {
        const visiblePanels = [
          ...document.querySelectorAll("[data-screen-panel]"),
        ].filter((panel) => !panel.hidden);
        const activeTheme = document.body.dataset.theme;
        const activeScreen = document.body.dataset.screen;
        const preview = document.querySelector(".preview-frame");
        return {
          activeTheme,
          activeScreen,
          visiblePanelCount: visiblePanels.length,
          innerWidth: window.innerWidth,
          clientWidth: document.documentElement.clientWidth,
          scrollWidth: document.documentElement.scrollWidth,
          bodyScrollWidth: document.body.scrollWidth,
          previewWidth: preview?.getBoundingClientRect().width ?? null,
          scrollHeight: document.documentElement.scrollHeight,
        };
      });

      const filename =
        `${target.theme}-${target.screen}-${target.viewport.name}.png`;
      await page.screenshot({
        path: path.join(outputDir, filename),
        fullPage: true,
      });

      results.push({
        theme: target.theme,
        screen: target.screen,
        viewport: target.viewport.name,
        title: await page.title(),
        heading: await page
          .locator("[data-screen-panel]:not([hidden]) h1")
          .first()
          .textContent()
          .catch(() => null),
        screenshot: filename,
        koreanFontInjected: Boolean(koreanFontData),
        layout,
        consoleErrors,
        pageErrors,
      });
      await context.close();
    }

    for (const preference of ["no-preference", "reduce"]) {
      const context = await browser.newContext({
        viewport: { width: 1440, height: 1000 },
        locale: "ko-KR",
        reducedMotion: preference,
      });
      const page = await context.newPage();
      const consoleErrors = [];
      const pageErrors = [];

      page.on("console", (message) => {
        if (message.type() === "error") consoleErrors.push(message.text());
      });
      page.on("pageerror", (error) => pageErrors.push(error.message));

      const url = new URL(pathToFileURL(sourcePath).href);
      url.searchParams.set("theme", "lane");
      url.searchParams.set("screen", "marketing");
      await page.goto(url.href, { waitUntil: "load", timeout: 20_000 });
      if (koreanFontData) {
        await page.addStyleTag({
          content: `
            @font-face {
              font-family: "Vitlane Korean";
              src: url("data:font/woff2;base64,${koreanFontData}") format("woff2");
              font-style: normal;
              font-weight: 100 900;
            }
          `,
        });
      }
      await page.evaluate(() => document.fonts.ready).catch(() => undefined);

      const readHero = () =>
        page.evaluate(() => ({
          frame: Number(document.body.dataset.heroFrame),
          motion: document.body.dataset.heroMotion,
          asset: document.querySelector('[data-hero-slot="asset"]')?.textContent.trim(),
          product: document.querySelector('[data-hero-slot="product"]')?.textContent.trim(),
          accessibleName: document.querySelector(".rotating-thesis")?.getAttribute("aria-label"),
        }));

      const before = await readHero();
      await page.waitForTimeout(3200);
      const after = await readHero();
      const screenshot =
        preference === "no-preference" ? "lane-marketing-motion-desktop.png" : null;

      if (screenshot) {
        await page.screenshot({
          path: path.join(outputDir, screenshot),
          fullPage: true,
        });
      }

      motion[preference] = {
        before,
        after,
        changed: before.frame !== after.frame,
        screenshot,
        consoleErrors,
        pageErrors,
      };
      await context.close();
    }
  } finally {
    await browser.close();
  }

  const manifest = {
    source: path.relative(outputDir, sourcePath),
    generatedAt: new Date().toISOString(),
    browser: "firefox",
    results,
    motion,
  };
  await fs.writeFile(
    path.join(outputDir, "manifest.json"),
    `${JSON.stringify(manifest, null, 2)}\n`,
  );
  process.stdout.write(`${JSON.stringify(manifest, null, 2)}\n`);

  if (
    results.some(
      ({ layout, consoleErrors, pageErrors }) =>
        layout.visiblePanelCount !== 1 ||
        layout.activeTheme === undefined ||
        layout.activeScreen === undefined ||
        layout.scrollWidth > layout.innerWidth ||
        layout.bodyScrollWidth > layout.innerWidth ||
        consoleErrors.length > 0 ||
        pageErrors.length > 0,
    ) ||
    !motion["no-preference"]?.changed ||
    motion.reduce?.changed ||
    motion["no-preference"]?.consoleErrors.length > 0 ||
    motion["no-preference"]?.pageErrors.length > 0 ||
    motion.reduce?.consoleErrors.length > 0 ||
    motion.reduce?.pageErrors.length > 0
  ) {
    process.exitCode = 1;
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
