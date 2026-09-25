import { type FormEvent, useCallback, useEffect, useState } from "react";
import { Link, useNavigate } from "react-router";
import { APIError } from "../../../shared/api/client";
import {
  localizeFixedCopy,
  useLocale,
  type Localize,
} from "../../../shared/i18n";
import { browserWalletCapability } from "../../../shared/browser/capabilities";
import { Button, ButtonLink, Disclosure, FeedbackState, Field, Input, Notice, PageHeader, ProductMedia, Chip } from "../../../shared/ui";
import { getSettlementConfig } from "../../payment/giwa/infra/settlementApi";
import {
  listAgencyOrders,
  type AgencyOrderProjection,
} from "../../ordering/infra/agencyOrderApi";
import {
  runWalletRegistrationFlow,
} from "../app/walletRegistrationFlow";
import { useCurrentUser } from "../app/useCurrentUser";
import {
  checkKYC as checkKYCCase,
  deregisterWallet,
  getAccountOverview,
  logoutAll,
  requestAccountDeletion,
  revealShippingProfile,
  retireShippingProfile,
  saveDefaultShippingProfile,
  startKYCVerification,
  type AccountOverview,
  type RevealedShippingAddress,
  type ShippingAddressInput,
  type WalletKYCProjection,
  type WalletProjection,
} from "../infra/accountApi";
import {
  shippingAddressErrorSummary,
  validateShippingAddress,
  type ShippingAddressErrors,
} from "../domain/shippingAddress";
import {
  listCatalogLikedCandidates,
  catalogLikedCandidatesChanged,
  type CatalogLikedCandidate,
} from "../../curation/research/infra/catalogLikedCandidates";
import {
  listPurchaseChecks,
  purchaseChecksChanged,
  undoPurchaseCheck,
  type PurchaseCheckItem,
} from "../../curation/research/infra/catalogPurchaseChecks";
import { sourceLabel } from "../../curation/domain/sourceLabels";

const emptyAddress = (l: Localize): ShippingAddressInput => ({
  label: l("Default shipping address", "기본 배송지"),
  recipientName: "",
  addressLine1: "",
  addressLine2: "",
  city: "",
  region: "",
  postalCode: "",
  country: "US",
  phone: "",
});

export type AccountOverviewView =
  | "all"
  | "account"
  | "liked"
  | "purchased"
  | "management";

