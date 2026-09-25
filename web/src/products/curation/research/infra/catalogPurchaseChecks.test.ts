// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../../../shared/api/client", () => ({ request: vi.fn() }));

import { request } from "../../../../shared/api/client";
import {
  listPurchaseChecks,
  purchaseCheckItem,
  purchaseChecksChanged,
  undoPurchaseCheck,
  type PurchaseCheckRecord,
} from "./catalogPurchaseChecks";
import { sourceLabel } from "../../domain/sourceLabels";

const amazon: PurchaseCheckRecord = {
  curationId: "curation-1", targetId: "target-1", targetTitle: "헤드폰", candidateId: "cand-amazon",
  variantRef: { source: "AMAZON", marketplace: "US", asin: "B012345678" },
  productUrl: "https://www.amazon.com/dp/B012345678", checked: true, version: 2,
  recordedAt: "2026-09-14T03:00:00Z", evidence: "SELF_REPORTED",
  snapshot: { productTitle: "Wireless Headset", variantTitle: "Black", priceMinor: 12999, priceUnknown: false, currency: "USD" },
  snapshotAt: "2026-09-14T03:00:00Z",
};
const korean: PurchaseCheckRecord = {
  curationId: "curation-1", candidateId: "cand-pen",
  productRef: { source: "COUPANG", marketplace: "KR", productId: "8825648110" },
  productUrl: "https://www.coupang.com/vp/products/8825648110", checked: true, version: 1,
  recordedAt: "2026-09-13T03:00:00Z", evidence: "SELF_REPORTED", snapshot: null,
};

describe("catalogPurchaseChecks", () => {
  beforeEach(() => {
    vi.mocked(request).mockReset();
  });

  it("계정 목록을 출처·대상 ID·큐레이션 경로로 투영한다", async () => {
    vi.mocked(request).mockResolvedValueOnce({ schemaVersion: "vitlane.account-purchase-checks.v1", records: [amazon, korean] });
    const items = await listPurchaseChecks();
    expect(request).toHaveBeenCalledWith("/api/v1/account/purchase-checks");
    expect(items.map(({ key, source, subjectId, curationPath }) => ({ key, source, subjectId, curationPath }))).toEqual([
      { key: "curation-1::cand-amazon::AMAZON:B012345678", source: "AMAZON", subjectId: "B012345678", curationPath: "/curations/curation-1" },
      { key: "curation-1::cand-pen::COUPANG:8825648110", source: "COUPANG", subjectId: "8825648110", curationPath: "/curations/curation-1" },
    ]);
    expect(items[1].snapshot).toBeNull();
  });

  it("체크 취소는 카드와 같은 per-Candidate 명령을 version과 함께 보내고 변경 이벤트를 낸다", async () => {
    vi.mocked(request).mockResolvedValue({});
    const changed = vi.fn();
    window.addEventListener(purchaseChecksChanged, changed);
    await undoPurchaseCheck(amazon);
    const [amazonUrl, amazonInit] = vi.mocked(request).mock.calls[0] as [string, RequestInit];
    expect(amazonUrl).toBe("/api/v1/curations/curation-1/catalog-research/candidates/cand-amazon/amazon/purchase-check");
    expect(amazonInit.method).toBe("PUT");
    expect((amazonInit.headers as Record<string, string>)["Idempotency-Key"]).toMatch(/^[0-9a-f-]{36}$/);
    expect(JSON.parse(amazonInit.body as string)).toEqual({ variantRef: amazon.variantRef, checked: false, expectedVersion: 2 });
    await undoPurchaseCheck(korean);
    const [koreanUrl, koreanInit] = vi.mocked(request).mock.calls[1] as [string, RequestInit];
    expect(koreanUrl).toBe("/api/v1/curations/curation-1/catalog-research/candidates/cand-pen/external-product/purchase-check");
    expect(JSON.parse(koreanInit.body as string)).toEqual({ schemaVersion: "vitlane.external-product-purchase.v1", productRef: korean.productRef, checked: false, expectedVersion: 1 });
    expect(changed).toHaveBeenCalledTimes(2);
    window.removeEventListener(purchaseChecksChanged, changed);
  });

  it("취소 명령이 실패하면 이벤트 없이 오류를 전달한다", async () => {
    vi.mocked(request).mockRejectedValueOnce(new Error("conflict"));
    const changed = vi.fn();
    window.addEventListener(purchaseChecksChanged, changed);
    await expect(undoPurchaseCheck(amazon)).rejects.toThrow("conflict");
    expect(changed).not.toHaveBeenCalled();
    window.removeEventListener(purchaseChecksChanged, changed);
  });
});

// The server owns the platform list, so a mall registered after this release
// must still reach the account list with its own name instead of failing the
// build or rendering as Amazon.
describe("purchase check sources follow the server registry", () => {
  it("keeps a newly registered mall's code and label", () => {
    const item = purchaseCheckItem({
      curationId: "curation-1", candidateId: "cand-kurly",
      productRef: { source: "KURLY", marketplace: "KR", productId: "5063110" },
      productUrl: "https://www.kurly.com/goods/5063110", checked: true, version: 1,
      recordedAt: "2026-09-16T03:00:00Z", evidence: "SELF_REPORTED", snapshot: null,
    });
    expect(item.source).toBe("KURLY");
    expect(item.subjectId).toBe("5063110");
    expect(sourceLabel(item.source, (en, ko) => ko)).toBe("컬리");
  });

  it("falls back to Amazon only when no reference carries a source", () => {
    const item = purchaseCheckItem({
      curationId: "curation-1", candidateId: "cand-bare",
      productUrl: "https://example.test/p/1", checked: true, version: 1,
      recordedAt: "2026-09-16T03:00:00Z", evidence: "SELF_REPORTED", snapshot: null,
    } as PurchaseCheckRecord);
    expect(item.source).toBe("AMAZON");
  });
});
