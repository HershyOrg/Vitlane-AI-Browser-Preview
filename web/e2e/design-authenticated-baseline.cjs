const { firefox } = require("playwright");
const { auditRadiusBoldBorders } = require("./shape-audit.cjs");
const { assertTextContrast } = require("./contrast.cjs");
const crypto = require("node:crypto");
const fs = require("node:fs/promises");
const path = require("node:path");

const webRoot = path.resolve(__dirname, "..");
const tokenSourcePath = path.join(
  webRoot,
  "src/shared/ui/design-system/tokens.source.json",
);
const outputDir = path.resolve(
  process.env.DESIGN_AUTH_BASELINE_DIR ??
    "../docs/design/evidence/authenticated-baseline",
);
const koreanFontPath = process.env.DESIGN_KOREAN_FONT;
const firefoxPath = process.env.E2E_FIREFOX_PATH;
const configuredBaseURL = process.env.DESIGN_AUTH_BASE_URL?.replace(/\/$/, "");
const skipContrast = process.env.DESIGN_AUTH_SKIP_CONTRAST === "1";
const targetFilter = new Set(
  (process.env.DESIGN_AUTH_TARGET ?? "").split(",").filter(Boolean),
);
const viewportFilter = new Set(
  (process.env.DESIGN_AUTH_VIEWPORT ?? "").split(",").filter(Boolean),
);
// Frame mode (marketing "왜 Vitlane인가" frames): one fixed viewport named
// "frame", the screenshot clipped to it instead of the full page, at the given
// device scale. Everything else — fixtures, routes, checks — stays the same.
const frameViewport = (() => {
  const match = /^(\d+)x(\d+)$/.exec(process.env.DESIGN_AUTH_FRAME ?? "");
  return match ? { width: Number(match[1]), height: Number(match[2]) } : null;
})();
const deviceScaleFactor = Number(process.env.DESIGN_AUTH_SCALE ?? "1");
const frameFormat = process.env.DESIGN_AUTH_FRAME_FORMAT === "jpeg" ? "jpeg" : "png";
// Order variant (marketing Lane frame 05, ADR-0074): `lane-shoes` turns the
// agency order into the landing's synthetic Daily Trainer order (90.00 USD, no
// agency fee) and the sidebar into that single curation, so the orders page
// continues the same story as the curation frames. Fixtures stay deterministic.
const orderVariant = process.env.DESIGN_AUTH_ORDER_VARIANT ?? "";
const laneProductImageDir = path.resolve(webRoot, "../marketing/assets/products");

const NOW = "2026-07-25T09:00:00.000Z";
const FUTURE = "2099-07-25T09:00:00.000Z";
const PLAN_ID = "plan-design-baseline";
const CURATION_ID = "curation-design-baseline";
const TARGET_READY_ID = "target-design-ready";
const TARGET_REVIEW_ID = "target-design-review";
const SESSION_READY_ID = "session-design-ready";
const SESSION_REVIEW_ID = "session-design-review";
const SESSION_CHECKOUT_ID = "session-design-checkout";
const PURCHASE_ID = "purchase-design-baseline";
const COMPLETED_PURCHASE_ID = "purchase-design-completed";
const PAYMENT_ID = "payment-design-baseline";
const fixtureImages = new Map();

const currentUser = {
  id: "operator-design-user",
  email: "minji@vitlane.example",
  displayName: "민지",
  marketingAdmin: true,
  phase5Operator: true,
};

const account = {
  wallets: [{
    wallet: {
      id: "wallet-design-default",
      userId: currentUser.id,
      address: "0x1111111111111111111111111111111111111111",
      accountId:
        "eip155:91342:0x1111111111111111111111111111111111111111",
      chainId: "eip155:91342",
      registrationStatus: "REGISTERED",
      currentOwnershipProofId: "proof-design-default",
      isDefault: true,
      registeredAt: NOW,
      createdAt: NOW,
      updatedAt: NOW,
    },
    ownership: {
      status: "VALID",
      proofId: "proof-design-default",
      verifiedAt: NOW,
      validUntil: FUTURE,
    },
    kyc: {
      eligibility: "VALID",
      actionEligible: true,
      providerKind: "MOCK_DOJANG",
      externalEffect: "SIMULATED",
      disclosure: "실제 외부 확인 없음",
      credential: {
        id: "credential-design-default",
        validUntil: FUTURE,
      },
      observation: {
        id: "observation-design-default",
        credentialId: "credential-design-default",
        status: "VALID",
        observedAt: NOW,
        validUntil: FUTURE,
      },
    },
    actions: {
      canSetDefault: false,
      canDeregister: true,
      canReauthenticate: false,
      canStartKYC: false,
      canCheckKYC: false,
    },
  }],
  buyerProfiles: [{
    id: "buyer-design-default",
    profileKind: "TEST_PROFILE",
    label: "Vitlane TEST 수령인",
    fixtureKey: "vitlane-design-baseline",
    country: "KR",
    city: "서울",
    version: 1,
    snapshotHash: `0x${"a".repeat(64)}`,
    containsRealPii: false,
    isDefault: true,
  }],
  shippingProfiles: [{
    id: "shipping-design-default",
    label: "집",
    country: "KR",
    maskedSummary: "KR · 서울 · •••12",
    keyVersion: "pii-v1",
    version: 1,
    isDefault: true,
    createdAt: NOW,
    updatedAt: NOW,
  }],
  policyAcceptances: [{
    policyId: "PHASE5_TEST_SETTLEMENT",
    policyVersion: "2026-07-24",
    acceptedAt: NOW,
  }],
  assurancePolicy: {
    PLAN_RESEARCH: "LOGIN",
    WALLET_REGISTRATION: "CURRENT_WALLET_OWNERSHIP_PROOF",
    TEST_LOW_VALUE: "CURRENT_WALLET_OWNERSHIP_PROOF",
    TEST_ADVANCED: "CURRENT_MOCK_DOJANG_KYC_CREDENTIAL",
    REAL_VALUE_PAYMENT: "NOT_AVAILABLE",
  },
};

function target(id, title, normalizedIntent, category, amount, orderIndex) {
  return {
    id,
    planId: PLAN_ID,
    curationId: CURATION_ID,
    userId: currentUser.id,
    title,
    normalizedIntent,
    category,
    allocatedBudget: { amount, currency: "USD" },
    researchScope: {
      category,
      country: "KR",
      city: "서울",
      allowedItems: ["한국 배송 가능", "평점 4점 이상"],
      blockedItems: ["중고", "병행수입"],
      minPrice: { amount: "20", currency: "USD" },
      maxPrice: { amount, currency: "USD" },
      urlMode: "NONE",
    },
    orderIndex,
    confirmedAt: NOW,
    targetHash: `target-hash-${id}`,
    targetHashSchema: "vitlane.plan-target.v1",
    version: 2,
    createdAt: NOW,
    updatedAt: NOW,
  };
}

const readyTarget = target(
  TARGET_READY_ID,
  "출퇴근용 노이즈 캔슬링 헤드폰",
  "장시간 착용해도 편하고 멀티포인트를 지원하는 헤드폰",
  "headphones",
  "350",
  0,
);
const reviewTarget = target(
  TARGET_REVIEW_ID,
  "휴대용 USB-C 충전기",
  "노트북과 휴대폰을 함께 충전하는 100W급 GaN 충전기",
  "usb-c-charger",
  "120",
  1,
);

function session(id, sourceTarget, status, extra = {}) {
  return {
    id,
    planTargetId: sourceTarget.id,
    userId: currentUser.id,
    targetSnapshot: {
      id: sourceTarget.id,
      planId: PLAN_ID,
      curationId: CURATION_ID,
      title: sourceTarget.title,
      normalizedIntent: sourceTarget.normalizedIntent,
      category: sourceTarget.category,
      allocatedBudget: sourceTarget.allocatedBudget,
      targetHash: sourceTarget.targetHash,
      targetHashSchema: sourceTarget.targetHashSchema,
      confirmedAt: NOW,
    },
    researchScopeSnapshot: sourceTarget.researchScope,
    status,
    version: 4,
    createdAt: NOW,
    updatedAt: NOW,
    ...extra,
  };
}

const readySession = session(SESSION_READY_ID, readyTarget, "READY");
const reviewSession = session(SESSION_REVIEW_ID, reviewTarget, "REVIEWING", {
  currentResearchRoundId: "round-design-review",
});
const checkoutSession = session(
  SESSION_CHECKOUT_ID,
  readyTarget,
  "REVIEWING",
  {
    currentResearchRoundId: "round-design-checkout",
  },
);

function journey(currentStep, currentStage, sessionCount) {
  const order = ["INTENT", "CURATION"];
  const labels = {
    INTENT: "요청",
    CURATION: "큐레이션",
  };
  const paths = {
    INTENT: "/plans/new",
    CURATION: `/curations/${CURATION_ID}`,
  };
  const currentIndex = order.indexOf(currentStep);
  return {
    currentStep,
    currentStage,
    resumePath: paths[currentStep],
    nextAction: "현재 단계 계속",
    steps: order.map((key, index) => ({
      key,
      label: labels[key],
      state:
        index < currentIndex
          ? "COMPLETED"
          : index === currentIndex
            ? "CURRENT"
            : "LOCKED",
      path: index <= currentIndex ? paths[key] : undefined,
      readOnly: index < currentIndex,
    })),
    sessionCount,
  };
}

function plan() {
  return {
    id: PLAN_ID,
    userId: currentUser.id,
    originalIntent:
      "출퇴근 장비를 정리하고 싶어요. 편한 노이즈 캔슬링 헤드폰과 노트북까지 충전되는 작은 충전기를 찾아주세요.",
    planningMode: "AUTO",
    executionMode: "EXPERIMENT",
    totalBudget: { amount: "470", currency: "USD" },
    locationContext: { country: "KR", city: "서울" },
    createdAt: NOW,
  };
}

const activePlanResult = {
  plan: plan(),
  curation: {
    id: CURATION_ID,
    shoppingPlanId: PLAN_ID,
    userId: currentUser.id,
    phase: "CURATING",
    version: 3,
    createdAt: NOW,
    updatedAt: NOW,
  },
  targets: [readyTarget, reviewTarget],
  sessions: [readySession, reviewSession],
  journey: journey("CURATION", "상품 조사", {
    ready: 1,
    researching: 0,
    reviewing: 1,
  }),
};

const planningPlanResult = {
  plan: plan(),
  curation: {
    id: CURATION_ID,
    shoppingPlanId: PLAN_ID,
    userId: currentUser.id,
    phase: "PLANNING",
    version: 1,
    createdAt: NOW,
    updatedAt: NOW,
  },
  targets: [],
  sessions: [],
  planningTask: {
    id: "planning-task-design",
    planId: PLAN_ID,
    userId: currentUser.id,
    contextVersion: 1,
    contextHash: "planning-context-design",
    status: "REQUESTED",
    expiresAt: FUTURE,
    firstDiscoveredAt: NOW,
    firstContextReadAt: NOW,
    lastAgentActivityAt: NOW,
    createdAt: NOW,
    updatedAt: NOW,
  },
  journey: {
    ...journey("CURATION", "PLANNING", {
      ready: 0,
      researching: 0,
      reviewing: 0,
    }),
    resumePath: `/curations/${CURATION_ID}`,
    nextAction: "Curation에서 조사 항목 구성을 확인해 주세요.",
  },
};

const completedExpansionResult = {
  run: {
    id: "run-design-expansion",
    planId: PLAN_ID,
    userId: currentUser.id,
    kind: "EXPANSION",
    instruction: "노트북과 함께 쓸 휴대용 USB-C 충전기도 추가해줘",
    planningTaskId: "planning-task-design-expansion",
    status: "COMPLETED",
    createdAt: NOW,
    updatedAt: NOW,
    completedAt: NOW,
  },
  planningTask: {
    id: "planning-task-design-expansion",
    planId: PLAN_ID,
    userId: currentUser.id,
    contextVersion: 1,
    contextHash: "planning-context-design-expansion",
    status: "COMPLETED",
    expiresAt: FUTURE,
    completedAt: NOW,
    firstDiscoveredAt: NOW,
    firstContextReadAt: NOW,
    lastAgentActivityAt: NOW,
    createdAt: NOW,
    updatedAt: NOW,
  },
  intelligenceJob: { jobId: "job-design-expansion", replay: false },
  replay: false,
};

function productImage(label, background, foreground) {
  const svg = [
    '<svg xmlns="http://www.w3.org/2000/svg" width="640" height="480">',
    `<rect width="640" height="480" fill="${background}"/>`,
    `<circle cx="320" cy="210" r="112" fill="${foreground}" opacity=".88"/>`,
    `<text x="320" y="390" text-anchor="middle" font-family="sans-serif" font-size="30" fill="${foreground}">${label}</text>`,
    "</svg>",
  ].join("");
  const pathname = `/__design-fixture__/${encodeURIComponent(label)}.svg`;
  fixtureImages.set(pathname, svg);
  return pathname;
}