export function AccountOverviewPage({
  embedded = false,
  view = "all",
}: {
  embedded?: boolean;
  view?: AccountOverviewView;
}) {
  const { l, locale } = useLocale();
  const navigate = useNavigate();
  const { user } = useCurrentUser();
  const [account, setAccount] = useState<AccountOverview | null>(null);
  const [agencyOrders, setAgencyOrders] = useState<AgencyOrderProjection[]>([]);
  const [catalogLikedCandidates, setCatalogLikedCandidates] = useState<CatalogLikedCandidate[]>([]);
  const [agencyOrdersError, setAgencyOrdersError] = useState<string | null>(null);
  const [likedCandidatesError, setLikedCandidatesError] = useState<string | null>(null);
  const [purchaseChecks, setPurchaseChecks] = useState<PurchaseCheckItem[]>([]);
  const [purchaseChecksError, setPurchaseChecksError] = useState<string | null>(null);
  const [purchaseUndoError, setPurchaseUndoError] = useState<string | null>(null);
  const [undoingPurchaseKey, setUndoingPurchaseKey] = useState<string | null>(null);
  const [address, setAddress] = useState(() => emptyAddress(l));
  const [shippingEditorOpen, setShippingEditorOpen] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [shippingError, setShippingError] = useState<string | null>(null);
  const [shippingFieldErrors, setShippingFieldErrors] = useState<ShippingAddressErrors>({});
  const [kycFeedback, setKYCFeedback] = useState<{
    message: string;
    tone: "neutral" | "danger";
    walletId: string;
  } | null>(null);
  const [ownershipFeedback, setOwnershipFeedback] = useState<{
    message: string;
    tone: "neutral" | "danger";
    walletId: string;
  } | null>(null);
  const [revealedAddresses, setRevealedAddresses] = useState<
    Record<string, RevealedShippingAddress>
  >({});

  const loadAccount = useCallback(async () => {
    try {
      const result = await getAccountOverview();
      setAccount(result.account);
      setError(null);
    } catch (caught) {
      setError(messageOf(caught));
    }
  }, []);

  const loadAgencyOrders = useCallback(async () => {
    try {
      const response = await listAgencyOrders({ view: "ALL", limit: 30 });
      setAgencyOrders(response.agencyOrders);
      setAgencyOrdersError(null);
    } catch (caught) {
      setAgencyOrdersError(messageOf(caught));
    }
  }, []);

  const loadLikedCandidates = useCallback(async () => {
    try {
      const catalogLiked = await listCatalogLikedCandidates();
      setCatalogLikedCandidates(catalogLiked);
      setLikedCandidatesError(null);
    } catch (caught) {
      setLikedCandidatesError(messageOf(caught));
    }
  }, []);

  useEffect(() => {
    const refreshCatalogLikes = () => void loadLikedCandidates();
    window.addEventListener(catalogLikedCandidatesChanged, refreshCatalogLikes);
    return () => {
      window.removeEventListener(catalogLikedCandidatesChanged, refreshCatalogLikes);
    };
  }, [loadLikedCandidates]);

  const loadPurchaseChecks = useCallback(async () => {
    try {
      setPurchaseChecks(await listPurchaseChecks());
      setPurchaseChecksError(null);
    } catch (caught) {
      setPurchaseChecksError(messageOf(caught));
    }
  }, []);

  useEffect(() => {
    // The curation cards dispatch this after every check or undo (ADR-0075).
    const refreshPurchaseChecks = () => void loadPurchaseChecks();
    window.addEventListener(purchaseChecksChanged, refreshPurchaseChecks);
    return () => {
      window.removeEventListener(purchaseChecksChanged, refreshPurchaseChecks);
    };
  }, [loadPurchaseChecks]);

  async function undoPurchase(item: PurchaseCheckItem) {
    if (undoingPurchaseKey) return;
    setUndoingPurchaseKey(item.key);
    setPurchaseUndoError(null);
    try {
      await undoPurchaseCheck(item);
    } catch {
      // A stale version (another tab already undid it) or a transient failure:
      // the reloaded list is the truth, not the button that was pressed.
      setPurchaseUndoError(l(
        "We couldn't undo this purchase check. The list was refreshed; try again if it is still listed.",
        "체크 취소를 저장하지 못했습니다. 목록을 다시 불러왔으니 아직 남아 있으면 다시 시도해 주세요.",
      ));
      await loadPurchaseChecks();
    } finally {
      setUndoingPurchaseKey(null);
    }
  }

  useEffect(() => {
    if (account && account.shippingProfiles.length === 0) {
      setShippingEditorOpen(true);
    }
  }, [account]);

  function applyWalletProjection(projection: WalletProjection) {
    setAccount((current) => {
      if (!current) return current;
      const existing = current.wallets.findIndex(
        ({ wallet }) => wallet.id === projection.wallet.id,
      );
      const wallets = [...current.wallets];
      if (existing >= 0) wallets[existing] = projection;
      else wallets.push(projection);
      return { ...current, wallets };
    });
  }

  async function registerWallet(
    scope = "account:new-wallet",
    forceReauthentication = false,
    expectedAccount?: string,
  ) {
    setBusy(scope === "account:new-wallet" ? "wallet-register" : scope);
    setError(null);
    setOwnershipFeedback(null);
    try {
      if (!user?.id) {
        throw new Error(l(
          "Confirm the signed-in user, then try registering the wallet again.",
          "로그인 사용자를 확인한 뒤 지갑 등록을 다시 시도해 주세요.",
        ));
      }
      const [{ settlement }] = await Promise.all([getSettlementConfig()]);
      const result = await runWalletRegistrationFlow(settlement, {
        scope: `${scope}:user:${user.id}`,
        wallets: account?.wallets,
        forceReauthentication,
        expectedAccount,
      });
      applyWalletProjection(result.wallet);
      setOwnershipFeedback({
        message: result.reused
          ? l(
              "Reused the valid wallet registration and ownership proof.",
              "유효한 지갑 등록과 소유권 증명을 다시 사용했습니다.",
            )
          : result.replay
            ? l(
                "Recovered the completed wallet registration.",
                "완료된 지갑 등록 결과를 복구했습니다.",
              )
            : l(
                "Wallet registration and ownership verification are complete.",
                "지갑 등록과 소유권 인증이 완료되었습니다.",
              ),
        tone: "neutral",
        walletId: result.wallet.wallet.id,
      });
    } catch (caught) {
      setError(messageOf(caught));
    } finally {
      setBusy(null);
    }
  }

  async function signOutEverywhere() {
    if (!window.confirm(l(
      "Sign out on every device? Your current work will also end.",
      "모든 기기에서 로그아웃할까요? 현재 작업도 종료됩니다.",
    ))) return;
    setBusy("logout-all");
    try {
      await logoutAll();
      navigate("/login", { replace: true });
    } catch (caught) {
      setError(messageOf(caught));
      setBusy(null);
    }
  }

  async function deleteAccount() {
    if (!window.confirm(l(
      "Request account deletion? All sessions will end immediately, while transaction and audit records remain under the retention policy.",
      "계정 삭제를 요청할까요? 모든 세션은 즉시 종료되고 거래·감사 자료는 보존정책에 따라 유지됩니다.",
    ))) return;
    setBusy("delete-account");
    try {
      await requestAccountDeletion();
      navigate("/login?account=deletion-requested", { replace: true });
    } catch (caught) {
      setError(messageOf(caught));
      setBusy(null);
    }
  }

  useEffect(() => {
    void loadAccount();
    if (view === "all") void loadAgencyOrders();
    if (view === "all" || view === "liked") void loadLikedCandidates();
    if (view === "all" || view === "purchased") void loadPurchaseChecks();
  }, [loadAccount, loadAgencyOrders, loadLikedCandidates, loadPurchaseChecks, view]);

  async function deregister(projection: WalletProjection) {
    const { wallet } = projection;
    if (!window.confirm(l(
      "Deregister this wallet from your Vitlane account? This does not affect onchain assets.",
      "이 지갑을 Vitlane 계정에서 등록 해제할까요? 온체인 자산에는 영향을 주지 않습니다.",
    ))) return;
    setBusy(wallet.id);
    try {
      await deregisterWallet(wallet.id);
      await loadAccount();
    } catch (caught) {
      setError(messageOf(caught));
    } finally {
      setBusy(null);
    }
  }

  async function saveAddress(event: FormEvent) {
    event.preventDefault();
    const fieldErrors = validateShippingAddress(address, { requireLabel: true });
    const fieldSummary = shippingAddressErrorSummary(fieldErrors);
    setShippingFieldErrors(fieldErrors);
    if (fieldSummary) {
      setShippingError(fieldSummary);
      return;
    }
    setBusy("shipping");
    setShippingError(null);
    try {
      await saveDefaultShippingProfile(address);
      setAddress(emptyAddress(l));
      setShippingFieldErrors({});
      await loadAccount();
      setShippingEditorOpen(false);
    } catch (caught) {
      setShippingError(messageOf(caught));
    } finally {
      setBusy(null);
    }
  }

  async function retireAddress(profileId: string) {
    if (!window.confirm(l(
      "Retire this shipping profile? Existing AgencyOrder snapshots remain for audit purposes.",
      "이 배송 프로필을 폐기할까요? 이미 생성된 AgencyOrder snapshot은 감사 목적으로 유지됩니다.",
    ))) return;
    setBusy("shipping");
    try {
      await retireShippingProfile(profileId);
      await loadAccount();
    } catch (caught) {
      setError(messageOf(caught));
    } finally {
      setBusy(null);
    }
  }

  async function toggleAddress(profileId: string) {
    if (revealedAddresses[profileId]) {
      setRevealedAddresses((current) => {
        const next = { ...current };
        delete next[profileId];
        return next;
      });
      return;
    }
    setBusy(`shipping-reveal:${profileId}`);
    setError(null);
    try {
      const result = await revealShippingProfile(profileId);
      setRevealedAddresses((current) => ({
        ...current,
        [profileId]: result.address,
      }));
    } catch (caught) {
      setError(messageOf(caught));
    } finally {
      setBusy(null);
    }
  }

  async function beginKYC(projection: WalletProjection) {
    const walletId = projection.wallet.id;
    const proofId = projection.ownership.proofId;
    setBusy(`kyc:${walletId}`);
    setError(null);
    setKYCFeedback(null);
    try {
      if (!proofId || projection.ownership.status !== "VALID") {
        throw new Error(l(
          "Reauthenticate the wallet before starting identity verification.",
          "신원 확인을 시작하려면 먼저 지갑을 재인증해 주세요.",
        ));
      }
      if (!user?.id) {
        throw new Error(l(
          "Confirm the signed-in user, then try identity verification again.",
          "로그인 사용자를 확인한 뒤 신원 확인을 다시 시도해 주세요.",
        ));
      }
      const operationScope = `kyc-start:${user.id}:${walletId}`;
      const result = await runRecoverableKYCOperation(
        operationScope,
        (clientOperationId) => startKYCVerification(
          walletId,
          proofId,
          clientOperationId,
        ),
      );
      clearRecoverableOperationKey(operationScope);
      await loadAccount();
      setKYCFeedback({
        message: result.replay
          ? l(
              "Loaded the identity verification already in progress. Continue checking the result.",
              "이미 시작된 신원 확인을 불러왔습니다. 결과 확인을 계속해 주세요.",
            )
          : l(
              "Started identity verification for this account's wallet. Continue checking the result.",
              "이 계정의 지갑에 대한 신원 확인을 시작했습니다. 결과 확인을 계속해 주세요.",
            ),
        tone: "neutral",
        walletId,
      });
    } catch (caught) {
      if (walletProjectionMayBeStale(caught)) {
        await loadAccount();
      }
      setKYCFeedback({
        message: kycMessageOf(caught),
        tone: "danger",
        walletId,
      });
    } finally {
      setBusy(null);
    }
  }

  async function reauthenticateWallet(projection: WalletProjection) {
    await registerWallet(
      `account:wallet:${projection.wallet.id}`,
      true,
      projection.wallet.address,
    );
  }

  async function checkKYC(caseId: string, walletId: string) {
    setBusy(`kyc-case:${caseId}`);
    setError(null);
    setKYCFeedback(null);
    try {
      if (!user?.id) {
        throw new Error(l(
          "Confirm the signed-in user, then try identity verification again.",
          "로그인 사용자를 확인한 뒤 신원 확인을 다시 시도해 주세요.",
        ));
      }
      const operationScope = `kyc-check:${user.id}:${caseId}`;
      const result = await runRecoverableKYCOperation(
        operationScope,
        (clientOperationId) => checkKYCCase(caseId, clientOperationId),
      );
      clearRecoverableOperationKey(operationScope);
      await loadAccount();
      setKYCFeedback({
        message: result.replay
          ? l(
              "Loaded the completed identity-verification result.",
              "이미 완료된 신원 확인 결과를 불러왔습니다.",
            )
          : l(
              "Identity verification is complete and has been applied to this wallet's eligibility.",
              "신원 확인이 완료되었습니다. 이 계정의 지갑 자격에 반영했습니다.",
            ),
        tone: "neutral",
        walletId,
      });
    } catch (caught) {
      if (walletProjectionMayBeStale(caught)) {
        await loadAccount();
      }
      setKYCFeedback({
        message: kycMessageOf(caught),
        tone: "danger",
        walletId,
      });
    } finally {
      setBusy(null);
    }
  }

  if (!account) {
    return (
      <div className="order-ui-page-state">
        <FeedbackState
          description={error ?? l(
            "Securely loading the information used for purchases.",
            "결제에 사용할 정보를 안전하게 불러오고 있습니다.",
          )}
          state={error ? "error" : "loading"}
          title={error
            ? l("We couldn't load your account", "계정 정보를 불러오지 못했습니다")
            : l("Preparing purchase settings", "결제 설정을 준비하고 있습니다")}
        />
      </div>
    );
  }

  const shippingProfile = account.shippingProfiles.find((item) => item.isDefault);
  const defaultWallet = account.wallets.find(
    ({ wallet }) => (
      wallet.isDefault && wallet.registrationStatus === "REGISTERED"
    ),
  );
  const walletCapability = browserWalletCapability();
  const activeAgencyOrders = agencyOrders.filter((item) => !isTerminalAgencyOrder(item));
  const recentAgencyOrders = agencyOrders.filter(isTerminalAgencyOrder).slice(0, 3);
  const showAccountDetails = view === "all" || view === "account";
  const showLikedCandidates = view === "all" || view === "liked";
  // The panel already shows the view title and description in its own header
  // (AccountPanel). A section header inside it would repeat both, so a panel
  // view keeps only the accessible name, the way account management does.
  const panelSectionOnly = (target: AccountOverviewView) => embedded && view === target;
  const showPurchaseChecks = view === "all" || view === "purchased";
  const showManagement = view === "all" || view === "management";

  return (
    <div
      className={[
        "account-overview",
        "order-ui-account-page",
        embedded ? "is-embedded" : "",
      ].filter(Boolean).join(" ")}
    >
      {!embedded && (
        <PageHeader
          eyebrow={l("Profile · purchases · payment", "프로필 · 구매 · 결제 설정")}
          title={l("Account", "계정")}
          description={l(
            "Manage your profile, purchase progress, shipping address, wallet, and identity verification in one place.",
            "프로필과 구매 진행 상황, 배송지, 지갑과 신원 확인 상태를 한곳에서 관리합니다.",
          )}
          summary={(
            <div className="order-ui-page-header-summary">
              <span>{l("Default wallet", "기본 지갑")} <strong>{defaultWallet ? l("Set", "설정됨") : l("Required", "필요")}</strong></span>
              <span>{l("Shipping address", "배송지")} <strong>{shippingProfile ? l("Saved", "저장됨") : l("Required", "필요")}</strong></span>
              <span>{l("KYC", "KYC")} <strong>{kycSummaryCopy(defaultWallet?.kyc, l)}</strong></span>
            </div>
          )}
        />
      )}

      {showAccountDetails && (
        <section className="order-ui-settings-section phase9-profile-section" aria-labelledby="account-profile-heading">
        <header>
          <div>
            <h2 id="account-profile-heading">{l("Profile", "프로필")}</h2>
            <p>{l("Basic information that identifies your account.", "계정을 식별하는 기본 정보입니다.")}</p>
          </div>
        </header>
        <dl className="account-ui-profile-grid">
          <div><dt>{l("Name", "이름")}</dt><dd>{user?.displayName || l("Vitlane user", "Vitlane 사용자")}</dd></div>
          <div><dt>{l("Email", "이메일")}</dt><dd>{user?.email || l("Development account", "개발 계정")}</dd></div>
          <div><dt>{l("User ID", "사용자 ID")}</dt><dd><code>{user?.id || "—"}</code></dd></div>
          <div><dt>{l("Joined", "가입일")}</dt><dd>{user?.createdAt ? new Date(user.createdAt).toLocaleDateString(locale) : "—"}</dd></div>
        </dl>
        </section>
      )}

      {showAccountDetails && (
        <section className="order-ui-settings-section" aria-labelledby="account-wallet-heading">
        <header className="account-ui-section-header-with-action">
          <div>
            <h2 id="account-wallet-heading">{l("Payment wallet", "결제 지갑")}</h2>
            <p>{l(
              "Manage your current payment wallet and its identity-verification status.",
              "현재 결제 지갑 하나와 그 지갑의 신원 확인 상태를 관리합니다.",
            )}</p>
          </div>
          {account.wallets.length === 0 && (
            <Button
              emphasis="secondary"
              busy={busy === "wallet-register"}
              disabled={walletCapability.kind !== "INJECTED_READY"}
              onClick={() => void registerWallet()}
            >
              {l("Register wallet", "지갑 등록")}
            </Button>
          )}
        </header>
        {walletCapability.kind !== "INJECTED_READY"
          && (
            account.wallets.length === 0
            || account.wallets.some(({ actions }) => actions.canReauthenticate)
          ) && (
          <p className="vt-field__hint">
            {l(
              "Wallet registration needs a supported EVM wallet extension on this device.",
              "이 기기에는 지원되는 EVM 지갑 확장이 없어 지갑을 등록할 수 없습니다.",
            )}
          </p>
        )}
        {account.wallets.length === 0 ? (
          <FeedbackState
            state="empty"
            title={l("No wallet registered yet", "아직 등록된 지갑이 없습니다")}
          />
        ) : (
          <ul className="order-ui-settings-list">
            {account.wallets.map((projection) => {
              const { wallet, ownership, kyc, actions } = projection;
              const kycPresentation = presentKYC(kyc, l, locale);
              const ownershipValid = ownership.status === "VALID";
              const registered = wallet.registrationStatus === "REGISTERED";
              const testKYC = kyc.providerKind === "MOCK_DOJANG"
                || kyc.externalEffect === "SIMULATED";
              const supportURL = kyc.nextAction?.kind === "CONTACT_SUPPORT"
                ? safeKYCActionURL(kyc.nextAction.url)
                : undefined;
              return (
                <li className="order-ui-settings-row account-ui-wallet-row" key={wallet.id}>
                  <Chip tone={registered && ownershipValid ? "done" : registered ? "progress" : "waiting"}>
                    {registered ? l("Registered", "등록됨") : l("Deregistered", "등록 해제됨")}
                  </Chip>
                  <div className="order-ui-settings-row__copy">
                    <strong>{shortAddress(wallet.address)}</strong>
                    <small>
                      {l("Current payment wallet", "현재 결제 지갑")}
                      {" · "}
                      {ownershipCopy(ownership.status, l)}
                    </small>
                    <div
                      className={`account-ui-wallet-kyc is-${kycPresentation.tone}`}
                      role="status"
                    >
                      <span className="account-ui-wallet-kyc__icon" aria-hidden="true">
                        {kycPresentation.icon}
                      </span>
                      <div>
                        <strong>{kycPresentation.title}</strong>
                        <span>{kycPresentation.method}</span>
                        <small>{kycPresentation.detail}</small>
                      </div>
                    </div>
                    {testKYC && (
                      <Notice
                        className="phase9-wallet-test-disclosure"
                        tone="warning"
                        title={l("TEST · SIMULATED", "TEST · SIMULATED")}
                      >
                        {l("No live external verification", "실제 외부 확인 없음")}
                        {kyc.disclosure ? ` · ${kyc.disclosure}` : ""}
                      </Notice>
                    )}
                    <p className="account-ui-wallet-permission">
                      {l("Current access", "현재 권한")} · {ownershipValid
                        ? l("TEST low-value purchases", "TEST 저액 결제")
                        : l("Reauthentication required", "재인증 필요")}
                      {kyc.actionEligible ? l(" · advanced TEST eligibility", " · 고급 TEST 자격") : ""}
                    </p>
                    {ownershipFeedback?.walletId === wallet.id && (
                      <Notice
                        announce
                        tone={ownershipFeedback.tone}
                        title={
                          ownershipFeedback.tone === "danger"
                            ? l("Wallet registration failed", "지갑 등록을 완료하지 못했습니다")
                            : l("Wallet registered", "지갑 등록 완료")
                        }
                      >
                        {ownershipFeedback.message}
                      </Notice>
                    )}
                    {kycFeedback?.walletId === wallet.id && (
                      <Notice
                        announce
                        tone={kycFeedback.tone}
                        title={
                          kycFeedback.tone === "danger"
                            ? l("Identity verification failed", "신원 확인을 진행하지 못했습니다")
                            : l("Identity verification", "신원 확인 안내")
                        }
                      >
                        {kycFeedback.message}
                      </Notice>
                    )}
                    <Disclosure summary={l("Technical details", "기술 세부 정보")}>
                      <div className="account-ui-wallet-attesters">
                        <code>
                          {wallet.chainId}{l(" · wallet ", " · wallet ")}{wallet.id}
                          {" · "}{wallet.accountId}
                        </code>
                        <code>
                          {l("registration ", "registration ")}{wallet.registrationStatus}
                          {" · "}{l("ownership ", "ownership ")}{ownership.status}
                          {ownership.proofId ? ` · proof ${ownership.proofId}` : ""}
                        </code>
                        {kyc.providerKind && (
                          <code>
                            {l("KYC · ", "KYC · ")}{kyc.providerKind}
                            {" · "}{kyc.externalEffect ?? "UNKNOWN"}
                            {" · "}{kyc.eligibility}
                          </code>
                        )}
                        {(kyc.failureCode || kyc.nextAction) && (
                          <code>
                            {l("KYC result · failure ", "KYC result · failure ")}{kyc.failureCode ?? "NONE"}
                            {" · "}{l("retryable ", "retryable ")}{
                              kyc.retryable === undefined
                                ? l("UNKNOWN", "UNKNOWN")
                                : String(kyc.retryable)
                            }
                            {" · "}{l("next ", "next ")}{kyc.nextAction?.kind ?? "NONE"}
                          </code>
                        )}
                      </div>
                    </Disclosure>
                  </div>
                  <div className="order-ui-settings-row__actions">
                    {actions.canReauthenticate || !registered ? (
                      <Button
                        emphasis="primary"
                        size="compact"
                        busy={busy === `account:wallet:${wallet.id}`}
                        disabled={
                          busy !== null
                          || walletCapability.kind !== "INJECTED_READY"
                        }
                        onClick={() => void reauthenticateWallet(projection)}
                      >
                        {registered ? l("Reauthenticate wallet", "지갑 재인증") : l("Register again", "다시 등록")}
                      </Button>
                    ) : registered && ownershipValid ? (
                      <Button
                        emphasis="secondary"
                        size="compact"
                        disabled
                      >
                        {l("Ownership verified", "소유권 인증 유효")}
                      </Button>
                    ) : null}
                    {kyc.eligibility === "VALID" ? (
                      <Button
                        emphasis="secondary"
                        size="compact"
                        disabled
                      >
                        {kyc.actionEligible
                          ? (testKYC
                              ? l("TEST identity verification valid", "TEST 신원 확인 유효")
                              : l("Identity verification valid", "신원 확인 유효"))
                          : l("KYC record retained · no current access", "KYC 기록 보존 · 현재 권한 없음")}
                      </Button>
                    ) : kyc.nextAction?.kind === "CONTACT_SUPPORT" ? (
                      supportURL ? (
                        <ButtonLink
                          emphasis="secondary"
                          href={supportURL}
                          rel={supportURL.startsWith("https://")
                            ? "noreferrer"
                            : undefined}
                          size="compact"
                          target={supportURL.startsWith("https://")
                            ? "_blank"
                            : undefined}
                        >
                          {kyc.nextAction.label || l("Contact KYC support", "KYC 지원 문의")}
                        </ButtonLink>
                      ) : (
                        <Button emphasis="secondary" size="compact" disabled>
                          {kyc.nextAction.label || l("KYC support required", "KYC 지원 문의 필요")}
                        </Button>
                      )
                    ) : actions.canStartKYC ? (
                      <Button
                        emphasis="primary"
                        size="compact"
                        busy={busy === `kyc:${wallet.id}`}
                        disabled={busy !== null || !ownershipValid}
                        onClick={() => void beginKYC(projection)}
                      >
                        {l("Start identity verification", "신원 확인 시작")}
                      </Button>
                    ) : actions.canCheckKYC && kyc.activeCase ? (
                      <Button
                        emphasis="primary"
                        size="compact"
                        busy={busy === `kyc-case:${kyc.activeCase.id}`}
                        disabled={busy !== null}
                        onClick={() => void checkKYC(kyc.activeCase!.id, wallet.id)}
                      >
                        {l("Check identity-verification result", "신원 확인 결과")}
                      </Button>
                    ) : (
                      <Button
                        emphasis="secondary"
                        size="compact"
                        disabled
                      >
                        {l("Identity verification required", "신원 확인 필요")}
                      </Button>
                    )}
                    {actions.canDeregister && (
                      <Button emphasis="quiet" size="compact" disabled={busy === wallet.id} onClick={() => void deregister(projection)}>
                        {l("Deregister wallet", "지갑 등록 해제")}
                      </Button>
                    )}
                  </div>
                </li>
              );
            })}
          </ul>
        )}
        </section>
      )}

      {showAccountDetails && (
        <section className="order-ui-settings-section" aria-labelledby="account-shipping-heading">
        <header>
          <div>
            <h2 id="account-shipping-heading">{l("Shipping address", "배송지")}</h2>
            <p>{l(
              "Saved addresses are masked by default. You can reveal your own address when needed; operators can reveal it only after recording an order-processing reason.",
              "저장한 주소는 기본적으로 가려집니다. 본인은 필요할 때 원문을 확인할 수 있고, 운영자는 주문 처리 사유를 남긴 뒤에만 볼 수 있습니다.",
            )}</p>
          </div>
        </header>
        {account.shippingProfiles.length > 0 ? (
          <ul className="order-ui-settings-list">
            {account.shippingProfiles.map((profile) => {
              const revealed = revealedAddresses[profile.id];
              return (
                <li className="order-ui-shipping-profile" key={profile.id}>
                  <div className="order-ui-shipping-profile__copy">
                    <strong>{profile.label}{profile.isDefault ? l(" · default", " · 기본") : ""}</strong>
                    {revealed ? (
                      <address className="account-ui-shipping-address">
                        <span>{revealed.recipientName}</span>
                        <span>{addressLines(revealed)}</span>
                        {revealed.phone && <span>{revealed.phone}</span>}
                      </address>
                    ) : (
                      <span>{profile.maskedSummary}</span>
                    )}
                    <small>
                      {revealed
                        ? l(
                            "Shown only in this browser view · server response must not be cached",
                            "이 브라우저 화면에만 표시 · 서버 응답 캐시 금지",
                          )
                        : l(
                            "Encrypted at rest · masked by default",
                            "암호화 저장됨 · 기본 화면에서는 가림",
                          )}
                    </small>
                  </div>
                  <div className="order-ui-settings-row__actions">
                    <Button
                      size="compact"
                      disabled={busy !== null}
                      busy={busy === `shipping-reveal:${profile.id}`}
                      onClick={() => void toggleAddress(profile.id)}
                    >
                      {revealed ? l("Hide address", "주소 가리기") : l("Reveal address", "주소 보기")}
                    </Button>
                    <Button emphasis="quiet" size="compact" disabled={busy === "shipping"} onClick={() => void retireAddress(profile.id)}>
                      {l("Delete shipping address", "배송지 삭제")}
                    </Button>
                  </div>
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="vt-field__hint">
            {l(
              "Add the address that will receive your orders before paying.",
              "결제 전에 상품을 받을 배송지를 저장해 주세요.",
            )}
          </p>
        )}

        <Disclosure
          className="order-ui-shipping-editor"
          open={shippingEditorOpen}
          onOpenChange={setShippingEditorOpen}
          summary={shippingProfile
            ? l("Replace the default shipping address", "새 기본 배송지로 교체")
            : l("Enter shipping address", "배송지 입력")}
        >
          <form onSubmit={(event) => void saveAddress(event)} autoComplete="on" noValidate>
            {shippingError && <Notice announce className="is-wide" tone="danger" title={l("We couldn't save the shipping address", "배송지를 저장하지 못했습니다")}>{shippingError} {l("Review the fields and try again.", "입력값을 확인한 뒤 다시 시도해 주세요.")}</Notice>}
            <Field id="shipping-label" label={l("Address label", "배송지 이름")} required error={shippingFieldErrors.label}>
              <Input required maxLength={80} value={address.label} onChange={(event) => setAddress({ ...address, label: event.target.value })} />
            </Field>
            <Field id="shipping-recipient" label={l("Recipient", "수령인")} required error={shippingFieldErrors.recipientName}>
              <Input required autoComplete="name" value={address.recipientName} onChange={(event) => setAddress({ ...address, recipientName: event.target.value })} />
            </Field>
            <Field className="is-wide" id="shipping-line-1" label={l("Address line 1", "주소 1")} required error={shippingFieldErrors.addressLine1}>
              <Input required autoComplete="address-line1" value={address.addressLine1} onChange={(event) => setAddress({ ...address, addressLine1: event.target.value })} />
            </Field>
            <Field className="is-wide" id="shipping-line-2" label={l("Address line 2", "주소 2")} hint={l("Optional", "선택")} error={shippingFieldErrors.addressLine2}>
              <Input autoComplete="address-line2" value={address.addressLine2} onChange={(event) => setAddress({ ...address, addressLine2: event.target.value })} />
            </Field>
            <Field id="shipping-city" label={l("City", "도시")} required error={shippingFieldErrors.city}>
              <Input required autoComplete="address-level2" value={address.city} onChange={(event) => setAddress({ ...address, city: event.target.value })} />
            </Field>
            <Field id="shipping-region" label={l("State / province", "주/도")} required error={shippingFieldErrors.region}>
              <Input required autoComplete="address-level1" value={address.region} onChange={(event) => setAddress({ ...address, region: event.target.value })} />
            </Field>
            <Field id="shipping-postal-code" label={l("Postal code", "우편번호")} required error={shippingFieldErrors.postalCode}>
              <Input required autoComplete="postal-code" value={address.postalCode} onChange={(event) => setAddress({ ...address, postalCode: event.target.value })} />
            </Field>
            <Field id="shipping-country" label={l("Country code", "국가 코드")} hint={l("2-letter ISO code", "ISO 2자리")} required error={shippingFieldErrors.country}>
              <Input required minLength={2} maxLength={2} autoComplete="country" value={address.country} onChange={(event) => setAddress({ ...address, country: event.target.value.toUpperCase() })} />
            </Field>
            <Field className="is-wide" id="shipping-phone" label={l("Phone number", "전화번호")} hint={l("Optional", "선택")} error={shippingFieldErrors.phone}>
              <Input autoComplete="tel" value={address.phone} onChange={(event) => setAddress({ ...address, phone: event.target.value })} />
            </Field>
            <Button className="is-wide" emphasis="primary" type="submit" busy={busy === "shipping"}>
              {l("Save shipping address", "배송지 저장")}
            </Button>
          </form>
        </Disclosure>
        </section>
      )}

      {view === "all" && (
        <section className="order-ui-settings-section account-ui-order-summary" aria-labelledby="account-agency-orders-heading">
        <header>
          <div>
            <h2 id="account-agency-orders-heading">{l("Track AgencyOrders", "AgencyOrder 추적")}</h2>
            <p>{l("Follow order issuance, payment, merchant processing, and receipts.", "주문 발행부터 결제, 상점 주문 처리와 영수증까지 확인합니다.")}</p>
          </div>
        </header>
        {agencyOrdersError && (
          <Notice tone="warning" title={l("We couldn't load AgencyOrder tracking", "AgencyOrder 추적만 불러오지 못했습니다")}>
            {l("Wallet registration and account settings remain available. Check the AgencyOrder list again shortly.", "지갑 등록과 계정 설정은 계속 사용할 수 있습니다. 잠시 뒤 AgencyOrder 목록을 다시 확인해 주세요.")}
          </Notice>
        )}
        <div className="account-ui-order-summary__columns">
          <AgencyOrderList title={l("In progress", "진행 중")} items={activeAgencyOrders} empty={l("No AgencyOrders are currently being processed.", "현재 처리 중인 AgencyOrder가 없습니다.")} />
          <AgencyOrderList title={l("Recently completed", "최근 완료")} items={recentAgencyOrders} empty={l("No AgencyOrders have been completed yet.", "아직 완료된 AgencyOrder가 없습니다.")} />
        </div>
	      <Link className="order-ui-primary-link" to="/agencyOrder">{l("View all AgencyOrders", "전체 AgencyOrder 보기")}</Link>
        </section>
      )}

      {showLikedCandidates && (
        <section
          className={[
            "order-ui-settings-section",
            "phase10-liked-products",
            panelSectionOnly("liked") ? "is-panel-section" : "",
          ].filter(Boolean).join(" ")}
          aria-label={panelSectionOnly("liked") ? l("Liked products", "좋아요한 상품") : undefined}
          aria-labelledby={panelSectionOnly("liked") ? undefined : "account-liked-heading"}
        >
        {!panelSectionOnly("liked") && (
          <header>
            <div>
              <h2 id="account-liked-heading">{l("Liked products", "좋아요한 상품")}</h2>
              <p>{l("Review products you liked in your curations.", "큐레이션에서 좋아요를 누른 상품을 다시 확인합니다.")}</p>
            </div>
          </header>
        )}
        {likedCandidatesError && catalogLikedCandidates.length === 0 ? (
          <Notice tone="warning" title={l("We couldn't load liked products", "좋아요한 상품만 불러오지 못했습니다")}>
            {l("Wallet registration and account settings remain available.", "지갑 등록과 계정 설정은 계속 사용할 수 있습니다.")}
          </Notice>
        ) : catalogLikedCandidates.length === 0 ? (
          <FeedbackState
            state="empty"
            title={l("No liked products yet", "아직 좋아요한 상품이 없습니다")}
          />
        ) : (
          <div className="product-ui-liked-products__grid">
            {catalogLikedCandidates.map((candidate) => (
              <article key={`catalog:${candidate.key}`}>
                <ProductMedia alt={candidate.productTitle} />
                <div>
                  <span>{candidate.targetTitle}</span>
                  <h3>
                    {candidate.productUrl ? (
                      <a href={candidate.productUrl} target="_blank" rel="noreferrer">
                        {candidate.productTitle}
                      </a>
                    ) : candidate.productTitle}
                  </h3>
                  <p>{candidate.priceUnknown ? l("Price unavailable", "가격 정보 없음") : formatCatalogLikedPrice(candidate.priceMinor, candidate.currency, locale)}</p>
                  <small>
                    {[l("Liked", "좋아요"), candidate.variantTitle, new Date(candidate.updatedAt).toLocaleDateString(locale)].filter(Boolean).join(" · ")}
                  </small>
                  <Link to={candidate.curationPath}>{l("View in curation", "큐레이션에서 보기")}</Link>
                </div>
              </article>
            ))}
          </div>
        )}
        </section>
      )}

      {showPurchaseChecks && (
        <section
          className={[
            "order-ui-settings-section",
            "phase10-liked-products",
            "phase10-purchase-checks",
            panelSectionOnly("purchased") ? "is-panel-section" : "",
          ].filter(Boolean).join(" ")}
          aria-label={panelSectionOnly("purchased") ? l("Purchase-checked products", "구매 체크한 상품") : undefined}
          aria-labelledby={panelSectionOnly("purchased") ? undefined : "account-purchase-checks-heading"}
        >
        {!panelSectionOnly("purchased") && (
          <header>
            <div>
              <h2 id="account-purchase-checks-heading">{l("Purchase-checked products", "구매 체크한 상품")}</h2>
              <p>{l("Products you marked as purchased in your curations. These are your own records, not order confirmations.", "큐레이션에서 구매했다고 직접 표시한 상품입니다. 주문 확인 내역이 아닙니다.")}</p>
            </div>
          </header>
        )}
        {purchaseUndoError && (
          <Notice announce tone="warning">{purchaseUndoError}</Notice>
        )}
        {purchaseChecksError && purchaseChecks.length === 0 ? (
          <Notice tone="warning" title={l("We couldn't load purchase checks", "구매 체크한 상품만 불러오지 못했습니다")}>
            {l("Wallet registration and account settings remain available.", "지갑 등록과 계정 설정은 계속 사용할 수 있습니다.")}
          </Notice>
        ) : purchaseChecks.length === 0 ? (
          <FeedbackState
            state="empty"
            title={l("No purchase checks yet", "아직 구매 체크한 상품이 없습니다")}
            description={l("Mark an external product as purchased to collect it here.", "외부 상품의 구매 체크를 누르면 이 계정에 모아 보여줍니다.")}
          />
        ) : (
          <div className="product-ui-liked-products__grid">
            {purchaseChecks.map((item) => {
              const title = item.snapshot?.productTitle ?? `${sourceLabel(item.source, l)} ${item.subjectId}`;
              return (
                <article key={`purchase:${item.key}`}>
                  <ProductMedia alt={title} />
                  <div>
                    <span>{item.targetTitle || sourceLabel(item.source, l)}</span>
                    <h3>
                      {item.productUrl ? (
                        <a href={item.productUrl} target="_blank" rel="noreferrer">{title}</a>
                      ) : title}
                    </h3>
                    <p>
                      {item.snapshot && !item.snapshot.priceUnknown && item.snapshot.currency
                        ? formatCatalogLikedPrice(item.snapshot.priceMinor, item.snapshot.currency, locale)
                        : l("Price unavailable", "가격 정보 없음")}
                    </p>
                    <small>
                      {[l("Marked purchased", "구매 체크"), item.snapshot?.variantTitle, new Date(item.recordedAt).toLocaleDateString(locale)].filter(Boolean).join(" · ")}
                    </small>
                    <div className="product-ui-purchase-checks__actions">
                      <Link to={item.curationPath}>{l("View in curation", "큐레이션에서 보기")}</Link>
                      <Button
                        emphasis="tertiary"
                        busy={undoingPurchaseKey === item.key}
                        disabled={undoingPurchaseKey !== null}
                        onClick={() => void undoPurchase(item)}
                      >
                        {l("Undo", "체크 취소")}
                      </Button>
                    </div>
                  </div>
                </article>
              );
            })}
          </div>
        )}
        </section>
      )}

      {showManagement && (
        <section
          className={[
            "order-ui-settings-section",
            "account-ui-account-actions",
            embedded && view === "management" ? "is-compact" : "",
          ].filter(Boolean).join(" ")}
          aria-label={embedded && view === "management"
            ? l("Account actions", "계정 작업")
            : undefined}
          aria-labelledby={embedded && view === "management"
            ? undefined
            : "account-actions-heading"}
        >
          {!(embedded && view === "management") && (
            <header>
              <div>
                <h2 id="account-actions-heading">{l("Account management", "계정 관리")}</h2>
                <p>{l("End sessions or request account deletion.", "세션을 종료하거나 계정 삭제를 요청할 수 있습니다.")}</p>
              </div>
            </header>
          )}
          <ul className="account-ui-account-actions__list">
            <li className="account-ui-account-action">
              <p>{l(
                "End every session, including the one on this device.",
                "현재 기기를 포함한 모든 세션을 종료합니다.",
              )}</p>
              <Button emphasis="secondary" busy={busy === "logout-all"} onClick={() => void signOutEverywhere()}>
                {l("Sign out on all devices", "모든 기기에서 로그아웃")}
              </Button>
            </li>
            <li className="account-ui-account-action">
              <p>{l(
                "Sign-in is blocked as soon as the deletion request is submitted.",
                "삭제 요청과 동시에 로그인이 차단됩니다.",
              )}</p>
              <Button emphasis="danger" busy={busy === "delete-account"} onClick={() => void deleteAccount()}>
                {l("Request account deletion", "계정 삭제 요청")}
              </Button>
            </li>
          </ul>
        </section>
      )}

      {error && <Notice announce tone="danger">{error}</Notice>}
    </div>
  );
}

function formatCatalogLikedPrice(minor: number, currency: string, locale: string) {
  const zeroDecimal = currency === "KRW" || currency === "JPY";
  return new Intl.NumberFormat(locale, {
    style: "currency",
    currency,
    maximumFractionDigits: zeroDecimal ? 0 : 2,
  }).format(zeroDecimal ? minor : minor / 100);
}

function recoverableOperationKey(scope: string) {
  const key = `vitlane:operation:${scope}`;
  try {
    const current = sessionStorage.getItem(key);
    if (current) return current;
  } catch {
    // Storage can be unavailable in privacy-restricted browser contexts.
  }
  const suffix = typeof crypto.randomUUID === "function"
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  const value = `${scope}:${suffix}`;
  try {
    sessionStorage.setItem(key, value);
  } catch {
    // The in-flight call still uses one stable value for this render.
  }
  return value;
}

function clearRecoverableOperationKey(scope: string) {
  try {
    sessionStorage.removeItem(`vitlane:operation:${scope}`);
  } catch {
    // Nothing else is required after a recovered terminal response.
  }
}

async function runRecoverableKYCOperation<T>(
  scope: string,
  operation: (clientOperationId: string) => Promise<T>,
) {
  try {
    return await operation(recoverableOperationKey(scope));
  } catch (caught) {
    if (!(caught instanceof APIError)) throw caught;
    if (caught.code === "KYC_IDEMPOTENCY_KEY_REUSED") {
      clearRecoverableOperationKey(scope);
      return operation(recoverableOperationKey(scope));
    }
    if ([
      "KYC_VERIFICATION_NOT_FOUND",
      "KYC_VERIFICATION_STATE_INVALID",
      "WALLET_NOT_REGISTERED",
      "WALLET_OWNERSHIP_PROOF_NOT_FOUND",
      "WALLET_OWNERSHIP_PROOF_EXPIRED",
      "WALLET_OWNERSHIP_PROOF_NOT_FRESH",
      "KYC_CREDENTIAL_INVALID",
      "KYC_EVIDENCE_OBSERVATION_INVALID",
      "KYC_VERIFICATION_INVALID",
    ].includes(caught.code)) {
      clearRecoverableOperationKey(scope);
    }
    throw caught;
  }
}

function shortAddress(value: string) {
  return `${value.slice(0, 8)}…${value.slice(-6)}`;
}

function addressLines(address: RevealedShippingAddress) {
  return [
    address.addressLine1,
    address.addressLine2,
    address.city,
    address.region,
    address.postalCode,
    address.country,
  ].filter(Boolean).join(", ");
}

function kycSummaryCopy(kyc: WalletKYCProjection | undefined, l: Localize) {
  if (kyc?.eligibility === "VALID") {
    return kyc.externalEffect === "SIMULATED"
      ? l("TEST valid", "TEST 유효")
      : l("Valid", "유효");
  }
  if (kyc?.eligibility === "PENDING") return l("In progress", "진행 중");
  return l("Required", "필요");
}

function presentKYC(kyc: WalletKYCProjection, l: Localize, locale: string) {
  const action = kycActionCopy(kyc, l);
  const failure = kycFailureCopy(kyc, l);
  if (kyc.eligibility === "NONE") {
    return {
      tone: "required",
      icon: "!",
      title: l("KYC is required", "KYC가 필요합니다"),
      method: l("No KYC completed", "진행한 KYC 없음"),
      detail: [
        l(
          "Start identity verification to see the method and result here.",
          "신원 확인을 시작하면 확인 방법과 결과가 여기에 표시됩니다.",
        ),
        action,
      ].filter(Boolean).join(" · "),
    };
  }

  const method = kyc.providerKind === "DOJANG"
    ? l("Dojang wallet-address KYC", "Dojang 지갑 주소 KYC")
    : kyc.providerKind === "MOCK_DOJANG"
      ? l("MockDojang simulated KYC", "MockDojang 모의 KYC")
      : l("KYC provider verification", "KYC 제공자 확인");
  const effect = kyc.externalEffect === "LIVE"
    ? l("External verification result", "외부 확인 결과")
    : l("TEST · SIMULATED · no live external verification", "TEST · SIMULATED · 실제 외부 확인 없음");

  if (kyc.eligibility === "VALID") {
    return {
      tone: "verified",
      icon: "✓",
      title: kyc.externalEffect === "SIMULATED"
        ? l("TEST simulated KYC valid", "TEST 모의 KYC 유효")
        : l("KYC verification valid", "KYC 확인 유효"),
      method,
      detail: [
        effect,
        kyc.observation?.validUntil
          ? l("Valid until {date}", "{date}까지 유효", { date: formatDate(kyc.observation.validUntil, locale) })
          : kyc.credential?.validUntil
            ? l("Valid until {date}", "{date}까지 유효", { date: formatDate(kyc.credential.validUntil, locale) })
            : undefined,
        kyc.observation?.recheckAfter
          ? l("Recheck after {date}", "{date} 이후 재확인", { date: formatDate(kyc.observation.recheckAfter, locale) })
          : undefined,
        kyc.disclosure,
        action,
      ].filter(Boolean).join(" · "),
    };
  }

  if (kyc.eligibility === "PENDING") {
    return {
      tone: kyc.failureCode && kyc.retryable === false
        ? "required"
        : "pending",
      icon: kyc.failureCode ? "!" : "…",
      title: kyc.failureCode
        ? (kyc.retryable === false
            ? l("KYC support is required", "KYC 확인에 지원이 필요합니다")
            : l("Try KYC verification again", "KYC 확인을 다시 시도해 주세요"))
        : l("KYC verification in progress", "KYC 확인 진행 중"),
      method,
      detail: [
        effect,
        failure,
        action ?? l("Check the result.", "결과를 확인해 주세요."),
      ].filter(Boolean).join(" · "),
    };
  }

  const terminalCopy: Record<WalletKYCProjection["eligibility"], string> = {
    NONE: l("KYC verification required", "KYC 확인이 필요합니다"),
    PENDING: l("KYC verification in progress", "KYC 확인 진행 중"),
    VALID: l("KYC verification valid", "KYC 확인 유효"),
    REJECTED: l("KYC could not be verified", "KYC를 확인하지 못했습니다"),
    EXPIRED: l("KYC verification expired", "KYC 확인 기한이 지났습니다"),
    REVOKED: l("KYC verification revoked", "KYC 확인이 폐기되었습니다"),
    RECHECK_REQUIRED: l("KYC recheck required", "KYC 재확인이 필요합니다"),
  };
  return {
    tone: "required",
    icon: "!",
    title: terminalCopy[kyc.eligibility],
    method,
    detail: [
      effect,
      kyc.observation?.validUntil
        ? l("Valid until {date}", "{date}까지 유효", { date: formatDate(kyc.observation.validUntil, locale) })
        : undefined,
      kyc.observation?.recheckAfter
        ? l("Recheck after {date}", "{date} 이후 재확인", { date: formatDate(kyc.observation.recheckAfter, locale) })
        : undefined,
      failure,
      kyc.disclosure ?? l("A new verification is required.", "새 확인이 필요합니다."),
      action,
    ].filter(Boolean).join(" · "),
  };
}

function kycFailureCopy(kyc: WalletKYCProjection, l: Localize) {
  if (!kyc.failureCode) return undefined;
  switch (kyc.failureCode) {
    case "PROVIDER_UNAVAILABLE":
    case "KYC_PROVIDER_UNAVAILABLE":
      return l(
        "The KYC provider response is temporarily delayed.",
        "KYC 제공자 응답이 일시적으로 지연되었습니다.",
      );
    case "INVALID_PROVIDER_RESULT":
    case "MISSING_PROVIDER_EVIDENCE":
    case "INVALID_PROVIDER_EVIDENCE":
      return l(
        "We couldn't safely verify the KYC provider result.",
        "KYC 제공자 결과를 안전하게 확인하지 못했습니다.",
      );
    case "WALLET_DEREGISTERED":
      return l(
        "Deregistering the wallet stopped the KYC verification in progress.",
        "지갑 등록 해제로 진행 중인 KYC가 중단되었습니다.",
      );
    default:
      if (kyc.eligibility === "REJECTED") {
        return l(
          "The KYC provider rejected the verification.",
          "KYC 제공자의 확인 결과가 거절되었습니다.",
        );
      }
      if (kyc.eligibility === "EXPIRED") {
        return l(
          "The KYC provider result has expired.",
          "KYC 제공자 결과의 유효기간이 지났습니다.",
        );
      }
      return l(
        "The identity-verification processing reason was recorded.",
        "신원 확인 처리 사유가 기록되었습니다.",
      );
  }
}

function kycActionCopy(kyc: WalletKYCProjection, l: Localize) {
  switch (kyc.nextAction?.kind) {
    case "REGISTER":
      return l(
        "Register the wallet again, then continue identity verification.",
        "지갑을 다시 등록한 뒤 신원 확인을 진행하세요.",
      );
    case "REAUTHENTICATE":
      return l(
        "Reauthenticate the wallet, then try identity verification again.",
        "지갑을 재인증한 뒤 신원 확인을 다시 시도하세요.",
      );
    case "START_KYC":
      return kyc.failureCode
        ? l("You can restart identity verification.", "신원 확인을 다시 시작할 수 있습니다.")
        : l("Start identity verification.", "신원 확인을 시작하세요.");
    case "CHECK_RESULT":
      return l(
        "Check the identity-verification result again shortly.",
        "잠시 뒤 신원 확인 결과를 다시 조회하세요.",
      );
    case "RECHECK":
      return l(
        "Check the latest identity-verification result again.",
        "최신 신원 확인 결과를 다시 확인하세요.",
      );
    case "CONTACT_SUPPORT":
      return kyc.nextAction.label
        ? l(
            "{label} is required.",
            "{label}가 필요합니다.",
            { label: kyc.nextAction.label },
          )
        : l(
            "This state cannot recover automatically. Contact KYC support.",
            "이 상태는 자동 복구할 수 없습니다. KYC 지원에 문의해 주세요.",
          );
    default:
      return undefined;
  }
}

function safeKYCActionURL(value?: string) {
  const trimmed = value?.trim();
  if (!trimmed) return undefined;
  if (trimmed.startsWith("/") || trimmed.startsWith("https://")) {
    return trimmed;
  }
  return undefined;
}

function ownershipCopy(
  status: WalletProjection["ownership"]["status"],
  l: Localize,
) {
  switch (status) {
    case "VALID":
      return l("Ownership verified", "소유권 인증 유효");
    case "REVOKED":
      return l("Ownership verification revoked", "소유권 인증 폐기됨");
    default:
      return l("Reauthentication required", "재인증 필요");
  }
}

function formatDate(value: string, locale: string) {
  return new Intl.DateTimeFormat(locale, {
    year: "numeric",
    month: "short",
    day: "numeric",
  }).format(new Date(value));
}

function AgencyOrderList({
  empty,
  items,
  title,
}: {
  empty: string;
  items: AgencyOrderProjection[];
  title: string;
}) {
  const { l } = useLocale();
  return (
    <section>
      <h3>{title}</h3>
      {items.length === 0 ? (
        <p>{empty}</p>
      ) : (
        <ul>
          {items.map((item) => (
            <li key={item.agencyOrder.id}>
              <Link to={`/agencyOrder/${item.agencyOrder.id}`}>
                {agencyOrderTitle(item)}
              </Link>
              <span>{formatMoney(item.agencyOrder.customerPayableTotal)}</span>
              <small>{isTerminalAgencyOrder(item)
                ? l("Review result", "처리 결과 확인")
                : l("Processing", "처리 중")}</small>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function isTerminalAgencyOrder(item: AgencyOrderProjection) {
  return item.process.state === "TERMINAL";
}

function agencyOrderTitle(item: AgencyOrderProjection) {
  const titles = item.agencyOrder.lines.map((line) => line.productTitle);
  if (titles.length === 0) return `AgencyOrder ${item.agencyOrder.id.slice(0, 8)}`;
  if (titles.length === 1) return titles[0];
  return localizeFixedCopy(
    "{title} and {count} more",
    "{title} 외 {count}개",
    { title: titles[0], count: titles.length - 1 },
  );
}

function formatMoney(money: { amountMinor: number; currency: string }) {
  return new Intl.NumberFormat("ko-KR", {
    style: "currency",
    currency: money.currency,
  }).format(money.amountMinor / 100);
}

function messageOf(caught: unknown) {
  if (caught instanceof APIError) return caught.message;
  if (caught instanceof Error) return caught.message;
  return localizeFixedCopy(
    "We couldn't update the account.",
    "계정 정보를 갱신하지 못했습니다.",
  );
}

function kycMessageOf(caught: unknown) {
  if (caught instanceof APIError) {
    switch (caught.code) {
      case "WALLET_NOT_REGISTERED":
        return localizeFixedCopy(
          "Identity verification can start only for a wallet registered to this account.",
          "이 계정에 등록된 지갑에서만 신원 확인을 시작할 수 있습니다.",
        );
      case "WALLET_OWNERSHIP_PROOF_EXPIRED":
      case "WALLET_OWNERSHIP_PROOF_NOT_FRESH":
      case "WALLET_OWNERSHIP_PROOF_NOT_FOUND":
      case "WALLET_OWNERSHIP_PROOF_INVALID":
        return localizeFixedCopy(
          "A recent ownership signature is required for identity verification. Reauthenticate this wallet and try again.",
          "신원 확인에 사용할 최근 소유권 서명이 필요합니다. 이 지갑을 재인증한 뒤 다시 시도해 주세요.",
        );
      case "KYC_VERIFICATION_STATE_INVALID":
        return localizeFixedCopy(
          "This identity verification is complete or cannot be checked in its current state. Refresh the page for the latest status.",
          "이 신원 확인은 이미 완료되었거나 현재 상태에서 다시 확인할 수 없습니다. 페이지를 새로고침해 최신 상태를 확인해 주세요.",
        );
      case "KYC_CREDENTIAL_INVALID":
      case "KYC_EVIDENCE_OBSERVATION_INVALID":
        return localizeFixedCopy(
          "The KYC provider did not return a valid credential and current verification result. Check again shortly.",
          "KYC 제공자의 유효한 credential과 최신 확인 결과를 받지 못했습니다. 잠시 뒤 결과를 다시 확인해 주세요.",
        );
      case "KYC_DISABLED":
        return localizeFixedCopy(
          "Identity verification is unavailable in this environment.",
          "현재 이 환경에서는 신원 확인을 지원하지 않습니다.",
        );
      default:
        return caught.message;
    }
  }
  return messageOf(caught);
}

function walletProjectionMayBeStale(caught: unknown) {
  return caught instanceof APIError && [
    "WALLET_NOT_REGISTERED",
    "WALLET_OWNERSHIP_PROOF_EXPIRED",
    "WALLET_OWNERSHIP_PROOF_NOT_FRESH",
    "WALLET_OWNERSHIP_PROOF_NOT_FOUND",
    "WALLET_OWNERSHIP_PROOF_INVALID",
    "KYC_PROVIDER_UNAVAILABLE",
    "KYC_VERIFICATION_STATE_INVALID",
    "KYC_VERIFICATION_INVALID",
    "KYC_CREDENTIAL_INVALID",
    "KYC_EVIDENCE_OBSERVATION_INVALID",
  ].includes(caught.code);
}
