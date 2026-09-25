import { localizeFixedCopy } from "../../../../shared/i18n";
import type { ExternalProductObservation } from "../../domain/sourceProduct";
import type {PurchaseFeedback, SourceProductRef, VariantObservation} from "./amazonApi";
export type LiveCatalogProduct = {
 axisAssessment?: import("../../domain/researchCriteria").AxisAssessment;
 source?: string;
 sourceProductRef?: SourceProductRef;
 variantObservation?: VariantObservation;
 externalObservation?: ExternalProductObservation;
 purchaseRoute?: "EXTERNAL" | "VITLANE_CHECKOUT";
  candidateId: string;
  title: string;
  description: string;
  priceMinimumMinor?: number;
  priceMaximumMinor?: number;
  currency: string;
  mediaUrl?: string;
  mediaAlt?: string;
  categories: string[];
  locator?: {
    kind: "PRODUCT_URL" | "MERCHANT_VARIANT";
    productUrl?: string;
    variantId?: string;
    sellerDomain?: string;
  };
  previewVariant?: {
    id: string;
    title: string;
    priceMinor: number;
    currency: string;
    available?: boolean;
    sellerName?: string;
    sellerDomain?: string;
  };
	intentPoint?: string;
	features: string[];
	specifications: string[];
  hydration?: {
    status: "READY" | "UNRESOLVED" | "FAILED";
    reasonCode?: string;
    retryable: boolean;
  };
};

export type LiveCatalogMetrics = {
  policyVersion: string;
  durationMilliseconds: number;
  aiCallCount: number;
  aiCostUsd: string;
  shopifyCallCount: number;
  providerCostStatus: string;
  providerBillingCredential: boolean;
  localCallsUsed: number;
  localCallsRemaining: number;
  localRateLimit: number;
  localRateWindowSeconds: number;
  externalEffect: string;
};

export type LiveCatalogProviderMessage = {
  type: string;
  code?: string;
  path?: string;
  subjectKind?: string;
  subjectRef?: string;
  contentType?: string;
  content: string;
  severity?: string;
  presentation?: string;
  imageUrl?: string;
  url?: string;
};

export type LiveCatalogResponse = {
  schemaVersion: string;
  source: "LIVE_SHOPIFY_GLOBAL_CATALOG" | "MULTI_SOURCE_CATALOG";
  provider: string;
  protocolVersion: string;
  outcome: "SUCCESS" | "BUSINESS_ERROR";
  products: LiveCatalogProduct[];
  messages: LiveCatalogProviderMessage[];
  candidateEligibleCount: number;
  discardedNoLocatorCount: number;
  hasNextPage: boolean;
  estimatedTotalCount?: number;
  appliedFilterVerified: boolean;
  appliedFilterCapability: string;
  metrics: LiveCatalogMetrics;
  targetId: string;
  poolVersion: number;
  expandOrdinal: number;
  mode: "APPEND";
  replay: boolean;
};

export type LiveCatalogError = {
  code: string;
  reasonCode: string;
  retryable: boolean;
  retryAfterSeconds?: number;
};

export type LiveCartItem = {
  cartItemId: string;
  targetId: string;
  candidateId: string;
  productTitle: string;
  productUrl?: string;
  productImageUrl?: string;
  merchantName?: string;
  sellerDomain?: string;
  intentPoint?: string;
  variantId: string;
  variantTitle: string;
  selectedOptions: string[];
  previewPriceMinor: number;
  previewCurrency: string;
  quantity: number;
  observedAt: string;
};

export type LiveVariantRow = {
  priceUnknown?: boolean;
  variantId: string;
  title: string;
  priceMinor: number;
  currency: string;
  available: boolean;
  selectedOptions: Array<{ name: string; value: string }>;
  mediaUrl?: string;
  productUrl?: string;
};

