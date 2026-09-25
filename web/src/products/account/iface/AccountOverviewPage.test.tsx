// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { APIError } from "../../../shared/api/client";
import {
  checkKYC,
  deregisterWallet,
  getAccountOverview,
  revealShippingProfile,
  retireShippingProfile,
  saveDefaultShippingProfile,
  startKYCVerification,
  type AccountOverview,
  type KYCCredential,
  type KYCEvidenceObservation,
} from "../infra/accountApi";
import { getSettlementConfig } from "../../payment/giwa/infra/settlementApi";
import {
  listAgencyOrders,
} from "../../ordering/infra/agencyOrderApi";
import { runWalletRegistrationFlow } from "../app/walletRegistrationFlow";
import { AccountOverviewPage } from "./AccountOverviewPage";
import { listCatalogLikedCandidates } from "../../curation/research/infra/catalogLikedCandidates";
import { listPurchaseChecks, undoPurchaseCheck, type PurchaseCheckItem } from "../../curation/research/infra/catalogPurchaseChecks";

vi.mock("../infra/accountApi", () => ({
  checkKYC: vi.fn(),
  deregisterWallet: vi.fn(),
  getAccountOverview: vi.fn(),
  revealShippingProfile: vi.fn(),
  retireShippingProfile: vi.fn(),
  saveDefaultShippingProfile: vi.fn(),
  logoutAll: vi.fn(),
  requestAccountDeletion: vi.fn(),
  startKYCVerification: vi.fn(),
}));

vi.mock("../../payment/giwa/infra/settlementApi", () => ({
  getSettlementConfig: vi.fn(),
}));

vi.mock("../../ordering/infra/agencyOrderApi", () => ({
  listAgencyOrders: vi.fn(),
}));

vi.mock("../../curation/research/infra/catalogLikedCandidates", () => ({
  listCatalogLikedCandidates: vi.fn(),
  catalogLikedCandidatesChanged: "vitlane:phase8-liked-candidates-changed",
}));

vi.mock("../../curation/research/infra/catalogPurchaseChecks", () => ({
  listPurchaseChecks: vi.fn(),
  undoPurchaseCheck: vi.fn(),
  purchaseChecksChanged: "vitlane:external-purchase-changed",
}));

const purchaseChecks: PurchaseCheckItem[] = [{
  key: "curation-1::cand-amazon::AMAZON:B012345678", curationId: "curation-1", targetId: "target-1", targetTitle: "헤드폰",
  candidateId: "cand-amazon", variantRef: { source: "AMAZON", marketplace: "US", asin: "B012345678" },
  productUrl: "https://www.amazon.com/dp/B012345678", checked: true, version: 2, recordedAt: "2026-09-14T03:00:00Z",
  evidence: "SELF_REPORTED", snapshot: { productTitle: "Wireless Headset", variantTitle: "Black", merchant: "Amazon", priceMinor: 12999, priceUnknown: false, currency: "USD" },
  snapshotAt: "2026-09-14T03:00:00Z", curationPath: "/curations/curation-1", source: "AMAZON", subjectId: "B012345678",
}, {
  key: "curation-1::cand-pen::COUPANG:8825648110", curationId: "curation-1", candidateId: "cand-pen",
  productRef: { source: "COUPANG", marketplace: "KR", productId: "8825648110" },
  productUrl: "https://www.coupang.com/vp/products/8825648110", checked: true, version: 1, recordedAt: "2026-09-13T03:00:00Z",
  evidence: "SELF_REPORTED", snapshot: { productTitle: "라미 사파리 만년필", priceMinor: 0, priceUnknown: true },
  curationPath: "/curations/curation-1", source: "COUPANG", subjectId: "8825648110",
}, {
  key: "curation-1::legacy::AMAZON:B000000001", curationId: "curation-1", candidateId: "legacy",
  variantRef: { source: "AMAZON", marketplace: "US", asin: "B000000001" },
  productUrl: "https://www.amazon.com/dp/B000000001", checked: true, version: 1, recordedAt: "2026-09-12T03:00:00Z",
  evidence: "SELF_REPORTED", snapshot: null, curationPath: "/curations/curation-1", source: "AMAZON", subjectId: "B000000001",
}];

const emptyAgencyOrders = {
  schemaVersion: "vitlane.agency-order-list.v1",
  agencyOrders: [],
  countsByView: {
    PAYMENT_REQUIRED: 0,
    IN_PROGRESS: 0,
    NEEDS_ATTENTION: 0,
    FINISHED: 0,
    ALL: 0,
  },
};

