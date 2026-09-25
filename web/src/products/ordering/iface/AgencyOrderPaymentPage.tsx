import { useEffect, useRef, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { CheckCircle2, CircleAlert, WalletCards } from "lucide-react";
import type { Address, Hex } from "viem";
import { Button, Checkbox, Notice } from "../../../shared/ui";
import { APIError } from "../../../shared/api/client";
import {
  acceptTestSettlementPolicy,
  getAccountOverview,
  TEST_SETTLEMENT_POLICY_VERSION,
  type AccountOverview,
  type WalletProjection,
} from "../../account/infra/accountApi";
import { runWalletRegistrationFlow } from "../../account/app/walletRegistrationFlow";
import {
  authorizedTotalBaseUnits,
  getSettlementConfig,
  type AuthorizationRecord,
  type SettlementConfig,
  type SettlementPayment,
} from "../../payment/giwa/infra/settlementApi";
import {
  approveExact,
  pay,
  readSettlementAssetStatus,
  type SettlementAssetStatus,
} from "../../payment/giwa/infra/wallet";
import {
  openTestAssetsEvent,
  testAssetsUpdatedEvent,
} from "../../payment/giwa/iface/TestAssetPanel";
import {
  authorizeAgencyOrder,
  customerActionOf,
  getAgencyOrder,
  getAgencyOrderCapability,
  getAgencyOrderSettlement,
  getPayPalCheckout,
  resumePayPalCheckout,
  startPayPalCheckout,
  submitAgencyOrderPayTransaction,
  submitAgencyOrderWalletTransaction,
  type AgencyOrder,
  type AgencyOrderCapability,
  type AgencyOrderProjection,
  type PayPalCheckout,
  type PaymentInstruction,
} from "../infra/agencyOrderApi";
import { PaymentModeBadge } from "./PaymentModeBadge";
import { useLocale } from "../../../shared/i18n";
import "./agency-order.css";

export function AgencyOrderPaymentPage() {
  const { l } = useLocale();
  const { agencyOrderId = "" } = useParams();
  const [order, setOrder] = useState<AgencyOrder>();
  const [projection, setProjection] = useState<AgencyOrderProjection>();
  const [instruction, setInstruction] = useState<PaymentInstruction>();
  const [config, setConfig] = useState<SettlementConfig>();
  const [overview, setOverview] = useState<AccountOverview>();
  const [account, setAccount] = useState<Address>();
  const [wallet, setWallet] = useState<WalletProjection>();
  const [proofID, setProofID] = useState<string>();
  const [authorization, setAuthorization] = useState<AuthorizationRecord>();
  const [payment, setPayment] = useState<SettlementPayment>();
  const [assets, setAssets] = useState<SettlementAssetStatus>();
  const [assetsLoading, setAssetsLoading] = useState(false);
  const [assetError, setAssetError] = useState<string>();
  const [assetRefreshVersion, setAssetRefreshVersion] = useState(0);
  const [approveHash, setApproveHash] = useState<Hex>();
  const [ackTest, setAckTest] = useState(false);
  const [ackNoSale, setAckNoSale] = useState(false);
  const [policyAccepted, setPolicyAccepted] = useState(false);
  const [working, setWorking] = useState<string>();
  const [error, setError] = useState<string>();
  const [instructionPending, setInstructionPending] = useState(false);
  const promptedFaucetKey = useRef<string | undefined>(undefined);

  useEffect(() => {
    let active = true;
    void Promise.all([
      getAgencyOrder(agencyOrderId),
      getSettlementConfig(),
      getAccountOverview(),
    ]).then(([orderResult, configResult, accountResult]) => {
      if (!active) return;
	      setOrder(orderResult.agencyOrder.agencyOrder);
	      setProjection(orderResult.agencyOrder);
	      setInstruction(orderResult.agencyOrder.paymentInstruction);
      setConfig(configResult.settlement);
      setOverview(accountResult.account);
      const accepted = accountResult.account.policyAcceptances.some(
        (item) => item.policyId === "PHASE5_TEST_SETTLEMENT"
          && item.policyVersion === TEST_SETTLEMENT_POLICY_VERSION,
      );
      setPolicyAccepted(accepted);
      setAckTest(accepted);
      setAckNoSale(accepted);
      void getAgencyOrderSettlement(agencyOrderId)
        .then((result) => {
          if (!active) return;
          setPayment(result.payment);
          setAuthorization(result.authorization);
        })
        .catch(() => undefined);
    }).catch(() => {
      if (active) setError(l("We couldn't load the issued order or payment configuration.", "발행된 주문 또는 결제 설정을 불러오지 못했습니다."));
    });
    return () => { active = false; };
  }, [agencyOrderId, l]);

  useEffect(() => {
    if (!payment || terminalPayment(payment)) return;
    const timer = window.setInterval(() => {
      void getAgencyOrderSettlement(agencyOrderId)
        .then((result) => {
          setPayment(result.payment);
          setAuthorization(result.authorization);
        })
        .catch(() => undefined);
    }, 3000);
    return () => window.clearInterval(timer);
  }, [agencyOrderId, payment]);

  const assetChainID = authorization?.domain.chainId ?? config?.chainId;
  const assetTokenAddress = authorization?.authorization.token ?? config?.tokenAddress;
  const assetSettlementAddress = authorization?.domain.verifyingContract ?? config?.settlementAddress;
  const requiredAssetAmount = authorization
    ? authorizedTotalBaseUnits(authorization.authorization)
    : instruction && config
      ? moneyToTokenBaseUnits(
          instruction.customerPayableTotal.amountMinor,
          instruction.customerPayableTotal.currency,
          config.tokenDecimals,
        )
      : 0n;

  useEffect(() => {
    let active = true;

    async function refreshAssets(pollUntilFunded = false) {
      if (
        !config || !account || !assetChainID || !assetTokenAddress
        || !assetSettlementAddress || requiredAssetAmount <= 0n
      ) return;
      setAssetsLoading(true);
      setAssetError(undefined);
      try {
        let nextAssets = await readSettlementAssetStatus(config, account, {
          chainId: assetChainID,
          tokenAddress: assetTokenAddress,
          settlementAddress: assetSettlementAddress,
        });
        if (!active) return;
        setAssets(nextAssets);
        // Faucet 수령 직후에는 공개 RPC 복제 지연으로 첫 재조회가 이전 잔고를
        // 돌려줄 수 있다 — 충족될 때까지 유한 재조회로 버튼 게이트를 갱신한다.
        if (pollUntilFunded) {
          for (
            let attempt = 0;
            attempt < 10 && active && nextAssets.tokenBalance < requiredAssetAmount;
            attempt += 1
          ) {
            await new Promise((resolve) => setTimeout(resolve, 1500));
            if (!active) return;
            nextAssets = await readSettlementAssetStatus(config, account, {
              chainId: assetChainID,
              tokenAddress: assetTokenAddress,
              settlementAddress: assetSettlementAddress,
            });
            if (!active) return;
            setAssets(nextAssets);
          }
        }
        const faucetKey = `${agencyOrderId}:${account.toLowerCase()}:${requiredAssetAmount}`;
        if (
          nextAssets.tokenBalance < requiredAssetAmount
          && ["LOCAL", "GIWA_TESTNET"].includes(config.environment)
          && promptedFaucetKey.current !== faucetKey
        ) {
          promptedFaucetKey.current = faucetKey;
          window.dispatchEvent(new CustomEvent(openTestAssetsEvent, {
            detail: { address: account },
          }));
        }
      } catch {
        if (!active) return;
        setAssetError(l("Check the TEST chain connection and try again.", "TEST 체인 연결을 확인한 뒤 다시 시도해 주세요."));
      } finally {
        if (active) setAssetsLoading(false);
      }
    }

    void refreshAssets();
    function handleTestAssetsUpdated() {
      void refreshAssets(true);
    }
    window.addEventListener(testAssetsUpdatedEvent, handleTestAssetsUpdated);
    return () => {
      active = false;
      window.removeEventListener(testAssetsUpdatedEvent, handleTestAssetsUpdated);
    };
  }, [
    account,
    agencyOrderId,
    assetChainID,
    assetRefreshVersion,
    assetSettlementAddress,
    assetTokenAddress,
    config,
    l,
    requiredAssetAmount,
  ]);

  async function run(name: string, action: () => Promise<void>) {
    setWorking(name);
    setError(undefined);
    try {
      await action();
    } catch (caught) {
      setError(caught instanceof APIError && caught.code === "SETTLEMENT_WALLET_OWNERSHIP_REQUIRED"
        ? l("Your wallet verification has changed or expired. Reload this page and authenticate the payment wallet again.", "지갑 인증이 변경되었거나 만료됐습니다. 이 페이지를 새로고침한 뒤 결제 지갑을 다시 인증해 주세요.")
        : caught instanceof Error ? caught.message : l("We couldn't continue the payment.", "결제를 진행하지 못했습니다."));
    } finally {
      setWorking(undefined);
    }
  }

  async function verifyWallet() {
    if (!config || !instruction) return;
    await run("wallet", async () => {
      const result = await runWalletRegistrationFlow(config, {
        scope: `agency-order:${agencyOrderId}`,
        wallets: overview?.wallets,
        requiredValidUntil: new Date(instruction.expiresAt).getTime(),
      });
      const connected = result.account as Address;
      if (
        authorization
        && authorization.authorization.payer.toLowerCase() !== connected.toLowerCase()
      ) {
        throw new Error(l("Authenticate again with the wallet linked to this order's existing payment terms.", "이 주문의 기존 결제 조건에 연결된 지갑으로 다시 인증해 주세요."));
      }
      setAccount(connected);
      setWallet(result.wallet);
      setProofID(result.ownershipProof.id);
    });
  }

  async function sealAuthorization() {
    if (!wallet || !proofID || !config || !account) return;
    await run("authorization", async () => {
      if (!policyAccepted) {
        await acceptTestSettlementPolicy();
        setPolicyAccepted(true);
      }
      const record = await authorizeAgencyOrder(
        agencyOrderId, wallet.wallet.id, proofID,
      );
      if ("outcome" in record) {
        setInstructionPending(true);
        return;
      }
      if (record.authorization.payer.toLowerCase() !== account.toLowerCase()) {
        throw new Error(l("The payment wallet fixed to this order differs from the current wallet. Reconnect the original wallet.", "이 주문에 고정된 결제 지갑과 현재 지갑이 다릅니다. 기존 지갑으로 다시 연결해 주세요."));
      }
      setInstructionPending(false);
      setAuthorization(record);
      const result = await getAgencyOrderSettlement(agencyOrderId);
      setPayment(result.payment);
    });
  }

  async function approveToken() {
    if (!config || !account || !authorization) return;
    await run("approve", async () => {
      const amount = authorizedTotalBaseUnits(authorization.authorization);
      if (!assets || assets.tokenBalance < amount) {
        throw new Error(l("Your tVITUSD balance is insufficient. Claim a balance from the TEST asset tool, then check again.", "tVITUSD 잔액이 부족합니다. TEST 자산 도구에서 잔액을 받은 뒤 다시 확인해 주세요."));
      }
      const hash = await approveExact(
        config, account, amount, authorization.domain.chainId,
        authorization.authorization.token, authorization.domain.verifyingContract,
      );
      await submitAgencyOrderWalletTransaction(agencyOrderId, "APPROVE", hash);
      setApproveHash(hash);
      // 승인 tx는 확정됐지만 공개 RPC 복제가 allowance를 늦게 보일 수 있다 —
      // 결제 버튼 게이트가 열릴 때까지 유한 재조회한다.
      let refreshed = await readSettlementAssetStatus(config, account, {
        chainId: authorization.domain.chainId,
        tokenAddress: authorization.authorization.token,
        settlementAddress: authorization.domain.verifyingContract,
      });
      setAssets(refreshed);
      for (let attempt = 0; attempt < 10 && refreshed.allowance < amount; attempt += 1) {
        await new Promise((resolve) => setTimeout(resolve, 1500));
        refreshed = await readSettlementAssetStatus(config, account, {
          chainId: authorization.domain.chainId,
          tokenAddress: authorization.authorization.token,
          settlementAddress: authorization.domain.verifyingContract,
        });
        setAssets(refreshed);
      }
    });
  }

  async function submitPayment() {
    if (!config || !account || !authorization) return;
    await run("pay", async () => {
      if (authorization.authorization.payer.toLowerCase() !== account.toLowerCase()) {
        throw new Error(l("Connect the payment wallet fixed in the authorization.", "승인에 고정된 결제 지갑을 연결해 주세요."));
      }
      const hash = await pay(config, account, authorization);
      await submitAgencyOrderPayTransaction(agencyOrderId, hash);
      const result = await getAgencyOrderSettlement(agencyOrderId);
      setPayment(result.payment);
    });
  }

  const amount = authorization
    ? authorizedTotalBaseUnits(authorization.authorization)
    : requiredAssetAmount;
  const allowanceReady = Boolean(authorization && assets && assets.allowance >= amount);
  const balanceReady = Boolean(assets && amount > 0n && assets.tokenBalance >= amount);
  const paid = payment && ["PAYMENT_SUBMITTED", "SAFE", "FINALIZED"].includes(payment.state);
  // PAY는 서버가 계산한 명령 공간에서 소비한다(ADR-0055 §5) — rail 선택 축도
  // action이 결정하고, action이 없으면 결제 진입을 열지 않는다(자문 공간 —
  // 권위 검증은 결제 endpoint가 재수행한다).
  const payAction = projection ? customerActionOf(projection, "PAY") : undefined;
  const payClosed = Boolean(projection) && !payAction && !paid;

  if (order && (payAction?.rail ?? order.paymentSelection?.rail) === "PAYPAL") {
    return <PayPalPaymentPage agencyOrderId={agencyOrderId} order={order} instruction={instruction} />;
  }

  return <main className="agency-order-page agency-payment-page">
    <header className="agency-order-hero"><span>{l("tVITUSD payment", "tVITUSD 결제")}</span> <PaymentModeBadge selection={instruction?.paymentSelection} /><h1>{l("Your order was issued securely.", "주문서가 안전하게 발행되었습니다.")}</h1><p>{l("Only the amount matching the immutable Order hash is authorized on the GIWA TEST payment rail.", "immutable Order hash와 같은 금액만 기존 GIWA TEST 결제 레일에 승인합니다.")}</p></header>
    {error ? <section className="agency-order-card agency-payment-error" role="alert"><CircleAlert aria-hidden="true" /><p>{error}</p></section> : null}
    {order && instruction && config ? <section className="agency-order-card agency-payment-card">
      <CheckCircle2 aria-hidden="true" /><div><span>{l("AgencyOrder · issued", "AgencyOrder · 발행됨")}</span><h2>{formatMoney(order.customerPayableTotal)}</h2><p>{l("Order", "Order")} {order.id}</p><code>{order.snapshotHash}</code></div>
      <div className="agency-payment-next"><WalletCards aria-hidden="true" /><div><strong>{l("tVITUSD payment instruction", "tVITUSD 결제 지시서")}</strong><span>{instruction.state}{l(" · 1% fee · ", " · 1% fee · ")}{instruction.paymentPolicyVersion}</span><small>{l("A failed or delayed payment does not change the order snapshot.", "결제 실패나 지연이 주문 snapshot을 바꾸지는 않습니다.")}</small></div></div>

      {payClosed ? <div className="agency-payment-status agency-payment-status--blocked" role="status"><strong>{l("Payment is not available at this stage.", "지금은 결제 단계가 아닙니다.")}</strong><small>{l("Funds have already been confirmed or the payment instruction expired. Check the order details for its current status.", "수납이 이미 확정되었거나 결제 지시가 만료되었습니다. 주문 상세에서 진행 상태를 확인해 주세요.")}</small></div> : null}
      {!account && !payClosed ? <Button emphasis="primary" type="button" onClick={() => void verifyWallet()} busy={working === "wallet"} disabled={Boolean(working)}>{l("Authenticate payment wallet", "결제 지갑 인증")}</Button> : null}
      {account && !authorization ? <div className="agency-payment-consent">
        {assetsLoading && !assets ? <p className="agency-payment-preflight" role="status">{l("Checking the payment wallet's tVITUSD balance…", "결제 지갑의 tVITUSD 잔액을 확인하고 있습니다…")}</p> : null}
        {assetError ? <div className="agency-payment-preflight agency-payment-preflight--error" role="alert"><span>{l("We couldn't check the payment wallet balance. {reason}", "결제 지갑 잔액을 확인하지 못했습니다. {reason}", { reason: assetError })}</span><Button emphasis="secondary" size="compact" type="button" onClick={() => setAssetRefreshVersion((current) => current + 1)}>{l("Check again", "다시 확인")}</Button></div> : null}
        {assets && !balanceReady ? <div className="agency-payment-preflight" role="status"><span>{l("There is not enough tVITUSD for this order. Claim TEST assets from the faucet, or close this page and continue later.", "이 주문을 결제할 tVITUSD 잔액이 부족합니다. Faucet에서 TEST 자산을 받거나 닫고 나중에 계속할 수 있습니다.")}</span><Button emphasis="secondary" size="compact" type="button" onClick={() => window.dispatchEvent(new CustomEvent(openTestAssetsEvent, { detail: { address: account } }))}>{l("Open tVITUSD Faucet", "tVITUSD Faucet 열기")}</Button></div> : null}
        <div><Checkbox id="agency-test-asset" checked={ackTest} onCheckedChange={(value) => setAckTest(value === true)} /><label htmlFor="agency-test-asset">{l("I understand that tVITUSD is a TEST asset with no real value.", "tVITUSD는 실제 가치가 없는 TEST 자산임을 확인했습니다.")}</label></div>
        <div><Checkbox id="agency-no-sale" checked={ackNoSale} onCheckedChange={(value) => setAckNoSale(value === true)} /><label htmlFor="agency-no-sale">{l("I understand that this payment does not mean the merchant order is complete or constitute a legal sale.", "이 결제가 실제 판매처 주문 완료나 법적 판매를 의미하지 않음을 확인했습니다.")}</label></div>
        <Button emphasis="primary" type="button" onClick={() => void sealAuthorization()} busy={working === "authorization"} disabled={!ackTest || !ackNoSale || !balanceReady || assetsLoading || Boolean(working)}>{instructionPending ? l("Check order confirmation", "주문 확인 상태 조회") : l("Confirm payment terms for order total", "주문 금액으로 결제 조건 확정")}</Button>
      </div> : null}
      {instructionPending ? <div className="agency-payment-status" role="status"><strong>{l("Order confirmation is pending.", "주문 확인이 대기 중입니다.")}</strong><small>{l("Your payment approval is saved. Check again shortly using the same approval. No payment has been submitted.", "결제 승인이 저장되었습니다. 잠시 후 같은 승인으로 다시 확인해 주세요. 결제는 제출되지 않았습니다.")}</small></div> : null}
      {account && authorization && !paid ? <div className="agency-payment-status" role="status"><strong>{l("Payment terms for the order total are confirmed.", "주문 금액의 결제 조건을 확정했습니다.")}</strong><span>{authorization.authorization.payer}</span><small>{l("Now prepare the tVITUSD balance and allowance. The guidance below is the next payment step, not a failed confirmation.", "이제 tVITUSD 잔액과 사용 한도를 준비합니다. 아래 안내는 조건 확정 실패가 아니라 다음 결제 단계입니다.")}</small></div> : null}
      {account && authorization && !paid && assetsLoading && !assets ? <div className="agency-payment-action" role="status"><p>{l("Checking the payment wallet's tVITUSD balance and allowance…", "결제 지갑의 tVITUSD 잔액과 사용 한도를 확인하고 있습니다…")}</p></div> : null}
      {account && authorization && !paid && assetError ? <div className="agency-payment-action" role="alert"><p>{l("We couldn't check the payment wallet balance. {reason}", "결제 지갑 잔액을 확인하지 못했습니다. {reason}", { reason: assetError })}</p><Button emphasis="secondary" type="button" onClick={() => setAssetRefreshVersion((current) => current + 1)}>{l("Check again", "다시 확인")}</Button></div> : null}
      {account && authorization && !paid && assets && !allowanceReady ? <div className="agency-payment-action"><p>{balanceReady ? l("In your wallet, approve a tVITUSD allowance that lets Settlement use only this order amount.", "지갑에서 Settlement가 이 주문 금액만 사용하도록 tVITUSD 사용 한도를 승인해 주세요.") : l("Payment terms are confirmed, but the tVITUSD balance is insufficient. Claim TEST assets from the faucet, then approve the allowance.", "결제 조건 확정은 완료됐지만 tVITUSD 잔액이 부족합니다. Faucet에서 TEST 자산을 받은 뒤 사용 한도를 승인해 주세요.")}</p>{!balanceReady ? <Button emphasis="secondary" type="button" disabled={Boolean(working)} onClick={() => window.dispatchEvent(new CustomEvent(openTestAssetsEvent, { detail: { address: account } }))}>{l("Open tVITUSD Faucet", "tVITUSD Faucet 열기")}</Button> : null}<Button emphasis="primary" type="button" onClick={() => void approveToken()} busy={working === "approve"} disabled={!balanceReady || Boolean(working)}>{l("Approve exact tVITUSD amount in wallet", "지갑에서 정확한 tVITUSD 사용 승인")}</Button></div> : null}
      {authorization && !paid && allowanceReady ? <div className="agency-payment-action"><p>{approveHash ? l("The amount approval is confirmed.", "금액 승인이 확인되었습니다.") : l("The allowance is already sufficient.", "이미 충분한 allowance가 있습니다.")} {l("Now pay this AgencyOrder.", "이제 이 AgencyOrder를 결제합니다.")}</p><Button emphasis="primary" type="button" onClick={() => void submitPayment()} busy={working === "pay"} disabled={Boolean(working)}>{l("Pay with tVITUSD", "tVITUSD로 결제")}</Button></div> : null}
      {paid ? <div className="agency-payment-status" role="status"><strong>{payment.state === "FINALIZED" ? l("Payment reached finality.", "결제가 finality에 도달했습니다.") : l("Payment submitted.", "결제를 제출했습니다.")}</strong><span>{payment.payTxHash || l("Checking chain", "chain 확인 중")}</span><small>{payment.state === "FINALIZED" ? l("Purchase processing continues in the next step.", "이후 구매 처리는 다음 Step의 범위입니다.") : l("The server continues checking SAFE and FINALIZED status even if you close this page.", "페이지를 닫아도 서버가 SAFE와 FINALIZED 상태를 계속 확인합니다.")}</small></div> : null}
      {payment && ["FAILED", "SUBMISSION_UNKNOWN"].includes(payment.state) ? <div className="agency-payment-status agency-payment-status--blocked" role="alert"><strong>{l("Do not attempt another payment.", "추가 결제를 시도하지 마세요.")}</strong><span>{payment.lastReasonCode || payment.state}</span><small>{l("The server could not confirm the chain result. Support review is required to prevent a duplicate payment.", "서버가 chain 결과를 확정하지 못했습니다. 중복 결제를 막기 위해 지원 확인이 필요합니다.")}</small></div> : null}
	      <p className="agency-payment-boundary">{l("The AgencyOrder and PaymentInstruction were issued atomically, and payment authorization consumes only their fixed hash and amount. No merchant order-confirmation call is made.", "AgencyOrder와 PaymentInstruction은 원자적으로 발행되었고, 결제 승인은 그 고정 hash·금액만 소비합니다. 판매처 주문 확정 호출은 하지 않습니다.")}</p>
	      <Link to={`/agencyOrder/${agencyOrderId}`}>{l("View AgencyOrder status", "AgencyOrder 처리 현황 보기")}</Link>
    </section> : <section className="agency-order-card">{l("Loading payment instructions…", "결제 지시서를 불러오고 있습니다…")}</section>}
  </main>;
}

// PayPalPaymentPage derives every money warning from this order's immutable
// profile. Redirect is only a wake-up; server GET reconciliation is authority.
function PayPalPaymentPage({ agencyOrderId, order, instruction }: {
  agencyOrderId: string;
  order: AgencyOrder;
  instruction?: PaymentInstruction;
}) {
  const { l } = useLocale();
  const [searchParams, setSearchParams] = useSearchParams();
  const providerEnvironment = instruction?.paymentSelection.providerEnvironment ??
    order.paymentSelection.providerEnvironment;
  const livePayment = providerEnvironment === "LIVE";
  const paymentMethod = livePayment ? "PAYPAL_LIVE" : "PAYPAL_SANDBOX";
  const [checkout, setCheckout] = useState<PayPalCheckout>();
  const [paypalCapability, setPaypalCapability] = useState<
    AgencyOrderCapability["paymentRails"]["paypalLive"] | null
  >();
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<string>();
  const resumedRef = useRef(false);

  const paypalReturn = searchParams.get("paypal");
  const returnNonce = searchParams.get("nonce") ?? "";

  useEffect(() => {
    let active = true;
    void getAgencyOrderCapability()
      .then((result) => {
        if (active) setPaypalCapability(livePayment
          ? result.capability.paymentRails.paypalLive
          : result.capability.paymentRails.paypalSandbox);
      })
      .catch(() => {
        if (active) setPaypalCapability(null);
      });
    return () => { active = false; };
  }, [livePayment]);

  useEffect(() => {
    let active = true;
    async function bootstrap() {
      try {
        if ((paypalReturn === "return" || paypalReturn === "cancel") && returnNonce && !resumedRef.current) {
          resumedRef.current = true;
          const resumed = await resumePayPalCheckout(agencyOrderId, returnNonce, paypalReturn === "cancel");
          if (!active) return;
          setCheckout(resumed);
          // redirect 파라미터는 1회성 wake-up이다 — URL을 정리해 재실행을 막는다.
          setSearchParams({}, { replace: true });
          return;
        }
        const current = await getPayPalCheckout(agencyOrderId);
        if (active) setCheckout(current);
      } catch {
        // 진행 중 결제가 없거나 만료 — 시작 버튼 상태로 남긴다.
      }
    }
    void bootstrap();
    return () => { active = false; };
  }, [agencyOrderId, paypalReturn, returnNonce, setSearchParams]);

  const paymentState = checkout?.payment.state;
  const paymentInitiationReady = paypalCapability?.orderIssueState === "READY" &&
    paypalCapability.paymentInitiationState === "READY" &&
    paypalCapability.providerEnvironment === providerEnvironment &&
    paypalCapability.paymentMethod === paymentMethod;
  const capabilityChecked = paypalCapability !== undefined;
  useEffect(() => {
    if (!paymentState || ![
      "PROCESSING",
      "OUTCOME_UNKNOWN",
    ].includes(paymentState)) return;
    const timer = window.setInterval(() => {
      void getPayPalCheckout(agencyOrderId).then(setCheckout).catch(() => undefined);
    }, 3000);
    return () => window.clearInterval(timer);
  }, [agencyOrderId, paymentState]);

  async function start() {
    if (!paymentInitiationReady) return;
    setWorking(true);
    setError(undefined);
    try {
      const started = await startPayPalCheckout(agencyOrderId);
      setCheckout(started);
      if (started.approvalUrl && started.payment.state === "ACTION_REQUIRED") {
        window.location.assign(started.approvalUrl);
      }
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : l("We couldn't start the PayPal payment.", "PayPal 결제를 시작하지 못했습니다."));
    } finally {
      setWorking(false);
    }
  }

	const authorized = paymentState !== undefined && [
		"AUTHORIZED", "PARTIALLY_CAPTURED", "CAPTURED", "CLOSED",
	].includes(paymentState);
  const outcomeUnknown = paymentState === "OUTCOME_UNKNOWN";
  const confirming = paymentState === "PROCESSING" || (paypalReturn === "return" && !checkout);

  return <main className="agency-order-page agency-payment-page">
    <header className="agency-order-hero"><span>{livePayment ? l("PayPal Live payment", "PayPal Live 결제") : l("PayPal Sandbox payment", "PayPal Sandbox 결제")}</span> <PaymentModeBadge selection={instruction?.paymentSelection ?? order.paymentSelection} /><h1>{l("Your order was issued securely.", "주문서가 안전하게 발행되었습니다.")}</h1><p>{livePayment ? l("PayPal approval authorizes the displayed USD total. Vitlane captures only a Shop's allocation when that Merchant Order enters Procurement.", "PayPal 승인은 표시된 USD 총액을 승인합니다. Vitlane은 각 Merchant Order가 조달에 들어갈 때 해당 Shop 배분액만 수납합니다.") : l("This uses PayPal Sandbox authorization and Shop-by-Shop Sandbox captures. No real money moves (realMoney=false).", "PayPal Sandbox 승인과 Shop별 Sandbox 수납을 사용합니다. 실제 금액은 이동하지 않습니다(realMoney=false).")}</p></header>
    {error ? <section className="agency-order-card agency-payment-error" role="alert"><CircleAlert aria-hidden="true" /><p>{error}</p></section> : null}
    {livePayment ? <Notice tone="danger" title={l("LIVE payment · real money", "LIVE 결제 · 실제 금액")}>{l("Check the order total and PayPal account before continuing. This badge and warning belong to this order and do not affect tVITUSD TEST orders.", "계속하기 전에 주문 총액과 PayPal 계정을 확인하세요. 이 배지와 경고는 이 주문에만 적용되며 tVITUSD TEST 주문에는 영향을 주지 않습니다.")}</Notice> : null}
    <section className="agency-order-card agency-payment-card">
      <CheckCircle2 aria-hidden="true" /><div><span>{l("AgencyOrder · issued", "AgencyOrder · 발행됨")}</span><h2>{formatMoney(order.customerPayableTotal)}</h2><p>{l("Order", "Order")} {order.id}</p><code>{order.snapshotHash}</code></div>
      <div className="agency-payment-next"><WalletCards aria-hidden="true" /><div><strong>{l("PayPal payment instruction", "PayPal 결제 지시서")}</strong><span>{instruction?.state ?? "PENDING"}{l(" · Vitlane fee: 5.4% + $0.30 per Merchant Order · ", " · Vitlane 수수료: Merchant Order마다 5.4% + $0.30 · ")}{instruction?.paymentPolicyVersion ?? (livePayment ? "PAYPAL_LIVE" : "PAYPAL_SANDBOX")}</span><small>{livePayment ? l("LIVE payment · real-money authorization. PayPal approval authorizes the full order total; each Shop allocation is captured when its Procurement starts.", "LIVE 결제 · 실제 금액 승인입니다. PayPal에서는 주문 총액 전체를 승인하고 각 Shop 배분액은 해당 조달 시작 시 수납합니다.") : l("This is a SANDBOX authorization. PayPal approval covers the full order total; each Shop allocation is captured when its Procurement starts. There is no real charge.", "SANDBOX 승인입니다. PayPal에서는 주문 총액 전체를 승인하고 각 Shop 배분액은 해당 조달 시작 시 수납합니다. 실제 청구는 없습니다.")}</small></div></div>

      {!capabilityChecked ? <div className="agency-payment-status" role="status"><strong>{l("Checking PayPal payment availability.", "PayPal 결제 가능 여부를 확인하고 있습니다.")}</strong></div> : null}
		{capabilityChecked && !paymentInitiationReady && !authorized ? <div className="agency-payment-status agency-payment-status--blocked" role="status"><strong>{l("PayPal payment initiation is paused.", "PayPal 결제 시작이 일시 중지되었습니다.")}</strong><small>{l("This AgencyOrder remains issued for review, but Vitlane will not create or expose a PayPal approval until this order's exact environment is ready. Existing provider operations continue to be reconciled safely.", "이 AgencyOrder는 검토할 수 있도록 발행 상태를 유지하지만, 이 주문의 정확한 환경이 준비될 때까지 PayPal 승인 생성·링크를 제공하지 않습니다. 이미 시작된 provider 작업은 계속 안전하게 대사합니다.")}</small></div> : null}
      {paymentInitiationReady && (!checkout || ["FAILED", "ABANDONED", "EXPIRED"].includes(checkout.payment.state)) ? (
        <div className="agency-payment-action">
			{checkout?.payment.state === "FAILED" ? <p role="alert">{l("The previous authorization attempt failed ({reason}). You can try again.", "이전 승인 시도가 실패했습니다({reason}). 다시 시도할 수 있습니다.", { reason: checkout.payment.lastReasonCode ?? "PROVIDER_DECLINED" })}</p> : <p>{l("Continue to PayPal to authorize the displayed order total. No Merchant Order is captured on this step.", "PayPal로 이동해 표시된 주문 총액을 승인합니다. 이 단계에서는 Merchant Order 금액을 수납하지 않습니다.")}</p>}
			<Button emphasis="primary" type="button" onClick={() => void start()} busy={working} disabled={working || confirming}>{l("Authorize with PayPal", "PayPal에서 승인")}</Button>
        </div>
      ) : null}
      {paymentInitiationReady && checkout && checkout.payment.state === "ACTION_REQUIRED" ? (
        <div className="agency-payment-action">
          <p>{l("PayPal approval is not complete yet. Continue on the approval page, or wait briefly for automatic confirmation if you already approved.", "PayPal 승인이 아직 완료되지 않았습니다. 승인 화면으로 이동해 계속하거나, 이미 승인했다면 잠시 뒤 자동으로 확정됩니다.")}</p>
          {checkout.approvalUrl ? <Button emphasis="primary" type="button" onClick={() => window.location.assign(checkout.approvalUrl ?? "")}>{l("Continue PayPal approval", "PayPal 승인 계속하기")}</Button> : null}
          <Button emphasis="secondary" type="button" busy={working} disabled={working} onClick={() => void (async () => {
            setWorking(true);
            try {
              if (checkout.returnNonce) {
                setCheckout(await resumePayPalCheckout(agencyOrderId, checkout.returnNonce, false));
              }
            } catch {
              setError(l("We couldn't check payment status. Try again shortly.", "결제 상태를 확인하지 못했습니다. 잠시 후 다시 시도해 주세요."));
            } finally {
              setWorking(false);
            }
          })()}>{l("Check payment status again", "결제 상태 다시 확인")}</Button>
        </div>
      ) : null}
		{confirming && !authorized ? <div className="agency-payment-status" role="status"><strong>{l("Confirming authorization.", "승인 확인 중입니다.")}</strong><span>{checkout?.attempt.paypalOrderId ?? l("PayPal Order", "PayPal Order")}</span><small>{l("Returning from the redirect alone does not confirm funding. The server re-reads the PayPal authorization before Procurement can start.", "redirect 복귀만으로 자금 승인을 확정하지 않습니다. 조달을 시작하기 전에 서버가 PayPal 승인을 다시 조회합니다.")}</small></div> : null}
		{authorized ? <div className="agency-payment-status" role="status"><strong>{l("PayPal authorization is confirmed.", "PayPal 승인이 확정되었습니다.")}</strong><span>{l("PayPal Order", "PayPal Order")} {checkout?.attempt.paypalOrderId}</span><small>{livePayment ? l("No Shop was charged on this approval step. Each exact Merchant Order allocation is captured only when its Procurement starts.", "이 승인 단계에서는 Shop 금액이 청구되지 않았습니다. 각 Merchant Order의 정확한 배분액은 해당 조달을 시작할 때만 수납됩니다.") : l("The Sandbox authorization is ready. Shop-by-Shop captures happen only as each Merchant Order enters Procurement; no real money moves.", "Sandbox 승인이 준비되었습니다. 각 Merchant Order가 조달에 들어갈 때만 Shop별로 수납되며 실제 금액은 이동하지 않습니다.")}</small></div> : null}
      {outcomeUnknown ? <div className="agency-payment-status agency-payment-status--blocked" role="alert"><strong>{l("Do not attempt another payment.", "추가 결제를 시도하지 마세요.")}</strong><span>{checkout?.payment.lastReasonCode ?? checkout?.payment.state}</span><small>{l("PayPal has not returned a final result yet. Vitlane is rechecking the same PayPal Order and will not send another charge.", "PayPal의 최종 결과가 아직 확인되지 않았습니다. Vitlane이 같은 PayPal Order를 재조회하며 추가 청구는 보내지 않습니다.")}</small></div> : null}
      {checkout?.payment.state === "SUPERSEDED" ? <div className="agency-payment-status agency-payment-status--blocked" role="alert"><strong>{l("Payment stopped because the order expired.", "주문 유효기간이 지나 결제가 중단되었습니다.")}</strong><small>{l("Issue a new order sheet before paying. There was no charge because capture had not occurred.", "주문서를 다시 발행한 뒤 결제해 주세요. 수납 전이므로 청구는 없습니다.")}</small></div> : null}
		<p className="agency-payment-boundary">{l("The AgencyOrder and PaymentInstruction were issued atomically. PayPal authorizes the fixed order total once; Vitlane captures only immutable whole-MO allocations at Procurement start.", "AgencyOrder와 PaymentInstruction은 원자적으로 발행됩니다. PayPal은 고정 주문 총액을 한 번 승인하고, Vitlane은 조달 시작 시 immutable MO 전체 배분액만 수납합니다.")}</p>
      <Link to={`/agencyOrder/${agencyOrderId}`}>{l("View AgencyOrder status", "AgencyOrder 처리 현황 보기")}</Link>
    </section>
  </main>;
}

function terminalPayment(payment: SettlementPayment) {
  return ["FINALIZED", "COMPLETED", "REFUNDED", "FAILED", "SUBMISSION_UNKNOWN"].includes(payment.state);
}

function formatMoney(money: { amountMinor: number; currency: string }) {
  return new Intl.NumberFormat("en-US", { style: "currency", currency: money.currency }).format(money.amountMinor / 100);
}

function moneyToTokenBaseUnits(
  amountMinor: number,
  currency: string,
  tokenDecimals: number,
) {
  if (currency !== "USD" || !Number.isSafeInteger(amountMinor) || amountMinor < 0) return 0n;
  if (tokenDecimals < 2) return BigInt(amountMinor) / (10n ** BigInt(2 - tokenDecimals));
  return BigInt(amountMinor) * (10n ** BigInt(tokenDecimals - 2));
}
