const fs = require("node:fs/promises");
const path = require("node:path");
const { firefox } = require("playwright");

const webRoot = path.resolve(__dirname, "..");
const outputDir = path.resolve(
  process.env.ORDER_ACCOUNTING_EVIDENCE_DIR ?? "/tmp/vitlane-order-accounting-ui",
);
const currentUser = {
  id: "operator-accounting",
  email: "accounting-operator@vitlane.example",
  displayName: "Accounting Operator",
  marketingAdmin: true,
  phase5Operator: true,
};

function merchantOrder(overrides = {}) {
  return {
    allocationId: "allocation-accounting-demo",
    merchantOrderId: "merchant-order-demo",
    shopDomain: "basalt.example.com",
    checkoutOrdinal: 1,
    passThroughMinor: 10_000,
    feeVariableMinor: 540,
    feeFixedMinor: 30,
    feeTotalMinor: 570,
    customerGrossMinor: 10_570,
    feePolicyVersion: "PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1",
    fundingState: "ACTIVE",
    merchantOrderState: "PLACED",
    merchantPaymentState: "SUCCEEDED",
    actualCustomerGrossInMinor: 10_570,
    actualProcessorFeeMinor: 496,
    actualNetCashInMinor: 10_074,
    actualMerchantSpendMinor: 10_000,
    actualCustomerCompensatedMinor: 0,
    actualMerchantRecoveredMinor: 500,
    unreconciledCashGrossMinor: 0,
    realizedBalanceMinor: 574,
    forecastNetCashInMinor: 0,
    forecastProcessorFeeMinor: 0,
    forecastMerchantSpendMinor: 0,
    expectedCompensationMinor: 0,
    forecastAdjustmentMinor: 0,
    forecastBalanceMinor: 574,
    attentionReasons: [],
    ...overrides,
  };
}

function accounting(overrides = {}) {
  return {
    agencyOrderId: "order-accounting-demo",
    customerPaymentId: "payment-accounting-demo",
    rail: "PAYPAL",
    providerEnvironment: "SANDBOX",
    paymentState: "PARTIALLY_CAPTURED",
    currency: "USD",
    actualCustomerGrossInMinor: 10_570,
    actualProcessorFeeMinor: 496,
    actualNetCashInMinor: 10_074,
    actualMerchantSpendMinor: 10_000,
    actualCustomerCompensatedMinor: 0,
    actualMerchantRecoveredMinor: 500,
    unreconciledCashGrossMinor: 0,
    realizedBalanceMinor: 574,
    forecastNetCashInMinor: 0,
    forecastProcessorFeeMinor: 0,
    forecastMerchantSpendMinor: 0,
    expectedCompensationMinor: 0,
    forecastAdjustmentMinor: 0,
    forecastBalanceMinor: 574,
    requiresAttention: false,
    attentionReasons: [],
    merchantOrders: [merchantOrder()],
    events: [{
      id: "cash-demo", kind: "CUSTOMER_CASH_IN", direction: "CREDIT",
      merchantOrderId: "merchant-order-demo", allocationId: "allocation-accounting-demo",
      amountMinor: 10_074, customerGrossMinor: 10_570, processorFeeMinor: 496,
      economicsReconciled: true, source: "PAYPAL_MO_CAPTURE", occurredAt: "2026-08-27T00:01:00Z",
    }, {
      id: "purchase-demo", kind: "MERCHANT_PURCHASE", direction: "DEBIT",
      merchantOrderId: "merchant-order-demo", allocationId: "allocation-accounting-demo",
      amountMinor: 10_000, economicsReconciled: true, source: "MERCHANT_PAYMENT",
      occurredAt: "2026-08-27T00:02:00Z",
    }, {
      id: "recovery-demo", kind: "MERCHANT_RECOVERY", direction: "CREDIT",
      merchantOrderId: "merchant-order-demo", allocationId: "allocation-accounting-demo",
      amountMinor: 500, economicsReconciled: true, source: "PROCUREMENT_RECOVERY",
      occurredAt: "2026-08-27T00:03:00Z",
    }],
    createdAt: "2026-08-27T00:00:00Z",
    ...overrides,
  };
}