vi.mock("../app/walletRegistrationFlow", () => ({
  runWalletRegistrationFlow: vi.fn(),
}));

vi.mock("../app/useCurrentUser", () => ({
  useCurrentUser: () => ({
    user: {
      id: "user-1",
      displayName: "Vitlane 사용자",
      email: "person@example.com",
      createdAt: "2026-07-24T00:00:00Z",
      marketingOperator: false,
      phase5Operator: false,
    },
    refresh: vi.fn(),
  }),
}));

const account: AccountOverview = {
  wallets: [{
    wallet: {
      id: "wallet-1",
      userId: "user-1",
      address: "0x1111111111111111111111111111111111111111",
      accountId: "eip155:91342:0x1111111111111111111111111111111111111111",
      chainId: "eip155:91342",
      registrationStatus: "REGISTERED",
      currentOwnershipProofId: "proof-1",
      isDefault: true,
      registeredAt: "2026-07-24T00:00:00Z",
      createdAt: "2026-07-24T00:00:00Z",
      updatedAt: "2026-07-24T00:00:00Z",
    },
    ownership: {
      status: "VALID",
      proofId: "proof-1",
      verifiedAt: "2026-07-24T00:00:00Z",
      validUntil: "2026-08-24T00:00:00Z",
    },
    kyc: {
      eligibility: "NONE",
      actionEligible: false,
    },
    actions: {
      canSetDefault: false,
      canDeregister: true,
      canReauthenticate: false,
      canStartKYC: true,
      canCheckKYC: false,
    },
  }],
  buyerProfiles: [{
    id: "profile-1",
    profileKind: "TEST_PROFILE" as const,
    label: "Vitlane TEST 수령인",
    fixtureKey: "vitlane-test-buyer-v1",
    country: "US",
    city: "Testville",
    version: 1,
    snapshotHash: `0x${"1".repeat(64)}`,
    containsRealPii: false as const,
    isDefault: true,
  }],
  shippingProfiles: [{
    id: "shipping-1",
    label: "집",
    country: "KR",
    maskedSummary: "KR · •••12",
    keyVersion: "pii-v1",
    version: 1,
    isDefault: true,
    createdAt: "2026-07-24T00:00:00Z",
    updatedAt: "2026-07-24T00:00:00Z",
  }],
  policyAcceptances: [{
    policyId: "TEST_SETTLEMENT",
    policyVersion: "2026-07-24",
    acceptedAt: "2026-07-24T00:00:00Z",
  }],
  assurancePolicy: {
    PLAN_RESEARCH: "LOGIN",
    WALLET_REGISTRATION: "CURRENT_WALLET_OWNERSHIP_PROOF",
    TEST_LOW_VALUE: "CURRENT_WALLET_OWNERSHIP_PROOF",
    TEST_ADVANCED: "CURRENT_MOCK_DOJANG_KYC_CREDENTIAL",
    REAL_VALUE_PAYMENT: "NOT_AVAILABLE",
  },
};

const kycCredential: KYCCredential = {
  id: "credential-1",
  userId: "user-1",
  walletId: "wallet-1",
  caseId: "kyc-1",
  level: "MOCK_DOJANG_VERIFIED",
  providerKind: "MOCK_DOJANG",
  providerVersion: "vitlane.mock-dojang.v2",
  externalEffect: "SIMULATED",
  subjectAccountId:
    "eip155:91342:0x1111111111111111111111111111111111111111",
  issuerRef: "mock-dojang",
  schemaRef: "vitlane.mock-dojang.v2",
  evidenceHash: "0xkyc",
  issuedAt: "2026-07-24T00:00:00Z",
  validUntil: "2026-08-24T00:00:00Z",
  createdAt: "2026-07-24T00:00:00Z",
};

const kycObservation: KYCEvidenceObservation = {
  id: "observation-1",
  credentialId: "credential-1",
  userId: "user-1",
  walletId: "wallet-1",
  status: "VALID",
  evidenceHash: "0xobservation",
  sourceVersion: 1,
  observedAt: "2026-07-24T00:00:00Z",
  validUntil: "2026-08-24T00:00:00Z",
  recheckAfter: "2026-07-24T00:10:00Z",
};

