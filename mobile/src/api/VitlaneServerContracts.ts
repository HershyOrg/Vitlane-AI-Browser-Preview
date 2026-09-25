export type ServerCurrency = "KRW" | "USD";

export type ServerMoney = {
  amount: string;
  currency: ServerCurrency;
};

export type ServerShoppingPlan = {
  id: string;
  originalIntent: string;
  totalBudget: ServerMoney;
  locationContext: { country: string; city?: string };
  budgetRequest?: {
    schemaVersion: "vitlane.curation-budget.v1";
    inputMode?: "AUTO" | "EXPLICIT";
    currency: ServerCurrency;
    totalAmount: string | null;
    allocationMode: "AUTO" | "EQUAL";
  };
};

export type ServerCuration = {
  id: string;
  shoppingPlanId: string;
  version: number;
  phase: "PLANNING" | "CURATING";
};

export type ServerPlanTarget = {
  id: string;
  title: string;
  normalizedIntent?: string;
  allocatedBudget?: ServerMoney;
  orderIndex?: number;
  confirmedAt?: string;
  removedAt?: string;
};

export type ServerPlanResult = {
  plan: ServerShoppingPlan;
  curation: ServerCuration;
  targets: ServerPlanTarget[];
};

export type ServerCart = {
  curationId: string;
  selections: Array<{
    selection: {
      candidateId: string;
      quantity: number;
    };
    candidate: {
      id: string;
      name: string;
      productUrl?: string;
      imageUrl?: string;
    };
    unitPrice: ServerMoney;
    lineTotal: ServerMoney;
  }>;
  total: ServerMoney;
  warnings: Array<{ code: string; message: string }>;
  updatedAt: string;
};

export type ServerSourceCoverage = {
  source: string;
  status: "SUCCEEDED" | "EMPTY" | "PARTIAL" | "FAILED" | "UNSUPPORTED" | "SKIPPED";
  reasonCode?: string;
  candidateCount: number;
};

export type ServerCatalogCandidate = {
  candidateId: string;
  source?: string;
  intentPoint?: string;
  features?: string[];
  specifications?: string[];
};

export type ServerCatalogPool = {
  targetId: string;
  version: number;
  sourceCoverage?: ServerSourceCoverage[] | null;
  products: ServerCatalogCandidate[];
  hiddenProducts: ServerCatalogCandidate[];
};

export type ServerActiveWork = {
  workTargetId: string;
  label: string;
  status: "QUEUED" | "RUNNING" | "RESULT_CONFIRMATION_REQUIRED";
  detail?: string;
};

export type ServerIntelligenceJobProgress = {
  jobId: string;
  actionId: string;
  targetKind: "PLANNING_TASK" | "RESEARCH_ROUND";
  targetId: string;
  provider: "MANAGED";
  status: "PENDING" | "RUNNING" | "SUCCEEDED" | "FAILED" | "CANCELLED";
  failureCode?: string;
  retryable: boolean;
  attempt: number;
  steps: unknown[];
  queueReason?: string;
  notBefore?: string;
  queueAhead?: number;
};

export type ServerWorkspace = {
  plan: ServerShoppingPlan;
  curation: ServerCuration;
  targets: ServerPlanTarget[];
  cart: ServerCart;
  timeline: ServerCurationTimelineItem[];
  coverage: "NONE" | "PARTIAL" | "COMPLETE";
  activeWork?: ServerActiveWork;
  intelligence: ServerIntelligenceJobProgress[];
  conversation?: {
    schemaVersion?: "vitlane.curation-conversation.v1";
    version: number;
    unfinished?: boolean;
    requests?: Array<{
      id: string;
      body: string;
      mode: string;
      status: "RESOLVING" | "DISPATCHED" | "COMPLETE";
      actionId?: string;
      createdAt: string;
    }>;
    messages: Array<{
      id: string;
      responseId?: string;
      kind: "RESULT" | "PROPOSAL" | "ERROR" | "NOTICE" | "CLARIFICATION";
      status: "PENDING" | "ACCEPTED" | "DISMISSED" | "ACKNOWLEDGED" | "SUPERSEDED";
      version: number;
      createdAt?: string;
      content: {
        code?: string;
        body?: string;
        locale?: "ko-KR" | "en-US";
        targetTitle?: string;
        added?: number;
        jobId?: string;
        reasonCode?: string;
        retryable?: boolean;
        availableAt?: string;
      };
    }>;
  };
  catalogResearch: {
    pools: ServerCatalogPool[];
  };
};