export type LiveVariantPage = {
  schemaVersion: string;
 relationToken?: string;
 relationStatus?: string;
 truncated?: boolean;
  source: "SHOPIFY" | "AMAZON" | "LIVE_SHOPIFY_STOREFRONT";
  candidateId: string;
  productTitle: string;
  merchantDomain: string;
  rows: LiveVariantRow[];
  pagination: {
    pageSize: number;
    hasPrevious: boolean;
    hasNext: boolean;
    nextCursor?: string;
  };
  observedAt: string;
  metrics: LiveCatalogMetrics;
};

export type AgencyOrderPreparationPreview = {
  schemaVersion: string;
  source: "FRESH_SHOPIFY_LOOKUP";
  status: "READY_FOR_AGENCY_ORDER" | "ACTION_REQUIRED";
  lines: Array<{
    cartItemId: string;
    status: "READY" | "PRICE_CHANGED" | "UNAVAILABLE" | "UNRESOLVED";
    productTitle: string;
    variantId: string;
    variantTitle: string;
    productUrl?: string;
    currentPriceMinor: number;
    currency: string;
    previewPriceMinor: number;
    previewCurrency: string;
    quantity: number;
    available: boolean;
    priceChanged: boolean;
    mediaUrl?: string;
    sellerName?: string;
    safeReasonCode?: string;
  }>;
  subtotalMinor: number;
  currency: string;
  observedAt: string;
  provider: string;
  protocolVersion: string;
  metrics: LiveCatalogMetrics;
};

export type LiveCatalogSearchInput = {
  curationId: string;
  targetId: string;
  mode: "APPEND";
  query: string;
  intent: string;
  country: string;
  currency: string;
  minimumMinor?: number;
  maximumMinor?: number;
  limit?: number;
	expectedPoolVersion: number;
	idempotencyKey: string;
};

export type LiveCatalogBusyRetryOptions = {
  sleep?: (milliseconds: number) => Promise<void>;
  fresh?: boolean;
};

const maximumLiveCatalogRateLimitRetries = 20;
const maximumLiveCatalogRateLimitWaitMilliseconds = 20_000;
const retryableLiveCatalogRateLimitReasons = new Set([
  "LIVE_CATALOG_REVIEW_BUSY",
  "LIVE_CATALOG_REVIEW_RATE_LIMITED",
  "RATE_LIMITED",
  "PROVIDER_RATE_LIMITED",
]);

export async function searchLiveCatalog(
  input: LiveCatalogSearchInput,
  options: LiveCatalogBusyRetryOptions = {},
): Promise<LiveCatalogResponse> {
  const sleep = options.sleep ?? sleepForLiveCatalogRetry;
  return retryLiveCatalogRateLimit(() => requestLiveCatalogSearch(input), sleep);
}

async function retryLiveCatalogRateLimit<T>(
  operation: () => Promise<T>,
  sleep: (milliseconds: number) => Promise<void>,
): Promise<T> {
  let rateLimitRetries = 0;
  let rateLimitWaitMilliseconds = 0;

  while (true) {
    try {
      return await operation();
    } catch (caught) {
      const retryDelayMilliseconds =
        caught instanceof LiveCatalogAPIError
          ? liveCatalogRateLimitRetryDelayMilliseconds(caught.fault)
          : 0;
      if (
        !(caught instanceof LiveCatalogAPIError) ||
        !retryableLiveCatalogRateLimitReasons.has(caught.fault.reasonCode) ||
        !caught.fault.retryable ||
        rateLimitRetries >= maximumLiveCatalogRateLimitRetries ||
        rateLimitWaitMilliseconds + retryDelayMilliseconds >
          maximumLiveCatalogRateLimitWaitMilliseconds
      ) {
        throw caught;
      }
      rateLimitRetries += 1;
      rateLimitWaitMilliseconds += retryDelayMilliseconds;
      await sleep(retryDelayMilliseconds);
    }
  }
}