function candidate(
  id,
  name,
  amount,
  orderIndex,
  {
    imageUrl,
    description,
    summary,
    matchedCriteria,
    tradeoffs,
    variantDiscovery = {
      schemaVersion: "vitlane.variant-discovery.v1",
      status: "UNKNOWN",
      fields: [],
      observedAt: NOW,
      evidence: {
        summary: "구매 전에 상품 페이지에서 옵션 유무를 확인해야 합니다.",
      },
    },
    purchaseStatus = "TEST_ORDER_FLOW_AVAILABLE",
    interaction,
  },
) {
  return {
    id,
    researchSubmissionId: "submission-design-review",
    shoppingSessionId: SESSION_REVIEW_ID,
    productUrl: `https://shop.example.test/products/${id}`,
    merchantDomain: "shop.example.test",
    category: "usb-c-charger",
    name,
    description,
    imageUrl,
    price: { amount, currency: "USD" },
    variantDiscovery,
    purchasePath: {
      providerKind: "GENERIC_WEB",
      executionMode: "MANUAL_MERCHANT_ORDER",
      externalEffect: "SIMULATED",
      liveOrderability: "UNVERIFIED",
      settlementStatus: "SUPPORTED",
      status: purchaseStatus,
      reasonCodes: [],
    },
    evidence: {
      summary,
      matchedCriteria,
      tradeoffs,
      sourceUrls: [`https://shop.example.test/products/${id}`],
    },
    observedAt: NOW,
    purchaseSupport: "UNKNOWN",
    eligibility: {
      hardChecks: "PASS",
      semanticReview: "REQUIRED",
      reasonCodes: [],
      policyVersion: "vitlane.research-policy.v1",
    },
    candidateHashSchema: "vitlane.candidate.v2",
    candidateHash: `candidate-hash-${id}`,
    orderIndex,
    createdAt: NOW,
    isActive: true,
    ...(interaction ? { interaction } : {}),
  };
}

const variantDiscovery = {
  schemaVersion: "vitlane.variant-discovery.v1",
  status: "OBSERVED_PARTIAL",
  fields: [
    {
      key: "color",
      label: "색상",
      inputKind: "ENUM",
      required: true,
      knownValues: [
        { value: "graphite", label: "그래파이트" },
        { value: "white", label: "화이트" },
      ],
      source: "AGENT_OBSERVATION",
      discoveryStatus: "OBSERVED_PARTIAL",
    },
    {
      key: "plug",
      label: "플러그",
      inputKind: "ENUM_OR_VALUE",
      required: true,
      knownValues: [
        { value: "us", label: "US" },
        { value: "eu", label: "EU" },
      ],
      source: "AGENT_OBSERVATION",
      discoveryStatus: "OBSERVED_PARTIAL",
    },
  ],
  observedAt: NOW,
  evidence: {
    summary: "상품 상세 페이지에 표시된 옵션을 기준으로 정리했습니다.",
    sourceUrls: ["https://shop.example.test/products/charger-a"],
  },
};

const researchResult = {
  planId: PLAN_ID,
  groups: [
    {
      session: readySession,
      candidates: [],
      rounds: [],
    },
    {
      session: reviewSession,
      round: {
        id: "round-design-review",
        shoppingSessionId: SESSION_REVIEW_ID,
        userId: currentUser.id,
        roundNumber: 1,
        contextSchema: "vitlane.research-context.v1",
        contextVersion: 1,
        contextHash: "research-context-design",
        status: "RESULTS_READY",
        resultSubmissionId: "submission-design-review",
        firstDiscoveredAt: NOW,
        firstContextReadAt: NOW,
        lastAgentActivityAt: NOW,
        createdAt: NOW,
        completedAt: NOW,
      },
      submission: {
        id: "submission-design-review",
        researchRoundId: "round-design-review",
        intelligenceJobId: "job-design-review",
        clientSubmissionId: "client-submission-design",
        schemaVersion: "vitlane.research-submission.v3",
        contextVersion: 1,
        contextHash: "research-context-design",
        submissionHash: "submission-hash-design",
        payload: {
          summary: "100W급 충전기 세 가지를 가격과 휴대성 중심으로 비교했습니다.",
          outcome: "RESULTS",
        },
        outcome: "RESULTS",
        validationStatus: "VALID",
        validationReasonCodes: [],
        submittedAt: NOW,
      },
      candidates: [
        candidate(
          "charger-a",
          "Anker Prime 100W GaN 초소형 3포트 USB-C 충전기 — 노트북·휴대폰 동시 충전",
          "84.99",
          0,
          {
            imageUrl: productImage("100W GaN", "#edf2f4", "#23343b"),
            description:
              "USB-C 두 개와 USB-A 한 개를 제공하며 단일 포트 최대 100W를 지원합니다. 접이식 플러그와 작은 본체로 출퇴근 가방에 넣기 좋지만, 케이블은 별도입니다.",
            summary:
              "예산 안에서 가장 작고, 노트북과 휴대폰을 동시에 충전할 때 출력 배분 정보가 명확합니다.",
            matchedCriteria: ["100W급", "3포트", "접이식 플러그"],
            tradeoffs: ["충전 케이블 별도", "세 후보 중 가장 높은 가격"],
            variantDiscovery,
            purchaseStatus: "OPTIONS_REQUIRED",
            interaction: {
              shoppingSessionId: SESSION_REVIEW_ID,
              candidateId: "charger-a",
              pinned: true,
              sentiment: "LIKE",
              version: 2,
              updatedAt: NOW,
            },
          },
        ),
        candidate(
          "charger-b",
          "UGREEN Nexode 100W 4-Port Charger",
          "59.99",
          1,
          {
            description:
              "포트 수가 많고 가격이 낮은 범용 충전기입니다. 이미지 제공 없이 수집된 후보라 상품 페이지에서 외형과 플러그 옵션을 다시 확인해야 합니다.",
            summary:
              "네 개의 기기를 연결할 수 있어 활용 범위가 넓고, 가격이 가장 낮습니다.",
            matchedCriteria: ["100W급", "4포트", "예산 절약"],
            tradeoffs: ["이미지 미제공", "동시 충전 출력 배분 재확인 필요"],
            interaction: {
              shoppingSessionId: SESSION_REVIEW_ID,
              candidateId: "charger-b",
              pinned: false,
              sentiment: "DISLIKE",
              version: 1,
              updatedAt: NOW,
            },
          },
        ),
        candidate(
          "charger-c",
          "Satechi 108W Pro USB-C PD Desktop Charger",
          "74.99",
          2,
          {
            imageUrl: productImage("108W PD", "#e9e5dc", "#564b3f"),
            description:
              "데스크 위에서 여러 기기를 충전하기 좋은 모델입니다. 총 출력은 충분하지만 전원 케이블이 따로 있어 휴대성은 다른 후보보다 낮습니다.",
            summary:
              "고정된 작업 공간에서 여러 기기를 안정적으로 충전하려는 경우 균형이 좋습니다.",
            matchedCriteria: ["108W 총출력", "다기기 충전"],
            tradeoffs: ["전원 케이블 필요", "휴대성 낮음"],
            interaction: {
              shoppingSessionId: SESSION_REVIEW_ID,
              candidateId: "charger-c",
              pinned: false,
              sentiment: "NONE",
              version: 1,
              updatedAt: NOW,
            },
          },
        ),
      ],
      rounds: [],
    },
  ],
};

const activeCart = {
  curationId: CURATION_ID,
  selections: [{
    selection: {
      id: "selection-design-charger",
      curationId: CURATION_ID,
      targetId: TARGET_REVIEW_ID,
      shoppingSessionId: SESSION_REVIEW_ID,
      candidateId: "charger-a",
      candidateConfigurationId: "configuration-design-charger",
      configurationHash: `0x${"7".repeat(64)}`,
      quantity: 1,
      version: 1,
      selectedAt: NOW,
      updatedAt: NOW,
    },
    candidate: {
      id: "charger-a",
      name: researchResult.groups[1].candidates[0].name,
      productUrl: researchResult.groups[1].candidates[0].productUrl,
      imageUrl: researchResult.groups[1].candidates[0].imageUrl,
    },
    unitPrice: researchResult.groups[1].candidates[0].price,
    lineTotal: researchResult.groups[1].candidates[0].price,
  }],
  total: researchResult.groups[1].candidates[0].price,
  warnings: [],
  updatedAt: NOW,
};

function actionDescriptor(
  id,
  alias,
  subjectType,
  bodySchema,
  effectKind,
  transcriptPolicy = "APPEND",
) {
  return {
    id,
    alias,
    enabled: true,
    subjectSchema: {
      type: subjectType,
      idRequired: !["INTENT", "TARGET_LIST"].includes(subjectType),
    },
    bodySchema,
    effectKind,
    transcriptPolicy,
    expectedResourceVersion: 3,
    requiresConfirmation: id.startsWith("PURCHASE_"),
  };
}

const planningActions = [
  actionDescriptor(
    "PLANNING_ADD_TARGETS",
    "@TargetList-AddTarget",
    "TARGET_LIST",
    "TEXT_REQUIRED",
    "AGENT_WORK",
  ),
  {
    ...actionDescriptor(
      "PLANNING_START_CURATING",
      "@Planning-NextStep",
      "CURATION",
      "NONE",
      "AGENT_WORK",
    ),
    requestedTransitionTo: "CURATING",
  },
];

const curatingActions = [
  actionDescriptor(
    "CURATION_ADD_TARGETS",
    "@Curation-AddTarget",
    "CURATION",
    "TEXT_REQUIRED",
    "AGENT_WORK",
  ),
  actionDescriptor(
    "TARGET_RESEARCH_AGAIN",
    "@Target-ResearchAgain",
    "TARGET",
    "TEXT_REQUIRED",
    "AGENT_WORK",
  ),
  actionDescriptor(
    "TARGET_REMOVE",
    "@Target-Remove",
    "TARGET",
    "NONE",
    "NONE",
    "PATCH_ONLY",
  ),
  // ADR-0054: CANDIDATE_INTERACTION·PURCHASE_DIRECT·PURCHASE_CART는 closed
  // action catalog에서 제거됐다 — mock workspace도 현재 catalog만 흉내낸다.
  actionDescriptor(
    "SELECTION_MUTATION",
    "@Selection-Mutation",
    "SELECTION",
    "TYPED_COMMAND",
    "NONE",
    "PATCH_ONLY",
  ),
];

const intentAction = {
  id: "intent-next-step-design",
  curationId: CURATION_ID,
  actorUserId: currentUser.id,
  type: "INTENT_NEXT_STEP",
  phaseAtRequest: "HAVING_INTENT",
  requestedTransitionTo: "PLANNING",
  subjectType: "INTENT",
  effectKind: "NONE",
  sourceRefType: "SHOPPING_PLAN",
  sourceRefId: PLAN_ID,
  expectedCurationVersion: 1,
  createdAt: NOW,
};

function curationWorkspace(fixture) {
  const planning = fixture === "planning-progress";
  const result = planning ? planningPlanResult : activePlanResult;
  return {
    plan: result.plan,
    curation: result.curation,
    targets: result.targets,
    // ADR-0038: the workspace projection is the browser's only progress
    // source, so the planning baseline shows a running intelligence job.
    intelligence: planning
      ? [
          {
            jobId: "job-design-planning",
            targetKind: "PLANNING_TASK",
            targetId: "planning-task-design",
            provider: "MANAGED",
            status: "RUNNING",
            retryable: false,
            attempt: 1,
            steps: [
              {
                id: "step-design-1",
                jobId: "job-design-planning",
                attemptId: "attempt-design-1",
                ordinal: 1,
                kind: "INTERPRETING",
                status: "RUNNING",
                startedAt: NOW,
              },
            ],
          },
        ]
      : [],
    research: planning
      ? { planId: PLAN_ID, groups: [] }
      : researchResult,
    cart: planning
      ? {
          curationId: CURATION_ID,
          selections: [],
          total: { amount: "0", currency: "USD" },
          warnings: [],
          updatedAt: NOW,
        }
      : activeCart,
    availableActions: planning ? planningActions : curatingActions,
    timeline: [{
      action: intentAction,
      displayBody: result.plan.originalIntent,
      result: {
        kind: "INTENT_ACCEPTED",
        summary: "Curation 계획을 시작했습니다.",
        occurredAt: NOW,
      },
    }],
    latestArtifact: planning ? "TARGET_LIST" : "CURATION_BOARD",
    coverage: "NONE",
    ...(planning
      ? {
          activeWork: {
            workTargetId: "planning-task-design",
            label: "조사 항목을 만들고 있습니다",
            status: "RUNNING",
          },
        }
      : {}),
  };
}

const catalogLikedVariants = [{
  curationId: CURATION_ID,
  candidateId: "candidate-design-phase8-charger",
  variantId: "gid://shopify/ProductVariant/8499001",
  productTitle: "Anker Prime 100W GaN Charger",
  variantTitle: "Graphite / US",
  productUrl: "https://design-store.myshopify.com/products/anker-prime-100w",
  merchant: "Shopify Design Store",
  priceMinor: 8499,
  currency: "USD",
  targetTitle: reviewTarget.title,
  updatedAt: NOW,
}];