function releasedAccounting(providerEnvironment = "SANDBOX") {
  const releasedMO = merchantOrder({
    allocationId: "allocation-released", merchantOrderId: "merchant-order-released",
    shopDomain: "cobalt.example.com", fundingState: "RELEASED", merchantOrderState: "CANCELLED",
    compensationAction: "VOID", compensationState: "SUCCEEDED",
    compensationCause: "CUSTOMER_CANCEL_PRE_EFFECT",
    actualCustomerGrossInMinor: 0, actualProcessorFeeMinor: 0, actualNetCashInMinor: 0,
    actualMerchantSpendMinor: 0, actualMerchantRecoveredMinor: 0,
    realizedBalanceMinor: 0, forecastBalanceMinor: 0,
  });
  return accounting({
    agencyOrderId: "order-released", customerPaymentId: "payment-released",
    providerEnvironment,
    paymentState: "VOIDED", actualCustomerGrossInMinor: 0, actualProcessorFeeMinor: 0,
    actualNetCashInMinor: 0, actualMerchantSpendMinor: 0, actualMerchantRecoveredMinor: 0,
    realizedBalanceMinor: 0, forecastBalanceMinor: 0, merchantOrders: [releasedMO],
    events: [{
      id: "void-demo", kind: "AUTHORIZATION_RELEASE", direction: "NEUTRAL",
      merchantOrderId: "merchant-order-released", allocationId: "allocation-released",
      amountMinor: 0, customerGrossMinor: 10_570, economicsReconciled: true,
      source: "MO_COMPENSATION", cause: "CUSTOMER_CANCEL_PRE_EFFECT",
      occurredAt: "2026-08-27T00:04:00Z",
    }],
  });
}

function accountingSummary(providerEnvironment = "LIVE") {
  const active = accounting({ providerEnvironment });
  const released = releasedAccounting(providerEnvironment);
  return {
    providerEnvironment, currency: "USD",
    actualCustomerGrossInMinor: 10_570, actualProcessorFeeMinor: 496,
    actualNetCashInMinor: 10_074, actualMerchantSpendMinor: 10_000,
    actualCustomerCompensatedMinor: 0, actualMerchantRecoveredMinor: 500,
    unreconciledCashGrossMinor: 0, realizedBalanceMinor: 574,
    forecastNetCashInMinor: 0, forecastProcessorFeeMinor: 0,
    forecastMerchantSpendMinor: 0, expectedCompensationMinor: 0,
    forecastAdjustmentMinor: 0, forecastBalanceMinor: 574,
    orderCount: 2, attentionOrderCount: 0, orders: [active, released],
    asOf: "2026-08-27T00:05:00Z",
  };
}

function emptyAccountingSummary(providerEnvironment = "LIVE") {
  return {
    providerEnvironment, currency: "USD",
    actualCustomerGrossInMinor: 0, actualProcessorFeeMinor: 0,
    actualNetCashInMinor: 0, actualMerchantSpendMinor: 0,
    actualCustomerCompensatedMinor: 0, actualMerchantRecoveredMinor: 0,
    unreconciledCashGrossMinor: 0, realizedBalanceMinor: 0,
    forecastNetCashInMinor: 0, forecastProcessorFeeMinor: 0,
    forecastMerchantSpendMinor: 0, expectedCompensationMinor: 0,
    forecastAdjustmentMinor: 0, forecastBalanceMinor: 0,
    orderCount: 0, attentionOrderCount: 0, orders: [],
    asOf: "2026-09-12T01:00:00Z",
  };
}

