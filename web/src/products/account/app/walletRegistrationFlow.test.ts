// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { APIError } from "../../../shared/api/client";
import {
  completeWalletRegistrationAttempt,
  createWalletRegistrationAttempt,
  type WalletProjection,
  type WalletRegistrationAttempt,
} from "../infra/accountApi";
import {
  type EIP1193Provider,
} from "../infra/eip1193Provider";
import {
  runWalletRegistrationFlow,
  type WalletRegistrationOperationStore,
} from "./walletRegistrationFlow";

vi.mock("../infra/accountApi", () => ({
  completeWalletRegistrationAttempt: vi.fn(),
  createWalletRegistrationAttempt: vi.fn(),
}));

const address = "0xa0Ee7A142d267C1f36714E4a8F75612F20a79720";
const config = {
  environment: "LOCAL",
  chainId: 91342,
  chainCaip2: "eip155:91342",
  rpcUrl: "http://127.0.0.1:8545",
  explorerUrl: "",
  tokenAddress: `0x${"1".repeat(40)}`,
  faucetAddress: `0x${"2".repeat(40)}`,
  settlementAddress: `0x${"3".repeat(40)}`,
  tokenSymbol: "tVITUSD",
  tokenDecimals: 6,
  feeBps: 100,
  feeRecipient: `0x${"4".repeat(40)}`,
  claimAmountBaseUnits: "100000000",
} as const;

const attempt: WalletRegistrationAttempt = {
  id: "attempt-1",
  address,
  accountId: `eip155:91342:${address}`,
  chainId: "eip155:91342",
  status: "PENDING",
  message: "Sign this Vitlane wallet registration",
  messageHash: `0x${"5".repeat(64)}`,
  nonce: "nonce-1",
  expiresAt: "2026-08-01T00:00:00Z",
};

const pendingAttemptNow = () =>
  new Date("2026-07-31T23:55:00Z").getTime();

const projection: WalletProjection = {
  wallet: {
    id: "wallet-1",
    userId: "user-1",
    address,
    accountId: `eip155:91342:${address}`,
    chainId: "eip155:91342",
    registrationStatus: "REGISTERED",
    currentOwnershipProofId: "proof-1",
    isDefault: true,
    registeredAt: "2026-07-29T00:00:00Z",
    createdAt: "2026-07-29T00:00:00Z",
    updatedAt: "2026-07-29T00:00:00Z",
  },
  ownership: {
    status: "VALID",
    proofId: "proof-1",
    verifiedAt: "2026-07-29T00:00:00Z",
    validUntil: "2026-08-29T00:00:00Z",
  },
  kyc: { eligibility: "NONE", actionEligible: false },
  actions: {
    canSetDefault: false,
    canDeregister: true,
    canReauthenticate: false,
    canStartKYC: true,
    canCheckKYC: false,
  },
};