const settlementConfig = {
  environment: "GIWA_TESTNET",
  chainId: 91342,
  chainCaip2: "eip155:91342",
  rpcUrl: "https://rpc.test.invalid",
  explorerUrl: "https://explorer.test.invalid",
  tokenAddress: `0x${"2".repeat(40)}`,
  faucetAddress: `0x${"3".repeat(40)}`,
  settlementAddress: `0x${"4".repeat(40)}`,
  tokenSymbol: "tVITUSD",
  tokenDecimals: 6,
  feeBps: 100,
  feeRecipient: `0x${"5".repeat(40)}`,
  claimAmountBaseUnits: "100000000",
};

const agencyOrderProjection = {
  agencyOrder: {
    id: "a0000000-0000-4000-8000-000000000001",
    status: "ISSUED",
    lines: [{
      lineId: "line-design-headphones",
      sourceCartItemId: "cart-line-design-headphones",
      planTargetId: TARGET_REVIEW_ID,
      candidateId: "candidate-design-headphones",
      productTitle: "Sony WH-1000XM6 Wireless Noise Cancelling Headphones",
      productUrl: "https://audio.example.test/products/headphones-design",
      imageUrl: productImage("NC HEADPHONES", "#e7ecef", "#1f2933"),
      variantTitle: "Black",
      selectedOptions: ["색상: 블랙"],
      quantity: 1,
      unitPrice: { amountMinor: 34_999, currency: "USD" },
      lineSubtotal: { amountMinor: 34_999, currency: "USD" },
      shopDomain: "audio.example.test",
    }],
    shippingAddress: {
      snapshotRef: "shipping-snapshot-design",
      snapshotRevision: 1,
      snapshotHash: `0x${"7".repeat(64)}`,
      maskedSummary: "US · New York · •••12",
      country: "US",
    },
    paymentSelection: {
      rail: "GIWA",
      providerEnvironment: "TESTNET",
      asset: "TVITUSD",
      economicEffect: "NO_REAL_VALUE",
      merchantExecution: "SIMULATED",
    },
    merchantCheckouts: [],
    passThroughTotal: { amountMinor: 34_999, currency: "USD" },
    agencyFee: {
      variable: { amountMinor: 350, currency: "USD" },
      fixed: { amountMinor: 0, currency: "USD" },
      total: { amountMinor: 350, currency: "USD" },
      policyVersion: "TVITUSD-1PCT",
    },
    customerPayableTotal: { amountMinor: 35_349, currency: "USD" },
    snapshotHash: `0x${"8".repeat(64)}`,
    issuedAt: NOW,
    expiresAt: FUTURE,
  },
  paymentInstruction: {
    id: "b0000000-0000-4000-8000-000000000001",
    agencyOrderId: "a0000000-0000-4000-8000-000000000001",
    agencyOrderSnapshotHash: `0x${"8".repeat(64)}`,
    paymentSelection: {
      rail: "GIWA",
      providerEnvironment: "TESTNET",
      asset: "TVITUSD",
      economicEffect: "NO_REAL_VALUE",
      merchantExecution: "SIMULATED",
    },
    customerPayableTotal: { amountMinor: 35_349, currency: "USD" },
    paymentPolicyVersion: "TVITUSD-1PCT",
    state: "CONSUMED",
    expiresAt: FUTURE,
  },
  process: {
    agencyOrderId: "a0000000-0000-4000-8000-000000000001",
    state: "PROCUREMENT_IN_PROGRESS",
    version: 4,
    createdAt: NOW,
    updatedAt: NOW,
  },
  availableActions: [],
  payment: {
    id: "c0000000-0000-4000-8000-000000000001",
    state: "FINALIZED",
    amountBaseUnits: "353490000",
    payTxHash: `0x${"d".repeat(64)}`,
    finalizedBlock: 912345,
    updatedAt: NOW,
  },
  chainTransactions: [{
    purpose: "PAY",
    txHash: `0x${"d".repeat(64)}`,
    state: "FINALIZED",
    blockNumber: 912345,
    updatedAt: NOW,
  }],
  merchantOrders: [{
    id: "d0000000-0000-4000-8000-000000000001",
    allocationId: "allocation-design-headphones",
    shopDomain: "audio.example.test",
    merchantId: "SHOP_EXAMPLE",
    checkoutOrdinal: 1,
    customerGrossAmount: { amountMinor: 35_349, currency: "USD" },
    fundingState: "AVAILABLE",
    executionMode: "SIMULATED_NO_EFFECT",
    state: "PLANNED",
    operational: {
      stage: "PROCUREMENT_PENDING",
      workStage: "PROCUREMENT",
      progress: {
        funding: "CURRENT",
        procurement: "WAITING",
        delivery: "WAITING",
        resolution: "WAITING",
      },
      units: {
        total: 1,
        ordered: 1,
        procuring: 0,
        awaitingShipment: 0,
        inTransit: 0,
        delivered: 0,
        exception: 0,
        returnInProgress: 0,
        refundRequested: 0,
        refundPending: 0,
        refunded: 0,
        procurementFailed: 0,
        cancelled: 0,
      },
    },
    units: [{
      id: "e0000000-0000-4000-8000-000000000001",
      lineId: "line-design-headphones",
      unitIndex: 1,
      disposition: "PENDING",
    }],
    updatedAt: NOW,
  }],
  shipments: [],
  units: [{
    merchantOrderUnitId: "e0000000-0000-4000-8000-000000000001",
    merchantOrderId: "d0000000-0000-4000-8000-000000000001",
    allocationId: "allocation-design-headphones",
    lineId: "line-design-headphones",
    unitIndex: 1,
    shopDomain: "audio.example.test",
    stage: "ORDERED",
    refundStatus: "AVAILABLE",
  }],
};

const purchase = {
  schemaVersion: "vitlane.purchase-intent.v2",
  id: PURCHASE_ID,
  shoppingSessionId: SESSION_CHECKOUT_ID,
  merchantId: "SHOP_EXAMPLE",
  quantity: 1,
  purchaseHash: `0x${"6".repeat(64)}`,
  status: "AWAITING_USER_APPROVAL",
  providerRef: {
    provider: "GENERIC_WEB",
    schemaVersion: "vitlane.provider-ref.v1",
    productId: "headphones-design",
  },
  merchantRef: {
    displayName: "Example Audio",
    canonicalHost: "audio.example.test",
  },
  offerRef: {
    provider: "GENERIC_WEB",
    offerFingerprint: "offer-design-headphones",
  },
  purchasePath: {
    schemaVersion: "vitlane.purchase-path.v1",
    providerKind: "GENERIC_WEB",
    executionMode: "MANUAL_MERCHANT_ORDER",
    externalEffect: "SIMULATED",
    liveOrderability: "UNVERIFIED",
    settlementStatus: "SUPPORTED",
    status: "TEST_ORDER_FLOW_AVAILABLE",
    reasonCodes: [],
  },
  candidateConfiguration: {
    id: "configuration-design-headphones",
    schemaVersion: "vitlane.candidate-configuration.v1",
    selections: [{
      key: "color",
      label: "색상",
      value: "black",
      valueLabel: "블랙",
      source: "USER_CONFIRMED",
    }],
    confirmsNoOptions: false,
    configurationHash: `0x${"7".repeat(64)}`,
  },
  variantResolution: {
    status: "USER_SPECIFIED_UNVERIFIED",
    configurationHash: `0x${"7".repeat(64)}`,
    resolvedBy: "USER",
  },
  candidate: {
    candidateId: "candidate-design-headphones",
    name: "Sony WH-1000XM6 Wireless Noise Cancelling Headphones",
    merchantDomain: "audio.example.test",
    productUrl: "https://audio.example.test/products/headphones-design",
    imageUrl: productImage("NC HEADPHONES", "#e7ecef", "#1f2933"),
    price: { amount: "349.99", currency: "USD" },
  },
};

const quote = {
  id: "quote-design-baseline",
  purchaseId: PURCHASE_ID,
  quoteHash: `0x${"8".repeat(64)}`,
  unitPrice: { amount: "349.99", currency: "USD" },
  quantity: 1,
  itemSubtotal: { amount: "349.99", currency: "USD" },
  shipping: { amount: "0", currency: "USD" },
  tax: { amount: "0", currency: "USD" },
  discount: { amount: "0", currency: "USD" },
  payableTotal: { amount: "349.99", currency: "USD" },
  settlementAmountBaseUnits: "349990000",
  tokenAddress: settlementConfig.tokenAddress,
  tokenDecimals: 6,
  settlementAddress: settlementConfig.settlementAddress,
  chainId: settlementConfig.chainId,
  merchantRegistryVersion: 1,
  feeBps: 100,
  feeRecipient: settlementConfig.feeRecipient,
  principalRecipient: `0x${"9".repeat(40)}`,
  expiresAt: FUTURE,
};

const completedPayment = {
  id: PAYMENT_ID,
  purchaseId: PURCHASE_ID,
  orderHash: `0x${"a".repeat(64)}`,
  payer: account.wallets[0].wallet.address,
  settlementAddress: settlementConfig.settlementAddress,
  amountBaseUnits: quote.settlementAmountBaseUnits,
  claimTxHash: `0x${"b".repeat(64)}`,
  approveTxHash: `0x${"c".repeat(64)}`,
  state: "COMPLETED",
  payTxHash: `0x${"d".repeat(64)}`,
  completeTxHash: `0x${"e".repeat(64)}`,
  finalizedBlock: 912345,
};

const receipt = {
  id: "receipt-design-baseline",
  purchaseId: PURCHASE_ID,
  settlementPaymentId: PAYMENT_ID,
  kind: "TEST",
  legalSale: false,
  merchantOfRecord: "MERCHANT",
  refundHandler: "NOT_APPLICABLE",
  terminalTxHash: completedPayment.completeTxHash,
  receiptHash: `0x${"f".repeat(64)}`,
  payload: {
    schemaVersion: "vitlane.test-receipt.v2",
    generatedAt: NOW,
    kind: "TEST",
    watermark: "TEST · 실제 구매 증빙이 아님",
    legalSale: false,
    responsibility: {
      merchantOfRecord: "MERCHANT",
      merchantOfRecordBasis: "POLICY_ONLY_NO_LEGAL_SALE",
      refundHandler: "NOT_APPLICABLE",
      testTokenRefundExecutor: "VITLANE_SETTLEMENT",
    },
    quote: {
      merchant: {
        id: purchase.merchantId,
        displayName: purchase.merchantRef.displayName,
        sourceDomain: purchase.merchantRef.canonicalHost,
      },
      product: {
        name: purchase.candidate.name,
        sourceUrl: purchase.candidate.productUrl,
        variant: { color: "블랙" },
      },
      quantity: 1,
      unitPrice: {
        amount: quote.unitPrice.amount,
        currency: "USD",
        basis: "RESEARCH_OBSERVED",
      },
      itemSubtotal: {
        amount: quote.itemSubtotal.amount,
        currency: "USD",
        basis: "QUANTITY_MULTIPLIED",
      },
      shipping: { amount: "0", currency: "USD", basis: "NOT_QUOTED" },
      tax: { amount: "0", currency: "USD", basis: "NOT_QUOTED" },
      discount: { amount: "0", currency: "USD", basis: "NOT_APPLICABLE" },
      payableTotalForTest: {
        amount: quote.payableTotal.amount,
        currency: "USD",
        basis: "TEST_SETTLEMENT_TOTAL",
      },
      quoteBasis: "RESEARCH_SNAPSHOT",
      researchEvidenceHash: "research-evidence-design",
      observedAt: NOW,
    },
    fulfillment: {
      mode: "MANUAL_MERCHANT_ORDER",
      processingMode: "MANUAL_OPERATOR",
      outcome: "ORDER_ACCEPTED",
      resultKind: "MOCK_MERCHANT_ORDER",
      handledAt: NOW,
      merchantOrderCreated: false,
      externalOrderReference: null,
      externalEffect: "SIMULATED",
      merchantOrderFlowCompleted: true,
      mockOrderReference: "mock-order-design",
      physicalDeliveryTracked: false,
      resultHash: "fulfillment-result-design",
      statement: "실제 주문·배송을 뜻하지 않습니다.",
    },
    settlement: {
      chainId: settlementConfig.chainId,
      tokenSymbol: "tVITUSD",
      tokenAddress: settlementConfig.tokenAddress,
      tokenDecimals: 6,
      settlementAddress: settlementConfig.settlementAddress,
      gross: { decimal: "349.99", baseUnits: "349990000" },
      fee: { decimal: "3.4999", baseUnits: "3499900" },
      net: { decimal: "346.4901", baseUnits: "346490100" },
      gasAsset: "GIWA Test ETH",
      transactions: [{
        purpose: "PAY",
        txHash: completedPayment.payTxHash,
        blockNumber: 912344,
        finality: "FINALIZED",
        gasUsed: "21000",
        effectiveGasPrice: "1000000000",
        gasCostWei: "21000000000000",
        gasPayer: "USER",
        fromAddress: account.wallets[0].wallet.address,
      }],
    },
    identity: {
      walletOwnership: {
        walletId: account.wallets[0].wallet.id,
        proofId: account.wallets[0].ownership.proofId,
        assurancePolicy: "WALLET_OWNERSHIP_ONLY",
        accountId: account.wallets[0].wallet.accountId,
        address: account.wallets[0].wallet.address,
        chainId: account.wallets[0].wallet.chainId,
        method: "EIP191_PERSONAL_SIGN",
        messageHash: `0x${"b".repeat(64)}`,
        verifiedAt: NOW,
        validUntil: FUTURE,
      },
    },
    terminal: {
      state: "COMPLETED",
      finalizedBlock: 912345,
      fulfillmentHash: "fulfillment-result-design",
    },
  },
  createdAt: NOW,
};

