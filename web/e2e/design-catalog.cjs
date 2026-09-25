const http = require("node:http");
const crypto = require("node:crypto");
const fs = require("node:fs/promises");
const path = require("node:path");
const { firefox } = require("playwright");

const webRoot = path.resolve(__dirname, "..");
const repositoryRoot = path.resolve(webRoot, "..");
const marketingRoot = path.join(repositoryRoot, "marketing");
const tokenSourcePath = path.join(
  webRoot,
  "src/shared/ui/design-system/tokens.source.json",
);
const copyPolicy = require(
  path.join(webRoot, "e2e/design-copy-policy.json"),
);
const outputDir = path.resolve(
  process.env.DESIGN_CATALOG_DIR ??
    "../docs/design/evidence/phase8-unified-product-system",
);
const koreanFontPath = process.env.DESIGN_KOREAN_FONT;

const currentUser = {
  id: "phase8-operator",
  email: "operator@vitlane.example",
  displayName: "Vitlane Operator",
  marketingAdmin: true,
  phase5Operator: true,
};

const account = {
  wallets: [],
  buyerProfiles: [],
  shippingProfiles: [],
  policyAcceptances: [],
  assurancePolicy: {
    PLAN_RESEARCH: "LOGIN",
    WALLET_REGISTRATION: "CURRENT_WALLET_OWNERSHIP_PROOF",
    TEST_LOW_VALUE: "CURRENT_WALLET_OWNERSHIP_PROOF",
    TEST_ADVANCED: "CURRENT_MOCK_DOJANG_KYC_CREDENTIAL",
    REAL_VALUE_PAYMENT: "NOT_AVAILABLE",
  },
};

// 고객 대화(ADR-0059) — 운영자 콘솔 fixture. 마지막 메시지가 고객 발신인
// 대화가 답변 대기다.
const supportConversations = [
  {
    userId: "phase8-support-user-1",
    customer: { email: "buyer@example.test", displayName: "구매자" },
    lastMessage: {
      id: "phase8-support-message-1",
      author: "CUSTOMER",
      body: "주문한 모니터 배송이 언제쯤 시작될까요? 일정이 궁금합니다.",
      createdAt: "2026-08-23T09:00:00.000Z",
    },
    awaitingReply: true,
  },
  {
    userId: "phase8-support-user-2",
    customer: { email: "", displayName: "" },
    lastMessage: {
      id: "phase8-support-message-2",
      author: "OPERATOR",
      body: "판매처 확인 결과를 안내드렸습니다. 더 궁금한 점이 있으면 알려주세요.",
      createdAt: "2026-08-22T18:00:00.000Z",
    },
    awaitingReply: false,
  },
];

// 고객 "도움 받기" 위젯(ADR-0059) fixture — 주문 첨부 답변은 구조화 참조로만
// 링크 칩이 된다.
const supportChatMessages = [
  {
    id: "phase8-support-chat-3",
    author: "OPERATOR",
    body: "판매처 확인이 끝났습니다. 내일 중으로 발송될 예정이에요.",
    agencyOrderId: "e5f00000-0000-4000-8000-000000000031",
    createdAt: "2026-08-23T10:00:00.000Z",
  },
  {
    id: "phase8-support-chat-2",
    author: "CUSTOMER",
    body: "주문한 모니터 배송이 언제쯤 시작될까요?",
    createdAt: "2026-08-23T09:00:00.000Z",
  },
  {
    id: "phase8-support-chat-1",
    author: "OPERATOR",
    body: "안녕하세요! 확인해 보고 바로 답변드릴게요.",
    createdAt: "2026-08-23T08:30:00.000Z",
    readAt: "2026-08-23T08:40:00.000Z",
  },
];

const viewports = {
  desktop: { width: 1440, height: 1000 },
  mobile: { width: 390, height: 844 },
};