async function requestLiveCatalogSearch(
  input: LiveCatalogSearchInput,
): Promise<LiveCatalogResponse> {
  const response = await fetch(
    `/api/v1/curations/${encodeURIComponent(input.curationId)}/targets/${encodeURIComponent(input.targetId)}/catalog-research/expansions`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": input.idempotencyKey,
    },
    body: JSON.stringify({
      mode: input.mode,
      query: input.query,
      intent: input.intent,
      country: input.country,
      currency: input.currency,
      minimumMinor: input.minimumMinor,
      maximumMinor: input.maximumMinor,
      limit: input.limit ?? 8,
	  expectedPoolVersion: input.expectedPoolVersion,
    }),
  });
  const payload = (await response.json()) as
    | LiveCatalogResponse
    | { error: LiveCatalogError };
  if (!response.ok || "error" in payload) {
    throw new LiveCatalogAPIError(
      "error" in payload
        ? payload.error
        : {
            code: "INTERNAL_FAILURE",
            reasonCode: "LIVE_CATALOG_RESPONSE_INVALID",
            retryable: false,
          },
    );
  }
  return payload;
}

function liveCatalogRateLimitRetryDelayMilliseconds(
  fault: LiveCatalogError,
): number {
  const retryAfterSeconds = fault.retryAfterSeconds;
  if (
    typeof retryAfterSeconds !== "number" ||
    !Number.isFinite(retryAfterSeconds)
  ) {
    return 1_000;
  }
  // Never wake before the provider/server Retry-After deadline. Delays beyond
  // the bounded 20-second foreground budget are surfaced to the Target UI.
  return Math.max(1_000, Math.ceil(retryAfterSeconds * 1_000));
}

function sleepForLiveCatalogRetry(milliseconds: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds));
}

export async function prepareAgencyOrderPreview(
  curationId: string,
): Promise<AgencyOrderPreparationPreview> {
  const response = await fetch(
    `/api/v1/curations/${encodeURIComponent(curationId)}/prepare-agency-order`,
    {
      method: "POST",
    },
  );
  const payload = (await response.json()) as
    | AgencyOrderPreparationPreview
    | { error: LiveCatalogError };
  if (!response.ok || "error" in payload) {
    throw new LiveCatalogAPIError(
      "error" in payload
        ? payload.error
        : {
            code: "INTERNAL_FAILURE",
            reasonCode: "AGENCY_ORDER_PREPARATION_RESPONSE_INVALID",
            retryable: false,
          },
    );
  }
  return payload;
}

export async function loadLiveVariantPage(input: {
  curationId: string;
  candidateId: string;
  cursorToken?: string;
}): Promise<LiveVariantPage> {
  const response = await fetch(
    `/api/v1/curations/${encodeURIComponent(input.curationId)}/catalog-research/candidates/${encodeURIComponent(input.candidateId)}/variant-pages`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        ...(input.cursorToken ? { cursorToken: input.cursorToken } : {}),
      }),
    },
  );
  const payload = (await response.json()) as
    | LiveVariantPage
    | { error: LiveCatalogError };
  if (!response.ok || "error" in payload) {
    throw new LiveCatalogAPIError(
      "error" in payload
        ? payload.error
        : {
            code: "INTERNAL_FAILURE",
            reasonCode: "LIVE_VARIANT_PAGE_RESPONSE_INVALID",
            retryable: false,
          },
    );
  }
  return payload;
}

