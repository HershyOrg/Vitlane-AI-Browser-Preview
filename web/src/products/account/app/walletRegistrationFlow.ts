import type { SettlementConfig } from "../../payment/giwa/infra/settlementApi";
import { APIError } from "../../../shared/api/client";
import { localizeFixedCopy } from "../../../shared/i18n";
import {
  completeWalletRegistrationAttempt,
  createWalletRegistrationAttempt,
  type WalletOwnershipProof,
  type WalletProjection,
  type WalletRegistrationAttempt,
} from "../infra/accountApi";
import {
  BrowserWalletError,
  browserWalletProvider,
  requestWalletAccount,
  signWalletRegistrationMessage,
  switchToSettlementChain,
  watchWalletProvider,
  type EIP1193Provider,
} from "../infra/eip1193Provider";

type RegistrationRecovery = {
  address: string;
  chainId: string;
  attemptOperationId: string;
  completionOperationId: string;
  attempt?: WalletRegistrationAttempt;
};

export type WalletRegistrationResult = {
  account: string;
  wallet: WalletProjection;
  ownershipProof: WalletOwnershipProof;
  replay: boolean;
  reused: boolean;
  provider: EIP1193Provider;
};

export type WalletRegistrationOptions = {
  scope: string;
  wallets?: WalletProjection[];
  provider?: EIP1193Provider;
  now?: () => number;
  requiredValidUntil?: number;
  operationStore?: WalletRegistrationOperationStore;
  forceReauthentication?: boolean;
  expectedAccount?: string;
};

export interface WalletRegistrationOperationStore {
  load(scope: string): RegistrationRecovery | undefined;
  save(scope: string, recovery: RegistrationRecovery): void;
  clear(scope: string): void;
}

export async function runWalletRegistrationFlow(
  config: SettlementConfig,
  options: WalletRegistrationOptions,
): Promise<WalletRegistrationResult> {
  const store = options.operationStore ?? browserOperationStore;
  try {
    return await runWalletRegistrationFlowOnce(config, options, store);
  } catch (caught) {
    if (
      caught instanceof BrowserWalletError
      && caught.code === "WALLET_REGISTRATION_RESULT_STALE"
    ) {
      store.clear(options.scope);
      throw caught;
    }
    if (!isTerminalRegistrationRecoveryError(caught)) throw caught;

    // A server-side terminal response means the persisted attempt can never
    // make progress. Clear it and create one fresh attempt exactly once.
    store.clear(options.scope);
    return runWalletRegistrationFlowOnce(config, options, store);
  }
}

async function runWalletRegistrationFlowOnce(
  config: SettlementConfig,
  options: WalletRegistrationOptions,
  store: WalletRegistrationOperationStore,
): Promise<WalletRegistrationResult> {
  const provider = browserWalletProvider(options.provider);
  await switchToSettlementChain(provider, config);
  const account = await requestWalletAccount(provider);
  if (
    options.expectedAccount
    && account.toLowerCase() !== options.expectedAccount.toLowerCase()
  ) {
    throw new BrowserWalletError(
      "WALLET_ACCOUNT_MISMATCH",
      localizeFixedCopy(
        "To register the selected wallet again, switch your wallet extension to {account}.",
        "선택한 지갑을 다시 등록하려면 지갑 확장에서 {account} 계정으로 전환해 주세요.",
        { account: options.expectedAccount },
      ),
    );
  }
  const reusable = options.forceReauthentication
    ? undefined
    : findReusableWallet(
      options.wallets ?? [],
      account,
      config.chainCaip2,
      options.now?.() ?? Date.now(),
      options.requiredValidUntil,
  );
  if (reusable) {
    // The server projection is authoritative after a completion response was
    // lost. Do not leave the now-terminal attempt in sessionStorage, otherwise
    // a later proof expiry would revive stale operation IDs.
    store.clear(options.scope);
    return {
      account,
      wallet: reusable,
      ownershipProof: proofFromProjection(reusable),
      replay: true,
      reused: true,
      provider,
    };
  }

  const recovery = recoverOrCreate(
    store.load(options.scope),
    account,
    config.chainCaip2,
    options.now?.() ?? Date.now(),
  );
  store.save(options.scope, recovery);

  let providerChanged = false;
  const stopWatching = watchWalletProvider(provider, () => {
    providerChanged = true;
  });

  try {
    const attemptResult = recovery.attempt
      ? { attempt: recovery.attempt }
      : await createWalletRegistrationAttempt(
        account,
        config.chainCaip2,
        recovery.attemptOperationId,
      );
    recovery.attempt = attemptResult.attempt;
    store.save(options.scope, recovery);

    if (providerChanged) throw providerChangedError();
    const signature = await signWalletRegistrationMessage(
      provider,
      account,
      attemptResult.attempt.message,
    );
    if (providerChanged) throw providerChangedError();

    await assertProviderStillMatches(
      provider,
      account,
      config.chainId,
    );
    const completed = await completeWalletRegistrationAttempt(
      attemptResult.attempt.id,
      attemptResult.attempt.nonce,
      signature,
      recovery.completionOperationId,
    );
    if (providerChanged) throw providerChangedError();
    const effectiveProof = registrationCompletionProof(completed);
    store.clear(options.scope);
    return {
      account,
      wallet: completed.latestWallet,
      ownershipProof: effectiveProof,
      replay: completed.replay,
      reused: effectiveProof.id !== completed.ownershipProof.id,
      provider,
    };
  } finally {
    stopWatching();
  }
}