function workSurface(orderAccounting) {
  return {
    schemaVersion: "vitlane.ordering-operator-work-items.v1",
    counts: {
      PAYMENT_RECONCILIATION: 0, PROCESS_INTERVENTION: 0, PROCUREMENT_EXECUTION: 1,
      REFUND_REVIEW: 0, DELIVERY_RESOLUTION: 0, RETURN_PROGRESS: 0,
    },
    items: [{
      kind: "PROCUREMENT_EXECUTION", id: "task-accounting-demo",
      agencyOrderId: orderAccounting.agencyOrderId, state: "CLAIMED",
      assignedOperatorUserId: currentUser.id, updatedAt: "2026-08-27T00:00:00Z",
      actions: ["REVEAL_SHIPPING", "REVEAL_CONTINUE_URL", "RECORD_PLACED", "RECORD_FAILURE"],
      accounting: orderAccounting,
      detail: {
        task: {
          id: "task-accounting-demo", merchantOrderId: "merchant-order-demo",
          agencyOrderId: orderAccounting.agencyOrderId, state: "CLAIMED",
          assignedOperatorUserId: currentUser.id, leaseUntil: "2099-08-27T00:00:00Z",
          updatedAt: "2026-08-27T00:00:00Z",
        },
        merchantOrder: {
          id: "merchant-order-demo", agencyOrderId: orderAccounting.agencyOrderId,
          merchantId: "merchant-demo", shopDomain: "basalt.example.com", checkoutOrdinal: 1,
          checkoutSnapshot: {
            authoritativeTotal: { amountMinor: 10_000, currency: "USD" },
            taxTotal: { amountMinor: 500, currency: "USD" }, expiresAt: "2099-08-27T00:00:00Z",
            continueUrlSafeRef: "vault:checkout-demo", deliveryGroups: [{
              id: "delivery-demo", selectedOptionRef: "standard",
              options: [{ id: "standard", title: "Standard", amountMinor: 500, currency: "USD" }],
            }],
          },
          executionMode: "SIMULATED_NO_EFFECT", state: "PLANNED",
        },
        units: [{ id: "unit-demo", lineId: "line-demo", unitIndex: 1, disposition: "PENDING" }],
        agencyOrder: {
          id: orderAccounting.agencyOrderId,
          paymentSelection: {
            rail: "PAYPAL", providerEnvironment: "SANDBOX", asset: "USD",
            economicEffect: "NO_REAL_VALUE", merchantExecution: "SIMULATED",
          },
          lines: [{
            lineId: "line-demo", shopDomain: "basalt.example.com", productTitle: "Commuter Pack",
            productUrl: "https://basalt.example.com/product", variantTitle: "Graphite",
            selectedOptions: ["Color: Graphite"], quantity: 1,
            unitPrice: { amountMinor: 9_000, currency: "USD" },
            lineSubtotal: { amountMinor: 9_000, currency: "USD" },
          }],
          merchantCheckouts: [{}], shippingAddress: { maskedSummary: "US · CA · •••05" },
          passThroughTotal: { amountMinor: 10_000, currency: "USD" },
          agencyFee: { total: { amountMinor: 570, currency: "USD" } },
          customerPayableTotal: { amountMinor: 10_570, currency: "USD" },
        },
        processState: "PROCUREMENT_IN_PROGRESS",
        logisticsSummary: { expectedUnits: 1, deliveredUnits: 0, exceptionUnits: 0 },
        delivery: { derived: true, title: "Standard", amountMinor: 500 },
      },
    }],
  };
}

async function configureAPI(context, livePayPalOrderCount) {
  const unexpected = [];
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() === "POST" && url.pathname === "/api/v1/curations/product-notices/sync") {
      return route.fulfill({ json: { schemaVersion: "vitlane.curation-notices.v1", curationIds: [] } });
    }
    if (request.method() === "GET" && url.pathname === "/api/v1/analytics/config") {
      return route.fulfill({ json: { schemaVersion: "vitlane.analytics-config.v1", mode: "disabled", measurementId: "", release: "fixture" } });
    }
    let body;
    if (request.method() === "GET" && url.pathname === "/api/v1/me") body = { user: currentUser };
    else if (request.method() === "GET" && url.pathname === "/api/v1/me/preferences") {
      const preferences = { schemaVersion: "vitlane.user-preferences.v1", version: 0, uiLocale: "ko-KR", preferredCurrency: "KRW", researchCountry: "KR" };
      body = { preferences, effective: preferences };
    }
    else if (request.method() === "GET" && url.pathname === "/api/v1/auth/capabilities") body = { googleEnabled: true, localReviewEnabled: false, localReviewSeeded: false, localReviewProfiles: [] };
    else if (request.method() === "GET" && url.pathname === "/api/v1/curations") body = { schemaVersion: "vitlane.curation-list.v2", curations: [] };
    else if (request.method() === "GET" && url.pathname === "/api/v1/support/summary") body = { schemaVersion: "vitlane.support-summary.v1", unread: 0 };
    else if (request.method() === "GET" && url.pathname === "/api/v1/admin/support/counts") body = { schemaVersion: "vitlane.support-counts.v1", counts: { awaiting: 0, actionRequired: 0 } };
    else if (request.method() === "GET" && url.pathname === "/api/v1/admin/ordering/work-items") body = workSurface(accounting());
    else if (request.method() === "GET" && url.pathname === "/api/v1/admin/ordering/work-items/counts") body = { schemaVersion: "vitlane.ordering-operator-work-item-counts.v2", counts: workSurface(accounting()).counts, livePayPalOrderCount };
    else if (request.method() === "GET" && url.pathname === "/api/v1/admin/ordering/order-accounting") body = {
      schemaVersion: "vitlane.order-accounting.v2",
      summary: livePayPalOrderCount === 0
        ? emptyAccountingSummary(url.searchParams.get("environment") || "LIVE")
        : accountingSummary(url.searchParams.get("environment") || "LIVE"),
    };
    else {
      unexpected.push(`${request.method()} ${url.pathname}`);
      await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: { code: "UNEXPECTED_FIXTURE", message: "unexpected request" } }) });
      return;
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  });
  return unexpected;
}