export type SourceCoverage = { source: string; status: "SUCCEEDED" | "EMPTY" | "PARTIAL" | "FAILED" | "UNSUPPORTED" | "SKIPPED"; reasonCode?: string; candidateCount: number };
export type CatalogWorkspaceResponse = {
  schemaVersion: string;
  messages: LiveCatalogProviderMessage[];
  pools: Array<{
    targetId: string;
    discoveryOutcome?: { schemaVersion: string; addedCount: number; duplicateCount: number; rejectedCount: number; evaluatedCount?: number; unevaluatedCount?: number; status: "ADDED" | "NO_NEW_CANDIDATES" | "EVALUATING" };
    version: number;
    expandOrdinal: number;
    latestMode?: "REPLACE" | "APPEND";
    sourceCoverage?: SourceCoverage[] | null;
    latestDurationMilliseconds: number;
    latestShopifyCalls: number;
    latestRateRemaining: number;
    products: LiveCatalogProduct[];
    hiddenProducts: LiveCatalogProduct[];
    messages: LiveCatalogProviderMessage[];
  }>;
  configurations: Array<{
    candidateId: string;
    // The saved option's version, so a card can save without reading it first.
    version?: number;
    variant: LiveVariantRow;
    observedAt: string;
  }>;
  interactions: Array<{
    candidateId: string;
    variantId: string;
    pinned: boolean;
    sentiment: "NONE" | "LIKE" | "DISLIKE";
  }>;
  // External card state for the whole curation, so a card grid needs no
  // per-card request. Absent from a server that does not carry it yet.
  purchaseFeedback?: PurchaseFeedback;
  productReactions?: Array<{ candidateId: string; pinned: boolean; sentiment: "NONE" | "LIKE" | "DISLIKE"; version: number }>;
  reactionAllowedCandidateIds?: string[];
  metrics: LiveCatalogMetrics;
};

export type CatalogCartResponse = {
  schemaVersion: "vitlane.cart-view.v2";
  curationId: string;
  version: number;
  country: string;
  currency: string;
  items: Array<LiveCartItem & { addedAt: string }>;
  updatedAt?: string;
};

type CatalogCartLoadOptions = {
  sleep?: (milliseconds: number) => Promise<void>;
};

const catalogCartLoadRetryDelaysMilliseconds = [100, 250, 500] as const;

type CatalogResearchHydrationInput = {
 source?: string;
  curationId: string;
  targetId: string;
  signal?: AbortSignal;
  sleep?: (milliseconds: number) => Promise<void>;
  timeoutMilliseconds?: number;
} & (
  | { scope: "VISIBLE_TARGET" | "HIDDEN_TARGET"; candidateId?: never }
  | { scope: "CANDIDATE"; candidateId: string }
);

export async function hydrateCatalogResearch(
  input: CatalogResearchHydrationInput,
): Promise<CatalogWorkspaceResponse> {
  const controller = new AbortController();
  let timedOut = false;
  const abortFromCaller = () => controller.abort(input.signal?.reason);
  input.signal?.addEventListener("abort", abortFromCaller, { once: true });
  if (input.signal?.aborted) abortFromCaller();
  const timeout = globalThis.setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, input.timeoutMilliseconds ?? 12_000);
  try {
    const workspace = await retryLiveCatalogRateLimit(
      () => requestCatalogJSON<CatalogWorkspaceResponse>(
        `/api/v1/curations/${encodeURIComponent(input.curationId)}/catalog-research/hydrations`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            scope: input.scope,
 source: input.source,
            targetId: input.targetId,
            ...(input.candidateId ? { candidateId: input.candidateId } : {}),
          }),
          signal: controller.signal,
        },
      ),
      (milliseconds) => abortableCatalogHydrationSleep(
        input.sleep ?? sleepForLiveCatalogRetry,
        milliseconds,
        controller.signal,
      ),
    );
    return normalizeCatalogHydration(workspace);
  } catch (caught) {
    if (timedOut) {
      throw new LiveCatalogAPIError({
        code: "TIMEOUT",
        reasonCode: "CATALOG_RESEARCH_HYDRATION_TIMED_OUT",
        retryable: true,
      });
    }
    throw caught;
  } finally {
    globalThis.clearTimeout(timeout);
    input.signal?.removeEventListener("abort", abortFromCaller);
  }
}