const trackingItems = [
  {
    purchaseId: COMPLETED_PURCHASE_ID,
    shoppingSessionId: SESSION_CHECKOUT_ID,
    productName: purchase.candidate.name,
    productUrl: purchase.candidate.productUrl,
    imageUrl: purchase.candidate.imageUrl,
    merchantId: purchase.merchantId,
    merchantDomain: purchase.candidate.merchantDomain,
    quantity: purchase.quantity,
    selectedOptions: purchase.candidateConfiguration.selections,
    confirmsNoOptions: purchase.candidateConfiguration.confirmsNoOptions,
    shippingSummary: "기본 배송지 · Seoul",
    shippingCountry: "KR",
    shippingSnapshotId: "shipping-snapshot-design",
    payableTotal: quote.payableTotal,
    purchaseStatus: "COMPLETED",
    paymentId: PAYMENT_ID,
    paymentState: "COMPLETED",
    chainTransactions: [
      {
        purpose: "PAY",
        state: "FINALIZED",
        txHash: completedPayment.payTxHash,
        blockNumber: 912344,
        submittedAt: NOW,
        updatedAt: NOW,
      },
      {
        purpose: "COMPLETE",
        state: "FINALIZED",
        txHash: completedPayment.completeTxHash,
        blockNumber: 912345,
        submittedAt: NOW,
        updatedAt: NOW,
      },
    ],
    requestState: "TERMINAL",
    fulfillmentState: "ORDER_ACCEPTED",
    resultKind: "MOCK_MERCHANT_ORDER",
    receiptIssued: true,
    updatedAt: NOW,
  },
  {
    purchaseId: "purchase-design-finality",
    shoppingSessionId: SESSION_CHECKOUT_ID,
    productName: "Apple 70W USB-C Power Adapter",
    productUrl: "https://www.apple.com/kr/shop/product/mqdj3kh-a",
    merchantId: "APPLE_KR",
    merchantDomain: "apple.com",
    quantity: 1,
    selectedOptions: [],
    confirmsNoOptions: true,
    shippingSummary: "기본 배송지 · Seoul",
    shippingCountry: "KR",
    shippingSnapshotId: "shipping-snapshot-design-finality",
    payableTotal: { amount: "69.00", currency: "USD" },
    purchaseStatus: "PAYMENT_SUBMITTED",
    paymentId: "payment-design-finality",
    paymentState: "SUBMITTED",
    chainTransactions: [
      {
        purpose: "PAY",
        state: "SAFE",
        txHash: `0x${"1".repeat(64)}`,
        blockNumber: 912346,
        submittedAt: NOW,
        updatedAt: NOW,
      },
    ],
    receiptIssued: false,
    updatedAt: NOW,
  },
];

const fulfillmentItem = {
  execution: {
    id: "execution-design",
    requestId: "request-design",
    settlementPaymentId: PAYMENT_ID,
    merchantId: purchase.merchantId,
    mode: "MANUAL_MERCHANT_ORDER",
    state: "PENDING",
    quoteId: quote.id,
    quoteHash: quote.quoteHash,
    quoteSnapshot: {
      candidate: {
        name: purchase.candidate.name,
        productUrl: purchase.candidate.productUrl,
      },
      payableTotal: quote.payableTotal,
      shipping: { basis: "NOT_QUOTED" },
      tax: { basis: "NOT_QUOTED" },
      quoteBasis: "RESEARCH_SNAPSHOT",
    },
    eligibleAt: NOW,
    merchantOrderCreated: false,
    externalOrderReference: null,
    externalEffect: "SIMULATED",
    adapterKind: "MOCK_MERCHANT_ORDER",
    merchantOrderFlowCompleted: false,
    createdAt: NOW,
    updatedAt: NOW,
  },
  purchaseId: PURCHASE_ID,
  buyerUserId: currentUser.id,
  shippingSnapshotId: "shipping-snapshot-design",
  orderHash: completedPayment.orderHash,
  merchantDisplayName: purchase.merchantRef.displayName,
  payer: account.wallets[0].wallet.address,
  amountBaseUnits: quote.settlementAmountBaseUnits,
  paymentState: "FINALIZED",
  finalizedAt: NOW,
  receiptIssued: false,
  request: {
    id: "request-design",
    settlementPaymentId: PAYMENT_ID,
    purchaseId: PURCHASE_ID,
    quoteId: quote.id,
    schemaVersion: "vitlane.fulfillment-request.v1",
    fulfillmentSpecHash: "fulfillment-spec-design",
    requestHash: "fulfillment-request-hash-design",
    payload: {
      schemaVersion: "vitlane.fulfillment-request.v1",
      settlementPaymentId: PAYMENT_ID,
      purchaseId: PURCHASE_ID,
      quoteId: quote.id,
      orderHash: completedPayment.orderHash,
      payer: account.wallets[0].wallet.address,
      amountBaseUnits: quote.settlementAmountBaseUnits,
      finalizedBlock: completedPayment.finalizedBlock,
      fulfillmentSpecHash: "fulfillment-spec-design",
      fulfillmentSpec: {
        schemaVersion: "vitlane.fulfillment-spec.v2",
        purchaseId: PURCHASE_ID,
        purchaseHash: purchase.purchaseHash,
        merchantId: purchase.merchantId,
        merchantRegistryVersion: quote.merchantRegistryVersion,
        lines: [{
          candidateId: purchase.candidate.candidateId,
          name: purchase.candidate.name,
          productUrl: purchase.candidate.productUrl,
          quantity: purchase.quantity,
          unitPrice: quote.unitPrice,
          merchantRef: purchase.merchantRef,
          candidateConfiguration: purchase.candidateConfiguration,
          configurationHash:
            purchase.candidateConfiguration.configurationHash,
          variantResolution: purchase.variantResolution,
        }],
        deliverySnapshot: {
          id: "shipping-snapshot-design",
          profileId: "shipping-design-default",
          profileVersion: 1,
          maskedSummary: "KR · 서울 · •••12",
          country: "KR",
          snapshotHmac: "shipping-snapshot-hmac-design",
        },
        shipping: quote.shipping,
        shippingBasis: "NOT_QUOTED",
        tax: quote.tax,
        taxBasis: "NOT_QUOTED",
        payableTotal: quote.payableTotal,
        quoteBasis: "RESEARCH_SNAPSHOT",
      },
      variantIntent: {
        configurationHash:
          purchase.candidateConfiguration.configurationHash,
        resolutionStatus: "USER_SPECIFIED_UNVERIFIED",
      },
    },
    variantIntent: {
      configurationHash: purchase.candidateConfiguration.configurationHash,
      resolutionStatus: "USER_SPECIFIED_UNVERIFIED",
    },
    state: "REQUESTED",
    createdAt: NOW,
    updatedAt: NOW,
  },
  piiAudit: [],
  chain: {
    payTxHash: completedPayment.payTxHash,
    safeBlock: 912344,
    finalizedBlock: 912345,
    paymentUpdatedAt: NOW,
  },
};

const scenarios = [
  scenario("home", "/", "무엇을 찾고 있나요?", ["desktop", "tablet", "mobile"], "active", true),
  scenario("plan-create", "/plans/new", "무엇을 찾고 있나요?", ["desktop", "tablet", "mobile"], "active", true),
  scenario("planning-progress", `/curations/${CURATION_ID}`, "요청 해석 중", ["desktop", "tablet", "mobile"], "planning-progress"),
  scenario("curation-review", `/curations/${CURATION_ID}`, "휴대용 USB-C 충전기", ["desktop", "tablet", "mobile"], "active", true),
  scenario("account", "/account", "프로필 · 구매 · 결제 설정", ["desktop", "tablet", "mobile"], "active", false, true, true, 2),
  scenario("agency-order", "/agencyOrder", "주문·결제와 구매대행 처리", ["desktop", "laptop", "tablet", "phone", "mobile", "narrow", "compact"], "active", false, true, true, 2),
  { ...scenario("agency-order-text-zoom", "/agencyOrder", "주문·결제와 구매대행 처리", ["desktop", "mobile", "compact"], "active", false, true, true, 2), textZoom: 2 },
  { ...scenario("agency-order-en", "/agencyOrder", "Orders, payment, and agency processing", ["desktop", "laptop", "tablet", "phone", "mobile", "narrow", "compact"], "active", false, true, false, 2), locale: "en-US", skipMobileSidebar: true },
  { ...scenario("agency-order-en-text-zoom", "/agencyOrder", "Orders, payment, and agency processing", ["desktop", "mobile", "compact"], "active", false, true, false, 2), locale: "en-US", skipMobileSidebar: true, textZoom: 2 },
  scenario("operator-ops", "/admin/ops", "운영 현황과 조치", ["desktop", "tablet", "mobile"], "active", false, true, false),
];

const viewports = {
  desktop: { width: 1440, height: 1000 },
  laptop: { width: 1280, height: 800 },
  tablet: { width: 768, height: 1024 },
  phone: { width: 430, height: 932 },
  mobile: { width: 390, height: 844 },
  narrow: { width: 360, height: 800 },
  compact: { width: 320, height: 568 },
};

// Contrast is asserted in every theme × accent combination (ADR-0082 palettes
// included); screenshots stay on the light/neutral reset below.
const appearanceMatrix = [
  { theme: "light", accent: "neutral" },
  { theme: "light", accent: "blue" },
  { theme: "light", accent: "moss" },
  { theme: "light", accent: "maple" },
  { theme: "light", accent: "iris" },
  { theme: "dark", accent: "neutral" },
  { theme: "dark", accent: "blue" },
  { theme: "dark", accent: "moss" },
  { theme: "dark", accent: "maple" },
  { theme: "dark", accent: "iris" },
];

function scenario(
  id,
  route,
  expectedText,
  viewportNames,
  fixture = "active",
  productShell = false,
  orderOperations = false,
  operationsConsumer = orderOperations,
  primaryActionLimit = 1,
) {
  return {
    id,
    route,
    expectedText,
    viewportNames,
    fixture,
    productShell,
    orderOperations,
    operationsConsumer,
    primaryActionLimit,
  };
}

// Sidebar summaries follow the UI language so the English scenarios do not
// mix languages; the fixture data itself stays the same.
function intentSummaryFor(locale, korean) {
  if (locale !== "en-US") return korean;
  return korean === activePlanResult.plan.originalIntent
    ? "Sort out my commute gear: comfortable noise-cancelling headphones and a small charger that also charges a laptop."
    : "Find a mechanical keyboard for home.";
}

// The selected option label is merchant data; the English scenarios show the
// same order with the label an English shop would return.
function localizedProjection(projection, locale) {
  if (locale !== "en-US") return projection;
  return JSON.parse(JSON.stringify(projection).replaceAll("색상: 블랙", "Color: Black"));
}

const laneRequest = {
  "ko-KR": "매일 신을 트레이닝화를 찾아줘. 예산은 120달러, 발볼이 넓고 리프팅할 때 안정적인 걸로.",
  "en-US": "Training shoes I can wear every day. Budget $120, wide fit and stable for lifting.",
};

// The `lane-shoes` order: the same product, options and amount the marketing
// curation frames show, in the projection shape the orders page reads.
function laneShoesProjection(locale) {
  const projection = JSON.parse(JSON.stringify(agencyOrderProjection));
  const korean = locale !== "en-US";
  const usd = (amountMinor) => ({ amountMinor, currency: "USD" });
  const line = projection.agencyOrder.lines[0];
  Object.assign(line, {
    lineId: "line-lane-shoes",
    sourceCartItemId: "cart-line-lane-shoes",
    candidateId: "daily-trainer",
    productTitle: "Daily Trainer",
    productUrl: "https://example.com/products/daily-trainer",
    imageUrl: "/__lane__/products/daily-trainer.svg",
    variantTitle: korean ? "네이비 / 280" : "Navy / 10",
    selectedOptions: korean ? ["색상: 네이비", "사이즈: 280"] : ["Color: Navy", "Size: 10"],
    unitPrice: usd(9_000),
    lineSubtotal: usd(9_000),
    shopDomain: "example.com",
  });
  projection.agencyOrder.passThroughTotal = usd(9_000);
  projection.agencyOrder.agencyFee = { variable: usd(0), fixed: usd(0), total: usd(0), policyVersion: "TVITUSD-1PCT" };
  projection.agencyOrder.customerPayableTotal = usd(9_000);
  projection.paymentInstruction.customerPayableTotal = usd(9_000);
  projection.payment.amountBaseUnits = "90000000";
  const merchantOrder = projection.merchantOrders[0];
  Object.assign(merchantOrder, {
    allocationId: "allocation-lane-shoes",
    shopDomain: "example.com",
    merchantId: "SHOP_PUBLIC_PREVIEW",
    customerGrossAmount: usd(9_000),
  });
  merchantOrder.units[0].lineId = "line-lane-shoes";
  Object.assign(projection.units[0], {
    allocationId: "allocation-lane-shoes",
    lineId: "line-lane-shoes",
    shopDomain: "example.com",
  });
  return projection;
}

