import { afterEach, describe, expect, it, vi } from "vitest";
import {
  LiveCatalogAPIError,
  hydrateCatalogResearch,
  loadCatalogCart,
  replaceCatalogCart,
  searchLiveCatalog,
  type LiveCatalogError,
  type LiveCatalogResponse,
  type LiveCatalogSearchInput,
  type LiveCartItem,
} from "./liveCatalogReviewApi";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("searchLiveCatalog", () => {
  it("retries a local busy fault with the same command identity", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(errorResponse({
        code: "RATE_LIMITED",
        reasonCode: "LIVE_CATALOG_REVIEW_BUSY",
        retryable: true,
        retryAfterSeconds: 2,
      }))
      .mockResolvedValueOnce(successResponse());
    vi.stubGlobal("fetch", fetchMock);
    const sleep = vi.fn().mockResolvedValue(undefined);

    await expect(searchLiveCatalog(searchInput(), { sleep })).resolves.toMatchObject({
      source: "LIVE_SHOPIFY_GLOBAL_CATALOG",
      poolVersion: 4,
    });

    expect(sleep).toHaveBeenCalledOnce();
    expect(sleep).toHaveBeenCalledWith(2_000);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    const requests = fetchMock.mock.calls.map(([, init]) => ({
      idempotencyKey: new Headers(init?.headers).get("Idempotency-Key"),
      body: JSON.parse(String(init?.body)),
    }));
    expect(requests[0]).toEqual(requests[1]);
    expect(requests[0]).toEqual({
      idempotencyKey: "phase8-command-fixed",
      body: expect.objectContaining({ expectedPoolVersion: 3 }),
    });
  });

  it("stops when the bounded local-busy wait budget is exhausted", async () => {
    const busy = errorResponse({
      code: "RATE_LIMITED",
      reasonCode: "LIVE_CATALOG_REVIEW_BUSY",
      retryable: true,
      retryAfterSeconds: 5,
    });
    const fetchMock = vi.fn().mockResolvedValue(busy);
    vi.stubGlobal("fetch", fetchMock);
    const sleep = vi.fn().mockResolvedValue(undefined);

    await expect(searchLiveCatalog(searchInput(), { sleep })).rejects.toMatchObject({
      fault: { reasonCode: "LIVE_CATALOG_REVIEW_BUSY" },
    });
    expect(fetchMock).toHaveBeenCalledTimes(5);
    expect(sleep).toHaveBeenCalledTimes(4);
    expect(sleep).toHaveBeenCalledWith(5_000);
  });

  it("surfaces a provider Retry-After beyond the foreground wait budget without sleeping early", async () => {
    const fetchMock = vi.fn().mockResolvedValue(errorResponse({
      code: "RATE_LIMITED",
      reasonCode: "RATE_LIMITED",
      retryable: true,
      retryAfterSeconds: 30,
    }));
    vi.stubGlobal("fetch", fetchMock);
    const sleep = vi.fn().mockResolvedValue(undefined);

    await expect(searchLiveCatalog(searchInput(), { sleep })).rejects.toMatchObject({
      fault: { reasonCode: "RATE_LIMITED", retryAfterSeconds: 30 },
    });
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(sleep).not.toHaveBeenCalled();
  });

  it.each([
    "PROVIDER_SCHEMA_MISMATCH",
    "PROVIDER_UNAVAILABLE",
  ])("does not retry %s", async (reasonCode) => {
    const fetchMock = vi.fn().mockResolvedValue(errorResponse({
      code: "UPSTREAM_FAILURE",
      reasonCode,
      retryable: true,
      retryAfterSeconds: 1,
    }));
    vi.stubGlobal("fetch", fetchMock);
    const sleep = vi.fn().mockResolvedValue(undefined);

    await expect(searchLiveCatalog(searchInput(), { sleep })).rejects.toBeInstanceOf(
      LiveCatalogAPIError,
    );
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(sleep).not.toHaveBeenCalled();
  });

  it("forwards cancellation to Catalog hydration and CartView requests", async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) =>
      abortableResponse(init?.signal),
    );
    vi.stubGlobal("fetch", fetchMock);
    const controller = new AbortController();

    const workspace = hydrateCatalogResearch({
      curationId: "curation-1",
      scope: "VISIBLE_TARGET",
      targetId: "target-1",
      signal: controller.signal,
    });
    const cart = loadCatalogCart("curation-1", controller.signal);
    controller.abort();

    await expect(workspace).rejects.toMatchObject({ name: "AbortError" });
    await expect(cart).rejects.toMatchObject({ name: "AbortError" });
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(fetchMock.mock.calls[0]?.[1]?.signal).not.toBe(controller.signal);
    expect(fetchMock.mock.calls[0]?.[1]?.signal?.aborted).toBe(true);
    expect(fetchMock.mock.calls[1]?.[1]?.signal).toBe(controller.signal);
    expect(fetchMock.mock.calls[1]?.[1]?.signal?.aborted).toBe(true);
  });

  it("bounds hydration transport time and returns a typed retryable timeout", async () => {
    vi.stubGlobal("fetch", vi.fn((_input: RequestInfo | URL, init?: RequestInit) =>
      abortableResponse(init?.signal)
    ));

    await expect(hydrateCatalogResearch({
      curationId: "curation-1",
      scope: "VISIBLE_TARGET",
      targetId: "target-1",
      timeoutMilliseconds: 1,
    })).rejects.toMatchObject({
      fault: {
        code: "TIMEOUT",
        reasonCode: "CATALOG_RESEARCH_HYDRATION_TIMED_OUT",
        retryable: true,
      },
    });
  });

  it("serializes a candidate-only retry and normalizes a rolling v1 unresolved response", async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(true, 200, {
      schemaVersion: "vitlane.catalog-research-hydration.v1",
      pools: [{
        targetId: "target-1",
        products: [{ candidateId: "candidate-12", title: "" }],
        hiddenProducts: [],
      }],
    }));
    vi.stubGlobal("fetch", fetchMock);

    const workspace = await hydrateCatalogResearch({
      curationId: "curation-1",
      scope: "CANDIDATE",
      targetId: "target-1",
      candidateId: "candidate-12",
    });

    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      scope: "CANDIDATE",
      targetId: "target-1",
      candidateId: "candidate-12",
    });
    expect(workspace.pools[0].products[0].hydration).toEqual({
      status: "UNRESOLVED",
      reasonCode: "CATALOG_RESEARCH_CANDIDATE_UNRESOLVED",
      retryable: true,
    });
  });

  it("retries a transient CartView connection failure without inventing version zero", async () => {
    const payload = {
      schemaVersion: "vitlane.cart-view.v2",
      curationId: "curation-1",
      version: 7,
      country: "US",
      currency: "USD",
      items: [],
    };
    const fetchMock = vi.fn()
      .mockRejectedValueOnce(new TypeError("server route is starting"))
      .mockResolvedValueOnce(response(true, 200, payload));
    vi.stubGlobal("fetch", fetchMock);
    const sleep = vi.fn().mockResolvedValue(undefined);

    await expect(loadCatalogCart(
      "curation-1",
      undefined,
      { sleep },
    )).resolves.toEqual(payload);

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(sleep).toHaveBeenCalledWith(100);
  });

  it("retries a transient local-busy hydration without changing the command", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(errorResponse({
        code: "RATE_LIMITED",
        reasonCode: "LIVE_CATALOG_REVIEW_BUSY",
        retryable: true,
        retryAfterSeconds: 1,
      }))
      .mockResolvedValueOnce(response(true, 200, { pools: [] }));
    vi.stubGlobal("fetch", fetchMock);
    const sleep = vi.fn().mockResolvedValue(undefined);

    await expect(hydrateCatalogResearch({
      curationId: "curation-1",
      scope: "VISIBLE_TARGET",
      targetId: "target-1",
      sleep,
    })).resolves.toEqual({ pools: [] });

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(fetchMock.mock.calls[0]).toEqual(fetchMock.mock.calls[1]);
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      "/api/v1/curations/curation-1/catalog-research/hydrations",
    );
    expect(sleep).toHaveBeenCalledWith(1_000);
  });

  it("serializes Cart commands without response-only addedAt or display media", async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(true, 200, {
      schemaVersion: "vitlane.cart-view.v2",
      curationId: "curation-1",
      version: 3,
      country: "US",
      currency: "USD",
      items: [],
    }));
    vi.stubGlobal("fetch", fetchMock);

    const responseItems: Array<LiveCartItem & { addedAt: string }> = [{
      cartItemId: "cart:candidate-1:variant-1",
      targetId: "target-1",
      candidateId: "candidate-1",
      productTitle: "Camp chair",
      productUrl: "https://shop.example/products/chair",
      productImageUrl: "https://cdn.example/chair.jpg",
      merchantName: "Camp Shop",
      sellerDomain: "shop.example",
      intentPoint: "Compact for travel.",
      variantId: "variant-1",
      variantTitle: "Blue",
      selectedOptions: ["Color: Blue"],
      previewPriceMinor: 12000,
      previewCurrency: "USD",
      quantity: 2,
      observedAt: "2026-08-14T00:00:00Z",
      addedAt: "2026-08-14T00:00:01Z",
    }];
    await replaceCatalogCart("curation-1", 2, responseItems);

    const body = JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body));
    expect(body.expectedVersion).toBe(2);
    expect(body.items[0]).toMatchObject({
      cartItemId: "cart:candidate-1:variant-1",
      quantity: 2,
    });
    expect(body.items[0]).not.toHaveProperty("addedAt");
    expect(body.items[0]).not.toHaveProperty("productImageUrl");
  });
});