function normalizeCatalogHydration(
  workspace: CatalogWorkspaceResponse,
): CatalogWorkspaceResponse {
  const normalize = (product: LiveCatalogProduct): LiveCatalogProduct => ({
    ...product,
    hydration: product.hydration ?? {
      status: product.title.trim() ? "READY" : "UNRESOLVED",
      ...(!product.title.trim()
        ? { reasonCode: "CATALOG_RESEARCH_CANDIDATE_UNRESOLVED" }
        : {}),
      retryable: !product.title.trim(),
    },
  });
  return {
    ...workspace,
    pools: workspace.pools.map((pool) => ({
      ...pool,
      products: pool.products.map(normalize),
      hiddenProducts: pool.hiddenProducts.map(normalize),
    })),
  };
}

function abortableCatalogHydrationSleep(
  sleep: (milliseconds: number) => Promise<void>,
  milliseconds: number,
  signal: AbortSignal,
): Promise<void> {
  if (signal.aborted) return Promise.reject(signal.reason);
  return new Promise<void>((resolve, reject) => {
    const abort = () => reject(signal.reason);
    signal.addEventListener("abort", abort, { once: true });
    void sleep(milliseconds).then(resolve, reject).finally(() => {
      signal.removeEventListener("abort", abort);
    });
  });
}

export async function saveCatalogConfiguration(input: {
  curationId: string;
  candidateId: string;
  variant: LiveVariantRow;
  observedAt: string;
}): Promise<void> {
  await requestCatalogJSON<void>(
    `/api/v1/curations/${encodeURIComponent(input.curationId)}/catalog-research/candidates/${encodeURIComponent(input.candidateId)}/configuration`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        variantId: input.variant.variantId,
        selectedOptions: input.variant.selectedOptions.map(
          ({ name, value }) => `${name}: ${value}`,
        ),
        observedAt: input.observedAt,
      }),
    },
  );
}

export async function saveCatalogInteraction(input: {
  curationId: string;
  candidateId: string;
  variantId: string;
  pinned: boolean;
  sentiment: "NONE" | "LIKE" | "DISLIKE";
  likedSnapshot?: CatalogLikedVariantSnapshot;
  relationToken?: string;
}): Promise<void> {
  await requestCatalogJSON<void>(
    `/api/v1/curations/${encodeURIComponent(input.curationId)}/catalog-research/candidates/${encodeURIComponent(input.candidateId)}/variants/${encodeURIComponent(input.variantId)}/interaction`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        pinned: input.pinned,
        sentiment: input.sentiment,
        likedSnapshot: input.likedSnapshot,
        ...(input.relationToken ? { relationToken: input.relationToken } : {}),
      }),
    },
  );
}

export type CatalogLikedVariantSnapshot = {
  productTitle: string;
  variantTitle: string;
  productUrl?: string;
  merchant: string;
  priceMinor: number;
  priceUnknown?: boolean;
  currency: string;
  targetTitle: string;
};

export async function loadCatalogCart(
  curationId: string,
  signal?: AbortSignal,
  options: CatalogCartLoadOptions = {},
): Promise<CatalogCartResponse> {
  const sleep = options.sleep ?? sleepForCatalogCartLoadRetry;
  for (let attempt = 0; ; attempt += 1) {
    try {
      return await requestCatalogJSON<CatalogCartResponse>(
        `/api/v1/curations/${encodeURIComponent(curationId)}/cart`,
        signal ? { signal } : undefined,
      );
    } catch (caught) {
      const retryDelay = catalogCartLoadRetryDelaysMilliseconds[attempt];
      if (
        signal?.aborted ||
        retryDelay === undefined ||
        !retryableCatalogCartLoadFailure(caught)
      ) {
        throw caught;
      }
      await sleep(retryDelay);
      signal?.throwIfAborted();
    }
  }
}

function retryableCatalogCartLoadFailure(caught: unknown) {
  return caught instanceof TypeError || (
    caught instanceof LiveCatalogAPIError && caught.fault.retryable
  );
}

function sleepForCatalogCartLoadRetry(milliseconds: number) {
  return new Promise<void>((resolve) => setTimeout(resolve, milliseconds));
}

