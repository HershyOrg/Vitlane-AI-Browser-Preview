import { useCallback, useEffect, useMemo, useState } from "react";
import type { Address } from "viem";
import { AppOverlay } from "../../../../shared/AppOverlay";
import {
  Button,
  Notice,
} from "../../../../shared/ui";
import {
  getAccountOverview,
} from "../../../account/infra/accountApi";
import {
  getSettlementConfig,
  type SettlementConfig,
} from "../infra/settlementApi";
import {
  browserWalletCapability,
  claimTestUSD,
  connectWallet,
  readTestAssetStatus,
  type TestAssetStatus,
} from "../infra/wallet";
import { useLocale, type Localize } from "../../../../shared/i18n";

export const openTestAssetsEvent = "vitlane:open-test-assets";
export const testAssetsUpdatedEvent = "vitlane:test-assets-updated";

export type OpenTestAssetsDetail = {
  address?: string;
};

type TestAssetWalletChoice = {
  wallet: {
    address: string;
    isDefault: boolean;
    registrationStatus: string;
  };
};

export function TestAssetPanel({
  showTrigger = true,
}: {
  showTrigger?: boolean;
}) {
  const { l, locale } = useLocale();
  const [open, setOpen] = useState(false);
  const [config, setConfig] = useState<SettlementConfig | null>(null);
  const [requestedAddress, setRequestedAddress] = useState<string | null>(null);
  const [walletAddress, setWalletAddress] = useState<string | null>(null);
  const [status, setStatus] = useState<TestAssetStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [claimResult, setClaimResult] = useState<{
    txHash: string;
    balance: bigint;
  } | null>(null);
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));
  const closePanel = useCallback(() => setOpen(false), []);

  const load = useCallback(async () => {
    try {
      const [{ settlement }, accountResult] = await Promise.all([
        getSettlementConfig(),
        getAccountOverview().catch(() => null),
      ]);
      const selectedAddress = selectTestAssetAddress(
        accountResult?.account.wallets ?? [],
        requestedAddress,
      );
      setConfig(settlement);
      setWalletAddress(selectedAddress);
      if (selectedAddress) {
        setStatus(await readTestAssetStatus(
          settlement,
          selectedAddress as Address,
        ));
      } else {
        setStatus(null);
      }
      setError(null);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : l("We couldn't load TEST asset information.", "TEST 자산 정보를 불러오지 못했습니다."));
    }
  }, [l, requestedAddress]);

  useEffect(() => {
    if (open) void load();
  }, [load, open]);

  useEffect(() => {
    function openPanel(event: Event) {
      const detail = (event as CustomEvent<OpenTestAssetsDetail>).detail;
      setRequestedAddress(
        typeof detail?.address === "string" && detail.address
          ? detail.address
          : null,
      );
      setOpen(true);
    }
    window.addEventListener(openTestAssetsEvent, openPanel);
    return () => window.removeEventListener(openTestAssetsEvent, openPanel);
  }, []);

  useEffect(() => {
    const timer = window.setInterval(
      () => setNow(Math.floor(Date.now() / 1000)),
      1000,
    );
    return () => window.clearInterval(timer);
  }, []);

  const remaining = useMemo(
    () => Math.max(0, (status?.claimableAtSeconds ?? 0) - now),
    [now, status],
  );
  const capability = browserWalletCapability();

  async function claim() {
    if (!config || !walletAddress) return;
    setBusy(true);
    setError(null);
    setClaimResult(null);
    try {
      const connected = await connectWallet(config);
      if (connected.toLowerCase() !== walletAddress.toLowerCase()) {
        throw new Error(
          requestedAddress
            ? l("Switch to the payment wallet account {wallet}.", "결제 지갑 {wallet} 계정으로 전환해 주세요.", { wallet: short(walletAddress) })
            : l("Switch to the default wallet account {wallet}.", "기본 지갑 {wallet} 계정으로 전환해 주세요.", { wallet: short(walletAddress) }),
        );
      }
      const txHash = await claimTestUSD(config, connected);
      const nextStatus = await readTestAssetStatus(config, connected);
      setStatus(nextStatus);
      setClaimResult({ txHash, balance: nextStatus.tokenBalance });
      window.dispatchEvent(new CustomEvent(testAssetsUpdatedEvent));
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : l("We couldn't claim the TEST asset.", "TEST 자산을 받지 못했습니다."));
    } finally {
      setBusy(false);
    }
  }

  if (config && !["LOCAL", "GIWA_TESTNET"].includes(config.environment)) return null;

  return (
    <>
      {showTrigger && (
        <Button
          className="account-ui-test-assets__trigger"
          emphasis="quiet"
          type="button"
          onClick={() => setOpen(true)}
        >
          <span aria-hidden="true">◌</span>
          {l("TEST assets", "TEST 자산")}
        </Button>
      )}
      {open && (
        <AppOverlay
          description={l("This faucet is for local review and is separate from purchases.", "구매와 분리된 로컬 검수용 Faucet입니다.")}
          eyebrow={l("TEST asset / faucet", "TEST 자산 / faucet")}
          onClose={closePanel}
          title={l("tVITUSD Faucet", "tVITUSD Faucet")}
        >
          <section
            className="account-ui-test-assets__content"
            aria-label={l("TEST assets", "TEST 자산")}
          >
        {walletAddress && (
          <dl>
            <div>
              <dt>{l("Claim account", "Claim 계정")}</dt>
              <dd data-testid="test-asset-claim-account">
                {short(walletAddress)}
              </dd>
            </div>
            <div><dt>{l("tVITUSD balance", "tVITUSD 잔액")}</dt><dd>{status ? formatToken(status.tokenBalance) : l("Loading", "조회 중")}</dd></div>
            <div><dt>{l("GIWA Test ETH", "GIWA Test ETH")}</dt><dd>{status ? formatNative(status.nativeBalance) : l("Loading", "조회 중")}</dd></div>
            <div><dt>{l("Amount per claim", "1회 수령량")}</dt><dd>{status ? `${formatToken(status.claimAmount)} tVITUSD` : l("Loading", "조회 중")}</dd></div>
            <div><dt>{l("Latest claim", "최근 Claim")}</dt><dd>{status?.lastClaimAtSeconds ? new Date(status.lastClaimAtSeconds * 1000).toLocaleString(locale) : l("None", "없음")}</dd></div>
            <div><dt>{l("Next claim", "다음 Claim")}</dt><dd>{remaining > 0 ? formatCountdown(remaining, l) : l("Available now", "지금 가능")}</dd></div>
          </dl>
        )}
        {claimResult && (
          <Notice announce title={l("TEST assets received", "TEST 자산을 받았습니다")}>
            {l("Current balance {balance} tVITUSD · Transaction {transaction}", "현재 잔액 {balance} tVITUSD · Transaction {transaction}", { balance: formatToken(claimResult.balance), transaction: short(claimResult.txHash) })}
          </Notice>
        )}
        {error && <Notice announce tone="danger">{error}</Notice>}
        <footer>
          <Button emphasis="quiet" size="compact" busy={busy} onClick={() => void load()}>
            {l("Refresh balance", "잔액 새로고침")}
          </Button>
          <Button
            emphasis="primary"
            busy={busy}
            disabled={!walletAddress || !status || remaining > 0 || status.paused || capability.kind !== "INJECTED_READY"}
            onClick={() => void claim()}
          >
            {l("Claim tVITUSD", "tVITUSD 받기")}
          </Button>
        </footer>
        {!walletAddress && (
          <p className="vt-field__hint">
            {l("Register the current payment wallet in your account.", "계정에서 현재 결제 지갑을 등록해 주세요.")}
          </p>
        )}
        {capability.kind !== "INJECTED_READY" && (
          <p className="vt-field__hint">
            {l("Open this purchase in a desktop browser with a wallet extension installed.", "지갑 확장이 설치된 데스크톱 브라우저에서 이 구매를 다시 여세요.")}
          </p>
        )}
        {status?.paused && (
          <p className="vt-field__hint">
            {l("The faucet is paused.", "Faucet이 일시 중지되었습니다.")}
          </p>
        )}
          </section>
        </AppOverlay>
      )}
    </>
  );
}

export function selectTestAssetAddress(
  wallets: readonly TestAssetWalletChoice[],
  requestedAddress?: string | null,
) {
  if (requestedAddress) return requestedAddress;
  return wallets.find(
    ({ wallet }) => (
      wallet.isDefault && wallet.registrationStatus === "REGISTERED"
    ),
  )?.wallet.address ?? null;
}

function short(value: string) {
  return value.length > 16 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value;
}

function formatToken(value: bigint) {
  const whole = value / 1_000_000n;
  const fraction = (value % 1_000_000n).toString().padStart(6, "0").replace(/0+$/, "");
  return fraction ? `${whole}.${fraction}` : whole.toString();
}

function formatNative(value: bigint) {
  const whole = value / 1_000_000_000_000_000_000n;
  const fraction = ((value % 1_000_000_000_000_000_000n) / 1_000_000_000_000_000n)
    .toString()
    .padStart(3, "0");
  return `${whole}.${fraction} ETH`;
}

function formatCountdown(seconds: number, l: Localize) {
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  return l("{minutes}m {seconds}s", "{minutes}분 {seconds}초", { minutes, seconds: rest.toString().padStart(2, "0") });
}