function searchInput(): LiveCatalogSearchInput {
  return {
    curationId: "curation-1",
    targetId: "target-1",
    mode: "APPEND",
    query: "lightweight commuter backpack",
    intent: "lightweight commuter backpack",
    country: "US",
    currency: "USD",
    limit: 8,
    expectedPoolVersion: 3,
    idempotencyKey: "phase8-command-fixed",
  };
}

function successResponse(): Response {
  const payload: LiveCatalogResponse = {
    schemaVersion: "vitlane.phase8-live-catalog-review.v1",
    source: "LIVE_SHOPIFY_GLOBAL_CATALOG",
    provider: "SHOPIFY_UCP_GLOBAL",
    protocolVersion: "2026-04-08",
    outcome: "SUCCESS",
    products: [],
    messages: [],
    candidateEligibleCount: 0,
    discardedNoLocatorCount: 0,
    hasNextPage: false,
    appliedFilterVerified: true,
    appliedFilterCapability: "dev.shopify.catalog.global",
    metrics: {
      policyVersion: "phase8-live-review.v1",
      durationMilliseconds: 20,
      aiCallCount: 0,
      aiCostUsd: "0.00",
      shopifyCallCount: 1,
      providerCostStatus: "NO_BILLING_CREDENTIAL",
      providerBillingCredential: false,
      localCallsUsed: 1,
      localCallsRemaining: 9,
      localRateLimit: 10,
      localRateWindowSeconds: 60,
      externalEffect: "CATALOG_READ_ONLY",
    },
    targetId: "target-1",
    poolVersion: 4,
    expandOrdinal: 1,
    mode: "APPEND",
    replay: false,
  };
  return response(true, 200, payload);
}

function errorResponse(error: LiveCatalogError): Response {
  return response(false, 429, { error });
}

function abortableResponse(signal?: AbortSignal | null): Promise<Response> {
  return new Promise((_resolve, reject) => {
    signal?.addEventListener("abort", () => {
      reject(new DOMException("The operation was aborted", "AbortError"));
    }, { once: true });
  });
}

function response(ok: boolean, status: number, payload: unknown): Response {
  return {
    ok,
    status,
    json: async () => payload,
  } as Response;
}