export function resetWalletRegistrationFlow(
  scope: string,
  store: WalletRegistrationOperationStore = browserOperationStore,
) {
  store.clear(scope);
}

export function ownershipProofIsCurrentlyValid(
  projection: WalletProjection,
  now = Date.now(),
  requiredValidUntil = now,
) {
  if (
    projection.wallet.registrationStatus !== "REGISTERED"
    || projection.ownership.status !== "VALID"
    || !projection.ownership.proofId
    || !projection.ownership.verifiedAt
    || !projection.ownership.validUntil
  ) {
    return false;
  }
  const verifiedAt = new Date(projection.ownership.verifiedAt).getTime();
  const validUntil = new Date(projection.ownership.validUntil).getTime();
  return Number.isFinite(verifiedAt)
    && Number.isFinite(validUntil)
    && verifiedAt <= now
    && validUntil > now
    && validUntil >= requiredValidUntil;
}

function findReusableWallet(
  wallets: WalletProjection[],
  address: string,
  chainId: string,
  now: number,
  requiredValidUntil?: number,
) {
  return wallets.find((projection) => (
    projection.wallet.chainId === chainId
    && projection.wallet.address.toLowerCase() === address.toLowerCase()
    && ownershipProofIsCurrentlyValid(
      projection,
      now,
      requiredValidUntil ?? now,
    )
  ));
}

function proofFromProjection(projection: WalletProjection): WalletOwnershipProof {
  if (
    !projection.ownership.proofId
    || !projection.ownership.verifiedAt
    || !projection.ownership.validUntil
  ) {
    throw new BrowserWalletError(
      "WALLET_OWNERSHIP_PROOF_INCOMPLETE",
      localizeFixedCopy(
        "The server did not return a complete ownership proof. Reauthenticate the wallet.",
        "서버가 완전한 소유권 증명을 반환하지 않았습니다. 지갑을 다시 인증해 주세요.",
      ),
    );
  }
  return {
    id: projection.ownership.proofId,
    walletId: projection.wallet.id,
    verifiedAt: projection.ownership.verifiedAt,
    validUntil: projection.ownership.validUntil,
  };
}

function recoverOrCreate(
  current: RegistrationRecovery | undefined,
  address: string,
  chainId: string,
  now: number,
): RegistrationRecovery {
  if (
    current
    && current.address.toLowerCase() === address.toLowerCase()
    && current.chainId === chainId
    && (!current.attempt
      || (
        current.attempt.status === "PENDING"
        && new Date(current.attempt.expiresAt).getTime() > now
      ))
  ) {
    return current;
  }
  return {
    address,
    chainId,
    attemptOperationId: operationId("wallet-registration-attempt"),
    completionOperationId: operationId("wallet-registration-complete"),
  };
}

function isTerminalRegistrationRecoveryError(caught: unknown) {
  if (!(caught instanceof APIError)) return false;
  return caught.status === 404
    || caught.status === 410
    || [
      "WALLET_REGISTRATION_ATTEMPT_NOT_FOUND",
      "WALLET_REGISTRATION_ATTEMPT_EXPIRED",
      "WALLET_REGISTRATION_ATTEMPT_CLOSED",
      "WALLET_REGISTRATION_OPERATION_REUSED",
    ].includes(caught.code);
}