describe("walletRegistrationFlow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    vi.mocked(createWalletRegistrationAttempt).mockResolvedValue({
      attempt,
      replay: false,
    });
    vi.mocked(completeWalletRegistrationAttempt).mockResolvedValue({
      registration: {
        walletId: "wallet-1",
        ownershipProofId: "proof-1",
        address,
        accountId: `eip155:91342:${address}`,
        chainId: "eip155:91342",
        verifiedAt: "2026-07-29T00:00:00Z",
        validUntil: "2026-08-29T00:00:00Z",
      },
      latestWallet: projection,
      ownershipProof: {
        id: "proof-1",
        walletId: "wallet-1",
        verifiedAt: "2026-07-29T00:00:00Z",
        validUntil: "2026-08-29T00:00:00Z",
      },
      replay: false,
    });
  });

  it("4001 network switch rejection never attempts add-chain or registration", async () => {
    const rejected = Object.assign(new Error("rejected"), { code: 4001 });
    const provider = providerWith({
      wallet_switchEthereumChain: vi.fn().mockRejectedValue(rejected),
    });

    await expect(runWalletRegistrationFlow(config, {
      scope: "account:new",
      provider,
      operationStore: memoryStore(),
    })).rejects.toMatchObject({
      code: "WALLET_CHAIN_SWITCH_REJECTED",
    });

    expect(provider.request).not.toHaveBeenCalledWith(
      expect.objectContaining({ method: "wallet_addEthereumChain" }),
    );
    expect(createWalletRegistrationAttempt).not.toHaveBeenCalled();
  });

  it("4902 unknown chain adds it once and only then creates the attempt", async () => {
    const unknownChain = Object.assign(new Error("unknown chain"), { code: 4902 });
    const switchChain = vi.fn()
      .mockRejectedValueOnce(unknownChain)
      .mockResolvedValueOnce(null);
    const provider = providerWith({
      wallet_switchEthereumChain: switchChain,
    });

    await runWalletRegistrationFlow(config, {
      scope: "account:new",
      provider,
      operationStore: memoryStore(),
    });

    expect(switchChain).toHaveBeenCalledTimes(2);
    expect(provider.request).toHaveBeenCalledWith(
      expect.objectContaining({ method: "wallet_addEthereumChain" }),
    );
    expect(createWalletRegistrationAttempt).toHaveBeenCalledTimes(1);
  });

  it("Checkout reuses a matching valid proof without another signature", async () => {
    const provider = providerWith();
    const store = memoryStore();
    store.save("checkout:session-1", {
      address,
      chainId: config.chainCaip2,
      attemptOperationId: "lost-response-attempt-operation",
      completionOperationId: "lost-response-completion-operation",
      attempt,
    });

    const result = await runWalletRegistrationFlow(config, {
      scope: "checkout:session-1",
      wallets: [projection],
      provider,
      now: () => new Date("2026-08-01T00:00:00Z").getTime(),
      operationStore: store,
    });

    expect(result.reused).toBe(true);
    expect(result.ownershipProof.id).toBe("proof-1");
    expect(provider.request).not.toHaveBeenCalledWith(
      expect.objectContaining({ method: "personal_sign" }),
    );
    expect(createWalletRegistrationAttempt).not.toHaveBeenCalled();
    expect(store.load("checkout:session-1")).toBeUndefined();
  });

  it("missing proof timestamps fail closed instead of creating an indefinitely valid proof", async () => {
    const incomplete = structuredClone(projection);
    incomplete.ownership.verifiedAt = undefined;
    incomplete.ownership.validUntil = undefined;
    const provider = providerWith();

    const result = await runWalletRegistrationFlow(config, {
      scope: "checkout:session-1",
      wallets: [incomplete],
      provider,
      now: () => new Date("2026-08-01T00:00:00Z").getTime(),
      operationStore: memoryStore(),
    });

    expect(result.reused).toBe(false);
    expect(createWalletRegistrationAttempt).toHaveBeenCalledTimes(1);
  });

  it("KYC freshness CTA forces a new signature even while the 24-hour proof is VALID", async () => {
    const provider = providerWith();

    const result = await runWalletRegistrationFlow(config, {
      scope: "account:wallet:wallet-1",
      wallets: [projection],
      provider,
      forceReauthentication: true,
      now: () => new Date("2026-08-01T00:00:00Z").getTime(),
      operationStore: memoryStore(),
    });

    expect(result.reused).toBe(false);
    expect(provider.request).toHaveBeenCalledWith(
      expect.objectContaining({ method: "personal_sign" }),
    );
  });

  it("card-specific re-registration rejects a different active account before mutation", async () => {
    const provider = providerWith();

    await expect(runWalletRegistrationFlow(config, {
      scope: "account:wallet:wallet-2",
      provider,
      expectedAccount: `0x${"9".repeat(40)}`,
      forceReauthentication: true,
      operationStore: memoryStore(),
    })).rejects.toMatchObject({
      code: "WALLET_ACCOUNT_MISMATCH",
    });

    expect(createWalletRegistrationAttempt).not.toHaveBeenCalled();
    expect(provider.request).not.toHaveBeenCalledWith(
      expect.objectContaining({ method: "personal_sign" }),
    );
  });

  it("Checkout reauthenticates when proof validity does not cover the quote deadline", async () => {
    const shortProof = structuredClone(projection);
    shortProof.ownership.verifiedAt = "2026-08-01T00:00:00Z";
    shortProof.ownership.validUntil = "2026-08-01T01:00:00Z";
    const provider = providerWith();

    const result = await runWalletRegistrationFlow(config, {
      scope: "checkout:session-1",
      wallets: [shortProof],
      provider,
      now: () => new Date("2026-08-01T00:30:00Z").getTime(),
      requiredValidUntil: new Date("2026-08-01T02:00:00Z").getTime(),
      operationStore: memoryStore(),
    });

    expect(result.reused).toBe(false);
    expect(createWalletRegistrationAttempt).toHaveBeenCalledTimes(1);
  });

  it("Checkout accepts a proof whose validUntil exactly covers the quote deadline", async () => {
    const exactProof = structuredClone(projection);
    exactProof.ownership.verifiedAt = "2026-08-01T00:00:00Z";
    exactProof.ownership.validUntil = "2026-08-01T02:00:00Z";
    const provider = providerWith();

    const result = await runWalletRegistrationFlow(config, {
      scope: "checkout:session-1",
      wallets: [exactProof],
      provider,
      now: () => new Date("2026-08-01T00:30:00Z").getTime(),
      requiredValidUntil: new Date("2026-08-01T02:00:00Z").getTime(),
      operationStore: memoryStore(),
    });

    expect(result.reused).toBe(true);
    expect(createWalletRegistrationAttempt).not.toHaveBeenCalled();
  });

  it("an expired proof performs a new signature and completion", async () => {
    const expired = structuredClone(projection);
    expired.ownership.validUntil = "2026-07-30T00:00:00Z";
    const provider = providerWith();

    const result = await runWalletRegistrationFlow(config, {
      scope: "checkout:session-1",
      wallets: [expired],
      provider,
      now: () => new Date("2026-08-01T00:00:00Z").getTime(),
      operationStore: memoryStore(),
    });

    expect(result.reused).toBe(false);
    expect(provider.request).toHaveBeenCalledWith(
      expect.objectContaining({ method: "personal_sign" }),
    );
    expect(completeWalletRegistrationAttempt).toHaveBeenCalledTimes(1);
  });

  it("signature rejection and retry reuse the same attempt and operation IDs", async () => {
    const rejected = Object.assign(new Error("rejected"), { code: 4001 });
    const sign = vi.fn()
      .mockRejectedValueOnce(rejected)
      .mockResolvedValueOnce("0xsignature");
    const provider = providerWith({ personal_sign: sign });
    const store = memoryStore();

    await expect(runWalletRegistrationFlow(config, {
      scope: "account:new",
      provider,
      now: pendingAttemptNow,
      operationStore: store,
    })).rejects.toMatchObject({ code: "WALLET_SIGNATURE_REJECTED" });

    await runWalletRegistrationFlow(config, {
      scope: "account:new",
      provider,
      now: pendingAttemptNow,
      operationStore: store,
    });

    expect(createWalletRegistrationAttempt).toHaveBeenCalledTimes(1);
    expect(sign).toHaveBeenCalledTimes(2);
    expect(completeWalletRegistrationAttempt).toHaveBeenCalledTimes(1);
    const operationId = vi.mocked(
      completeWalletRegistrationAttempt,
    ).mock.calls[0][3];
    expect(operationId).toMatch(/^wallet-registration-complete:/);
  });

  it("a lost completion response replays with the same completion operation ID", async () => {
    const provider = providerWith();
    const store = memoryStore();
    vi.mocked(completeWalletRegistrationAttempt)
      .mockRejectedValueOnce(new TypeError("network response lost"))
      .mockResolvedValueOnce({
        registration: {
          walletId: "wallet-1",
          ownershipProofId: "proof-1",
          address,
          accountId: `eip155:91342:${address}`,
          chainId: "eip155:91342",
          verifiedAt: "2026-07-29T00:00:00Z",
          validUntil: "2026-08-29T00:00:00Z",
        },
        latestWallet: projection,
        ownershipProof: {
          id: "proof-1",
          walletId: "wallet-1",
          verifiedAt: "2026-07-29T00:00:00Z",
          validUntil: "2026-08-29T00:00:00Z",
        },
        replay: true,
      });

    await expect(runWalletRegistrationFlow(config, {
      scope: "checkout:session-1",
      provider,
      now: pendingAttemptNow,
      operationStore: store,
    })).rejects.toThrow("network response lost");
    const firstOperationId = vi.mocked(
      completeWalletRegistrationAttempt,
    ).mock.calls[0][3];

    const recovered = await runWalletRegistrationFlow(config, {
      scope: "checkout:session-1",
      provider,
      now: pendingAttemptNow,
      operationStore: store,
    });

    expect(recovered.replay).toBe(true);
    expect(createWalletRegistrationAttempt).toHaveBeenCalledTimes(1);
    expect(completeWalletRegistrationAttempt).toHaveBeenCalledTimes(2);
    expect(
      vi.mocked(completeWalletRegistrationAttempt).mock.calls[1][3],
    ).toBe(firstOperationId);
  });

  it.each([
    new APIError(
      "WALLET_REGISTRATION_ATTEMPT_NOT_FOUND",
      "missing",
      404,
    ),
    new APIError(
      "WALLET_REGISTRATION_ATTEMPT_EXPIRED",
      "expired",
      410,
    ),
    new APIError(
      "WALLET_REGISTRATION_ATTEMPT_CLOSED",
      "closed",
      409,
    ),
  ])(
    "a terminal persisted attempt is cleared and replaced once (%s)",
    async (terminalError) => {
      const provider = providerWith();
      const store = memoryStore();
      const refreshedProjection = structuredClone(projection);
      refreshedProjection.wallet.currentOwnershipProofId = "proof-2";
      refreshedProjection.ownership.proofId = "proof-2";
      refreshedProjection.ownership.verifiedAt = "2026-07-29T00:01:00Z";
      refreshedProjection.ownership.validUntil = "2026-07-30T00:01:00Z";
      vi.mocked(completeWalletRegistrationAttempt)
        .mockRejectedValueOnce(terminalError)
        .mockResolvedValueOnce({
          registration: {
            walletId: "wallet-1",
            ownershipProofId: "proof-2",
            address,
            accountId: `eip155:91342:${address}`,
            chainId: "eip155:91342",
            verifiedAt: "2026-07-29T00:01:00Z",
            validUntil: "2026-07-30T00:01:00Z",
          },
          latestWallet: refreshedProjection,
          ownershipProof: {
            id: "proof-2",
            walletId: "wallet-1",
            verifiedAt: "2026-07-29T00:01:00Z",
            validUntil: "2026-07-30T00:01:00Z",
          },
          replay: false,
        });

      const result = await runWalletRegistrationFlow(config, {
        scope: "account:user-1:new-wallet",
        provider,
        operationStore: store,
      });

      expect(result.ownershipProof.id).toBe("proof-2");
      expect(createWalletRegistrationAttempt).toHaveBeenCalledTimes(2);
      expect(completeWalletRegistrationAttempt).toHaveBeenCalledTimes(2);
      expect(
        vi.mocked(createWalletRegistrationAttempt).mock.calls[0][2],
      ).not.toBe(
        vi.mocked(createWalletRegistrationAttempt).mock.calls[1][2],
      );
    },
  );

  it("an expired session attempt is discarded before another signature", async () => {
    const provider = providerWith();
    const store = memoryStore();
    store.save("account:user-1:new-wallet", {
      address,
      chainId: config.chainCaip2,
      attemptOperationId: "expired-attempt-operation",
      completionOperationId: "expired-completion-operation",
      attempt: {
        ...attempt,
        expiresAt: "2026-07-29T00:00:00Z",
      },
    });

    await runWalletRegistrationFlow(config, {
      scope: "account:user-1:new-wallet",
      provider,
      now: () => new Date("2026-07-29T00:01:00Z").getTime(),
      operationStore: store,
    });

    expect(createWalletRegistrationAttempt).toHaveBeenCalledTimes(1);
    expect(
      vi.mocked(createWalletRegistrationAttempt).mock.calls[0][2],
    ).not.toBe("expired-attempt-operation");
    expect(provider.request).toHaveBeenCalledWith(
      expect.objectContaining({ method: "personal_sign" }),
    );
  });

  it("a malformed persisted recovery record is removed before registration", async () => {
    const scope = "account:user-1:malformed";
    sessionStorage.setItem(
      `vitlane:wallet-registration:${scope}`,
      JSON.stringify({ chainId: config.chainCaip2 }),
    );

    const result = await runWalletRegistrationFlow(config, {
      scope,
      provider: providerWith(),
    });

    expect(result.wallet.wallet.id).toBe("wallet-1");
    expect(createWalletRegistrationAttempt).toHaveBeenCalledTimes(1);
    expect(
      sessionStorage.getItem(`vitlane:wallet-registration:${scope}`),
    ).toBeNull();
  });

  it("an old completion replay adopts the newer current proof without another mutation", async () => {
    const provider = providerWith();
    const latest = structuredClone(projection);
    latest.wallet.currentOwnershipProofId = "proof-2";
    latest.ownership.proofId = "proof-2";
    latest.ownership.verifiedAt = "2026-07-29T01:00:00Z";
    latest.ownership.validUntil = "2026-07-30T01:00:00Z";
    vi.mocked(completeWalletRegistrationAttempt).mockResolvedValueOnce({
      registration: {
        walletId: "wallet-1",
        ownershipProofId: "proof-1",
        address,
        accountId: `eip155:91342:${address}`,
        chainId: "eip155:91342",
        verifiedAt: "2026-07-29T00:00:00Z",
        validUntil: "2026-07-30T00:00:00Z",
      },
      latestWallet: latest,
      ownershipProof: {
        id: "proof-1",
        walletId: "wallet-1",
        verifiedAt: "2026-07-29T00:00:00Z",
        validUntil: "2026-07-30T00:00:00Z",
      },
      replay: true,
    });

    const result = await runWalletRegistrationFlow(config, {
      scope: "account:user-1:new-wallet",
      provider,
      operationStore: memoryStore(),
    });

    expect(result.replay).toBe(true);
    expect(result.reused).toBe(true);
    expect(result.ownershipProof.id).toBe("proof-2");
    expect(createWalletRegistrationAttempt).toHaveBeenCalledTimes(1);
    expect(completeWalletRegistrationAttempt).toHaveBeenCalledTimes(1);
  });

  it("a completion replay cannot silently re-register a Wallet deregistered elsewhere", async () => {
    const provider = providerWith();
    const store = memoryStore();
    const deregistered = structuredClone(projection);
    deregistered.wallet.registrationStatus = "DEREGISTERED";
    deregistered.wallet.currentOwnershipProofId = undefined;
    deregistered.wallet.deregisteredAt = "2026-07-29T01:00:00Z";
    deregistered.ownership = {
      status: "REVOKED",
      nextAction: { kind: "REGISTER" },
    };
    vi.mocked(completeWalletRegistrationAttempt).mockResolvedValueOnce({
      registration: {
        walletId: "wallet-1",
        ownershipProofId: "proof-1",
        address,
        accountId: `eip155:91342:${address}`,
        chainId: "eip155:91342",
        verifiedAt: "2026-07-29T00:00:00Z",
        validUntil: "2026-07-30T00:00:00Z",
      },
      latestWallet: deregistered,
      ownershipProof: {
        id: "proof-1",
        walletId: "wallet-1",
        verifiedAt: "2026-07-29T00:00:00Z",
        validUntil: "2026-07-30T00:00:00Z",
      },
      replay: true,
    });

    await expect(runWalletRegistrationFlow(config, {
      scope: "account:user-1:new-wallet",
      provider,
      operationStore: store,
    })).rejects.toMatchObject({
      code: "WALLET_REGISTRATION_RESULT_STALE",
    });

    expect(store.load("account:user-1:new-wallet")).toBeUndefined();
    expect(createWalletRegistrationAttempt).toHaveBeenCalledTimes(1);
    expect(completeWalletRegistrationAttempt).toHaveBeenCalledTimes(1);
  });
});

function providerWith(
  overrides: Partial<Record<string, () => unknown>> = {},
): EIP1193Provider & { request: ReturnType<typeof vi.fn> } {
  const handlers: Record<string, () => unknown> = {
    wallet_switchEthereumChain: vi.fn().mockResolvedValue(null),
    wallet_addEthereumChain: vi.fn().mockResolvedValue(null),
    eth_requestAccounts: vi.fn().mockResolvedValue([address]),
    eth_accounts: vi.fn().mockResolvedValue([address]),
    eth_chainId: vi.fn().mockResolvedValue("0x164ce"),
    personal_sign: vi.fn().mockResolvedValue("0xsignature"),
    ...overrides,
  };
  return {
    request: vi.fn(async ({ method }: { method: string }) => (
      await handlers[method]()
    )),
  };
}

function memoryStore(): WalletRegistrationOperationStore {
  const values = new Map<string, unknown>();
  return {
    load: (scope) => values.get(scope) as never,
    save: (scope, recovery) => values.set(scope, structuredClone(recovery)),
    clear: (scope) => {
      values.delete(scope);
    },
  };
}