const allScenarios = [
  marketingScenario("marketing-home", "/ko/", "더 잘 삽니다", "desktop"),
  marketingScenario(
    "marketing-home",
    "/ko/",
    "더 잘 삽니다",
    "mobile",
    { reducedMotion: "reduce" },
  ),
  marketingScenario(
    "marketing-privacy",
    "/ko/privacy/",
    "개인정보처리방침",
    "desktop",
    { enforceConsumerCopy: false },
  ),
  marketingScenario(
    "marketing-privacy",
    "/ko/privacy/",
    "개인정보처리방침",
    "mobile",
    { enforceConsumerCopy: false },
  ),
  marketingScenario(
    "marketing-terms",
    "/ko/terms/",
    "이용·결제 기준",
    "desktop",
    { enforceConsumerCopy: false },
  ),
  marketingScenario(
    "marketing-terms",
    "/ko/terms/",
    "이용·결제 기준",
    "mobile",
    { enforceConsumerCopy: false },
  ),
  appScenario("app-login", "/login", "로그인", "desktop", {
    user: null,
  }),
  appScenario("app-login", "/login", "로그인", "mobile", {
    user: null,
    reducedMotion: "reduce",
  }),
  appScenario(
    "app-login-error",
    "/login?error=AUTH_PROVIDER_FAILED",
    "로그인을 완료하지 못했습니다",
    "desktop",
    { user: null },
  ),
  appScenario(
    "support-admin",
    "/admin/support",
    "고객 대화",
    "desktop",
    { enforceConsumerCopy: false },
  ),
  appScenario(
    "support-admin",
    "/admin/support",
    "고객 대화",
    "mobile",
    { enforceConsumerCopy: false },
  ),
  appScenario(
    "support-chat",
    "/",
    "메시지",
    "desktop",
    { action: "open-support-chat", primaryActionLimit: 2 },
  ),
  appScenario(
    "support-chat",
    "/",
    "메시지",
    "mobile",
    { action: "open-support-chat", primaryActionLimit: 2 },
  ),
  appScenario(
    "not-found",
    "/this-route-does-not-exist",
    "이 화면을 찾을 수 없습니다",
    "desktop",
    { primaryActionLimit: 2 },
  ),
  appScenario(
    "not-found",
    "/this-route-does-not-exist",
    "이 화면을 찾을 수 없습니다",
    "mobile",
    { primaryActionLimit: 2 },
  ),
];
const scenarioTarget = process.env.DESIGN_CATALOG_TARGET?.trim();
const scenarios = allScenarios.filter(
  (scenario) => !scenarioTarget || scenario.id === scenarioTarget,
);

function marketingScenario(
  id,
  route,
  expectedText,
  viewport,
  options = {},
) {
  return {
    id,
    surface: "marketing",
    route,
    expectedText,
    viewport,
    reducedMotion: "no-preference",
    enforceConsumerCopy: true,
    primaryActionLimit: 1,
    ...options,
  };
}

function appScenario(id, route, expectedText, viewport, options = {}) {
  return {
    id,
    surface: "app",
    route,
    expectedText,
    viewport,
    user: currentUser,
    reducedMotion: "no-preference",
    enforceConsumerCopy: true,
    primaryActionLimit: 1,
    ...options,
  };
}

function contentType(file) {
  if (file.endsWith(".html")) return "text/html; charset=utf-8";
  if (file.endsWith(".css")) return "text/css; charset=utf-8";
  if (file.endsWith(".js")) return "text/javascript; charset=utf-8";
  if (file.endsWith(".svg")) return "image/svg+xml";
  if (file.endsWith(".woff2")) return "font/woff2";
  return "application/octet-stream";
}