describe("AccountOverviewPage", () => {
  let container: HTMLDivElement | undefined;

  beforeEach(() => {
    vi.mocked(listPurchaseChecks).mockResolvedValue([]);
    vi.mocked(undoPurchaseCheck).mockReset();
    sessionStorage.clear();
    localStorage.clear();
    vi.mocked(listCatalogLikedCandidates).mockResolvedValue([]);
    vi.mocked(getSettlementConfig).mockResolvedValue({
      settlement: {
        environment: "LOCAL",
        chainId: 91342,
        chainCaip2: "eip155:91342",
        rpcUrl: "http://127.0.0.1:8545",
        explorerUrl: "",
      },
    } as never);
  });

  afterEach(() => {
    container?.remove();
    container = undefined;
    vi.restoreAllMocks();
    vi.clearAllMocks();
  });

  it("기본 지갑과 마스킹된 실제 배송 프로필을 보여주고 원문은 다시 렌더링하지 않는다", async () => {
    vi.mocked(getAccountOverview).mockResolvedValue({ account });
    vi.mocked(listAgencyOrders).mockResolvedValue(emptyAgencyOrders);
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("0x111111…111111");
    expect(container.textContent).toContain("registration REGISTERED");
    expect(container.textContent).toContain("집");
    expect(container.textContent).toContain("KR · •••12");
    expect(container.textContent).toContain("암호화 저장됨 · 기본 화면에서는 가림");
    expect(container.textContent).not.toContain("보호 방식");
    expect(container.textContent).not.toContain("default view MASKED");
    expect(container.textContent).not.toContain("pii-v1");
    expect(container.textContent).toContain("신원 확인 시작");
    expect(container.textContent).toContain("소유권 인증 유효");
    expect(container.textContent).toContain("프로필과 구매 진행 상황");
    expect(container.textContent).not.toContain("주소는 암호화해 저장합니다");
    expect(container.textContent).not.toContain("서울특별시 실제 주소");
    expect(listAgencyOrders).toHaveBeenCalledTimes(1);
    expect(deregisterWallet).not.toHaveBeenCalled();
    expect(saveDefaultShippingProfile).not.toHaveBeenCalled();
    expect(retireShippingProfile).not.toHaveBeenCalled();
    expect(startKYCVerification).not.toHaveBeenCalled();
    expect(checkKYC).not.toHaveBeenCalled();
    expect(runWalletRegistrationFlow).not.toHaveBeenCalled();

    await act(async () => root.unmount());
  });

  it.each([false, true])("Phase 8 Variant 좋아요를 복구하고 가격 미확인=%s를 유지한다", async (priceUnknown) => {
    vi.mocked(getAccountOverview).mockResolvedValue({ account });
    vi.mocked(listAgencyOrders).mockResolvedValue(emptyAgencyOrders);
    vi.mocked(listCatalogLikedCandidates).mockResolvedValue([{
      key: "live-one::variant-2",
      curationId: "curation-1",
      candidateId: "live-one",
      variantId: "variant-2",
      variantTitle: "Blue / Medium",
      productTitle: "Live liked backpack",
      productUrl: "https://shop.example/products/live-one",
      merchant: "Shop Example",
      priceMinor: priceUnknown ? 0 : 8400,
      priceUnknown,
      currency: "USD",
      targetTitle: "Commuter backpack",
      curationPath: "/curations/curation-1",
      updatedAt: "2026-08-13T00:00:00Z",
    }]);
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage view="liked" /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("Live liked backpack");
    expect(container.textContent).toContain("Blue / Medium");
    if (priceUnknown) {
      expect(container.textContent).toContain("가격 정보 없음");
      expect(container.textContent).not.toContain("$0.00");
    }
    expect(container.querySelector('a[href="/curations/curation-1"]')).not.toBeNull();
    await act(async () => root.unmount());
  });

  it("구매 체크한 상품을 계정에서 조회하고 스냅샷 없는 기록은 출처와 ID로 표시한다", async () => {
    vi.mocked(getAccountOverview).mockResolvedValue({ account });
    vi.mocked(listPurchaseChecks).mockResolvedValue(purchaseChecks);
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage view="purchased" /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());

    const text = container.textContent ?? "";
    expect(text).toContain("구매 체크한 상품");
    expect(text).toContain("주문 확인 내역이 아닙니다");
    expect(text).toContain("Wireless Headset");
    expect(text).toContain("Black");
    expect(text).toContain("129.99");
    expect(text).toContain("헤드폰");
    expect(text).toContain("라미 사파리 만년필");
    expect(text).toContain("가격 정보 없음");
    expect(text).toContain("Amazon B000000001");
    expect(text).not.toContain("$0.00");
    expect(container.querySelectorAll('a[href="/curations/curation-1"]')).toHaveLength(3);
    expect(container.querySelector('a[href="https://www.coupang.com/vp/products/8825648110"]')).not.toBeNull();
    expect([...container.querySelectorAll("button")].filter((button) => button.textContent === "체크 취소")).toHaveLength(3);
    expect(listCatalogLikedCandidates).not.toHaveBeenCalled();
    expect(listAgencyOrders).not.toHaveBeenCalled();
    await act(async () => root.unmount());
  });

  it("계정 목록의 체크 취소는 같은 구매 체크 명령을 쓰고 목록을 다시 읽는다", async () => {
    vi.mocked(getAccountOverview).mockResolvedValue({ account });
    vi.mocked(listPurchaseChecks).mockResolvedValueOnce(purchaseChecks.slice(0, 1)).mockResolvedValue([]);
    vi.mocked(undoPurchaseCheck).mockImplementation(async () => {
      window.dispatchEvent(new Event("vitlane:external-purchase-changed"));
    });
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage view="purchased" /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());
    expect(container.textContent).toContain("Wireless Headset");

    const undo = [...container.querySelectorAll("button")].find((button) => button.textContent === "체크 취소");
    await act(async () => { undo?.click(); });
    await act(async () => Promise.resolve());

    expect(undoPurchaseCheck).toHaveBeenCalledTimes(1);
    expect(vi.mocked(undoPurchaseCheck).mock.calls[0][0]).toMatchObject({ candidateId: "cand-amazon", version: 2, variantRef: { asin: "B012345678" } });
    expect(listPurchaseChecks).toHaveBeenCalledTimes(2);
    expect(container.textContent).not.toContain("Wireless Headset");
    expect(container.textContent).toContain("아직 구매 체크한 상품이 없습니다");
    await act(async () => root.unmount());
  });

  it("체크 취소가 실패하면 안내하고 목록을 다시 읽는다", async () => {
    vi.mocked(getAccountOverview).mockResolvedValue({ account });
    vi.mocked(listPurchaseChecks).mockResolvedValue(purchaseChecks.slice(0, 1));
    vi.mocked(undoPurchaseCheck).mockRejectedValue(new Error("PURCHASE_RECORD_VERSION_CONFLICT"));
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage view="purchased" /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());

    const undo = [...container.querySelectorAll("button")].find((button) => button.textContent === "체크 취소");
    await act(async () => { undo?.click(); });
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("체크 취소를 저장하지 못했습니다");
    expect(listPurchaseChecks).toHaveBeenCalledTimes(2);
    expect(container.textContent).toContain("Wireless Headset");
    expect(undo?.hasAttribute("disabled")).toBe(false);
    await act(async () => root.unmount());
  });

  it.each([
    ["liked", "좋아요한 상품", "Live liked backpack"],
    ["purchased", "구매 체크한 상품", "Wireless Headset"],
  ] as const)("패널로 연 %s 목록은 패널 머리글과 겹치는 섹션 제목을 그리지 않는다", async (view, label, item) => {
    vi.mocked(getAccountOverview).mockResolvedValue({ account });
    vi.mocked(listCatalogLikedCandidates).mockResolvedValue([{
      key: "live-one::variant-2", curationId: "curation-1", candidateId: "live-one", variantId: "variant-2",
      variantTitle: "Blue / Medium", productTitle: "Live liked backpack", productUrl: "https://shop.example/products/live-one",
      merchant: "Shop Example", priceMinor: 8400, priceUnknown: false, currency: "USD", targetTitle: "Commuter backpack",
      curationPath: "/curations/curation-1", updatedAt: "2026-08-13T00:00:00Z",
    }]);
    vi.mocked(listPurchaseChecks).mockResolvedValue(purchaseChecks.slice(0, 1));
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage embedded view={view} /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());

    const section = container.querySelector("section.order-ui-settings-section");
    expect(container.querySelectorAll("h2")).toHaveLength(0);
    expect(section?.getAttribute("aria-label")).toBe(label);
    expect(section?.getAttribute("aria-labelledby")).toBeNull();
    expect(section?.classList.contains("is-panel-section")).toBe(true);
    expect(container.textContent).toContain(item);
    await act(async () => root.unmount());

    // 전체 계정 페이지에서는 섹션마다 제목이 필요하므로 그대로 남는다.
    const standalone = createRoot(container);
    await act(async () => {
      standalone.render(<MemoryRouter><AccountOverviewPage view={view} /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());
    expect([...container.querySelectorAll("h2")].map((heading) => heading.textContent)).toContain(label);
    expect(container.querySelector("section.order-ui-settings-section")?.classList.contains("is-panel-section")).toBe(false);
    await act(async () => standalone.unmount());
  });

  it("계정 관리 전용 화면은 중복 제목과 보존 안내 배너 없이 두 작업만 간결하게 표시한다", async () => {
    vi.mocked(getAccountOverview).mockResolvedValue({ account });
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(
        <MemoryRouter>
          <AccountOverviewPage embedded view="management" />
        </MemoryRouter>,
      );
    });
    await act(async () => Promise.resolve());

    expect(container.querySelector(".account-ui-account-actions > header")).toBeNull();
    expect(container.querySelector(".account-ui-account-actions .vt-notice")).toBeNull();
    expect(container.querySelectorAll(".account-ui-account-action")).toHaveLength(2);
    expect(container.textContent).not.toContain("데이터 보존 안내");
    expect(container.textContent).not.toContain("결제·환불·보안·감사");
    expect(container.textContent).toContain("현재 기기를 포함한 모든 세션을 종료합니다.");
    expect(container.textContent).toContain("삭제 요청과 동시에 로그인이 차단됩니다.");
    expect(
      [...container.querySelectorAll("button")].map((button) => button.textContent),
    ).toEqual(["모든 기기에서 로그아웃", "계정 삭제 요청"]);
    expect(listAgencyOrders).not.toHaveBeenCalled();
    expect(listCatalogLikedCandidates).not.toHaveBeenCalled();
    expect(listPurchaseChecks).not.toHaveBeenCalled();

    await act(async () => root.unmount());
  });

  it("구매 추적과 좋아요 조회가 실패해도 지갑 등록 panel은 정상적으로 남는다", async () => {
    vi.mocked(getAccountOverview).mockResolvedValue({ account });
    vi.mocked(listAgencyOrders).mockRejectedValue(
      new Error("purchase unavailable"),
    );
    vi.mocked(listCatalogLikedCandidates).mockRejectedValue(
      new Error("liked unavailable"),
    );
    vi.mocked(listPurchaseChecks).mockRejectedValue(
      new Error("purchase checks unavailable"),
    );
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("지갑 등록");
    expect(container.textContent).toContain("registration REGISTERED");
    expect(container.textContent).toContain("AgencyOrder 추적만 불러오지 못했습니다");
    expect(container.textContent).toContain("좋아요한 상품만 불러오지 못했습니다");
    expect(container.textContent).not.toContain("계정 정보를 불러오지 못했습니다");

    await act(async () => root.unmount());
  });

  it("본인이 주소 보기를 요청한 동안에만 저장 배송지 원문을 표시한다", async () => {
    vi.mocked(getAccountOverview).mockResolvedValue({ account });
    vi.mocked(listAgencyOrders).mockResolvedValue(emptyAgencyOrders);
    vi.mocked(revealShippingProfile).mockResolvedValue({
      address: {
        recipientName: "홍길동",
        addressLine1: "서울특별시 실제 주소",
        addressLine2: "101호",
        city: "서울",
        region: "서울",
        postalCode: "01234",
        country: "KR",
        phone: "010-0000-0000",
      },
    });
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());
    const reveal = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "주소 보기",
    )!;
    await act(async () => {
      reveal.click();
      await Promise.resolve();
    });
    expect(revealShippingProfile).toHaveBeenCalledWith("shipping-1");
    expect(container.textContent).toContain("서울특별시 실제 주소");

    const hide = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "주소 가리기",
    )!;
    await act(async () => hide.click());
    expect(container.textContent).not.toContain("서울특별시 실제 주소");
    expect(container.textContent).toContain("KR · •••12");

    await act(async () => root.unmount());
  });

  it("기본 지갑의 KYC 방법과 상태를 첫 정보로 강조한다", async () => {
    const verified = structuredClone(account);
    verified.wallets[0].kyc = {
      eligibility: "VALID",
      actionEligible: true,
      providerKind: "MOCK_DOJANG",
      externalEffect: "SIMULATED",
      disclosure: "실제 외부 확인 없음",
      credential: kycCredential,
      observation: kycObservation,
    };
    vi.mocked(getAccountOverview).mockResolvedValue({ account: verified });
    vi.mocked(listAgencyOrders).mockResolvedValue(emptyAgencyOrders);
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("KYC TEST 유효");
    expect(container.textContent).toContain("TEST 모의 KYC 유효");
    expect(container.textContent).toContain("TEST 신원 확인 유효");
    expect(container.textContent).toContain("MockDojang 모의 KYC");
    expect(container.textContent).toContain("TEST · SIMULATED");
    expect(container.textContent).toContain("실제 외부 확인 없음");
    expect(container.querySelector(".account-ui-wallet-kyc.is-verified")).not.toBeNull();

    await act(async () => root.unmount());
  });

  it("latest observation이 RECHECK_REQUIRED이면 과거 credential을 완료로 표시하지 않는다", async () => {
    const stale = structuredClone(account);
    stale.wallets[0].kyc = {
      eligibility: "RECHECK_REQUIRED",
      actionEligible: false,
      providerKind: "MOCK_DOJANG",
      externalEffect: "SIMULATED",
      disclosure: "실제 외부 확인 없음",
      credential: kycCredential,
      observation: kycObservation,
      nextAction: { kind: "RECHECK" },
    };
    stale.wallets[0].actions.canStartKYC = false;
    stale.wallets[0].actions.canCheckKYC = true;
    vi.mocked(getAccountOverview).mockResolvedValue({ account: stale });
    vi.mocked(listAgencyOrders).mockResolvedValue(emptyAgencyOrders);
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());

    expect(container.textContent).toContain("KYC 재확인이 필요합니다");
    expect(container.textContent).toContain("TEST · SIMULATED");
    expect(container.textContent).toContain("2026년 8월 24일까지 유효");
    expect(container.textContent).toContain("2026년 7월 24일 이후 재확인");
    expect(container.textContent).not.toContain("TEST 신원 확인 유효");

    await act(async () => root.unmount());
  });

  it("retryable provider 장애의 이유와 같은 KYC 작업 재시도를 안내한다", async () => {
    const unavailable = structuredClone(account);
    unavailable.wallets[0].kyc = {
      eligibility: "PENDING",
      actionEligible: false,
      providerKind: "MOCK_DOJANG",
      externalEffect: "SIMULATED",
      disclosure: "실제 외부 확인 없음",
      failureCode: "PROVIDER_UNAVAILABLE",
      retryable: true,
      nextAction: {
        kind: "START_KYC",
        label: "신원 확인 다시 시도",
      },
    };
    unavailable.wallets[0].actions.canStartKYC = true;
    vi.mocked(getAccountOverview).mockResolvedValue({ account: unavailable });
    vi.mocked(listAgencyOrders).mockResolvedValue(emptyAgencyOrders);
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());

    const walletRow = container.querySelector(".account-ui-wallet-row")!;
    expect(walletRow.textContent).toContain("KYC 확인을 다시 시도해 주세요");
    expect(walletRow.textContent).toContain(
      "KYC 제공자 응답이 일시적으로 지연되었습니다.",
    );
    expect(walletRow.textContent).toContain(
      "신원 확인을 다시 시작할 수 있습니다.",
    );
    expect(walletRow.textContent).toContain(
      "failure PROVIDER_UNAVAILABLE · retryable true · next START_KYC",
    );
    expect(
      [...walletRow.querySelectorAll("button")].some(
        (button) => button.textContent === "신원 확인 시작",
      ),
    ).toBe(true);

    await act(async () => root.unmount());
  });

  it("nonretryable provider 실패는 재시작을 숨기고 stable code와 지원 행동을 표시한다", async () => {
    const failed = structuredClone(account);
    failed.wallets[0].kyc = {
      eligibility: "PENDING",
      actionEligible: false,
      providerKind: "MOCK_DOJANG",
      externalEffect: "SIMULATED",
      disclosure: "실제 외부 확인 없음",
      failureCode: "INVALID_PROVIDER_RESULT",
      retryable: false,
      nextAction: {
        kind: "CONTACT_SUPPORT",
        label: "KYC 지원 문의",
      },
    };
    failed.wallets[0].actions.canStartKYC = false;
    failed.wallets[0].actions.canCheckKYC = false;
    vi.mocked(getAccountOverview).mockResolvedValue({ account: failed });
    vi.mocked(listAgencyOrders).mockResolvedValue(emptyAgencyOrders);
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());

    const walletRow = container.querySelector(".account-ui-wallet-row")!;
    expect(walletRow.textContent).toContain("KYC 확인에 지원이 필요합니다");
    expect(walletRow.textContent).toContain(
      "KYC 제공자 결과를 안전하게 확인하지 못했습니다.",
    );
    expect(walletRow.textContent).toContain(
      "failure INVALID_PROVIDER_RESULT · retryable false · next CONTACT_SUPPORT",
    );
    expect(
      [...walletRow.querySelectorAll("button")].some(
        (button) => button.textContent === "KYC 지원 문의",
      ),
    ).toBe(true);
    expect(
      [...walletRow.querySelectorAll("button")].some(
        (button) => button.textContent === "신원 확인 시작",
      ),
    ).toBe(false);
    expect(
      [...walletRow.querySelectorAll("button")].some(
        (button) => button.textContent === "신원 확인 결과",
      ),
    ).toBe(false);

    await act(async () => root.unmount());
  });

  it("REJECTED terminal case의 이유를 보존하고 새 KYC 시작을 안내한다", async () => {
    const rejected = structuredClone(account);
    rejected.wallets[0].kyc = {
      eligibility: "REJECTED",
      actionEligible: false,
      providerKind: "MOCK_DOJANG",
      externalEffect: "SIMULATED",
      disclosure: "실제 외부 확인 없음",
      failureCode: "MOCK_DOJANG_REJECTED",
      retryable: true,
      nextAction: {
        kind: "START_KYC",
        label: "신원 확인 다시 시작",
      },
    };
    rejected.wallets[0].actions.canStartKYC = true;
    vi.mocked(getAccountOverview).mockResolvedValue({ account: rejected });
    vi.mocked(listAgencyOrders).mockResolvedValue(emptyAgencyOrders);
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());

    const walletRow = container.querySelector(".account-ui-wallet-row")!;
    expect(walletRow.textContent).toContain("KYC를 확인하지 못했습니다");
    expect(walletRow.textContent).toContain(
      "KYC 제공자의 확인 결과가 거절되었습니다.",
    );
    expect(walletRow.textContent).toContain(
      "failure MOCK_DOJANG_REJECTED · retryable true · next START_KYC",
    );
    expect(
      [...walletRow.querySelectorAll("button")].some(
        (button) => button.textContent === "신원 확인 시작",
      ),
    ).toBe(true);

    await act(async () => root.unmount());
  });

  it("KYC 실패 이유를 해당 지갑 카드 안에서 즉시 안내한다", async () => {
    const reauthenticationRequired = structuredClone(account);
    reauthenticationRequired.wallets[0].ownership = {
      status: "REAUTH_REQUIRED",
      nextAction: { kind: "REAUTHENTICATE" },
    };
    reauthenticationRequired.wallets[0].actions.canReauthenticate = true;
    reauthenticationRequired.wallets[0].actions.canStartKYC = false;
    vi.mocked(getAccountOverview)
      .mockResolvedValueOnce({ account })
      .mockResolvedValueOnce({ account: reauthenticationRequired });
    vi.mocked(listAgencyOrders).mockResolvedValue(emptyAgencyOrders);
    vi.mocked(startKYCVerification).mockRejectedValue(
      new APIError(
        "WALLET_OWNERSHIP_PROOF_NOT_FRESH",
        "Wallet 소유권 증명이 충분히 최근이 아닙니다.",
        422,
      ),
    );
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());
    const start = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "신원 확인 시작",
    )!;
    await act(async () => {
      start.click();
      await Promise.resolve();
    });

    const walletRow = container.querySelector(".account-ui-wallet-row")!;
    const alert = walletRow.querySelector('[role="alert"]');
    expect(alert?.textContent).toContain("신원 확인을 진행하지 못했습니다");
    expect(alert?.textContent).toContain(
      "이 지갑을 재인증한 뒤 다시 시도해 주세요.",
    );
    expect(
      [...walletRow.querySelectorAll("button")].some(
        (button) => button.textContent === "지갑 재인증",
      ),
    ).toBe(true);
    expect(
      [...walletRow.querySelectorAll("button")].some(
        (button) => button.textContent === "신원 확인 시작",
      ),
    ).toBe(false);

    await act(async () => root.unmount());
  });

  it("KYC payload가 바뀐 복구 key는 한 번 교체해 영구 고착을 막는다", async () => {
    vi.mocked(getAccountOverview).mockResolvedValue({ account });
    vi.mocked(listAgencyOrders).mockResolvedValue(emptyAgencyOrders);
    vi.mocked(startKYCVerification)
      .mockRejectedValueOnce(new APIError(
        "KYC_IDEMPOTENCY_KEY_REUSED",
        "이 작업 key는 이전 proof에 사용됐습니다.",
        409,
      ))
      .mockResolvedValueOnce({
        kycCase: {
          id: "kyc-1",
          userId: "user-1",
          walletId: "wallet-1",
          startedWithOwnershipProofId: "proof-1",
          requestedLevel: "ADVANCED_TEST_KYC",
          providerKind: "MOCK_DOJANG",
          providerVersion: "vitlane.mock-dojang.v2",
          externalEffect: "SIMULATED",
          state: "PENDING_PROVIDER",
          createdAt: "2026-07-24T00:00:00Z",
          updatedAt: "2026-07-24T00:00:00Z",
        },
        replay: true,
      });
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());
    const start = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "신원 확인 시작",
    )!;
    await act(async () => {
      start.click();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(startKYCVerification).toHaveBeenCalledTimes(2);
    expect(
      vi.mocked(startKYCVerification).mock.calls[0][2],
    ).not.toBe(
      vi.mocked(startKYCVerification).mock.calls[1][2],
    );
    expect(sessionStorage.getItem(
      "vitlane:operation:kyc-start:user-1:wallet-1",
    )).toBeNull();

    await act(async () => root.unmount());
  });

  it("24시간 proof가 VALID여도 KYC freshness 재인증 action을 가리지 않는다", async () => {
    const needsReauthentication = structuredClone(account);
    needsReauthentication.wallets[0].ownership.nextAction = {
      kind: "REAUTHENTICATE",
    };
    needsReauthentication.wallets[0].actions.canReauthenticate = true;
    needsReauthentication.wallets[0].actions.canStartKYC = false;
    const verified = structuredClone(account);
    vi.mocked(getAccountOverview).mockResolvedValue({
      account: needsReauthentication,
    });
    vi.mocked(listAgencyOrders).mockResolvedValue(emptyAgencyOrders);
    vi.mocked(runWalletRegistrationFlow).mockResolvedValue({
      account: verified.wallets[0].wallet.address,
      wallet: verified.wallets[0],
      ownershipProof: {
        id: "proof-1",
        walletId: "wallet-1",
        verifiedAt: "2026-07-24T00:00:00Z",
        validUntil: "2026-08-24T00:00:00Z",
      },
      replay: false,
      reused: false,
      provider: { request: vi.fn() },
    });
    Object.defineProperty(window, "ethereum", {
      configurable: true,
      value: { request: vi.fn() },
    });
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());

    const ownership = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "지갑 재인증",
    )!;
    const kycBefore = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "신원 확인 필요",
    )!;
    expect(ownership.disabled).toBe(false);
    expect(kycBefore.disabled).toBe(true);

    await act(async () => {
      ownership.click();
      await Promise.resolve();
    });

    expect(runWalletRegistrationFlow).toHaveBeenCalledWith(
      expect.objectContaining({ chainCaip2: "eip155:91342" }),
      expect.objectContaining({
        forceReauthentication: true,
        scope: "account:wallet:wallet-1:user:user-1",
        wallets: needsReauthentication.wallets,
      }),
    );
    expect(container.textContent).toContain("지갑 등록 완료");
    expect(container.textContent).toContain("소유권 인증 유효");
    const kycAfter = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "신원 확인 시작",
    )!;
    expect(kycAfter.disabled).toBe(false);

    await act(async () => root.unmount());
    Reflect.deleteProperty(window, "ethereum");
  });

  it("현재 Wallet 하나만 표시하고 추가 등록·default 선택을 숨긴다", async () => {
    vi.mocked(getAccountOverview).mockResolvedValue({ account });
    vi.mocked(listAgencyOrders).mockResolvedValue(emptyAgencyOrders);
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());
    expect(container.querySelectorAll(".account-ui-wallet-row")).toHaveLength(1);
    expect(container.textContent).toContain("현재 결제 지갑");
    expect(container.textContent).not.toContain("기본 지갑으로 설정");
    expect(
      [...container.querySelectorAll("button")]
        .filter((button) => button.textContent === "지갑 등록"),
    ).toHaveLength(0);

    await act(async () => root.unmount());
  });

  it("등록 해제 결과를 DEREGISTERED/REVOKED projection으로 표시한다", async () => {
    const deregistered = structuredClone(account);
    deregistered.wallets = [];
    vi.mocked(getAccountOverview)
      .mockResolvedValueOnce({ account })
      .mockResolvedValueOnce({ account: deregistered });
    vi.mocked(listAgencyOrders).mockResolvedValue(emptyAgencyOrders);
    vi.mocked(deregisterWallet).mockResolvedValue();
    vi.spyOn(window, "confirm").mockReturnValue(true);
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(<MemoryRouter><AccountOverviewPage /></MemoryRouter>);
    });
    await act(async () => Promise.resolve());
    const deregisterButton = [...container.querySelectorAll("button")].find(
      (button) => button.textContent === "지갑 등록 해제",
    )!;
    await act(async () => {
      deregisterButton.click();
      await Promise.resolve();
    });

    expect(deregisterWallet).toHaveBeenCalledWith("wallet-1");
    expect(container.textContent).toContain("아직 등록된 지갑이 없습니다");
    expect(
      [...container.querySelectorAll("button")]
        .some((button) => button.textContent === "지갑 등록"),
    ).toBe(true);

    await act(async () => root.unmount());
  });
});