function responseFor(request, fixture, locale = "ko-KR") {
  const url = new URL(request.url());
  const pathname = url.pathname;
  const method = request.method();

  if (pathname === "/api/v1/analytics/config" && method === "GET") {
    return { schemaVersion: "vitlane.analytics-config.v1", mode: "disabled", measurementId: "", release: "fixture" };
  }
  if (pathname === "/api/v1/curations/product-notices/sync" && method === "POST") {
    return { schemaVersion: "vitlane.curation-notices.v1", curationIds: [] };
  }
  if (pathname === `/api/v1/curations/${CURATION_ID}/background-research` && method === "GET") {
    return { schemaVersion: "vitlane.background-research.v1", subscriptions: [], findings: [] };
  }
  const isCompletedCheckout = fixture === "checkout-receipt";
  const planResult =
    fixture === "planning-progress"
      ? planningPlanResult
      : activePlanResult;

  if (pathname === "/api/v1/me/preferences" && method === "GET") {
    const preferences = { schemaVersion: "vitlane.user-preferences.v1", version: 0, uiLocale: locale, preferredCurrency: "USD", researchCountry: "KR" };
    return { preferences, effective: preferences };
  }
  if (pathname === `/api/v1/curations/${CURATION_ID}/threads` && method === "GET") return { schemaVersion: "vitlane.curation-thread.v2", controlMode: { mode: "AUTO", version: 1 }, threads: [] };
  if (pathname.endsWith("/research-settings") && method === "GET") return { schemaVersion: "vitlane.research-settings.v1", country: "KR", version: 0 };
  if (pathname.endsWith("/exchange-rate") && method === "GET") return { schemaVersion: "vitlane.exchange-rate.v1", status: "UNAVAILABLE" };
  if (pathname === "/api/v1/me" && method === "GET") {
    return { user: currentUser };
  }
  if (pathname === "/api/v1/auth/capabilities" && method === "GET") {
    return {
      googleEnabled: false,
      localReviewEnabled: false,
      localReviewSeeded: false,
      localReviewProfiles: [],
    };
  }
  if (pathname === "/api/v1/account/overview" && method === "GET") {
    return { account };
  }
  // ADR-0032. The composer asks before it renders, so the baseline has to
  // answer as a configured deployment would: MANAGED available and default.
  // That is the arrangement these screenshots are supposed to represent.
  if (pathname === "/api/v1/managed-runner/capability" && method === "GET") {
    return {
      enabled: true,
      models: [
        { key: "gpt-5-nano", label: "빠름 · gpt-5-nano" },
        { key: "gpt-5.6-luna", label: "정밀 · gpt-5.6-luna" },
      ],
      defaultModelKey: "gpt-5.6-luna",
      serverExhausted: false,
    };
  }
  if (pathname === "/api/v1/managed-runner/usage" && method === "GET") {
    return {
      enabled: true,
      usageDate: "2026-07-25",
      userSpentMicros: 20_000,
      userLimitMicros: 100_000,
      userExhausted: false,
      serverExhausted: false,
      serverLimitMicros: 8_000_000,
    };
  }
  if (
    pathname === "/api/v1/account/liked-variants" &&
    method === "GET"
  ) {
    return { candidates: catalogLikedVariants };
  }
  if (pathname === "/api/v1/account/purchase-checks" && method === "GET") {
    return { schemaVersion: "vitlane.account-purchase-checks.v1", records: [] };
  }
  if (pathname === "/api/v1/agencyOrder" && method === "GET") {
    return {
      schemaVersion: "vitlane.agency-order-list.v1",
      agencyOrders: [
        orderVariant === "lane-shoes"
          ? laneShoesProjection(locale)
          : localizedProjection(agencyOrderProjection, locale),
      ],
      countsByView: {
        PAYMENT_REQUIRED: 0,
        IN_PROGRESS: 1,
        NEEDS_ATTENTION: 0,
        COMPLETED: 0,
        CLOSED: 0,
        ALL: 1,
      },
      nextCursor: "",
    };
  }
  if (
    pathname === `/api/v1/curations/${CURATION_ID}/cart` &&
    method === "GET"
  ) {
    return {
      schemaVersion: "vitlane.cart-view.v2",
      curationId: CURATION_ID,
      version: 0,
      country: "US",
      currency: "USD",
      items: [],
    };
  }
  // The product shell's sidebar reads one page of titles, newest created first
  // (ADR-0079). No saved views, counts or cursors exist any more.
  if (pathname === "/api/v1/curations" && method === "GET") {
    const rows = orderVariant === "lane-shoes"
      ? [{ curationId: CURATION_ID, intentSummary: laneRequest[locale] ?? laneRequest["ko-KR"], createdAt: NOW }]
      : [
          {
            curationId: "curation-design-secondary",
            intentSummary: intentSummaryFor(locale, "집에서 쓸 기계식 키보드를 찾아주세요."),
            createdAt: "2026-07-24T08:00:00.000Z",
          },
          {
            curationId: CURATION_ID,
            intentSummary: intentSummaryFor(locale, activePlanResult.plan.originalIntent),
            createdAt: "2026-07-23T09:00:00.000Z",
          },
        ];
    return { schemaVersion: "vitlane.curation-list.v2", curations: rows, latestCursor: "design-latest" };
  }
  if (pathname.startsWith(`/api/v1/curations/${CURATION_ID}/targets/`) && pathname.endsWith('/criteria') && method === 'GET') return null;
  if (pathname === `/api/v1/curations/${CURATION_ID}/budget` && method === "GET") {
    return { schemaVersion: "vitlane.curation-budget.v1", version: 0, researchVersion: 0,
      enabled: false, currency: "KRW", totalAmount: null,
      allocations: curationWorkspace(fixture).targets.map(target => ({ targetId: target.id, quantity: 1, amount: null })) };
  }
  if (
    pathname === `/api/v1/curations/${CURATION_ID}/workspace` &&
    method === "GET"
  ) {
    return curationWorkspace(fixture);
  }
  if (
    pathname ===
      `/api/v1/shopping-plans/${PLAN_ID}/expansions/current` &&
    method === "GET"
  ) {
    return fixture === "planning-progress" ? null : completedExpansionResult;
  }
  if (
    pathname === `/api/v1/shopping-plans/${PLAN_ID}` &&
    method === "GET"
  ) {
    return planResult;
  }
  if (
    pathname === `/api/v1/shopping-sessions/${SESSION_READY_ID}` &&
    method === "GET"
  ) {
    return { session: readySession };
  }
  if (
    pathname === `/api/v1/shopping-sessions/${SESSION_CHECKOUT_ID}` &&
    method === "GET"
  ) {
    return { session: checkoutSession };
  }
  if (pathname === "/api/v1/settlement/config" && method === "GET") {
    return { settlement: settlementConfig };
  }
  if (pathname === "/api/v1/support/summary" && method === "GET") {
    // 도움 받기 안 읽음 뱃지 폴링(ADR-0059) — 인증 셸의 모든 화면이 읽는다.
    return { schemaVersion: "vitlane.support-summary.v1", unread: 0 };
  }
  if (pathname === "/api/v1/admin/support/counts" && method === "GET") {
    // 고객 대화 답변 대기 nav 뱃지 폴링(ADR-0059).
    return {
      schemaVersion: "vitlane.support-counts.v2",
      counts: { awaiting: 1 },
    };
  }
  if (
    pathname === "/api/v1/admin/ordering/work-items/counts" &&
    method === "GET"
  ) {
    // 예외 처리 nav 뱃지 폴링(운영정합 2차 P2) — 운영자 세션의 모든 admin
    // 화면이 이 카운트를 읽는다.
    return {
      schemaVersion: "vitlane.ordering-operator-work-item-counts.v1",
      counts: {
        PROCESS_INTERVENTION: 0,
        PROCUREMENT_EXECUTION: 1,
        REFUND_REVIEW: 1,
        DELIVERY_RESOLUTION: 0,
        RETURN_PROGRESS: 0,
      },
    };
  }
  if (pathname === "/api/v1/admin/liveControl" && method === "GET") {
    return {
      schemaVersion: "vitlane.live-control.v1",
      liveControl: {
        version: 1,
        orderIssue: {
          staticAllowed: true,
          runtimeKilled: true,
          effective: false,
        },
        paypalMoney: {
          staticAllowed: true,
          runtimeKilled: true,
          effective: false,
        },
        merchantEffect: {
          staticAllowed: true,
          runtimeKilled: true,
          effective: false,
        },
        changedAt: NOW,
        changedBy: "system:migration",
        reason: "Initial fail-closed state",
      },
    };
  }
  if (
    pathname === "/api/v1/admin/ops/health" &&
    method === "GET"
  ) {
    return {
      status: "degraded",
      core: {
        status: "ready",
        reasonCodes: [],
        databasePool: {
          maxOpenConnections: 20,
          openConnections: 7,
          inUse: 3,
          idle: 4,
          waitCount: 2,
          waitDurationNanoseconds: 1_400_000,
          maxIdleClosed: 0,
          maxIdleTimeClosed: 1,
          maxLifetimeClosed: 0,
        },
      },
      degradedReasonCodes: ["MANAGED_COST_RESERVATION_UNKNOWN"],
      settlement: {
        status: "ready",
        workers: {
          eventConsumer: {
            lastAttemptAt: NOW,
            lastSuccessAt: NOW,
          },
          finalityWorker: {
            lastAttemptAt: NOW,
            lastSuccessAt: NOW,
          },
        },
        rpcSafeBlock: 912_344,
        rpcFinalizedBlock: 912_340,
        finalizedCursor: 912_339,
        finalizedCursorSeen: true,
        outboxConflictCount: 0,
        reasonCodes: [],
      },
      http: {
        totalRequests: 18_421,
        totalServerErrors: 7,
        requestsLast5m: 143,
        serverErrorsLast5m: 0,
        requestsLast60m: 1_402,
        serverErrorsLast60m: 1,
      },
      observed: {
        piiAccess: {
          granted24h: 9,
          denied24h: 2,
          brokenChainLinks: 0,
        },
        piiLifecycle: {
          pendingDeletions: 1,
          staleKeyRows: 0,
        },
        intelligence: {
          attempts24h: 214,
          failed24h: 3,
          failureRate24h: 0.014,
          effectUnknownOpen: 0,
        },
        costReservations: {
          unknownCount: 2,
          oldestUnknownAgeSeconds: 7_800,
        },
        accounts: {
          activeSessions: 18,
          usersCreated24h: 4,
          activeUsers24h: 13,
        },
      },
      host: {
        generatedAt: NOW,
        diskUsedPct: 43,
        memoryUsedPct: 67,
        tlsDaysLeft: 55,
        services: {
          postgres: { state: "running", restartCount: 0 },
          vitlane: { state: "running", restartCount: 1 },
          caddy: { state: "running", restartCount: 0 },
        },
        ageSeconds: 180,
        stale: false,
      },
    };
  }
  if (
    pathname === "/api/v1/admin/ops/heartbeats" &&
    method === "GET"
  ) {
    return {
      configured: true,
      fetchedAt: "2026-08-08T11:45:00.000Z",
      checks: [
        {
          name: "vitlane-ops-watch",
          slug: "vitlane-ops-watch",
          status: "up",
          lastPing: "2026-08-08T11:45:00.000Z",
          timeoutSeconds: 300,
          graceSeconds: 600,
        },
        {
          name: "vitlane-postgres-backup",
          slug: "vitlane-postgres-backup",
          status: "up",
          lastPing: "2026-08-08T11:45:00.000Z",
          schedule: "0 3 * * *",
        },
      ],
    };
  }
  if (
    pathname === "/api/v1/admin/ops/samples" &&
    method === "GET"
  ) {
    return {
      hours: 24,
      points: Array.from({ length: 12 }, (_, index) => ({
        sampledAt: new Date(Date.parse(NOW) - (11 - index) * 300_000).toISOString(),
        status: "ready",
        requests5m: 120 + index * 2,
        serverErrors5m: index === 7 ? 1 : 0,
        dbPoolInUse: 2 + (index % 2),
        activeSessions: 16 + (index % 3),
        intelligenceFailureRate24h: 0.012 + index * 0.0002,
        unknownReservations: index < 8 ? 3 : 2,
        piiGranted24h: 8 + (index % 2),
        piiDenied24h: 2,
        cursorLagBlocks: 1,
        diskUsedPct: 42 + (index % 2),
        memoryUsedPct: 65 + (index % 3),
      })),
    };
  }
  if (
    pathname === "/api/v1/admin/ops/sessions" &&
    method === "GET"
  ) {
    return {
      count: 3,
      sessions: [
        {
          sessionId: "session-operator-design-primary",
          userId: currentUser.id,
          email: currentUser.email,
          operator: true,
          createdAt: "2026-07-25T07:10:00.000Z",
          expiresAt: "2026-08-01T07:10:00.000Z",
        },
        {
          sessionId: "session-operator-design-mobile",
          userId: currentUser.id,
          email: currentUser.email,
          operator: true,
          createdAt: "2026-07-25T08:20:00.000Z",
          expiresAt: "2026-08-01T08:20:00.000Z",
        },
        {
          sessionId: "session-buyer-design-primary",
          userId: "buyer-design-user",
          email: "buyer@vitlane.example",
          operator: false,
          createdAt: "2026-07-24T03:30:00.000Z",
          expiresAt: "2026-07-31T03:30:00.000Z",
        },
      ],
    };
  }
  return undefined;
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
  let vite;
  let baseURL = configuredBaseURL;
  if (!baseURL) {
    const { createServer } = await import("vite");
    vite = await createServer({
      root: webRoot,
      configFile: path.join(webRoot, "vite.config.ts"),
      clearScreen: false,
      logLevel: "error",
      server: {
        host: "127.0.0.1",
        port: 4176,
        strictPort: false,
        fs: { allow: [webRoot, await fs.realpath(path.join(webRoot, "node_modules"))] },
      },
    });
    await vite.listen();
    baseURL = vite.resolvedUrls?.local?.[0]?.replace(/\/$/, "");
  }
  if (!baseURL) throw new Error("Vite local URL을 확인하지 못했습니다.");

  const browser = await firefox.launch({
    headless: true,
    ...(firefoxPath ? { executablePath: firefoxPath } : {}),
  });
  const results = [];

  try {
    for (const target of scenarios.filter(
      ({ id }) => targetFilter.size === 0 || targetFilter.has(id),
    )) {
      for (const viewportName of frameViewport
        ? ["frame"]
        : target.viewportNames.filter(
          (name) => viewportFilter.size === 0 || viewportFilter.has(name),
        )) {
        const viewport = frameViewport ?? viewports[viewportName];
        const context = await browser.newContext({
          viewport,
          deviceScaleFactor,
          locale: target.locale ?? "ko-KR",
          colorScheme: "light",
          reducedMotion: "reduce",
          userAgent:
            viewportName === "mobile"
              ? "Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) Mobile"
              : undefined,
        });
        const unexpectedAPI = [];
        const consoleErrors = [];
        const pageErrors = [];
        const failedResponses = [];
        const page = await context.newPage();

        // Lane frame 05: the real marketing catalog image for the shoes order.
        await context.route("**/__lane__/products/*", async (route) => {
          const file = path.basename(new URL(route.request().url()).pathname);
          await route.fulfill({
            status: 200,
            contentType: file.endsWith(".svg")
              ? "image/svg+xml"
              : file.endsWith(".png")
                ? "image/png"
                : "image/jpeg",
            body: await fs.readFile(path.join(laneProductImageDir, file)),
          });
        });
        await context.route("**/__design-fixture__/**", async (route) => {
          const svg = fixtureImages.get(new URL(route.request().url()).pathname);
          if (!svg) {
            await route.fulfill({ status: 404, body: "fixture image not found" });
            return;
          }
          await route.fulfill({
            status: 200,
            contentType: "image/svg+xml",
            body: svg,
          });
        });
        await context.route("**/api/v1/**", async (route) => {
          const body = responseFor(route.request(), target.fixture, target.locale ?? "ko-KR");
          if (body === undefined) {
            unexpectedAPI.push(
              `${route.request().method()} ${new URL(route.request().url()).pathname}`,
            );
            await route.fulfill({
              status: 500,
              contentType: "application/json",
              body: JSON.stringify({
                error: {
                  code: "UNEXPECTED_DESIGN_FIXTURE_REQUEST",
                  message: "인증 디자인 기준선에 등록되지 않은 요청입니다.",
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

        page.on("console", (message) => {
          if (message.type() === "error") consoleErrors.push(message.text());
        });
        page.on("pageerror", (error) => pageErrors.push(error.message));
        page.on("response", (response) => {
          if (response.status() >= 400) {
            failedResponses.push(`${response.status()} ${response.url()}`);
          }
        });

        const response = await page.goto(`${baseURL}${target.route}`, {
          waitUntil: "domcontentloaded",
          timeout: 30_000,
        });
        try {
          await page.waitForFunction(
            (expectedText) => document.body?.innerText.includes(expectedText),
            target.expectedText,
            { timeout: 15_000 },
          );
        } catch (error) {
          const bodyText = await page.locator("body").innerText()
            .catch(() => "");
          throw new Error(
            `Authenticated baseline did not render ${target.id}:${viewportName} ` +
              `with expected text ${JSON.stringify(target.expectedText)}; ` +
              `url=${page.url()}; console=${JSON.stringify(consoleErrors)}; ` +
              `pageErrors=${JSON.stringify(pageErrors)}; ` +
              `failedResponses=${JSON.stringify(failedResponses)}; ` +
              `body=${JSON.stringify(bodyText.slice(0, 2_000))}`,
            { cause: error },
          );
        }

        if (target.id === "curations-list") {
          await page.getByRole("tab", { name: /Curations/ }).click();
          await page.getByRole("heading", { name: "큐레이션 내역" })
            .waitFor({ state: "visible", timeout: 5_000 });
        }

        const injectKoreanFont = Boolean(koreanFontData);
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
        }
        await page.evaluate(() => document.fonts.ready).catch(() => undefined);
        await page.evaluate(() => {
          document
            .querySelectorAll('img[loading="lazy"]')
            .forEach((image) => {
              image.loading = "eager";
            });
        });
        await page
          .waitForFunction(
            () => [...document.images].every((image) => image.complete),
            undefined,
            { timeout: 5_000 },
          )
          .catch(() => undefined);
        await page.waitForTimeout(250);

        if (target.textZoom) {
          await page.addStyleTag({
            content: `html { font-size: ${target.textZoom * 100}% !important; }`,
          });
          await page.evaluate(() => new Promise((resolve) => {
            requestAnimationFrame(() => requestAnimationFrame(resolve));
          }));
        }

        let mobileSidebar = null;
        const mobileSidebarAvailable = await page.locator(
          ".shell-mobile-sidebar-trigger",
        ).evaluate((trigger) => getComputedStyle(trigger).display !== "none");
        if (mobileSidebarAvailable && !target.skipMobileSidebar) {
          mobileSidebar = await page.evaluate(() => {
            const sidebar = document.querySelector(".shell-product-sidebar");
            const trigger = document.querySelector(
              ".shell-mobile-sidebar-trigger",
            );
            const productBody = document.querySelector(
              ".shell-product-body",
            );
            return {
              initialSidebarVisibility: sidebar
                ? getComputedStyle(sidebar).visibility
                : null,
              initialTriggerDisplay: trigger
                ? getComputedStyle(trigger).display
                : null,
              mobileHeaderPresent: Boolean(
                document.querySelector(".shell-mobile-shell-bar"),
              ),
              profileMenuZIndex: null,
              sidebarZIndex: sidebar
                ? Number.parseInt(getComputedStyle(sidebar).zIndex, 10)
                : null,
              productBodyTouchAction: productBody
                ? getComputedStyle(productBody).touchAction
                : null,
              profileMenuTopLayer: null,
              profileMenuWithinSidebar: null,
              profileMenuInlineInsets: null,
            };
          });
          await page.getByRole("button", { name: "사이드바 열기" }).click();
          await page.waitForFunction(() => {
            const sidebar = document.querySelector(".shell-product-sidebar");
            return sidebar?.classList.contains("is-mobile-open")
              && getComputedStyle(sidebar).visibility === "visible"
              && document.activeElement?.getAttribute("aria-label") ===
                "사이드바 닫기";
          }).catch(async error => {
            console.error({target:target.id,viewportName,sidebar:await page.locator(".shell-product-sidebar").getAttribute("class"),focus:await page.evaluate(()=>document.activeElement?.outerHTML)});
            throw error;
          });
          Object.assign(
            mobileSidebar,
            await page.evaluate(() => ({
              openSidebarVisibility: getComputedStyle(
                document.querySelector(".shell-product-sidebar"),
              ).visibility,
              openExpanded: document
                .querySelector(".shell-mobile-sidebar-trigger")
                ?.getAttribute("aria-expanded"),
              openProductBodyInert: document
                .querySelector(".shell-product-body")
                ?.inert === true,
              focusedControl: document.activeElement
                ?.getAttribute("aria-label"),
            })),
          );
          if (target.id === "home") {
            await page.getByRole("button", { name: "프로필 메뉴" }).click();
            const profileMenu = page.locator(".shell-sidebar-profile__menu");
            await profileMenu.waitFor();
            Object.assign(
              mobileSidebar,
              await profileMenu.evaluate((menu) => {
                const rect = menu.getBoundingClientRect();
                const sidebar = document.querySelector(
                  ".shell-product-sidebar",
                );
                const sidebarRect = sidebar?.getBoundingClientRect();
                const centerX = rect.left + rect.width / 2;
                const centerY = rect.top + rect.height / 2;
                return {
                  profileMenuZIndex: Number.parseInt(
                    getComputedStyle(menu).zIndex,
                    10,
                  ),
                  profileMenuTopLayer: document
                    .elementFromPoint(centerX, centerY)
                    ?.closest('[data-slot="dropdown-menu-content"]') === menu,
                  profileMenuWithinSidebar: sidebarRect
                    ? rect.left >= sidebarRect.left + 8 &&
                      rect.right <= sidebarRect.right - 8
                    : false,
                  profileMenuInlineInsets: sidebarRect
                    ? {
                        start: rect.left - sidebarRect.left,
                        end: sidebarRect.right - rect.right,
                      }
                    : null,
                };
              }),
            );
            if (viewportName === "mobile") {
              mobileSidebar.profileMenuScreenshot =
                "home-mobile-profile-menu.png";
              await page.screenshot({
                path: path.join(outputDir, mobileSidebar.profileMenuScreenshot),
                fullPage: true,
              });
            }
            await page.keyboard.press("Escape");
            await profileMenu.waitFor({ state: "hidden" });
          }
          await page.keyboard.press("Escape");
          await page.waitForFunction(() => {
            const sidebar = document.querySelector(".shell-product-sidebar");
            return !sidebar?.classList.contains("is-mobile-open")
              && getComputedStyle(sidebar).visibility === "hidden"
              && document.activeElement?.getAttribute("aria-label") ===
                "사이드바 열기";
          });
          Object.assign(
            mobileSidebar,
            await page.evaluate(() => ({
              closedExpanded: document
                .querySelector(".shell-mobile-sidebar-trigger")
                ?.getAttribute("aria-expanded"),
              closedProductBodyInert: document
                .querySelector(".shell-product-body")
                ?.inert === true,
              restoredFocus: document.activeElement
                ?.getAttribute("aria-label"),
            })),
          );
          mobileSidebar.openGestureStartX = await page.evaluate(() => {
            const startX = Math.round(window.innerWidth * 0.5);
            document.querySelector(".shell-product-body")
              ?.dispatchEvent(new PointerEvent("pointerdown", {
                bubbles: true,
                clientX: startX,
                clientY: 220,
                isPrimary: true,
                pointerId: 66,
                pointerType: "touch",
              }));
            return startX;
          });
          await page.evaluate(() => {
            const startX = Math.round(window.innerWidth * 0.5);
            document.querySelector(".shell-product-body")
              ?.dispatchEvent(new PointerEvent("pointermove", {
                bubbles: true,
                cancelable: true,
                clientX: startX + 72,
                clientY: 224,
                isPrimary: true,
                pointerId: 66,
                pointerType: "touch",
              }));
          });
          await page.waitForFunction(() =>
            document.querySelector(".shell-product-frame")
              ?.classList.contains("is-mobile-sidebar-dragging"),
          );
          mobileSidebar.dragOpenPreview = await page.locator(
            ".shell-product-sidebar",
          ).evaluate((sidebar) => getComputedStyle(sidebar).transform);
          await page.evaluate(() => {
            const startX = Math.round(window.innerWidth * 0.5);
            document.querySelector(".shell-product-body")
              ?.dispatchEvent(new PointerEvent("pointerup", {
                bubbles: true,
                clientX: startX + 100,
                clientY: 224,
                isPrimary: true,
                pointerId: 66,
                pointerType: "touch",
              }));
          });
          await page.waitForFunction(() =>
            document.querySelector(".shell-product-sidebar")
              ?.classList.contains("is-mobile-open"),
          );
          mobileSidebar.swipeOpen = true;
          await page.evaluate(() => {
            document.body.dispatchEvent(new PointerEvent("pointerdown", {
              bubbles: true,
              clientX: 220,
              clientY: 220,
              isPrimary: true,
              pointerId: 67,
              pointerType: "touch",
            }));
          });
          await page.evaluate(() => {
            document.body.dispatchEvent(new PointerEvent("pointermove", {
              bubbles: true,
              cancelable: true,
              clientX: 156,
              clientY: 222,
              isPrimary: true,
              pointerId: 67,
              pointerType: "touch",
            }));
          });
          await page.waitForFunction(() =>
            document.querySelector(".shell-product-frame")
              ?.classList.contains("is-mobile-sidebar-dragging"),
          );
          mobileSidebar.dragClosePreview = await page.locator(
            ".shell-product-sidebar",
          ).evaluate((sidebar) => getComputedStyle(sidebar).transform);
          await page.evaluate(() => {
            document.body.dispatchEvent(new PointerEvent("pointerup", {
              bubbles: true,
              clientX: 120,
              clientY: 224,
              isPrimary: true,
              pointerId: 67,
              pointerType: "touch",
            }));
          });
          await page.waitForFunction(() =>
            !document.querySelector(".shell-product-sidebar")
              ?.classList.contains("is-mobile-open"),
          );
          mobileSidebar.swipeClose = true;
        }

        if (target.id === "purchases") {
          await page.locator(
            ".workspace-purchase-disclosure .vt-disclosure__trigger",
          ).first().click();
          await page.getByRole("link", { name: "결제 상세" }).first()
            .waitFor({ state: "visible" });
        }

        if (target.id.startsWith("agency-order")) {
          await page.locator(
            ".workspace-agency-order-disclosure .vt-disclosure__trigger",
          ).first().click();
          await page.getByRole("link", {
            name: target.locale === "en-US" ? "Payment details" : "결제 상세",
          }).first()
            .waitFor({ state: "visible" });
          await page.evaluate(() => {
            const productBody = document.querySelector(".shell-product-body");
            productBody?.scrollTo({ top: 0, left: 0, behavior: "instant" });
            window.scrollTo({ top: 0, left: 0, behavior: "instant" });
          });
        }

        const appearanceContrast = [];
        for (const appearance of appearanceMatrix) {
          await page.evaluate(({ theme, accent }) => {
            const root = document.documentElement;
            root.classList.toggle("dark", theme === "dark");
            root.dataset.theme = theme;
            root.dataset.accent = accent;
          }, appearance);
          await page.evaluate(() => new Promise((resolve) => {
            requestAnimationFrame(() => requestAnimationFrame(resolve));
          }));
          await page.waitForTimeout(200);
          const label = [
            target.id,
            viewportName,
            appearance.theme,
            appearance.accent,
          ].join(":");
          if (skipContrast) {
            appearanceContrast.push({ ...appearance, result: "SKIPPED" });
          } else {
            await assertTextContrast(page, label);
            appearanceContrast.push({ ...appearance, result: "PASS" });
          }
        }
        await page.evaluate(() => {
          const root = document.documentElement;
          root.classList.remove("dark");
          root.dataset.theme = "light";
          root.dataset.accent = "neutral";
        });

        const filename = `${target.id}-${viewportName}.${frameViewport && frameFormat === "jpeg" ? "jpg" : "png"}`;
        const shapeViolations = await page.evaluate(auditRadiusBoldBorders);
        const layout = await page.evaluate(() => {
          function overlap(firstSelector, secondSelector) {
            const first = document.querySelector(firstSelector);
            const second = document.querySelector(secondSelector);
            if (!first || !second) return null;
            const a = first.getBoundingClientRect();
            const b = second.getBoundingClientRect();
            return Math.max(0, Math.min(a.right, b.right) - Math.max(a.left, b.left))
              * Math.max(0, Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top));
          }
          const clientWidth = document.documentElement.clientWidth;
          const overflowElements = [...document.querySelectorAll("body *")]
            // The contents of a collapsed disclosure are still laid out inside
            // a paint-contained subtree, so getBoundingClientRect reports a box
            // for them anchored to the closed control. Nobody can see it, and
            // measuring it reports overflow that does not exist. Only the
            // summary is actually rendered while closed.
            .filter(
              (element) =>
                !element.closest("details:not([open]) > :not(summary)") &&
                !element.closest("[data-horizontal-scroll]"),
            )
            .filter((element) => {
              const style = getComputedStyle(element);
              return style.display !== "none" && style.visibility !== "hidden";
            })
            .map((element) => {
              const rect = element.getBoundingClientRect();
              return {
                tag: element.tagName.toLowerCase(),
                id: element.id || undefined,
                classes:
                  typeof element.className === "string"
                    ? element.className.trim() || undefined
                    : undefined,
                left: Math.round(rect.left),
                right: Math.round(rect.right),
                width: Math.round(rect.width),
                // A bare tag and a coordinate do not identify which element to
                // fix when the offender has no class or id, so carry enough of
                // its text to find it in the source.
                text: element.textContent?.replace(/\s+/g, " ").trim().slice(0, 60),
              };
            })
            .filter(({ left, right, width }) =>
              width > 0 && (left < -1 || right > clientWidth + 1),
            )
            .slice(0, 20);
          const ids = [...document.querySelectorAll("[id]")]
            .map((element) => element.id)
            .filter(Boolean);
          const duplicateIds = [...new Set(
            ids.filter((id, index) => ids.indexOf(id) !== index),
          )];
          const unnamedButtons = [...document.querySelectorAll("button")]
            .filter((button) =>
              !(
                button.textContent?.trim() ||
                button.getAttribute("aria-label") ||
                button.getAttribute("aria-labelledby") ||
                button.getAttribute("title") ||
                button.labels?.length
              ),
            )
            .length;
          const candidateCardHeights = [
            ...document.querySelectorAll(".shell-candidate-grid"),
          ].map((grid) =>
            [...grid.querySelectorAll(":scope > .vt-candidate-card")].map(
              (card) => Math.round(card.getBoundingClientRect().height),
            ),
          );
          const candidateGridMetrics = [
            ...document.querySelectorAll(".shell-candidate-grid"),
          ].map((grid) => {
            const cards = [
              ...grid.querySelectorAll(":scope > .vt-candidate-card"),
            ];
            const cardWidths = cards.map(
              (card) => Math.round(card.getBoundingClientRect().width),
            );
            for (const card of cards.slice(1)) card.style.display = "none";
            const singleCardWidth = cards[0]
              ? Math.round(cards[0].getBoundingClientRect().width)
              : null;
            const singleCardColumns = getComputedStyle(grid).gridTemplateColumns
              .split(" ")
              .filter(Boolean).length;
            for (const card of cards.slice(1)) card.style.removeProperty("display");
            return {
              columns: getComputedStyle(grid).gridTemplateColumns
                .split(" ")
                .filter(Boolean).length,
              cardWidths,
              singleCardWidth,
              singleCardColumns,
            };
          });
          const candidateContentOverlaps = [
            ...document.querySelectorAll(".vt-candidate-card__body"),
          ].flatMap((body, cardIndex) => {
            const children = [...body.children]
              .filter((element) => {
                const style = window.getComputedStyle(element);
                return style.display !== "none" && style.visibility !== "hidden";
              })
              .map((element) => ({
                label: element.className || element.tagName.toLowerCase(),
                top: element.getBoundingClientRect().top,
                bottom: element.getBoundingClientRect().bottom,
              }));
            return children.slice(1).flatMap((current, index) => {
              const previous = children[index];
              return current.top < previous.bottom - 1
                ? [{
                    cardIndex,
                    previous: previous.label,
                    current: current.label,
                    overlap: Math.round(previous.bottom - current.top),
                  }]
                : [];
            });
          });
          const candidateHitAreaLabels = [
            ...document.querySelectorAll(
              ".vt-candidate-card__hit-area .vt-visually-hidden",
            ),
          ].map((label) => {
            const rect = label.getBoundingClientRect();
            const style = window.getComputedStyle(label);
            return {
              width: Math.round(rect.width),
              height: Math.round(rect.height),
              position: style.position,
              clipPath: style.clipPath,
            };
          });
          const forbiddenVisibleTerms = ["Session", "Workspace", "Target"]
            .filter((term) =>
              new RegExp(`(^|\\s)${term}(?=\\s|$|[·:])`, "m")
                .test(document.body.innerText),
            );
          const operationsForbiddenVisibleTerms = [
            "Session",
            "Workspace",
            "Purchase",
            "CheckoutQuote",
          ].filter((term) =>
            new RegExp(`(^|\\s)${term}(?=\\s|$|[·:])`, "m")
              .test(document.body.innerText),
          );
          const visiblePrimaryActionElements = [
            ...document.querySelectorAll(
              ".vt-button--primary:not(:disabled), .order-ui-primary-link",
            ),
          ].filter((element) => {
            const style = window.getComputedStyle(element);
            const rect = element.getBoundingClientRect();
            return style.visibility !== "hidden"
              && style.display !== "none"
              && rect.width > 0
              && rect.height > 0;
          });
          const visiblePrimaryActions = visiblePrimaryActionElements.length;
          // The count alone says a route broke the one-primary-action rule but
          // not which control to demote, which is the only thing the reader
          // needs next. Name them.
          const visiblePrimaryActionLabels = visiblePrimaryActionElements
            .map((element) => element.textContent?.trim())
            .filter(Boolean);
          const curationRowOverlaps = [
            ...document.querySelectorAll(".workspace-curation-row"),
          ].flatMap((row, rowIndex) => {
            const identity = row.querySelector(".workspace-curation-row__identity");
            const status = row.querySelector(".workspace-curation-row__status");
            if (!identity || !status) return [];
            const identityRect = identity.getBoundingClientRect();
            const statusRect = status.getBoundingClientRect();
            const overlapWidth = Math.min(identityRect.right, statusRect.right) -
              Math.max(identityRect.left, statusRect.left);
            const overlapHeight = Math.min(identityRect.bottom, statusRect.bottom) -
              Math.max(identityRect.top, statusRect.top);
            return overlapWidth > 1 && overlapHeight > 1
              ? [{
                  rowIndex,
                  area: Math.round(overlapWidth * overlapHeight),
                }]
              : [];
          });
          const adminTabLabels = [
            ...document.querySelectorAll(".account-ui-section-tabs.is-admin a"),
          ]
            // 뱃지 카운트(예: "예외 처리 3")는 데이터라 라벨 검증에서 걷어낸다.
            .map((link) => link.textContent?.trim().replace(/\s*\d+$/, ""))
            .filter(Boolean);
          const operatorStatuses = [
            ...document.querySelectorAll(
              ".order-ui-operator-console .vt-chip",
            ),
          ].map((status) => {
            const style = window.getComputedStyle(status);
            return {
              text: status.textContent?.trim(),
              color: style.color,
              backgroundColor: style.backgroundColor,
            };
          });
          const operatorTabList = document.querySelector(
            ".workspace-operator-tabs",
          );
          const operatorOwnership = document.querySelector(
            ".workspace-operator-ownership",
          );
          const operatorTabs = operatorTabList
            ? (() => {
                const style = window.getComputedStyle(operatorTabList);
                const tabRect = operatorTabList.getBoundingClientRect();
                const ownershipRect = operatorOwnership?.getBoundingClientRect();
                const activeTab = operatorTabList.querySelector(
                  '[aria-selected="true"]',
                );
                const activeStyle = activeTab
                  ? window.getComputedStyle(activeTab)
                  : null;
                const activeAfterStyle = activeTab
                  ? window.getComputedStyle(activeTab, "::after")
                  : null;
                return {
                  buttonCount: operatorTabList.querySelectorAll("button").length,
                  clientWidth: operatorTabList.clientWidth,
                  scrollWidth: operatorTabList.scrollWidth,
                  clientHeight: operatorTabList.clientHeight,
                  scrollHeight: operatorTabList.scrollHeight,
                  overflowX: style.overflowX,
                  overflowY: style.overflowY,
                  ownershipGap: ownershipRect
                    ? Math.round(ownershipRect.top - tabRect.bottom)
                    : null,
                  simulatedNoticeVisible: document.body.innerText.includes(
                    "TEST · Mock merchant · 외부 효과 SIMULATED",
                  ),
                  activeIndicator: activeTab ? {
                    borderBottomColor: activeStyle?.borderBottomColor,
                    afterDisplay: activeAfterStyle?.display,
                    afterOpacity: activeAfterStyle?.opacity,
                  } : null,
                };
              })()
            : null;
          const opsHeaderElement = document.querySelector(
            ".product-ui-ops > .vt-page-header",
          );
          const opsHeader = opsHeaderElement
            ? (() => {
                const mainRect = document.querySelector(".product-ui-ops")
                  ?.getBoundingClientRect();
                const headerRect = opsHeaderElement.getBoundingClientRect();
                const copyRect = opsHeaderElement.querySelector(
                  ".vt-page-header__copy",
                )?.getBoundingClientRect();
                return {
                  mainWidth: Math.round(mainRect?.width ?? 0),
                  headerWidth: Math.round(headerRect.width),
                  copyWidth: Math.round(copyRect?.width ?? 0),
                };
              })()
            : null;
          const walletKycCards = [
            ...document.querySelectorAll(".account-ui-wallet-kyc"),
          ].map((card) => card.textContent?.replace(/\s+/g, " ").trim());
          const accountHeadingMetrics = [
            ...document.querySelectorAll(
              ".order-ui-settings-section > header h2",
            ),
          ].map((heading) => {
            const rect = heading.getBoundingClientRect();
            const style = getComputedStyle(heading);
            return {
              text: heading.textContent?.trim(),
              width: Math.round(rect.width),
              height: Math.round(rect.height),
              writingMode: style.writingMode,
            };
          });
          const planningControlOverlaps = [
            [".shell-planning-toggle", ".shell-planning-help"],
            [".shell-planning-help", ".shell-intent-composer__submit"],
            [".shell-planning-toggle", ".shell-intent-composer__submit"],
          ].map(([first, second]) => ({
            first,
            second,
            area: overlap(first, second),
          })).filter(({ area }) => area !== null);
          const homeFrame = document.querySelector(
            ".shell-product-frame.is-home",
          );
          const productBody = homeFrame?.querySelector(
            ".shell-product-body",
          );
          const homeMain = homeFrame?.querySelector(".shell-main");
          const homeViewport = {
            active: Boolean(homeFrame),
            bodyClientHeight: productBody?.clientHeight ?? null,
            bodyScrollHeight: productBody?.scrollHeight ?? null,
            bodyOverflowY: productBody
              ? getComputedStyle(productBody).overflowY
              : null,
            mainClientHeight: homeMain?.clientHeight ?? null,
            mainScrollHeight: homeMain?.scrollHeight ?? null,
          };
          const productBodyElement = document.querySelector(
            ".shell-product-body",
          );
          const pageHeaderElement = document.querySelector(
            ".order-ui-tracking-page > .vt-page-header",
          );
          const pageHeaderCopy = pageHeaderElement?.querySelector(
            ".vt-page-header__copy",
          );
          const pageHeaderSummary = pageHeaderElement?.querySelector(
            ".order-ui-page-header-summary",
          );
          const horizontalLayout = {
            productBody: productBodyElement ? {
              clientWidth: productBodyElement.clientWidth,
              scrollWidth: productBodyElement.scrollWidth,
              scrollLeft: productBodyElement.scrollLeft,
              overflowX: getComputedStyle(productBodyElement).overflowX,
            } : null,
            pageHeader: pageHeaderElement ? {
              width: Math.round(pageHeaderElement.getBoundingClientRect().width),
              minWidth: getComputedStyle(pageHeaderElement).minWidth,
              gridTemplateColumns: getComputedStyle(pageHeaderElement).gridTemplateColumns,
            } : null,
            pageHeaderCopy: pageHeaderCopy ? {
              width: Math.round(pageHeaderCopy.getBoundingClientRect().width),
              minWidth: getComputedStyle(pageHeaderCopy).minWidth,
              maxWidth: getComputedStyle(pageHeaderCopy).maxWidth,
            } : null,
            pageHeaderSummary: pageHeaderSummary ? {
              width: Math.round(pageHeaderSummary.getBoundingClientRect().width),
              minWidth: getComputedStyle(pageHeaderSummary).minWidth,
              gridTemplateColumns: getComputedStyle(pageHeaderSummary).gridTemplateColumns,
            } : null,
          };
          return {
            innerWidth: window.innerWidth,
            clientWidth,
            scrollWidth: document.documentElement.scrollWidth,
            innerHeight: window.innerHeight,
            scrollHeight: document.documentElement.scrollHeight,
            horizontalOverflow:
              document.documentElement.scrollWidth > clientWidth,
            overflowElements,
            images: [...document.images].map((image) => ({
              src: image.getAttribute("src"),
              complete: image.complete,
              naturalWidth: image.naturalWidth,
              naturalHeight: image.naturalHeight,
            })),
            productShell: {
              h1Count: document.querySelectorAll("h1").length,
              unnamedButtons,
              duplicateIds,
              taskStepCount: document.querySelectorAll(
                ".shell-task-nav > ol > li",
              ).length,
              candidateCardHeights,
              candidateGridMetrics,
              candidateContentOverlaps,
              candidateHitAreaLabels,
              forbiddenVisibleTerms,
            },
            orderOperations: {
              h1Count: document.querySelectorAll("h1").length,
              unnamedButtons,
              duplicateIds,
              visiblePrimaryActions,
              visiblePrimaryActionLabels,
              curationRowOverlaps,
              forbiddenVisibleTerms: operationsForbiddenVisibleTerms,
            },
            adminTabLabels,
            operatorStatuses,
            operatorTabs,
            opsHeader,
            walletKycCards,
            accountHeadingMetrics,
            planningControlOverlaps,
            homeViewport,
            horizontalLayout,
          };
        });
        // The tracking list polls every 5s; if a reload re-mounted the first
        // order card between the click above and this screenshot, the card
        // is collapsed. Evidence must show the expanded card, so re-open it.
        if (target.id.startsWith("agency-order")) {
          const trigger = page.locator(
            ".workspace-agency-order-disclosure .vt-disclosure__trigger",
          ).first();
          if ((await trigger.getAttribute("aria-expanded")) !== "true") {
            await trigger.click();
            await page.getByRole("link", {
              name: target.locale === "en-US" ? "Payment details" : "결제 상세",
            }).first().waitFor({ state: "visible" });
          }
        }
        await page.screenshot({
          path: path.join(outputDir, filename),
          ...(frameViewport
            ? { clip: { x: 0, y: 0, ...frameViewport }, type: frameFormat, ...(frameFormat === "jpeg" ? { quality: 90 } : {}) }
            : { fullPage: true }),
        });

        results.push({
          target: target.id,
          fixture: target.fixture,
          productShell: target.productShell,
          orderOperations: target.orderOperations,
          operationsConsumer: target.operationsConsumer,
          primaryActionLimit: target.primaryActionLimit,
          viewport: viewportName,
          route: target.route,
          finalURL: page.url(),
          status: response?.status() ?? null,
          title: await page.title(),
          expectedText: target.expectedText,
          screenshot: filename,
          koreanFontInjected: injectKoreanFont,
          layout,
          shapeViolations,
          appearanceContrast,
          mobileSidebar,
          unexpectedAPI,
          consoleErrors,
          pageErrors,
          failedResponses,
        });
        await context.close();
      }
    }
  } finally {
    await browser.close();
    await vite?.close();
  }

  const manifest = {
    capturedAt: new Date().toISOString(),
    baseURL,
    outputDir,
    source: "real React routes with browser-level deterministic API fixtures",
    tokenSourceHash,
    // N9: computed-style audit per route capture; must stay empty.
    radiusBoldBorderViolations: results.flatMap((entry) =>
      entry.shapeViolations.map((offender) => `${entry.target}/${entry.viewport}: ${offender}`),
    ),
    results,
  };
  await fs.writeFile(
    path.join(outputDir, "manifest.json"),
    `${JSON.stringify(manifest, null, 2)}\n`,
  );
  process.stdout.write(`${JSON.stringify(manifest, null, 2)}\n`);
  if (manifest.radiusBoldBorderViolations.length > 0) {
    console.error(`N9 radius+bold-border violations:\n${manifest.radiusBoldBorderViolations.map((entry) => `- ${entry}`).join("\n")}`);
    process.exitCode = 1;
  }

  if (
    results.some(
      ({
        target,
        status,
        unexpectedAPI,
        pageErrors,
        consoleErrors,
        productShell,
        orderOperations,
        operationsConsumer,
        primaryActionLimit,
        layout,
        appearanceContrast,
        mobileSidebar,
      }) =>
        status === null ||
        status >= 400 ||
        unexpectedAPI.length > 0 ||
        pageErrors.length > 0 ||
        consoleErrors.length > 0 ||
        layout.horizontalOverflow ||
        (
          layout.horizontalLayout.productBody &&
          layout.horizontalLayout.productBody.scrollWidth >
            layout.horizontalLayout.productBody.clientWidth + 1
        ) ||
        layout.overflowElements.length > 0 ||
        (!skipContrast && appearanceContrast.some(({ result }) => result !== "PASS")) ||
        layout.planningControlOverlaps.some(({ area }) => area > 0) ||
        (
          layout.homeViewport.active &&
          (
            layout.homeViewport.bodyOverflowY !== "hidden" ||
            layout.homeViewport.bodyClientHeight !== layout.innerHeight ||
            layout.homeViewport.bodyScrollHeight >
              layout.homeViewport.bodyClientHeight + 1 ||
            layout.homeViewport.mainScrollHeight >
              layout.homeViewport.mainClientHeight + 1
          )
        ) ||
        layout.accountHeadingMetrics.some(
          ({ width, writingMode }) => width < 96 || writingMode !== "horizontal-tb",
        ) ||
        (
          mobileSidebar &&
          (
            mobileSidebar.initialSidebarVisibility !== "hidden" ||
            mobileSidebar.initialTriggerDisplay === "none" ||
            mobileSidebar.mobileHeaderPresent ||
            mobileSidebar.productBodyTouchAction !== "pan-y" ||
            mobileSidebar.openSidebarVisibility !== "visible" ||
            mobileSidebar.openExpanded !== "true" ||
            mobileSidebar.openProductBodyInert !== true ||
            mobileSidebar.focusedControl !== "사이드바 닫기" ||
            (mobileSidebar.profileMenuZIndex !== null &&
              mobileSidebar.profileMenuZIndex <= mobileSidebar.sidebarZIndex) ||
            mobileSidebar.profileMenuTopLayer === false ||
            mobileSidebar.profileMenuWithinSidebar === false ||
            mobileSidebar.closedExpanded !== "false" ||
            mobileSidebar.closedProductBodyInert !== false ||
            mobileSidebar.restoredFocus !== "사이드바 열기" ||
            mobileSidebar.openGestureStartX <= 32 ||
            !mobileSidebar.dragOpenPreview ||
            mobileSidebar.dragOpenPreview === "none" ||
            !mobileSidebar.dragClosePreview ||
            mobileSidebar.dragClosePreview === "none" ||
            mobileSidebar.swipeOpen !== true ||
            mobileSidebar.swipeClose !== true
          )
        ) ||
        (
          layout.adminTabLabels.length > 0 &&
          layout.adminTabLabels.join("|") !==
            "주문 처리|예외 처리|주문 회계|고객 대화|API 사용량|운영 현황"
        ) ||
        layout.operatorStatuses.some(
          ({ color, backgroundColor }) =>
            color === "rgb(255, 255, 255)" ||
            backgroundColor !== "rgba(0, 0, 0, 0)",
        ) ||
        (
          layout.operatorTabs &&
          (
            layout.operatorTabs.buttonCount !== 7 ||
            layout.operatorTabs.scrollWidth >
              layout.operatorTabs.clientWidth + 1 ||
            layout.operatorTabs.scrollHeight >
              layout.operatorTabs.clientHeight + 1 ||
            layout.operatorTabs.overflowX === "auto" ||
            layout.operatorTabs.overflowX === "scroll" ||
            layout.operatorTabs.overflowY === "auto" ||
            layout.operatorTabs.overflowY === "scroll" ||
            layout.operatorTabs.ownershipGap < 16 ||
            layout.operatorTabs.simulatedNoticeVisible
          )
        ) ||
        (
          layout.walletKycCards.length > 0 &&
          layout.walletKycCards.some((copy) => !copy?.includes("KYC"))
        ) ||
        (
          productShell &&
          (
            layout.productShell.h1Count !== (target === "curation-review" ? 0 : 1) ||
            layout.productShell.unnamedButtons > 0 ||
            layout.productShell.duplicateIds.length > 0 ||
            layout.productShell.candidateContentOverlaps.length > 0 ||
            layout.productShell.candidateHitAreaLabels.some(
              ({ width, height, position, clipPath }) =>
                width > 1 ||
                height > 1 ||
                position !== "absolute" ||
                clipPath === "none",
            ) ||
            layout.productShell.forbiddenVisibleTerms.length > 0 ||
            layout.productShell.candidateCardHeights.some(
              (heights) =>
                heights.length > 1 &&
                Math.max(...heights) - Math.min(...heights) > 4,
            ) ||
            layout.productShell.candidateGridMetrics.some(
              ({
                columns,
                cardWidths,
                singleCardWidth,
                singleCardColumns,
              }) =>
                columns !== (
                  layout.innerWidth <= 640
                    ? 1
                    : layout.innerWidth <= 1120
                      ? 2
                      : 3
                ) ||
                singleCardColumns !== columns ||
                singleCardWidth > 257 ||
                cardWidths.some((width) => width > 257),
            )
          )
        ) ||
        (
          orderOperations &&
          (
            layout.orderOperations.h1Count !== 1 ||
            layout.orderOperations.unnamedButtons > 0 ||
            layout.orderOperations.duplicateIds.length > 0 ||
            layout.orderOperations.visiblePrimaryActions > primaryActionLimit ||
            layout.orderOperations.curationRowOverlaps.length > 0 ||
            (
              operationsConsumer &&
              layout.orderOperations.forbiddenVisibleTerms.length > 0
            )
          )
        ) ||
        (
          layout.operatorTabs?.activeIndicator &&
          layout.operatorTabs.activeIndicator.afterDisplay !== "none" &&
          layout.operatorTabs.activeIndicator.afterOpacity !== "0"
        ) ||
        (
          layout.opsHeader &&
          (
            layout.opsHeader.headerWidth < layout.opsHeader.mainWidth - 2 ||
            layout.opsHeader.copyWidth < layout.opsHeader.headerWidth * 0.65
          )
        ),
    )
  ) {
    process.exitCode = 1;
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