export type ServerCurationTimelineItem = {
  action: {
    id: string;
    curationId: string;
    type: string;
    phaseAtRequest: "HAVING_INTENT" | "PLANNING" | "CURATING";
    requestedTransitionTo?: "PLANNING" | "CURATING";
    subjectType: "INTENT" | "TARGET_LIST" | "CURATION" | "TARGET" | "SELECTION";
    subjectId?: string;
    effectKind: "NONE" | "INTELLIGENCE";
    expectedCurationVersion: number;
    createdAt: string;
  };
  displayBody?: string;
  result?: {
    kind: "INTENT_ACCEPTED" | "TARGET_EXPANSION" | "RESEARCH_STARTED" | "TARGET_RESEARCHED";
    summary: string;
    occurredAt: string;
    diff?: {
      added?: string[];
      changed?: string[];
      removed?: string[];
    };
  };
};

export type ServerBackgroundResearch = {
  schemaVersion: "vitlane.background-research.v1";
  subscriptions: Array<{
    id: string;
    curationId: string;
    targetId: string;
    status: "ACTIVE" | "EXPIRED" | "CANCELLED" | "TARGET_REMOVED" | "ARCHIVED";
    terms: {
      schemaVersion: "vitlane.research-subscription-terms.v1";
      criteria: unknown;
      country: "KR" | "US";
      currency: ServerCurrency;
      maximumMinor?: number;
      keywords: string[];
      expiresAt: string;
    };
    createdAt: string;
  }>;
  findings: Array<{
    id: string;
    subscriptionId: string;
    targetId: string;
    status: "NEW" | "HIDDEN" | "ADDED";
    product: {
      schemaVersion: "vitlane.deal-product.v1";
      provider: string;
      externalId: string;
      identity: string;
      title: string;
      description?: string;
      url: string;
      imageUrl?: string;
      country: "KR" | "US";
      currency: ServerCurrency;
      priceMinor?: number;
      shippingMinor?: number;
      observedAt: string;
      expiresAt: string;
    };
    reason: string;
    createdAt: string;
    candidateId?: string;
  }>;
};

export type ServerUserPreferencesResult = {
  preferences: {
    schemaVersion: "vitlane.user-preferences.v1";
    version: number;
    uiLocale?: "ko-KR" | "en-US";
    preferredCurrency?: ServerCurrency;
    researchCountry?: "KR" | "US";
  };
  effective: {
    schemaVersion: "vitlane.user-preferences.v1";
    version: number;
    uiLocale: "ko-KR" | "en-US";
    preferredCurrency: ServerCurrency;
    researchCountry: "KR" | "US";
  };
};

export type ServerThreadQuestion = {
  id: string;
  prompt: string;
  options: Array<{ id: string; label: string }> | null;
};

export type ServerThreadAnswer = {
  revision: number;
  questionId: string;
  optionId?: string;
  text?: string;
};

export type ServerThreadAction = {
  id: string;
  status: "PENDING" | "RUNNING" | "WAITING_SELECTION" | "SUCCEEDED" | "FAILED" | "CANCELLED" | "SKIPPED";
  question?: ServerThreadQuestion;
  questions?: ServerThreadQuestion[] | null;
  answers?: ServerThreadAnswer[] | null;
  response?: {
    kind: "COMMENT" | "ANSWER";
    body: string;
  };
  effects?: Array<{
    kind: string;
    after?: {
      enabled?: boolean;
      currency?: ServerCurrency;
      totalAmount?: string | null;
    };
  }>;
};

export type ServerCurationThread = {
  id: string;
  curationId: string;
  revision: number;
  origin: "REQUEST" | "MANUAL" | "PRIMITIVE";
  status: "INTERPRETING" | "WAITING_SELECTION" | "RUNNING" | "SUCCEEDED" | "FAILED" | "CANCELLED";
  request: string;
  reasonCode?: string;
  actions: ServerThreadAction[] | null;
  createdAt: string;
  updatedAt: string;
};

export type ServerThreadList = {
  threads: ServerCurationThread[];
};