async function newContext(browser, height) {
  const context = await browser.newContext({ viewport: { width: 1440, height }, locale: "ko-KR", colorScheme: "dark" });
  await context.addInitScript(() => {
    localStorage.setItem("vitlane.appearance.v2", JSON.stringify({ theme: "dark", accent: "blue" }));
    localStorage.setItem("vitlane.locale.v1", "ko-KR");
  });
  return context;
}

async function capture(browser, baseURL) {
  const context = await newContext(browser, 1200);
  const unexpected = await configureAPI(context, 0);
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(`${baseURL}/admin/order-accounting`, { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "주문별 현금 원장" }).waitFor();
  if (await page.getByRole("combobox", { name: "회계 환경" }).inputValue() !== "LIVE") {
    throw new Error("accounting: Live PayPal is not the default environment");
  }
  const liveBadge = page.locator('a[href="/admin/agencyOrder"] .account-ui-nav-count.is-live-paypal');
  if (await liveBadge.count() !== 0) {
    throw new Error("accounting: zero LIVE PayPal order count must hide the navigation badge");
  }
  await page.getByText("집계할 주문이 없습니다").waitFor();
  await page.goto(`${baseURL}/admin/order-accounting`, { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "주문별 현금 원장" }).waitFor();
  await page.screenshot({ path: path.join(outputDir, "order-accounting.png"), fullPage: true });
  if (unexpected.length || errors.length) throw new Error(`accounting: unexpected=${unexpected.join(",")} errors=${errors.join(",")}`);
  await context.close();
}

async function verifyPositiveLiveBadge(browser, baseURL) {
  const context = await newContext(browser, 900);
  const unexpected = await configureAPI(context, 2);
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(`${baseURL}/admin/order-accounting`, { waitUntil: "domcontentloaded" });
  await page.getByRole("heading", { name: "주문별 현금 원장" }).waitFor();
  const liveBadge = page.locator('a[href="/admin/agencyOrder"] .account-ui-nav-count.is-live-paypal');
  await liveBadge.waitFor();
  if ((await liveBadge.textContent())?.trim() !== "2" || await liveBadge.getAttribute("aria-label") !== "Live PayPal 주문 2건") {
    throw new Error("accounting: positive LIVE PayPal order count badge is missing or incorrect");
  }
  await page.getByText("실제로 움직인 금액").first().waitFor();
  await page.getByRole("button", { name: /order-released/ }).click();
  const body = await page.locator("body").innerText();
  for (const expected of ["실현 잔고", "예상 잔고", "고객 수납", "상점 구매", "승인 해제", "현금 이동 없음"]) {
    if (!body.includes(expected)) throw new Error(`accounting positive fixture: missing ${expected}`);
  }
  if (body.includes("Vitlane 부담") || body.includes("지급 의무")) throw new Error("accounting positive fixture: legacy funding semantics remain");
  if (unexpected.length || errors.length) throw new Error(`accounting positive badge: unexpected=${unexpected.join(",")} errors=${errors.join(",")}`);
  await context.close();
}

async function main() {
  await fs.mkdir(outputDir, { recursive: true });
  const dependencyRoot = await fs.realpath(path.join(webRoot, "node_modules"));
  const { createServer } = await import("vite");
  const vite = await createServer({
    root: webRoot, configFile: path.join(webRoot, "vite.config.ts"), clearScreen: false,
    logLevel: "error", server: { host: "127.0.0.1", port: 4191, strictPort: false, fs: { allow: [webRoot, dependencyRoot] } },
  });
  await vite.listen();
  const baseURL = vite.resolvedUrls?.local?.[0]?.replace(/\/$/, "");
  if (!baseURL) throw new Error("Vite URL unavailable");
  const browser = await firefox.launch({ headless: true });
  try {
    await capture(browser, baseURL);
    await verifyPositiveLiveBadge(browser, baseURL);
  }
  finally { await browser.close(); await vite.close(); }
  await fs.writeFile(path.join(outputDir, "manifest.json"), `${JSON.stringify({
    capturedAt: new Date().toISOString(), browser: "firefox", screenshots: ["order-accounting.png"],
  }, null, 2)}\n`);
  process.stdout.write(`Order accounting UI evidence: ${outputDir}\n`);
}

main().catch((error) => { console.error(error); process.exitCode = 1; });
