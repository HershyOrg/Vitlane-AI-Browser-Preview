import { VitlaneApiClient, type FetchLike, type ServerWorkspace } from "../api";
import { ServerCommerceAdapter } from "./ServerCommerceAdapter";

const ids = {
  curation: "11111111-1111-4111-8111-111111111111",
  plan: "22222222-2222-4222-8222-222222222222",
  target: "33333333-3333-4333-8333-333333333333",
  thread: "44444444-4444-4444-8444-444444444444",
  action: "55555555-5555-4555-8555-555555555555",
  question: "66666666-6666-4666-8666-666666666666",
  option: "77777777-7777-4777-8777-777777777777",
  command: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  generated: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
  message: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
  subscription: "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
  finding: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
  browserRun: "f1111111-1111-4111-8111-111111111111",
};

const jsonResponse = (body: unknown, status = 200): Response => ({
  status,
  ok: status >= 200 && status < 300,
  json: async () => body,
}) as Response;

type RecordedRequest = {
  url: URL;
  method: string;
  headers: Record<string, string>;
  body?: Record<string, unknown>;
};

type ServerFixtureOptions = {
  nullableCollections?: boolean;
  koreanHydrationStatus?: "READY" | "UNRESOLVED" | "FAILED";
  cartCandidateIds?: readonly string[];
  intelligenceStatus?: "PENDING" | "RUNNING" | "SUCCEEDED" | "FAILED" | "CANCELLED";
  intelligenceStatuses?: readonly ("PENDING" | "RUNNING" | "SUCCEEDED" | "FAILED" | "CANCELLED")[];
  intelligenceActionIds?: readonly string[];
  activeWorkStatus?: "QUEUED" | "RUNNING" | "RESULT_CONFIRMATION_REQUIRED";
  threadStatus?: "INTERPRETING" | "WAITING_SELECTION" | "RUNNING" | "SUCCEEDED" | "FAILED" | "CANCELLED";
  sourceCoverage?: readonly {
    source: string;
    status: "SUCCEEDED" | "EMPTY" | "PARTIAL" | "FAILED" | "UNSUPPORTED" | "SKIPPED";
    candidateCount: number;
    reasonCode?: string;
  }[];
  initialBudgetMode?: "AUTO" | "EXPLICIT";
  cartTotal?: { amount: string; currency: "KRW" | "USD" };
  cartWarnings?: readonly { code: string; message: string }[];
  initialBudgetEffectOrigin?: "REQUEST" | "MANUAL" | "PRIMITIVE";
  failFirstWorkspaceReadAfterMutation?: boolean;
  loseFirstMutationResponse?: "answer" | "budget" | "follow_up";
  advanceCurationRevisionOnFollowUpCommit?: boolean;
  museShoppingFeatures?: boolean;
  conversationMessages?: NonNullable<ServerWorkspace["conversation"]>["messages"];
};