export type ServerBudget = {
  schemaVersion: "vitlane.curation-budget.v1";
  version: number;
  researchVersion: number;
  enabled: boolean;
  currency: ServerCurrency;
  totalAmount: string | null;
  allocations: Array<{
    targetId: string;
    quantity: number;
    amount: string | null;
    minimumUnitAmount?: string;
  }>;
};

export type ServerHydratedCandidate = ServerCatalogCandidate & {
  source: string;
  title?: string;
  description?: string;
  priceMinimumMinor?: number;
  priceMaximumMinor?: number;
  currency?: string;
  mediaUrl?: string;
  mediaAlt?: string;
  locator?: {
    kind: "PRODUCT_URL" | "MERCHANT_VARIANT";
    productUrl?: string;
    variantId?: string;
    sellerDomain?: string;
  };
  sourceProductRef?:
    | { source: "SHOPIFY"; productId: string }
    | { source: "AMAZON"; marketplace: "US"; anchorAsin: string }
    | { source: string; marketplace: "KR"; productId: string };
  variantObservation?: {
    observationId: string;
    variantRef: { source: "AMAZON"; marketplace: "US"; asin: string };
    price:
      | { kind: "OBSERVED"; amountMinor: number; currency: "USD" }
      | { kind: "UNKNOWN"; reasonCode: string };
    availability: "AVAILABLE" | "UNAVAILABLE" | "UNKNOWN";
    seller: { kind: "KNOWN" | "UNKNOWN"; id?: string; name?: string };
    purchaseRoute: "EXTERNAL";
    productUrl: string;
    observedAt: string;
    refreshAfter: string;
  };
  externalObservation?: {
    schemaVersion: "vitlane.external-product-observation.v1";
    productRef: { source: string; marketplace: "KR"; productId: string };
    productUrl: string;
    title: string;
    description?: string;
    imageUrl?: string;
    price:
      | { kind: "OBSERVED"; amountMinor: number; currency: string }
      | { kind: "UNKNOWN"; reasonCode?: string };
    priceScope: "PRODUCT";
    seller: { kind: "KNOWN" | "UNKNOWN"; id?: string; name?: string };
    observedAt: string;
    provenance: {
      apiProvider: string;
      apiProduct: string;
      discoveryChannel: string;
      detailApiProvider?: string;
      detailApiProduct?: string;
      country: "KR";
      queryLanguage: "ko";
      providerLookupId?: string;
    };
  };
  purchaseRoute?: "VITLANE_CHECKOUT" | "EXTERNAL";
  hydration?: {
    status: "READY" | "UNRESOLVED" | "FAILED";
    reasonCode?: string;
    retryable: boolean;
  };
};

export type ServerHydration = {
  pools: Array<{
    targetId: string;
    sourceCoverage?: ServerSourceCoverage[] | null;
    products: ServerHydratedCandidate[];
    hiddenProducts: ServerHydratedCandidate[];
  }>;
};

export type ManagedRunnerCapability = {
  enabled: boolean;
  defaultModelKey: string;
  serverExhausted: boolean;
  models: Array<{ key: string; label: string }>;
};

export type ServerBrowserRunState =
  | "AWAITING_NAVIGATION_APPROVAL"
  | "NAVIGATION_APPROVED"
  | "AWAITING_PREPARATION_APPROVAL"
  | "PREPARATION_APPROVED"
  | "USER_CONTROL"
  | "RESUME_REQUIRES_OBSERVATION"
  | "PAUSED"
  | "READY_FOR_USER_PAYMENT"
  | "COMPLETED"
  | "PARTIAL"
  | "FAILED"
  | "RESULT_UNKNOWN"
  | "CANCELLED";

export type ServerBrowserRun = {
  id: string;
  curationId: string;
  candidateId: string;
  productUrl: string;
  merchantOrigin: string;
  merchantHost: string;
  state: ServerBrowserRunState;
  controlOwner: "NONE" | "AGENT" | "USER";
  version: number;
  freshObservationRequired: boolean;
  createdAt: string;
  updatedAt: string;
  navigationApprovedAt?: string;
};

export type ServerBrowserRunResponse = {
  schemaVersion: "vitlane.browser-run.v1";
  run: ServerBrowserRun;
};