async function startMarketingServer() {
  const server = http.createServer(async (request, response) => {
    try {
      const url = new URL(request.url, "http://127.0.0.1");
      const relative =
        url.pathname === "/"
          ? "index.html"
          : url.pathname.endsWith("/")
            ? `${url.pathname.slice(1)}index.html`
            : url.pathname.slice(1);
      const resolved = path.resolve(marketingRoot, relative);
      if (
        !resolved.startsWith(`${marketingRoot}${path.sep}`) &&
        resolved !== path.join(marketingRoot, "index.html")
      ) {
        response.writeHead(403);
        response.end("forbidden");
        return;
      }
      const body = await fs.readFile(resolved);
      response.writeHead(200, { "Content-Type": contentType(resolved) });
      response.end(body);
    } catch {
      response.writeHead(404);
      response.end("not found");
    }
  });

  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("marketing test server address is unavailable");
  }
  return {
    baseURL: `http://127.0.0.1:${address.port}`,
    close: () => new Promise((resolve) => server.close(resolve)),
  };
}

function responseFor(request, scenario) {
  const url = new URL(request.url());
  if (url.pathname === "/api/v1/analytics/config" && request.method() === "GET") {
    return { schemaVersion: "vitlane.analytics-config.v1", mode: "disabled", measurementId: "", release: "fixture" };
  }
  if (url.pathname === "/api/v1/curations/product-notices/sync" && request.method() === "POST") {
    return { schemaVersion: "vitlane.curation-notices.v1", curationIds: [] };
  }
  if (url.pathname === "/api/v1/me/preferences" && request.method() === "GET") {
    const preferences = { schemaVersion: "vitlane.user-preferences.v1", version: 0, uiLocale: "ko-KR", preferredCurrency: "KRW", researchCountry: "KR" };
    return { preferences, effective: preferences };
  }
  if (url.pathname === "/api/v1/admin/catalog-apis" && request.method() === "GET") return { schemaVersion: "vitlane.catalog-api-list.v1", apis: [] };
  if (url.pathname === "/api/v1/me" && request.method() === "GET") {
    return { user: scenario.user };
  }
  // The product shell renders the curation rail on every authenticated route,
  // including the operator screens and the not-found page, so its list request
  // belongs to the fixture rather than counting as an unexpected call.
  if (
    url.pathname === "/api/v1/curations" &&
    request.method() === "GET"
  ) {
    return { schemaVersion: "vitlane.curation-list.v2", curations: [] };
  }
  if (
    url.pathname === "/api/v1/auth/capabilities" &&
    request.method() === "GET"
  ) {
    return {
      googleEnabled: true,
      localReviewEnabled: false,
      localReviewSeeded: false,
      localReviewProfiles: [],
    };
  }
  if (
    url.pathname === "/api/v1/account/overview" &&
    request.method() === "GET"
  ) {
    return { account };
  }
  // 인증 셸은 모든 화면에서 도움 받기 안 읽음 summary를 폴링한다(ADR-0059)
  // — fixture 응답으로 흡수한다.
  if (
    url.pathname === "/api/v1/support/summary" &&
    request.method() === "GET"
  ) {
    return { schemaVersion: "vitlane.support-summary.v1", unread: 0 };
  }
  if (
    url.pathname === "/api/v1/support/messages" &&
    request.method() === "GET"
  ) {
    return {
      schemaVersion: "vitlane.support-messages.v1",
      messages: supportChatMessages,
    };
  }
  if (
    url.pathname === "/api/v1/support/messages/read" &&
    request.method() === "POST"
  ) {
    return { read: true };
  }
  // 홈 composer는 ManagedAgent capability를 조회한다 — fixture로 흡수한다.
  if (
    url.pathname === "/api/v1/managed-runner/capability" &&
    request.method() === "GET"
  ) {
    return {
      enabled: true,
      models: [],
      defaultModelKey: "",
      serverExhausted: false,
    };
  }
  // 운영자 셸 nav는 모든 admin 화면에서 고객 대화 답변 대기 카운트를
  // 폴링한다(ADR-0059) — fixture 응답으로 흡수한다.
  if (
    url.pathname === "/api/v1/admin/support/counts" &&
    request.method() === "GET"
  ) {
    return {
      schemaVersion: "vitlane.support-counts.v2",
      counts: { awaiting: 1 },
    };
  }
  if (
    url.pathname === "/api/v1/admin/support/conversations" &&
    request.method() === "GET"
  ) {
    return {
      schemaVersion: "vitlane.support-conversations.v2",
      conversations: supportConversations,
    };
  }
  // 운영자 셸 nav는 모든 admin 화면에서 예외 처리 뱃지 카운트를 폴링한다
  // (운영정합 2차 P2) — fixture 응답으로 흡수한다.
  if (
    url.pathname === "/api/v1/admin/ordering/work-items/counts" &&
    request.method() === "GET"
  ) {
    return {
      schemaVersion: "vitlane.ordering-operator-work-item-counts.v1",
      counts: {
        PROCESS_INTERVENTION: 0,
        PROCUREMENT_EXECUTION: 0,
        REFUND_REVIEW: 0,
        DELIVERY_RESOLUTION: 0,
        RETURN_PROGRESS: 0,
      },
    };
  }
  return undefined;
}