function createServer(options: ServerFixtureOptions = {}) {
  const requests: RecordedRequest[] = [];
  let budgetTotal = "150000";
  let budgetVersion = 3;
  let curationVersion = 7;
  let budgetChanged = Boolean(options.initialBudgetEffectOrigin);
  let budgetChangeOrigin = options.initialBudgetEffectOrigin;
  let mutationAccepted = false;
  let postMutationWorkspaceFailureUsed = false;
  let lostMutationResponse = false;
  let committedAnswer: Record<string, unknown> | undefined;
  const committedBudgetCommands = new Map<string, {
    request: string;
    response: Record<string, unknown>;
  }>();
  const committedFollowUps = new Map<string, {
    request: string;
    response: Record<string, unknown>;
  }>();
  const mutationCommits = { answer: 0, budget: 0, followUp: 0 };
  let proposalStatus = "PENDING";
  let proposalVersion = 1;
  let subscriptionStatus = "ACTIVE";
  let findingStatus = "NEW";
  let preferenceVersion = 2;
  let browserRunVersion = 1;
  let browserRunState = "AWAITING_NAVIGATION_APPROVAL";
  let preferences = {
    uiLocale: "ko-KR",
    preferredCurrency: "KRW",
    researchCountry: "KR",
  };
  const workspace = () => ({
    plan: {
      id: ids.plan,
      originalIntent: "6명 홈파티 상품을 찾아줘",
      totalBudget: { amount: "150000", currency: "KRW" },
      locationContext: { country: "KR", city: "Seoul" },
      budgetRequest: {
        schemaVersion: "vitlane.curation-budget.v1",
        inputMode: options.initialBudgetMode ?? "EXPLICIT",
        currency: "KRW",
        totalAmount: "150000",
        allocationMode: "AUTO",
      },
    },
    curation: {
      id: ids.curation,
      shoppingPlanId: ids.plan,
      version: curationVersion,
      phase: "CURATING",
    },
    targets: [{
      id: ids.target,
      title: "파티 간식",
      orderIndex: 0,
      confirmedAt: "2026-09-24T00:00:00Z",
    }],
    cart: {
      curationId: ids.curation,
      selections: (options.cartCandidateIds ?? []).map((candidateId) => ({
        selection: { candidateId, quantity: 1 },
        candidate: { id: candidateId, name: `Selected ${candidateId}` },
        unitPrice: { amount: "1000", currency: "KRW" },
        lineTotal: { amount: "1000", currency: "KRW" },
      })),
      total: options.cartTotal ?? {
        amount: String((options.cartCandidateIds?.length ?? 0) * 1000),
        currency: "KRW",
      },
      warnings: options.cartWarnings ?? [],
      updatedAt: "2026-09-24T00:00:00Z",
    },
    timeline: options.museShoppingFeatures ? [{
      action: {
        id: ids.action,
        curationId: ids.curation,
        type: "INTENT_NEXT_STEP",
        phaseAtRequest: "HAVING_INTENT",
        subjectType: "INTENT",
        effectKind: "INTELLIGENCE",
        expectedCurationVersion: 1,
        createdAt: "2026-09-24T00:00:00Z",
      },
      displayBody: "6명 홈파티 상품을 찾아줘",
      result: {
        kind: "INTENT_ACCEPTED",
        summary: "요청을 받고 상품 조사를 시작했어요.",
        occurredAt: "2026-09-24T00:00:01Z",
      },
    }] : [],
    ...(options.museShoppingFeatures ? {
      conversation: {
        schemaVersion: "vitlane.curation-conversation.v1",
        version: proposalVersion,
        unfinished: false,
        requests: [],
        messages: options.conversationMessages ?? [{
          id: ids.message,
          responseId: ids.command,
          kind: "PROPOSAL",
          status: proposalStatus,
          version: proposalVersion,
          createdAt: "2026-09-24T00:03:00Z",
          content: {
            code: "SUBSCRIBE_DEALS",
            body: "이 조건에 맞는 딜을 7일 동안 지켜볼까요?",
            locale: "ko-KR",
            targetTitle: "파티 간식",
          },
        }],
      },
    } : {}),
    coverage: "PARTIAL",
    ...((options.intelligenceStatuses ?? [options.intelligenceStatus ?? "RUNNING"])
      .some((status) => status === "PENDING" || status === "RUNNING")
      ? {
          activeWork: {
            workTargetId: ids.target,
            label: "카탈로그 조사",
            status: options.activeWorkStatus ?? "RUNNING",
          },
        }
      : {}),
    intelligence: (options.intelligenceStatuses ?? [options.intelligenceStatus ?? "RUNNING"])
      .map((status, index) => ({
        jobId: `99999999-9999-4999-8999-${String(index + 1).padStart(12, "0")}`,
        actionId: options.intelligenceActionIds?.[index] ?? ids.action,
        targetKind: index === 0 ? "PLANNING_TASK" : "RESEARCH_ROUND",
        targetId: ids.target,
        provider: "MANAGED",
        status,
        ...(status === "FAILED" ? { failureCode: "MODEL_UPSTREAM_FAILED" } : {}),
        retryable: status === "FAILED",
        attempt: 1,
        steps: [],
      })),
    catalogResearch: {
      pools: [{
        targetId: ids.target,
        version: 2,
        sourceCoverage: options.nullableCollections
          ? null
          : options.sourceCoverage ?? [
              { source: "SHOPIFY", status: "SUCCEEDED", candidateCount: 1 },
              { source: "AMAZON", status: "SUCCEEDED", candidateCount: 1 },
              { source: "ELEVENST", status: "SUCCEEDED", candidateCount: 1 },
            ],
        products: [
          { candidateId: "candidate-1", source: "SHOPIFY" },
          { candidateId: "candidate-amazon", source: "AMAZON" },
          { candidateId: "candidate-korean", source: "ELEVENST" },
        ],
        hiddenProducts: [],
      }],
    },
  });
  const question = () => ({
    id: ids.question,
    prompt: "매운맛을 포함할까요?",
    options: [
      { id: ids.option, label: "포함" },
      { id: "88888888-8888-4888-8888-888888888888", label: "제외" },
    ],
  });
  const browserRun = () => ({
    schemaVersion: "vitlane.browser-run.v1",
    run: {
      id: ids.browserRun,
      curationId: ids.curation,
      candidateId: "candidate-amazon",
      productUrl: "https://checkout.example.test/products/server-canonical",
      merchantOrigin: "https://checkout.example.test",
      merchantHost: "checkout.example.test",
      state: browserRunState,
      controlOwner: "NONE",
      version: browserRunVersion,
      freshObservationRequired: false,
      createdAt: "2026-09-24T00:10:00Z",
      updatedAt: "2026-09-24T00:10:00Z",
      ...(browserRunState === "NAVIGATION_APPROVED"
        ? { navigationApprovedAt: "2026-09-24T00:10:01Z" }
        : {}),
    },
  });
  const threads = () => ({
    threads: [{
      id: ids.thread,
      curationId: ids.curation,
      revision: 4,
      origin: budgetChangeOrigin ?? "REQUEST",
      status: options.threadStatus ?? (committedAnswer
        ? "RUNNING"
        : budgetChanged ? "SUCCEEDED" : "WAITING_SELECTION"),
      request: "선호를 확인해줘",
      createdAt: "2026-09-24T00:00:00Z",
      updatedAt: "2026-09-24T00:00:01Z",
      actions: options.nullableCollections ? null : [{
        id: ids.action,
        status: committedAnswer ? "RUNNING" : budgetChanged ? "SUCCEEDED" : "WAITING_SELECTION",
        ...(committedAnswer ? {} : { question: question() }),
        questions: committedAnswer ? [question()] : [],
        answers: committedAnswer ? [committedAnswer] : [],
        effects: budgetChanged ? [{
          kind: "BUDGET_CHANGED",
          after: {
            enabled: true,
            currency: "KRW",
            totalAmount: budgetTotal,
          },
        }] : [],
      }],
    }, ...[...committedFollowUps.values()].map(({ response }) => response)],
  });
  const fetcher: FetchLike = async (input, init) => {
    const url = new URL(input);
    const method = init?.method ?? "GET";
    const headers = (init?.headers ?? {}) as Record<string, string>;
    const body = typeof init?.body === "string"
      ? JSON.parse(init.body) as Record<string, unknown>
      : undefined;
    requests.push({ url, method, headers, ...(body ? { body } : {}) });

    if (url.pathname === "/api/v1/managed-runner/capability" && method === "GET") {
      return jsonResponse({
        enabled: true,
        defaultModelKey: "managed-default",
        serverExhausted: false,
        models: [{ key: "managed-default", label: "Managed default" }],
      });
    }
    if (url.pathname === "/api/v1/shopping-plans" && method === "POST") {
      return jsonResponse({ plan: workspace().plan, curation: workspace().curation, targets: workspace().targets });
    }
    if (url.pathname === `/api/v1/curations/${ids.curation}/workspace` && method === "GET") {
      if (
        options.failFirstWorkspaceReadAfterMutation
        && mutationAccepted
        && !postMutationWorkspaceFailureUsed
      ) {
        postMutationWorkspaceFailureUsed = true;
        return jsonResponse({ error: { code: "UPSTREAM_TIMEOUT", message: "try again" } }, 503);
      }
      return jsonResponse(workspace());
    }
    if (url.pathname === `/api/v1/curations/${ids.curation}/budget` && method === "GET") {
      return jsonResponse({
        schemaVersion: "vitlane.curation-budget.v1",
        version: budgetVersion,
        researchVersion: 1,
        enabled: true,
        currency: "KRW",
        totalAmount: budgetTotal,
        allocations: [{ targetId: ids.target, quantity: 1, amount: budgetTotal }],
      });
    }
    if (url.pathname === `/api/v1/curations/${ids.curation}/budget` && method === "PATCH") {
      const commandId = String(body?.commandId ?? "");
      const request = JSON.stringify(body);
      const previous = committedBudgetCommands.get(commandId);
      if (previous) {
        if (previous.request !== request) {
          return jsonResponse({ error: { code: "BUDGET_IDEMPOTENCY_CONFLICT" } }, 409);
        }
        return jsonResponse(previous.response);
      }
      budgetTotal = String(body?.totalAmount);
      budgetVersion += 1;
      budgetChanged = true;
      budgetChangeOrigin = "MANUAL";
      mutationAccepted = true;
      mutationCommits.budget += 1;
      const response = {
        schemaVersion: "vitlane.curation-budget.v1",
        version: budgetVersion,
        researchVersion: 2,
        enabled: true,
        currency: "KRW",
        totalAmount: budgetTotal,
        allocations: [{ targetId: ids.target, quantity: 1, amount: budgetTotal }],
      };
      committedBudgetCommands.set(commandId, { request, response });
      if (options.loseFirstMutationResponse === "budget" && !lostMutationResponse) {
        lostMutationResponse = true;
        throw new Error("budget response lost after commit");
      }
      return jsonResponse(response);
    }
    if (url.pathname === `/api/v1/curations/${ids.curation}/threads` && method === "GET") {
      return jsonResponse(threads());
    }
    if (
      options.museShoppingFeatures
      && url.pathname === `/api/v1/curations/${ids.curation}/background-research`
      && method === "GET"
    ) {
      return jsonResponse({
        schemaVersion: "vitlane.background-research.v1",
        subscriptions: [{
          id: ids.subscription,
          curationId: ids.curation,
          targetId: ids.target,
          status: subscriptionStatus,
          terms: {
            schemaVersion: "vitlane.research-subscription-terms.v1",
            criteria: {},
            country: "KR",
            currency: "KRW",
            maximumMinor: 35000,
            keywords: ["홈파티", "간식"],
            expiresAt: "2026-10-01T00:00:00Z",
          },
          createdAt: "2026-09-24T00:02:00Z",
        }],
        findings: [{
          id: ids.finding,
          subscriptionId: ids.subscription,
          targetId: ids.target,
          status: findingStatus,
          product: {
            schemaVersion: "vitlane.deal-product.v1",
            provider: "APIFY",
            externalId: "deal-1",
            identity: "deal:1",
            title: "관측된 파티 간식 딜",
            url: "https://seller.example.test/deals/1",
            country: "KR",
            currency: "KRW",
            priceMinor: 29900,
            observedAt: "2026-09-24T00:05:00Z",
            expiresAt: "2026-09-25T00:05:00Z",
          },
          reason: "저장한 가격 상한과 인원 조건에 맞아요.",
          createdAt: "2026-09-24T00:05:00Z",
          ...(findingStatus === "ADDED" ? { candidateId: "candidate-imported" } : {}),
        }],
      });
    }
    if (options.museShoppingFeatures && url.pathname === "/api/v1/me/preferences" && method === "GET") {
      return jsonResponse({
        preferences: {
          schemaVersion: "vitlane.user-preferences.v1",
          version: preferenceVersion,
          ...preferences,
        },
        effective: {
          schemaVersion: "vitlane.user-preferences.v1",
          version: preferenceVersion,
          ...preferences,
        },
      });
    }
    if (options.museShoppingFeatures && url.pathname === "/api/v1/me/preferences" && method === "PATCH") {
      if (body?.expectedVersion !== preferenceVersion) {
        return jsonResponse({ error: { code: "VERSION_CONFLICT" } }, 409);
      }
      preferences = {
        uiLocale: String(body?.uiLocale ?? preferences.uiLocale),
        preferredCurrency: String(body?.preferredCurrency ?? preferences.preferredCurrency),
        researchCountry: String(body?.researchCountry ?? preferences.researchCountry),
      };
      preferenceVersion += 1;
      return jsonResponse({
        preferences: { schemaVersion: "vitlane.user-preferences.v1", version: preferenceVersion, ...preferences },
        effective: { schemaVersion: "vitlane.user-preferences.v1", version: preferenceVersion, ...preferences },
      });
    }
    if (
      options.museShoppingFeatures
      && url.pathname === `/api/v1/curations/${ids.curation}/follow-ups/${ids.message}/responses`
      && method === "POST"
    ) {
      proposalStatus = body?.response === "ACCEPT" ? "ACCEPTED" : body?.response === "DISMISS" ? "DISMISSED" : "ACKNOWLEDGED";
      proposalVersion += 1;
      return jsonResponse({ status: "RECORDED" });
    }
    if (
      options.museShoppingFeatures
      && url.pathname === `/api/v1/curations/${ids.curation}/subscriptions/${ids.subscription}/cancel`
      && method === "POST"
    ) {
      subscriptionStatus = "CANCELLED";
      return jsonResponse({ status: "RECORDED" });
    }
    if (
      options.museShoppingFeatures
      && url.pathname === `/api/v1/curations/${ids.curation}/findings/${ids.finding}/hide`
      && method === "POST"
    ) {
      findingStatus = "HIDDEN";
      return jsonResponse({ status: "RECORDED" });
    }
    if (
      options.museShoppingFeatures
      && url.pathname === `/api/v1/curations/${ids.curation}/findings/${ids.finding}/candidates`
      && method === "POST"
    ) {
      findingStatus = "ADDED";
      return jsonResponse({ candidateId: "candidate-imported" });
    }
    if (url.pathname === `/api/v1/curations/${ids.curation}/threads` && method === "POST") {
      const clientRequestId = String(body?.clientRequestId ?? "");
      const request = JSON.stringify(body);
      const previous = committedFollowUps.get(clientRequestId);
      if (previous) {
        if (previous.request !== request) {
          return jsonResponse({ error: { code: "IDEMPOTENCY_KEY_REUSED" } }, 409);
        }
        return jsonResponse(previous.response, 202);
      }
      mutationAccepted = true;
      mutationCommits.followUp += 1;
      if (options.advanceCurationRevisionOnFollowUpCommit) curationVersion += 1;
      const response = {
        id: clientRequestId,
        curationId: ids.curation,
        revision: 1,
        origin: "REQUEST",
        status: "INTERPRETING",
        request: String(body?.request ?? ""),
        createdAt: "2026-09-24T00:00:02Z",
        updatedAt: "2026-09-24T00:00:02Z",
        actions: [],
      };
      committedFollowUps.set(clientRequestId, { request, response });
      if (options.loseFirstMutationResponse === "follow_up" && !lostMutationResponse) {
        lostMutationResponse = true;
        throw new Error("follow-up response lost after commit");
      }
      return jsonResponse(response, 202);
    }
    if (
      url.pathname === `/api/v1/curations/${ids.curation}/threads/${ids.thread}/answer`
      && method === "POST"
    ) {
      mutationAccepted = true;
      mutationCommits.answer += 1;
      committedAnswer = { ...(body ?? {}) };
      if (options.loseFirstMutationResponse === "answer" && !lostMutationResponse) {
        lostMutationResponse = true;
        throw new Error("answer response lost after commit");
      }
      return jsonResponse(threads().threads[0], 202);
    }
    if (
      url.pathname === `/api/v1/curations/${ids.curation}/catalog-research/candidates/candidate-amazon/browser-runs`
      && method === "POST"
    ) {
      if (Object.keys(body ?? {}).length !== 0) {
        return jsonResponse({ error: { code: "BROWSER_RUN_INVALID" } }, 400);
      }
      return jsonResponse(browserRun(), 201);
    }
    if (
      url.pathname === `/api/v1/browser-runs/${ids.browserRun}/navigation-approvals`
      && method === "POST"
    ) {
      if (body?.expectedVersion !== browserRunVersion) {
        return jsonResponse({ error: { code: "BROWSER_RUN_CONFLICT" } }, 409);
      }
      browserRunVersion += 1;
      browserRunState = "NAVIGATION_APPROVED";
      return jsonResponse(browserRun());
    }
    if (
      url.pathname === `/api/v1/curations/${ids.curation}/catalog-research/hydrations`
      && method === "POST"
    ) {
      const source = String(body?.source ?? "");
      const expectedCandidateId = source === "AMAZON"
        ? "candidate-amazon"
        : source === "ELEVENST"
          ? "candidate-korean"
          : undefined;
      const validScope = source === "SHOPIFY"
        ? body?.scope === "VISIBLE_TARGET" && body?.candidateId === undefined
        : body?.scope === "CANDIDATE" && body?.candidateId === expectedCandidateId;
      if (!validScope) {
        return jsonResponse({
          error: {
            code: source === "ELEVENST"
              ? "EXTERNAL_PRODUCT_CANDIDATE_SCOPE_REQUIRED"
              : "CATALOG_RESEARCH_HYDRATION_SCOPE_INVALID",
            message: "invalid hydration scope",
          },
        }, 400);
      }
      const products = source === "AMAZON"
        ? [{
            candidateId: "candidate-amazon",
            source: "AMAZON",
            title: "Observed Amazon party kit",
            description: "US marketplace candidate",
            mediaUrl: "https://images.example.test/amazon.jpg",
            sourceProductRef: { source: "AMAZON", marketplace: "US", anchorAsin: "B000000001" },
            variantObservation: {
              observationId: "amazon-observation",
              variantRef: { source: "AMAZON", marketplace: "US", asin: "B000000001" },
              price: { kind: "OBSERVED", amountMinor: 2499, currency: "USD" },
              availability: "AVAILABLE",
              seller: { kind: "KNOWN", name: "Example seller" },
              purchaseRoute: "EXTERNAL",
              productUrl: "https://www.amazon.com/dp/B000000001",
              observedAt: "2026-09-24T00:00:00Z",
              refreshAfter: "2026-09-24T01:00:00Z",
            },
            purchaseRoute: "EXTERNAL",
            features: [],
            specifications: [],
            hydration: { status: "READY", retryable: false },
          }]
        : source === "ELEVENST"
          ? [{
              candidateId: "candidate-korean",
              source: "ELEVENST",
              sourceProductRef: { source: "ELEVENST", marketplace: "KR", productId: "12345" },
              externalObservation: {
                schemaVersion: "vitlane.external-product-observation.v1",
                productRef: { source: "ELEVENST", marketplace: "KR", productId: "12345" },
                productUrl: "https://www.11st.co.kr/products/12345",
                title: "국내 파티 장식",
                imageUrl: "https://images.example.test/korean.jpg",
                price: { kind: "UNKNOWN", reasonCode: "DETAIL_PRICE_MISSING" },
                priceScope: "PRODUCT",
                seller: { kind: "UNKNOWN" },
                observedAt: "2026-09-24T00:02:00Z",
                provenance: {
                  apiProvider: "OPEN_WEB_NINJA",
                  apiProduct: "PRODUCT_SEARCH",
                  discoveryChannel: "PRODUCT_API",
                  country: "KR",
                  queryLanguage: "ko",
                },
              },
              purchaseRoute: "EXTERNAL",
              features: [],
              specifications: [],
              hydration: {
                status: options.koreanHydrationStatus ?? "READY",
                retryable: options.koreanHydrationStatus === "UNRESOLVED",
              },
            }]
          : [{
              candidateId: "candidate-1",
              source: "SHOPIFY",
              title: "실제 파티 간식 세트",
              description: "6인용 구성",
              priceMinimumMinor: 32000,
              priceMaximumMinor: 48000,
              currency: "KRW",
              mediaUrl: "https://images.example.test/shopify.jpg",
              locator: { kind: "PRODUCT_URL", productUrl: "https://shop.example.test/products/party" },
              purchaseRoute: "VITLANE_CHECKOUT",
              intentPoint: "인원에 맞는 묶음",
              features: ["개별 포장"],
              specifications: [],
              hydration: { status: "READY", retryable: false },
            }];
      return jsonResponse({
        pools: [{
          targetId: ids.target,
          products,
          hiddenProducts: [],
        }],
      });
    }
    return jsonResponse({ error: { code: "NOT_FOUND", message: `${method} ${url.pathname}` } }, 404);
  };
  return { fetcher, requests, mutationCommits };
}

