const { firefox } = require("playwright");
const fs = require("node:fs/promises");
const path = require("node:path");

const marketingBaseURL = (
  process.env.MARKETING_BASE_URL ?? "https://vitlane.com"
).replace(/\/$/, "");
const appBaseURL = (
  process.env.APP_BASE_URL ?? "https://app.vitlane.com"
).replace(/\/$/, "");
const outputDir = path.resolve(
  process.env.DESIGN_BASELINE_DIR ??
    "../docs/design/evidence/current-baseline",
);
const koreanFontPath = process.env.DESIGN_KOREAN_FONT;

const targets = [
  {
    id: "marketing-home",
    url: `${marketingBaseURL}/`,
    viewports: [
      { name: "desktop", width: 1440, height: 1000 },
      { name: "mobile", width: 390, height: 844 },
    ],
  },
  {
    id: "marketing-privacy",
    url: `${marketingBaseURL}/privacy/`,
    viewports: [{ name: "desktop", width: 1440, height: 1000 }],
  },
  {
    id: "marketing-terms",
    url: `${marketingBaseURL}/terms/`,
    viewports: [{ name: "desktop", width: 1440, height: 1000 }],
  },
  {
    id: "app-login",
    url: `${appBaseURL}/login`,
    viewports: [
      { name: "desktop", width: 1440, height: 1000 },
      { name: "mobile", width: 390, height: 844 },
    ],
  },
];

async function main() {
  await fs.mkdir(outputDir, { recursive: true });
  const koreanFontData = koreanFontPath
    ? (await fs.readFile(koreanFontPath)).toString("base64")
    : null;
  const browser = await firefox.launch({ headless: true });
  const results = [];

  try {
    for (const target of targets) {
      for (const viewport of target.viewports) {
        const context = await browser.newContext({
          viewport: { width: viewport.width, height: viewport.height },
          locale: "ko-KR",
        });
        const page = await context.newPage();
        const consoleErrors = [];
        const pageErrors = [];
        const failedResponses = [];

        page.on("console", (message) => {
          if (message.type() === "error") consoleErrors.push(message.text());
        });
        page.on("pageerror", (error) => pageErrors.push(error.message));
        page.on("response", (response) => {
          if (response.status() >= 400) {
            failedResponses.push(`${response.status()} ${response.url()}`);
          }
        });

        const response = await page.goto(target.url, {
          waitUntil: "networkidle",
          timeout: 30_000,
        });
        const injectKoreanFont =
          Boolean(koreanFontData) && target.id === "app-login";
        if (injectKoreanFont) {
          await page.addStyleTag({
            content: `
              @font-face {
                font-family: "Vitlane Baseline Korean";
                src: url("data:font/woff2;base64,${koreanFontData}") format("woff2");
                font-style: normal;
                font-weight: 100 900;
              }
              html, body, h1, h2, h3, h4, h5, h6, p, span, strong, small,
              label, a, button, input, select, textarea, li, dt, dd {
                font-family: "Vitlane Baseline Korean", sans-serif !important;
              }
            `,
          });
          await page.waitForTimeout(300);
        }
        await page.evaluate(() => document.fonts.ready).catch(() => undefined);

        const filename = `${target.id}-${viewport.name}.png`;
        const layout = await page.evaluate(() => ({
          innerWidth: window.innerWidth,
          clientWidth: document.documentElement.clientWidth,
          scrollWidth: document.documentElement.scrollWidth,
          innerHeight: window.innerHeight,
          scrollHeight: document.documentElement.scrollHeight,
        }));
        await page.screenshot({
          path: path.join(outputDir, filename),
          fullPage: true,
        });

        results.push({
          target: target.id,
          viewport: viewport.name,
          requestedURL: target.url,
          finalURL: page.url(),
          status: response?.status() ?? null,
          title: await page.title(),
          heading: await page.locator("h1").first().textContent().catch(() => null),
          screenshot: filename,
          koreanFontInjected: injectKoreanFont,
          layout,
          consoleErrors,
          pageErrors,
          failedResponses,
        });
        await context.close();
      }
    }
  } finally {
    await browser.close();
  }

  process.stdout.write(`${JSON.stringify({ outputDir, results }, null, 2)}\n`);
  if (
    results.some(
      ({ status, pageErrors }) =>
        status === null || status >= 400 || pageErrors.length > 0,
    )
  ) {
    process.exitCode = 1;
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