async function inspectPage(page, scenario) {
  const focus = await page.evaluate(() => {
    const active = document.activeElement;
    if (!(active instanceof HTMLElement)) return null;
    const style = getComputedStyle(active);
    return {
      tag: active.tagName,
      backgroundColor: style.backgroundColor,
      backgroundFocusCandidate: active.matches(
        ".shell-product-sidebar__brand, .shell-product-sidebar__home, .shell-product-sidebar__brand-trigger, .shell-product-sidebar__toggle, .shell-product-sidebar__primary-link, .shell-product-sidebar__curation, .shell-product-sidebar__cart, .shell-sidebar-profile__trigger",
      ),
      boxShadow: style.boxShadow,
      outlineStyle: style.outlineStyle,
      outlineWidth: style.outlineWidth,
    };
  });

  return page.evaluate(
    ({ copyRules, enforceConsumerCopy, focusState }) => {
      const clientWidth = document.documentElement.clientWidth;
      const ids = [...document.querySelectorAll("[id]")]
        .map((element) => element.id)
        .filter(Boolean);
      const duplicateIds = [
        ...new Set(ids.filter((id, index) => ids.indexOf(id) !== index)),
      ];
      const unnamedButtons = [...document.querySelectorAll("button")].filter(
        (button) =>
          !(
            button.textContent?.trim() ||
            button.getAttribute("aria-label") ||
            button.getAttribute("title")
          ),
      ).length;
      const visiblePrimaryActions = [
        ...document.querySelectorAll(
          ".vt-button--primary, .button-link-primary, .submit-button:not(:disabled)",
        ),
      ].filter((element) => {
        const style = getComputedStyle(element);
        const rect = element.getBoundingClientRect();
        return (
          !element.closest("[hidden]") &&
          style.display !== "none" &&
          style.visibility !== "hidden" &&
          rect.width > 0 &&
          rect.height > 0
        );
      }).length;
      const forbiddenTerms = enforceConsumerCopy
        ? copyRules.consumerForbiddenTerms.filter((term) =>
            document.body.innerText.includes(term),
          )
        : [];
      const copyRoot = document.body.cloneNode(true);
      for (const selector of copyRules.technicalDisclosureSelectors) {
        copyRoot.querySelectorAll(selector).forEach((element) => element.remove());
      }
      const visibleCopy = copyRoot.textContent ?? "";
      const rawEnums = enforceConsumerCopy
        ? [
            ...new Set(
              [
                ...visibleCopy.matchAll(
                  /\b[A-Z][A-Z0-9]+(?:_[A-Z0-9]+)+\b/g,
                ),
              ].map((match) => match[0]),
            ),
            ...copyRules.consumerForbiddenRawEnums.filter((term) =>
              new RegExp(`\\b${term}\\b`).test(visibleCopy),
            ),
          ].filter((term) => !copyRules.consumerAllowedUppercaseTerms.includes(term))
        : [];
      const bodyFontFamily = getComputedStyle(document.body).fontFamily;
      const loginLane = document.querySelector(".catalog-ui-login");
      const loginShell = document.querySelector(".catalog-ui-login-shell");
      const loginGlass = document.querySelector(".catalog-ui-login__glass");
      const loginBrand = document.querySelector(".catalog-ui-login__brand");
      const loginContent = document.querySelector(".catalog-ui-login__content");
      const googleAction = [...document.querySelectorAll("a")].find((link) =>
        link.textContent?.includes("Google로 계속"),
      );
      const loginContentStyle = loginContent
        ? getComputedStyle(loginContent)
        : null;
      const loginShellStyle = loginShell ? getComputedStyle(loginShell) : null;
      const loginLaneStyle = loginLane ? getComputedStyle(loginLane) : null;
      const loginGlassStyle = loginGlass ? getComputedStyle(loginGlass) : null;
      // The glass light must be the Still hue, also under reduced motion where
      // a transitioning color probe once returned the text color.
      const loginGlassTint = (() => {
        const canvas = loginGlass?.querySelector("canvas");
        const context = canvas?.getContext("2d");
        if (!canvas || !context || canvas.width === 0) return null;
        const [red, , blue] = context.getImageData(
          Math.floor(canvas.width * 0.7),
          Math.floor(canvas.height * 0.3),
          1,
          1,
        ).data;
        return blue - red;
      })();
      const googleActionStyle = googleAction
        ? getComputedStyle(googleAction)
        : null;
      const rectangle = (element) => {
        if (!(element instanceof Element)) return null;
        const rect = element.getBoundingClientRect();
        return {
          bottom: rect.bottom,
          height: rect.height,
          left: rect.left,
          right: rect.right,
          top: rect.top,
          width: rect.width,
        };
      };

      return {
        h1Count: document.querySelectorAll("h1").length,
        mainCount: document.querySelectorAll("main").length,
        innerWidth: window.innerWidth,
        scrollWidth: document.documentElement.scrollWidth,
        horizontalOverflow:
          document.documentElement.scrollWidth > clientWidth,
        duplicateIds,
        unnamedButtons,
        visiblePrimaryActions,
        forbiddenTerms,
        rawEnums,
        fonts: {
          bodyFontFamily,
          bodyUsesPretendard: bodyFontFamily.includes("Pretendard Variable"),
          pretendardReady: document.fonts.check('14px "Pretendard Variable"'),
          ibmPlexMonoReady: document.fonts.check('14px "IBM Plex Mono"'),
        },
        images: [...document.images].map((image) => ({
          src: image.getAttribute("src"),
          complete: image.complete,
          naturalWidth: image.naturalWidth,
        })),
        login: loginLane
          ? {
              lane: rectangle(loginLane),
              shellBackground: loginShellStyle?.backgroundColor ?? null,
              laneBackground: loginLaneStyle?.backgroundColor ?? null,
              // ADR-0078: still reeded glass on the lane surface.
              glass: rectangle(loginGlass),
              glassBackground: loginGlassStyle?.backgroundColor ?? null,
              glassMotion: loginGlass?.getAttribute("data-motion") ?? null,
              glassPainted: loginGlass?.getAttribute("data-painted") === "true",
              glassBlueLead: loginGlassTint,
              brand: rectangle(loginBrand),
              content: rectangle(loginContent),
              contentBackground: loginContentStyle?.backgroundColor ?? null,
              contentBorderStyle: loginContentStyle?.borderTopStyle ?? null,
              contentBoxShadow: loginContentStyle?.boxShadow ?? null,
              googleAction: rectangle(googleAction),
              googleBackground: googleActionStyle?.backgroundColor ?? null,
            }
          : null,
        focus: focusState,
      };
    },
    {
      copyRules: copyPolicy,
      enforceConsumerCopy: scenario.enforceConsumerCopy,
      focusState: focus,
    },
  );
}