function adapterWithServer(
  options: ServerFixtureOptions = {},
  adapterOptions: { now?: () => number } = {},
) {
  const server = createServer(options);
  const api = new VitlaneApiClient({
    baseUrl: "https://api.example.test",
    fetch: server.fetcher,
    accessToken: () => "session-token",
  });
  return {
    ...server,
    adapter: new ServerCommerceAdapter({
      api,
      idFactory: () => ids.generated,
      ...(adapterOptions.now ? { now: adapterOptions.now } : {}),
    }),
  };
}

describe("ServerCommerceAdapter", () => {
  it("creates and navigation-approves a BrowserRun using only stored candidate coordinates", async () => {
    const { adapter, requests } = adapterWithServer();

    const context = {
      createIdempotencyKey: "browser-run:create:11111111-1111-4111-8111-111111111111",
      navigationApprovalIdempotencyKey: "browser-run:navigate:11111111-1111-4111-8111-111111111111",
    };
    const run = await adapter.startBrowserRun(ids.curation, "candidate-amazon", context);
    const replayed = await adapter.startBrowserRun(ids.curation, "candidate-amazon", context);

    expect(run).toEqual(expect.objectContaining({
      id: ids.browserRun,
      curationId: ids.curation,
      candidateId: "candidate-amazon",
      productUrl: "https://checkout.example.test/products/server-canonical",
      merchantHost: "checkout.example.test",
      state: "NAVIGATION_APPROVED",
      version: 2,
    }));
    expect(replayed).toEqual(run);
    const create = requests.find((request) => request.url.pathname.endsWith("/browser-runs"));
    expect(create).toEqual(expect.objectContaining({
      method: "POST",
      body: {},
      headers: expect.objectContaining({
        "Idempotency-Key": "browser-run:create:11111111-1111-4111-8111-111111111111",
      }),
    }));
    expect(create?.body).not.toHaveProperty("productUrl");
    const approve = requests.find((request) => request.url.pathname.endsWith("/navigation-approvals"));
    expect(approve).toEqual(expect.objectContaining({
      method: "POST",
      body: { expectedVersion: 1 },
      headers: expect.objectContaining({
        "Idempotency-Key": "browser-run:navigate:11111111-1111-4111-8111-111111111111",
      }),
    }));
    expect(requests.filter(
      (request) => request.url.pathname.endsWith("/navigation-approvals"),
    )).toHaveLength(1);
  });

  it("creates a managed plan and maps live workspace, question, candidate, and coverage data", async () => {
    const { adapter, requests } = adapterWithServer();
    const view = await adapter.createWorkspace("  6명 홈파티 상품을 찾아줘  ", {
      idempotencyKey: ids.command,
      baseRevision: 0,
    });

    expect(view).toEqual(expect.objectContaining({
      id: ids.curation,
      mode: "server",
      revision: 7,
      activeQuestionId: ids.question,
      references: { planId: ids.plan, curationId: ids.curation },
      activeWork: expect.objectContaining({ status: "RUNNING" }),
    }));
    expect(view?.questions[0]).toEqual(expect.objectContaining({
      id: ids.question,
      state: "unanswered",
      allowCustom: true,
    }));
    expect(view?.candidates[0]).toEqual(expect.objectContaining({
      id: "candidate-1",
      price: {
        kind: "RANGE",
        minimum: { amount: 32000, currency: "KRW" },
        maximum: { amount: 48000, currency: "KRW" },
      },
      imageUrl: "https://images.example.test/shopify.jpg",
    }));
    expect(view?.candidates.find((candidate) => candidate.id === "candidate-amazon")).toEqual(
      expect.objectContaining({
        price: { kind: "OBSERVED", amount: { amount: 24.99, currency: "USD" } },
        provenance: expect.objectContaining({
          source: "AMAZON",
          availability: "AVAILABLE",
          observedAt: "2026-09-24T00:00:00Z",
        }),
      }),
    );
    expect(view?.candidates.find((candidate) => candidate.id === "candidate-korean")).toEqual(
      expect.objectContaining({
        title: { "ko-KR": "국내 파티 장식", "en-US": "국내 파티 장식" },
        price: { kind: "UNKNOWN", reasonCode: "DETAIL_PRICE_MISSING" },
        imageUrl: "https://images.example.test/korean.jpg",
        provenance: expect.objectContaining({
          source: "ELEVENST",
          apiProvider: "OPEN_WEB_NINJA",
          discoveryChannel: "PRODUCT_API",
        }),
      }),
    );
    expect(view?.sourceCoverage).toEqual([
      expect.objectContaining({ source: "SHOPIFY", status: "SUCCEEDED" }),
      expect.objectContaining({ source: "AMAZON", status: "SUCCEEDED" }),
      expect.objectContaining({ source: "ELEVENST", status: "SUCCEEDED" }),
    ]);

    const create = requests.find((request) => request.url.pathname === "/api/v1/shopping-plans");
    expect(create?.headers["Idempotency-Key"]).toBe(ids.command);
    expect(create?.body).toEqual(expect.objectContaining({
      originalIntent: "6명 홈파티 상품을 찾아줘",
      agentMode: "MANAGED",
      modelKey: "managed-default",
      budget: expect.objectContaining({ totalAmount: null, inputMode: "AUTO" }),
    }));
    const hydrations = requests.filter((request) => (
      request.url.pathname.endsWith("/catalog-research/hydrations")
    ));
    expect(hydrations.map((request) => request.body)).toEqual(expect.arrayContaining([
      { scope: "VISIBLE_TARGET", source: "SHOPIFY", targetId: ids.target },
      {
        scope: "CANDIDATE",
        source: "AMAZON",
        targetId: ids.target,
        candidateId: "candidate-amazon",
      },
      {
        scope: "CANDIDATE",
        source: "ELEVENST",
        targetId: ids.target,
        candidateId: "candidate-korean",
      },
    ]));
  });

  it("projects shopping activity, proposals, deal monitoring, and explicit account defaults", async () => {
    const { adapter } = adapterWithServer({ museShoppingFeatures: true });
    const view = await adapter.getWorkspace(ids.curation);

    expect(view.goal).toEqual(expect.objectContaining({
      activeTargetCount: 1,
      candidateCount: 3,
      coverage: "PARTIAL",
    }));
    expect(view.activity).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: "request", body: "6명 홈파티 상품을 찾아줘" }),
      expect.objectContaining({ kind: "result", body: "요청을 받고 상품 조사를 시작했어요." }),
    ]));
    expect(view.agentMessages).toEqual([
      expect.objectContaining({
        id: ids.message,
        code: "SUBSCRIBE_DEALS",
        status: "PENDING",
      }),
    ]);
    expect(view.backgroundResearch).toEqual(expect.objectContaining({
      availability: "available",
      subscriptions: [expect.objectContaining({
        id: ids.subscription,
        maximum: { currency: "KRW", amount: 35000 },
      })],
      findings: [expect.objectContaining({
        id: ids.finding,
        price: { kind: "OBSERVED", amount: { currency: "KRW", amount: 29900 } },
      })],
    }));
    expect(view.shoppingPreferences).toEqual(expect.objectContaining({
      availability: "available",
      version: 2,
      effective: {
        uiLocale: "ko-KR",
        preferredCurrency: "KRW",
        researchCountry: "KR",
      },
    }));
  });

  it("does not present a superseded server message as a completed activity", async () => {
    const supersededMessageId = "dddddddd-1111-4111-8111-dddddddddddd";
    const { adapter } = adapterWithServer({
      museShoppingFeatures: true,
      conversationMessages: [
        {
          id: ids.message,
          responseId: ids.action,
          kind: "ERROR",
          status: "PENDING",
          version: 1,
          createdAt: "2026-09-25T01:58:10Z",
          content: { code: "RESEARCH_FAILED", reasonCode: "PROVIDER_HTTP_REJECTED" },
        },
        {
          id: supersededMessageId,
          responseId: ids.action,
          kind: "RESULT",
          status: "SUPERSEDED",
          version: 1,
          createdAt: "2026-09-25T01:58:10Z",
          content: { code: "RESEARCH_PARTIAL_FAILURE" },
        },
      ],
    });

    const view = await adapter.getWorkspace(ids.curation);

    expect(view.activity).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: `message:${ids.message}`, status: "failed" }),
    ]));
    expect(view.activity?.some((item) => item.id === `message:${supersededMessageId}`)).toBe(false);
    expect(view.agentMessages?.some((message) => message.id === supersededMessageId)).toBe(false);
  });

  it("keeps a superseded successful result as completed audit history", async () => {
    const completedMessageId = "dddddddd-2222-4222-8222-dddddddddddd";
    const { adapter } = adapterWithServer({
      museShoppingFeatures: true,
      conversationMessages: [{
        id: completedMessageId,
        responseId: ids.action,
        kind: "RESULT",
        status: "SUPERSEDED",
        version: 1,
        createdAt: "2026-09-25T02:06:10Z",
        content: { code: "RESEARCH_COMPLETED", body: "후보 7개를 찾았어요." },
      }],
    });

    const view = await adapter.getWorkspace(ids.curation);

    expect(view.activity).toEqual(expect.arrayContaining([
      expect.objectContaining({
        id: `message:${completedMessageId}`,
        status: "complete",
        body: "후보 7개를 찾았어요.",
      }),
    ]));
    expect(view.agentMessages).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: completedMessageId, code: "RESEARCH_COMPLETED" }),
    ]));
  });

  it("responds to a server proposal with its exact message version and command UUID", async () => {
    const { adapter, requests } = adapterWithServer({ museShoppingFeatures: true });
    await adapter.getWorkspace(ids.curation);
    const updated = await adapter.respondToAgentMessage(
      ids.curation,
      ids.message,
      1,
      "ACCEPT",
      { idempotencyKey: ids.command, baseRevision: 7 },
    );

    const response = requests.find((request) => request.url.pathname.endsWith(`/follow-ups/${ids.message}/responses`));
    expect(response?.headers["Idempotency-Key"]).toBe(ids.command);
    expect(response?.body).toEqual({
      response: "ACCEPT",
      expectedVersion: 1,
      clientRequestId: ids.command,
    });
    expect(updated.agentMessages?.[0]).toEqual(expect.objectContaining({
      status: "ACCEPTED",
      version: 2,
    }));
  });

  it("uses the canonical deal and preference mutations, then reloads their stored state", async () => {
    const { adapter, requests } = adapterWithServer({ museShoppingFeatures: true });
    await adapter.getWorkspace(ids.curation);

    const cancelled = await adapter.cancelResearchSubscription(ids.curation, ids.subscription);
    expect(cancelled.backgroundResearch?.subscriptions[0]?.status).toBe("CANCELLED");

    const hidden = await adapter.hideResearchFinding(ids.curation, ids.finding);
    expect(hidden.backgroundResearch?.findings[0]?.status).toBe("HIDDEN");

    const imported = await adapter.importResearchFinding(ids.curation, ids.finding);
    expect(imported.backgroundResearch?.findings[0]).toEqual(expect.objectContaining({
      status: "ADDED",
      candidateId: "candidate-imported",
    }));

    const preferred = await adapter.updateShoppingPreferences(ids.curation, {
      uiLocale: "en-US",
      preferredCurrency: "USD",
      researchCountry: "US",
    });
    expect(preferred.shoppingPreferences?.effective).toEqual({
      uiLocale: "en-US",
      preferredCurrency: "USD",
      researchCountry: "US",
    });
    expect(requests).toEqual(expect.arrayContaining([
      expect.objectContaining({
        method: "POST",
        url: expect.objectContaining({
          pathname: `/api/v1/curations/${ids.curation}/subscriptions/${ids.subscription}/cancel`,
        }),
      }),
      expect.objectContaining({
        method: "POST",
        url: expect.objectContaining({
          pathname: `/api/v1/curations/${ids.curation}/findings/${ids.finding}/hide`,
        }),
      }),
      expect.objectContaining({
        method: "POST",
        url: expect.objectContaining({
          pathname: `/api/v1/curations/${ids.curation}/findings/${ids.finding}/candidates`,
        }),
      }),
    ]));
    expect(requests.find((request) => request.method === "PATCH" && request.url.pathname === "/api/v1/me/preferences")?.body).toEqual({
      schemaVersion: "vitlane.user-preferences.v1",
      expectedVersion: 2,
      uiLocale: "en-US",
      preferredCurrency: "USD",
      researchCountry: "US",
    });

    await adapter.createWorkspace("Find a party snack set", {
      idempotencyKey: ids.generated,
      baseRevision: 0,
    });
    expect(requests.find((request) => request.method === "POST" && request.url.pathname === "/api/v1/shopping-plans")?.body).toEqual(
      expect.objectContaining({
        budget: expect.objectContaining({ currency: "USD" }),
        location: { country: "US" },
      }),
    );
  });

  it("tolerates nullable OpenAPI collections without inventing a question", async () => {
    const { adapter } = adapterWithServer({ nullableCollections: true });
    const view = await adapter.getWorkspace(ids.curation);

    expect(view.activeQuestionId).toBeNull();
    expect(view.questions).toEqual([]);
    expect(view.sourceCoverage).toEqual([]);
    expect(view.processing).toEqual(expect.objectContaining({
      status: "WAITING_SELECTION",
      shouldPoll: false,
    }));
  });

  it("preserves every server cart selection instead of inventing one primary candidate", async () => {
    const { adapter } = adapterWithServer({
      cartCandidateIds: ["candidate-1", "candidate-korean"],
    });
    const view = await adapter.getWorkspace(ids.curation);

    expect(view.selectedCandidateIds).toEqual(["candidate-1", "candidate-korean"]);
  });

  it("projects a failed initial intelligence job instead of reporting a ready workspace", async () => {
    const { adapter } = adapterWithServer({
      intelligenceStatus: "FAILED",
      threadStatus: "SUCCEEDED",
    });
    const view = await adapter.getWorkspace(ids.curation);

    expect(view.processing).toEqual({
      status: "FAILED",
      label: "Target 계획",
      detail: "MODEL_UPSTREAM_FAILED",
      shouldPoll: false,
    });
  });

  it("projects a cancelled initial intelligence job as a terminal cancellation", async () => {
    const { adapter } = adapterWithServer({
      intelligenceStatus: "CANCELLED",
      threadStatus: "SUCCEEDED",
    });
    const view = await adapter.getWorkspace(ids.curation);

    expect(view.processing).toEqual(expect.objectContaining({
      status: "CANCELLED",
      shouldPoll: false,
    }));
  });

  it("keeps an operational result confirmation distinct from a user choice", async () => {
    const { adapter } = adapterWithServer({
      intelligenceStatus: "PENDING",
      threadStatus: "SUCCEEDED",
      activeWorkStatus: "RESULT_CONFIRMATION_REQUIRED",
      initialBudgetEffectOrigin: "MANUAL",
    });
    const view = await adapter.getWorkspace(ids.curation);

    expect(view.processing).toEqual(expect.objectContaining({
      status: "RESULT_CONFIRMATION_REQUIRED",
      shouldPoll: false,
    }));
    expect(view.activeQuestionId).toBeNull();
  });

  it("keeps a failed research job visible when a later parallel job succeeded", async () => {
    const { adapter } = adapterWithServer({
      intelligenceStatuses: ["SUCCEEDED", "FAILED", "SUCCEEDED"],
      threadStatus: "SUCCEEDED",
    });
    const view = await adapter.getWorkspace(ids.curation);

    expect(view.processing).toEqual(expect.objectContaining({
      status: "FAILED",
      detail: "MODEL_UPSTREAM_FAILED",
      shouldPoll: false,
    }));
  });

  it("does not let an older failed action poison a newer successful action", async () => {
    const { adapter } = adapterWithServer({
      intelligenceStatuses: ["FAILED", "SUCCEEDED"],
      intelligenceActionIds: [ids.action, "12121212-1212-4212-8212-121212121212"],
      threadStatus: "SUCCEEDED",
    });
    const view = await adapter.getWorkspace(ids.curation);

    expect(view.processing).toEqual(expect.objectContaining({
      status: "SUCCEEDED",
      shouldPoll: false,
    }));
  });

  it("aggregates duplicate source coverage without hiding an earlier target failure", async () => {
    const { adapter } = adapterWithServer({
      sourceCoverage: [
        { source: "SHOPIFY", status: "FAILED", candidateCount: 0, reasonCode: "UPSTREAM_FAILED" },
        { source: "SHOPIFY", status: "SUCCEEDED", candidateCount: 2 },
      ],
    });
    const view = await adapter.getWorkspace(ids.curation);

    expect(view.sourceCoverage).toEqual([{
      source: "SHOPIFY",
      status: "FAILED",
      candidateCount: 2,
      reasonCode: "UPSTREAM_FAILED",
    }]);
    expect(view.notice["ko-KR"]).toContain("일부 소스");
  });

  it.each(["UNSUPPORTED", "SKIPPED"] as const)(
    "keeps an earlier %s source visible when another target succeeded",
    async (status) => {
      const { adapter } = adapterWithServer({
        sourceCoverage: [
          { source: "SHOPIFY", status, candidateCount: 0 },
          { source: "SHOPIFY", status: "SUCCEEDED", candidateCount: 2 },
        ],
      });
      const view = await adapter.getWorkspace(ids.curation);

      expect(view.sourceCoverage).toEqual([expect.objectContaining({
        source: "SHOPIFY",
        status,
        candidateCount: 2,
      })]);
      expect(view.notice["ko-KR"]).toContain("일부 소스");
    },
  );

  it("projects EMPTY plus SUCCEEDED targets as partial source coverage", async () => {
    const { adapter } = adapterWithServer({
      sourceCoverage: [
        { source: "SHOPIFY", status: "EMPTY", candidateCount: 0 },
        { source: "SHOPIFY", status: "SUCCEEDED", candidateCount: 2 },
      ],
    });
    const view = await adapter.getWorkspace(ids.curation);

    expect(view.sourceCoverage).toEqual([expect.objectContaining({
      source: "SHOPIFY",
      status: "PARTIAL",
      candidateCount: 2,
    })]);
    expect(view.notice["ko-KR"]).toContain("일부 소스");
  });

  it("answers the current server question with its thread revision", async () => {
    const { adapter, requests } = adapterWithServer();
    await adapter.getWorkspace(ids.curation);
    await adapter.answerQuestion(ids.curation, {
      questionId: ids.question,
      selectedOptionIds: [ids.option],
      disposition: "apply",
    }, { idempotencyKey: ids.command, baseRevision: 7 });

    const answer = requests.find((request) => request.url.pathname.endsWith("/answer"));
    expect(answer?.body).toEqual({
      revision: 4,
      questionId: ids.question,
      optionId: ids.option,
    });
  });

  it("keeps budget preview local and applies the authoritative CAS command", async () => {
    const { adapter, requests } = adapterWithServer();
    await adapter.getWorkspace(ids.curation);
    const beforePreview = requests.length;
    const preview = await adapter.previewBudget(ids.curation, 120000, {
      idempotencyKey: ids.command,
      baseRevision: 7,
    });
    expect(requests).toHaveLength(beforePreview);
    expect(preview.requestedBudget).toEqual({ currency: "KRW", amount: 120000 });

    await adapter.applyBudgetPreview(ids.curation, preview.id, {
      idempotencyKey: ids.command,
      baseRevision: 7,
    });
    const patch = requests.find((request) => request.method === "PATCH");
    expect(patch?.body).toEqual({
      schemaVersion: "vitlane.curation-budget.v1",
      commandId: ids.command,
      expectedVersion: 3,
      kind: "SET_TOTAL",
      currency: "KRW",
      totalAmount: "120000",
      allocationMode: "PROPORTIONAL",
    });
    expect(requests.some((request) => request.url.pathname.endsWith("/budget/proposals"))).toBe(false);
  });

  it("marks an AUTO budget as user-confirmed after the canonical budget change effect", async () => {
    const { adapter } = adapterWithServer({ initialBudgetMode: "AUTO" });
    const initial = await adapter.getWorkspace(ids.curation);
    expect(initial.conditions.find((condition) => condition.id === "budget")?.confirmed).toBe(false);

    const preview = await adapter.previewBudget(ids.curation, 120000, {
      idempotencyKey: ids.command,
      baseRevision: 7,
    });
    const updated = await adapter.applyBudgetPreview(ids.curation, preview.id, {
      idempotencyKey: ids.command,
      baseRevision: 7,
    });

    expect(updated.conditions.find((condition) => condition.id === "budget")).toEqual(
      expect.objectContaining({ confirmed: true }),
    );
  });

  it("does not label a managed budget effect as user-confirmed", async () => {
    const { adapter } = adapterWithServer({
      initialBudgetMode: "AUTO",
      initialBudgetEffectOrigin: "PRIMITIVE",
    });
    const view = await adapter.getWorkspace(ids.curation);

    expect(view.conditions.find((condition) => condition.id === "budget")).toEqual(
      expect.objectContaining({ confirmed: false }),
    );
  });

  it("preserves a mixed-currency partial total and withholds a fabricated budget remainder", async () => {
    const { adapter } = adapterWithServer({
      cartTotal: { amount: "24.99", currency: "USD" },
      cartWarnings: [{
        code: "MULTIPLE_CURRENCIES",
        message: "Only the first currency is included.",
      }],
    });
    const view = await adapter.getWorkspace(ids.curation);

    expect(view.plan.totals).toEqual(expect.objectContaining({
      knownTotal: { currency: "USD", amount: 24.99 },
      totalState: "PARTIAL",
      budgetComparisonState: "INCOMPLETE",
      remainingBudget: { currency: "KRW", amount: 0 },
      overBudget: { currency: "KRW", amount: 0 },
    }));

    const preview = await adapter.previewBudget(ids.curation, 120000, {
      idempotencyKey: ids.command,
      baseRevision: 7,
    });
    expect(preview.projectedPlan.totals).toEqual(expect.objectContaining({
      knownTotal: { currency: "USD", amount: 24.99 },
      budgetComparisonState: "INCOMPLETE",
    }));
  });

  it("submits follow-up text as an idempotent curation thread", async () => {
    const { adapter, requests } = adapterWithServer();
    await adapter.getWorkspace(ids.curation);
    await adapter.submitFollowUp(ids.curation, " 더 저렴한 후보를 찾아줘 ", {
      idempotencyKey: ids.command,
      baseRevision: 7,
    });

    const submitted = requests.find((request) => (
      request.url.pathname.endsWith("/threads") && request.method === "POST"
    ));
    expect(submitted?.headers["Idempotency-Key"]).toBe(ids.command);
    expect(submitted?.body).toEqual({
      clientRequestId: ids.command,
      request: "더 저렴한 후보를 찾아줘",
      expectedCurationVersion: 7,
    });
  });

  it("keeps polling after an accepted follow-up when the immediate projection refresh fails", async () => {
    const { adapter, requests } = adapterWithServer({ failFirstWorkspaceReadAfterMutation: true });
    await adapter.getWorkspace(ids.curation);
    const view = await adapter.submitFollowUp(ids.curation, "더 저렴하게", {
      idempotencyKey: ids.command,
      baseRevision: 7,
    });

    expect(view.processing).toEqual(expect.objectContaining({ status: "RUNNING", shouldPoll: true }));
    expect(requests.filter((request) => (
      request.url.pathname.endsWith("/threads") && request.method === "POST"
    ))).toHaveLength(1);
  });

  it("hides a submitted question while reconciling an accepted answer", async () => {
    const { adapter, requests } = adapterWithServer({ failFirstWorkspaceReadAfterMutation: true });
    await adapter.getWorkspace(ids.curation);
    const view = await adapter.answerQuestion(ids.curation, {
      questionId: ids.question,
      selectedOptionIds: [ids.option],
      disposition: "apply",
    }, { idempotencyKey: ids.command, baseRevision: 7 });

    expect(view.activeQuestionId).toBeNull();
    expect(view.processing).toEqual(expect.objectContaining({ shouldPoll: true }));
    expect(requests.filter((request) => request.url.pathname.endsWith("/answer"))).toHaveLength(1);
  });

  it("keeps polling after an accepted budget patch when projection refresh fails", async () => {
    const { adapter, requests } = adapterWithServer({ failFirstWorkspaceReadAfterMutation: true });
    await adapter.getWorkspace(ids.curation);
    const preview = await adapter.previewBudget(ids.curation, 120000, {
      idempotencyKey: ids.command,
      baseRevision: 7,
    });
    const view = await adapter.applyBudgetPreview(ids.curation, preview.id, {
      idempotencyKey: ids.command,
      baseRevision: 7,
    });

    expect(view.processing).toEqual(expect.objectContaining({ shouldPoll: true }));
    expect(requests.filter((request) => request.method === "PATCH")).toHaveLength(1);
  });

  it("reconciles an answer committed before its response was lost without requiring a waiting question", async () => {
    const { adapter, requests, mutationCommits } = adapterWithServer({
      loseFirstMutationResponse: "answer",
    });
    await adapter.getWorkspace(ids.curation);
    const answer = {
      questionId: ids.question,
      selectedOptionIds: [ids.option],
      disposition: "apply" as const,
    };
    const context = { idempotencyKey: ids.command, baseRevision: 7 };

    await expect(adapter.answerQuestion(ids.curation, answer, context)).rejects.toThrow(
      "answer response lost after commit",
    );
    const reconciled = await adapter.answerQuestion(ids.curation, answer, context);

    expect(reconciled.answers).toEqual([expect.objectContaining({
      questionId: ids.question,
      selectedOptionIds: [ids.option],
    })]);
    expect(reconciled.activeQuestionId).toBeNull();
    expect(requests.filter((request) => request.url.pathname.endsWith("/answer"))).toHaveLength(1);
    expect(mutationCommits.answer).toBe(1);
  });

  it("replays the exact budget command after the PATCH committed but its response was lost", async () => {
    const { adapter, requests, mutationCommits } = adapterWithServer({
      loseFirstMutationResponse: "budget",
    });
    await adapter.getWorkspace(ids.curation);
    const preview = await adapter.previewBudget(ids.curation, 120000, {
      idempotencyKey: ids.generated,
      baseRevision: 7,
    });
    const context = { idempotencyKey: ids.command, baseRevision: 7 };

    await expect(adapter.applyBudgetPreview(ids.curation, preview.id, context)).rejects.toThrow(
      "budget response lost after commit",
    );
    const reconciled = await adapter.applyBudgetPreview(ids.curation, preview.id, context);

    const patches = requests.filter((request) => request.method === "PATCH");
    expect(patches).toHaveLength(2);
    expect(patches[1]?.body).toEqual(patches[0]?.body);
    expect(patches[0]?.body).toEqual(expect.objectContaining({
      commandId: ids.command,
      expectedVersion: 3,
      totalAmount: "120000",
    }));
    expect(mutationCommits.budget).toBe(1);
    expect(reconciled.plan.totals.budget).toEqual({ currency: "KRW", amount: 120000 });
  });

  it("replays the same follow-up clientRequestId and body after a committed response was lost", async () => {
    const { adapter, requests, mutationCommits } = adapterWithServer({
      loseFirstMutationResponse: "follow_up",
      advanceCurationRevisionOnFollowUpCommit: true,
    });
    await adapter.getWorkspace(ids.curation);
    const context = { idempotencyKey: ids.command, baseRevision: 7 };

    await expect(adapter.submitFollowUp(ids.curation, "더 저렴하게", context)).rejects.toThrow(
      "follow-up response lost after commit",
    );
    const restored = await adapter.getWorkspace(ids.curation);
    expect(restored.revision).toBe(8);
    const reconciled = await adapter.submitFollowUp(ids.curation, "더 저렴하게", context);

    const posts = requests.filter((request) => (
      request.url.pathname.endsWith("/threads") && request.method === "POST"
    ));
    expect(posts).toHaveLength(2);
    expect(posts[1]?.headers["Idempotency-Key"]).toBe(ids.command);
    expect(posts[1]?.body).toEqual(posts[0]?.body);
    expect(posts[0]?.body).toEqual({
      clientRequestId: ids.command,
      request: "더 저렴하게",
      expectedCurationVersion: 7,
    });
    expect(mutationCommits.followUp).toBe(1);
    expect(reconciled.revision).toBe(8);
    expect(reconciled.lastChange[0]?.["en-US"]).toContain("submitted");
  });

  it("does not spend provider hydration quota on every workspace poll", async () => {
    const { adapter, requests } = adapterWithServer();
    await adapter.getWorkspace(ids.curation);
    const afterFirstRead = requests.filter((request) => (
      request.url.pathname.endsWith("/catalog-research/hydrations")
    )).length;
    await adapter.getWorkspace(ids.curation);

    expect(requests.filter((request) => (
      request.url.pathname.endsWith("/catalog-research/hydrations")
    ))).toHaveLength(afterFirstRead);
    expect(afterFirstRead).toBeGreaterThan(0);
  });

  it("treats a non-ready provider response as incomplete and retries after the short cache TTL", async () => {
    let time = 0;
    const { adapter, requests } = adapterWithServer(
      { koreanHydrationStatus: "UNRESOLVED" },
      { now: () => time },
    );
    const first = await adapter.getWorkspace(ids.curation);
    const firstHydrationCount = requests.filter((request) => (
      request.url.pathname.endsWith("/catalog-research/hydrations")
    )).length;

    expect(first.notice["en-US"]).toContain("Some sources could not be loaded");
    expect(first.noticeTone).toBe("warning");
    time = 9_000;
    await adapter.getWorkspace(ids.curation);
    expect(requests.filter((request) => (
      request.url.pathname.endsWith("/catalog-research/hydrations")
    ))).toHaveLength(firstHydrationCount);

    time = 10_001;
    await adapter.getWorkspace(ids.curation);
    expect(requests.filter((request) => (
      request.url.pathname.endsWith("/catalog-research/hydrations")
    )).length).toBeGreaterThan(firstHydrationCount);
  });
});