export async function replaceCatalogCart(
  curationId: string,
  expectedVersion: number,
  items: LiveCartItem[],
): Promise<CatalogCartResponse> {
  return requestCatalogJSON<CatalogCartResponse>(
    `/api/v1/curations/${encodeURIComponent(curationId)}/cart`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        expectedVersion,
        // Cart GET rows also carry response-only fields such as `addedAt`.
        // Build the command DTO explicitly so a server response can never be
        // reflected into the strict request decoder by accident.
        items: items.map(catalogCartCommandItem),
      }),
    },
  );
}

export async function loadCombinationStatus(curationId: string, threadId: string) {
  return requestCatalogJSON<import("../../domain/combination").CombinationStatus>(
    `/api/v1/curations/${encodeURIComponent(curationId)}/threads/${encodeURIComponent(threadId)}/combination`);
}

export async function applyCombinationCart(curationId: string, threadId: string, expectedVersion: number, mode: import("../../domain/combination").CombinationCartMode, items: LiveCartItem[], commandId: string) {
  return requestCatalogJSON<CatalogCartResponse>(
    `/api/v1/curations/${encodeURIComponent(curationId)}/threads/${encodeURIComponent(threadId)}/combination-cart`,
    { method: "POST", headers: { "Content-Type": "application/json", "Idempotency-Key": commandId },
      body: JSON.stringify({ commandId, expectedVersion, mode, items: items.map(catalogCartCommandItem) }) });
}

function catalogCartCommandItem(item: LiveCartItem) {
  return {
    cartItemId: item.cartItemId,
    targetId: item.targetId,
    candidateId: item.candidateId,
    productTitle: item.productTitle,
    ...(item.productUrl ? { productUrl: item.productUrl } : {}),
    ...(item.merchantName ? { merchantName: item.merchantName } : {}),
    ...(item.sellerDomain ? { sellerDomain: item.sellerDomain } : {}),
    ...(item.intentPoint ? { intentPoint: item.intentPoint } : {}),
    variantId: item.variantId,
    variantTitle: item.variantTitle,
    selectedOptions: [...item.selectedOptions],
    previewPriceMinor: item.previewPriceMinor,
    previewCurrency: item.previewCurrency,
    quantity: item.quantity,
    observedAt: item.observedAt,
  };
}

async function requestCatalogJSON<T>(
  path: string,
  init?: RequestInit,
): Promise<T> {
  const response = await fetch(path, init);
  if (response.status === 204) return undefined as T;
  const payload = (await response.json()) as T | { error: LiveCatalogError };
  if (!response.ok || (payload && typeof payload === "object" && "error" in payload)) {
    throw new LiveCatalogAPIError(
      payload && typeof payload === "object" && "error" in payload
        ? payload.error
        : {
            code: "INTERNAL_FAILURE",
            reasonCode: "PHASE8_RESPONSE_INVALID",
            retryable: false,
          },
    );
  }
  return payload as T;
}

export class LiveCatalogAPIError extends Error {
  constructor(readonly fault: LiveCatalogError) {
    super(fault.reasonCode);
    this.name = "LiveCatalogAPIError";
  }
}

export function formatMinor(
  amount: number,
  currency: string,
): string {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency,
  }).format(amount / (currency === "KRW" || currency === "JPY" ? 1 : 100));
}

export async function applyRepresentativeCart(curationId:string,expectedVersion:number,items:LiveCartItem[],commandId:string):Promise<CatalogCartResponse> {
 return requestCatalogJSON<CatalogCartResponse>(`/api/v1/curations/${encodeURIComponent(curationId)}/representative-cart`,{method:"POST",headers:{"Content-Type":"application/json","Idempotency-Key":commandId},body:JSON.stringify({schemaVersion:"vitlane.representative-cart-command.v1",expectedVersion,items:items.map(catalogCartCommandItem),commandId})});
}