async function main() {
  if (scenarios.length === 0) {
    throw new Error(`알 수 없는 Phase 8 target: ${scenarioTarget}`);
  }
  await fs.mkdir(outputDir, { recursive: true });
  const tokenSourceHash = `sha256:${crypto
    .createHash("sha256")
    .update(await fs.readFile(tokenSourcePath))
    .digest("hex")}`;
  const koreanFontData = koreanFontPath
    ? (await fs.readFile(koreanFontPath)).toString("base64")
    : null;
  const marketing = process.env.MARKETING_BASE_URL
    ? {
        baseURL: process.env.MARKETING_BASE_URL.replace(/\/$/, ""),
        close: async () => undefined,
      }
    : await startMarketingServer();
  const { createServer } = await import("vite");
  const vite = await createServer({
    root: webRoot,
    configFile: path.join(webRoot, "vite.config.ts"),
    clearScreen: false,
    logLevel: "error",
    server: {
      host: "127.0.0.1",
      port: 4179,
      strictPort: false,
    },
  });
  await vite.listen();
  const appBaseURL = vite.resolvedUrls?.local?.[0]?.replace(/\/$/, "");
  if (!appBaseURL) throw new Error("Vite local URL을 확인하지 못했습니다.");

  const browser = await firefox.launch({ headless: true });
  const results = [];

  try {
    for (const scenario of scenarios) {
      const context = await browser.newContext({
        viewport: viewports[scenario.viewport],
        locale: "ko-KR",
        colorScheme: "light",
        reducedMotion: scenario.reducedMotion,
      });
      const unexpectedAPI = [];
      const consoleErrors = [];
      const pageErrors = [];
      const page = await context.newPage();

      if (scenario.surface === "marketing") {
        await context.route("https://static.cloudflareinsights.com/**", async (route) => {
          await route.fulfill({
            status: 200,
            contentType: "application/javascript",
            body: "",
          });
        });
        await context.route("https://cloudflareinsights.com/**", async (route) => {
          await route.fulfill({ status: 204 });
        });
      }

      if (scenario.surface === "app") {
        await context.route("**/api/v1/**", async (route) => {
          const body = responseFor(route.request(), scenario);
          if (body === undefined) {
            unexpectedAPI.push(
              `${route.request().method()} ${new URL(route.request().url()).pathname}`,
            );
            await route.fulfill({
              status: 500,
              contentType: "application/json",
              body: JSON.stringify({
                error: {
                  code: "UNEXPECTED_PHASE8_FIXTURE_REQUEST",
                  message: "Phase 8 fixture에 등록되지 않은 요청입니다.",
                },
              }),
            });
            return;
          }
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify(body),
          });
        });
      }

      page.on("console", (message) => {
        if (message.type() === "error") consoleErrors.push(message.text());
      });
      page.on("pageerror", (error) => pageErrors.push(error.message));

      const baseURL =
        scenario.surface === "marketing" ? marketing.baseURL : appBaseURL;
      const response = await page.goto(`${baseURL}${scenario.route}`, {
        waitUntil: "domcontentloaded",
        timeout: 30_000,
      });
      const initialExpectedText = scenario.action === "open-support-chat"
          ? "무엇을 찾고 있나요?"
          : scenario.expectedText;
      try {
        await page.waitForFunction(
          (expectedText) => document.body?.innerText.includes(expectedText),
          initialExpectedText,
          { timeout: 15_000 },
        );
      } catch (error) {
        const renderedText = await page.locator("body").innerText()
          .catch(() => "");
        throw new Error(
          `${scenario.id}:${scenario.viewport}에서 "${initialExpectedText}"를 찾지 못했습니다. console=${consoleErrors.join(" | ")} page=${pageErrors.join(" | ")} rendered=${renderedText.slice(0, 400)}`,
          { cause: error },
        );
      }

      if (koreanFontData) {
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
      }
      await page
        .evaluate(async () => {
          await Promise.all([
            document.fonts.load('14px "Pretendard Variable"'),
            document.fonts.load('14px "IBM Plex Mono"'),
          ]);
          await document.fonts.ready;
        })
        .catch(() => undefined);

      // Lazy-loaded media (the marketing Lane figures on narrow viewports)
      // only requests when scrolled near. Walk the page once so the full-page
      // evidence and the image check see the loaded state, then return to top.
      await page.evaluate(async () => {
        const step = Math.max(window.innerHeight, 1);
        for (let y = 0; y <= document.documentElement.scrollHeight; y += step) {
          window.scrollTo(0, y);
          await new Promise((resolve) => setTimeout(resolve, 40));
        }
        window.scrollTo(0, 0);
      });
      await page.waitForLoadState("networkidle").catch(() => undefined);
      if (scenario.id === "marketing-home") {
        // A fast scroll pass can finish before the gallery's IntersectionObserver
        // requests its lazy images on a busy CI runner. Wait at the actual target.
        await page.locator("#examples").scrollIntoViewIfNeeded();
        await page.waitForFunction(() => {
          const images = [...document.querySelectorAll(".gallery-card img")];
          return images.length === 6 && images.every(image => image.complete && image.naturalWidth > 0);
        });
        await page.evaluate(() => window.scrollTo(0, 0));
      }

      let motionCheck = null;
      if (scenario.id === "marketing-home") {
        // 2026-09-18: the H1 condition rotates over a fixed result clause, and
        // reduced motion freezes it on the first condition.
        const before = await page.locator(".hero-title-live").innerText();
        await page.waitForTimeout(3100);
        const after = await page.locator(".hero-title-live").innerText();
        const transitionDuration = await page
          .locator(".hero-title-live")
          .evaluate((element) => getComputedStyle(element).transitionDuration);
        motionCheck = {
          before,
          after,
          changed: before !== after,
          transitionDuration,
        };
      }

      if (scenario.action === "open-support-chat") {
        // 프로필 메뉴 "메시지"가 쏘는 이벤트와 동일한 진입 경로다.
        await page.evaluate(() => {
          window.dispatchEvent(new CustomEvent("vitlane:open-support-chat"));
        });
        await page.getByRole("dialog", { name: "Vitlane 메시지" }).waitFor();
        await page.getByText("주문 진행 안내와 문의 답변을 한곳에서 확인하세요").waitFor();
        await page.getByText("관련 주문 보기").waitFor();
      }

      const filename = `${scenario.id}-${scenario.viewport}.png`;
      const captureDefaultLoginState =
        scenario.id === "app-login" && scenario.viewport === "desktop";

      if (captureDefaultLoginState) {
        await page.screenshot({
          path: path.join(outputDir, filename),
          fullPage: true,
        });
      }

      if (scenario.viewport === "desktop") {
        await page.keyboard.press("Tab");
      }

      const layout = await inspectPage(page, scenario);
      if (!captureDefaultLoginState) {
        await page.screenshot({
          path: path.join(outputDir, filename),
          fullPage: true,
        });
      }

      results.push({
        target: scenario.id,
        surface: scenario.surface,
        viewport: scenario.viewport,
        reducedMotion: scenario.reducedMotion,
        route: scenario.route,
        status: response?.status() ?? null,
        expectedText: scenario.expectedText,
        primaryActionLimit: scenario.primaryActionLimit,
        screenshot: filename,
        motionCheck,
        layout,
        unexpectedAPI,
        consoleErrors,
        pageErrors,
      });
      await context.close();
    }
  } finally {
    await browser.close();
    await vite.close();
    await marketing.close();
  }

  const manifest = {
    capturedAt: new Date().toISOString(),
    source:
      "real static Marketing and React routes with deterministic browser fixtures",
    tokenSourceHash,
    outputDir,
    results,
  };
  await fs.writeFile(
    path.join(outputDir, "manifest.json"),
    `${JSON.stringify(manifest, null, 2)}\n`,
  );
  process.stdout.write(`${JSON.stringify(manifest, null, 2)}\n`);

  const failed = results.some((result) => {
    const motionFailed =
      result.target === "marketing-home" &&
      (result.reducedMotion === "reduce"
        ? result.motionCheck?.changed ||
          result.motionCheck?.transitionDuration !== "0s"
        : !result.motionCheck?.changed);
    const focus = result.layout.focus;
    const hasVisibleFocus =
      focus &&
      (
        (
          focus.outlineStyle !== "none" &&
          focus.outlineWidth !== "0px"
        ) ||
        focus.boxShadow !== "none" ||
        (
          focus.backgroundFocusCandidate &&
          focus.backgroundColor !== "transparent" &&
          focus.backgroundColor !== "rgba(0, 0, 0, 0)"
        )
      );
    const login = result.layout.login;
    const leftGutter = login ? login.lane.left : null;
    const rightGutter = login
      ? result.layout.innerWidth - login.lane.right
      : null;
    const loginLayoutFailed = result.target === "app-login" && (
      !login?.lane ||
      !login.glass ||
      login.glassMotion !== "still" ||
      !login.glassPainted ||
      !(login.glassBlueLead >= 30) ||
      !login.brand ||
      !login.content ||
      login.contentBorderStyle === "none" ||
      login.contentBoxShadow === "none" ||
      !login.googleAction ||
      !login.shellBackground ||
      !login.laneBackground ||
      !login.contentBackground ||
      !login.glassBackground ||
      !login.googleBackground ||
      login.shellBackground === login.laneBackground ||
      login.laneBackground === login.contentBackground ||
      login.glassBackground !== login.laneBackground ||
      login.brand.left <= login.lane.left ||
      login.brand.top <= login.lane.top ||
      Math.abs(login.glass.width - login.lane.width) > 2 ||
      Math.abs(login.glass.height - login.lane.height) > 1 ||
      login.googleAction.height < 56 ||
      login.googleAction.width >= login.content.width ||
      Math.abs(
        (login.content.left + login.content.width / 2) -
          result.layout.innerWidth / 2,
      ) > 1 ||
      (result.viewport === "desktop" && (
        login.lane.width >= result.layout.innerWidth ||
        leftGutter === null ||
        rightGutter === null ||
        Math.abs(leftGutter - rightGutter) > 1
      )) ||
      (result.viewport === "mobile" &&
        Math.abs(login.lane.width - result.layout.innerWidth) > 1)
    );
    return (
      result.status === null ||
      result.status >= 400 ||
      result.unexpectedAPI.length > 0 ||
      result.consoleErrors.length > 0 ||
      result.pageErrors.length > 0 ||
      result.layout.h1Count !== 1 ||
      result.layout.mainCount !== 1 ||
      result.layout.horizontalOverflow ||
      result.layout.duplicateIds.length > 0 ||
      result.layout.unnamedButtons > 0 ||
      result.layout.visiblePrimaryActions > result.primaryActionLimit ||
      result.layout.forbiddenTerms.length > 0 ||
      result.layout.rawEnums.length > 0 ||
      !result.layout.fonts.bodyUsesPretendard ||
      !result.layout.fonts.pretendardReady ||
      !result.layout.fonts.ibmPlexMonoReady ||
      result.layout.images.some(
        (image) => !image.complete || image.naturalWidth === 0,
      ) ||
      (result.viewport === "desktop" &&
        !hasVisibleFocus) ||
      loginLayoutFailed ||
      motionFailed
    );
  });
  if (failed) process.exitCode = 1;
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