function registrationCompletionProof(completed: {
  registration: {
    walletId: string;
    ownershipProofId: string;
    address: string;
    accountId: string;
    chainId: string;
    verifiedAt: string;
    validUntil: string;
  };
  latestWallet: WalletProjection;
  ownershipProof: WalletOwnershipProof;
}) {
  if (
    completed.registration.walletId !== completed.ownershipProof.walletId
    || completed.registration.ownershipProofId !== completed.ownershipProof.id
    || completed.registration.address.toLowerCase()
      !== completed.latestWallet.wallet.address.toLowerCase()
    || completed.registration.accountId
      !== completed.latestWallet.wallet.accountId
    || completed.registration.chainId
      !== completed.latestWallet.wallet.chainId
    || completed.latestWallet.wallet.id
      !== completed.ownershipProof.walletId
  ) {
    throw new BrowserWalletError(
      "WALLET_REGISTRATION_RESULT_STALE",
      localizeFixedCopy(
        "The wallet-registration result does not match the latest wallet state. Refresh the account page and try again.",
        "지갑 등록 결과의 계보가 최신 지갑 상태와 일치하지 않습니다. 계정 화면을 새로고침한 뒤 다시 시도해 주세요.",
      ),
    );
  }
  if (
    completed.latestWallet.wallet.registrationStatus === "REGISTERED"
    && completed.latestWallet.wallet.currentOwnershipProofId
      === completed.ownershipProof.id
    && completed.latestWallet.ownership.proofId
      === completed.ownershipProof.id
  ) {
    return completed.ownershipProof;
  }
  if (
    completed.latestWallet.wallet.registrationStatus === "REGISTERED"
    && completed.latestWallet.wallet.currentOwnershipProofId
      === completed.latestWallet.ownership.proofId
  ) {
    return proofFromProjection(completed.latestWallet);
  }
  throw new BrowserWalletError(
    "WALLET_REGISTRATION_RESULT_STALE",
    localizeFixedCopy(
      "This wallet was deregistered by another operation. Check the latest state and explicitly register it again.",
      "이 지갑은 다른 작업에서 등록 해제되었습니다. 최신 상태를 확인한 뒤 명시적으로 다시 등록해 주세요.",
    ),
  );
}

async function assertProviderStillMatches(
  provider: EIP1193Provider,
  expectedAddress: string,
  expectedChainId: number,
) {
  const [accounts, chainId] = await Promise.all([
    provider.request({ method: "eth_accounts" }),
    provider.request({ method: "eth_chainId" }),
  ]);
  const address = Array.isArray(accounts) && typeof accounts[0] === "string"
    ? accounts[0]
    : "";
  if (
    address.toLowerCase() !== expectedAddress.toLowerCase()
    || BigInt(String(chainId)) !== BigInt(expectedChainId)
  ) {
    throw providerChangedError();
  }
}

function providerChangedError() {
  return new BrowserWalletError(
    "WALLET_ACCOUNT_CHANGED",
    localizeFixedCopy(
      "The wallet account or network changed during signing. Start again with the current account.",
      "등록 서명 중 지갑 계정 또는 네트워크가 변경되었습니다. 현재 계정으로 다시 시작해 주세요.",
    ),
  );
}

function operationId(scope: string) {
  const suffix = typeof crypto.randomUUID === "function"
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `${scope}:${suffix}`;
}

const browserOperationStore: WalletRegistrationOperationStore = {
  load(scope) {
    if (typeof sessionStorage === "undefined") return undefined;
    try {
      const raw = sessionStorage.getItem(storageKey(scope));
      if (!raw) return undefined;
      const parsed = JSON.parse(raw) as unknown;
      if (isRegistrationRecovery(parsed)) return parsed;
      sessionStorage.removeItem(storageKey(scope));
      return undefined;
    } catch {
      try {
        sessionStorage.removeItem(storageKey(scope));
      } catch {
        // Storage may be unavailable in privacy-restricted browser contexts.
      }
      return undefined;
    }
  },
  save(scope, recovery) {
    if (typeof sessionStorage === "undefined") return;
    try {
      sessionStorage.setItem(storageKey(scope), JSON.stringify(recovery));
    } catch {
      // The in-flight flow still keeps its stable operation IDs in memory.
    }
  },
  clear(scope) {
    if (typeof sessionStorage === "undefined") return;
    try {
      sessionStorage.removeItem(storageKey(scope));
    } catch {
      // A terminal response does not require storage cleanup to succeed.
    }
  },
};

function isRegistrationRecovery(value: unknown): value is RegistrationRecovery {
  if (!value || typeof value !== "object") return false;
  const recovery = value as Record<string, unknown>;
  if (
    typeof recovery.address !== "string"
    || !/^0x[0-9a-fA-F]{40}$/.test(recovery.address)
    || typeof recovery.chainId !== "string"
    || !/^eip155:[1-9][0-9]*$/.test(recovery.chainId)
    || typeof recovery.attemptOperationId !== "string"
    || recovery.attemptOperationId.length < 8
    || typeof recovery.completionOperationId !== "string"
    || recovery.completionOperationId.length < 8
  ) {
    return false;
  }
  if (recovery.attempt === undefined) return true;
  if (!recovery.attempt || typeof recovery.attempt !== "object") return false;
  const attempt = recovery.attempt as Record<string, unknown>;
  return typeof attempt.id === "string"
    && typeof attempt.address === "string"
    && typeof attempt.accountId === "string"
    && typeof attempt.chainId === "string"
    && typeof attempt.status === "string"
    && typeof attempt.message === "string"
    && typeof attempt.messageHash === "string"
    && typeof attempt.nonce === "string"
    && typeof attempt.expiresAt === "string";
}

function storageKey(scope: string) {
  return `vitlane:wallet-registration:${scope}`;
}
