import {
  createPublicClient,
  createWalletClient,
  custom,
  defineChain,
  http,
  type Address,
  type Hex,
} from "viem";
import { browserWalletCapability } from "../../../../shared/browser/capabilities";
import { localizeFixedCopy } from "../../../../shared/i18n";
import { faucetABI, settlementABI, testUSDABI } from "../../../../shared/onchain/abi";
import {
  browserWalletProvider,
  requestWalletAccount,
  switchToSettlementChain,
} from "../../../account/infra/eip1193Provider";
import type {
  AuthorizationRecord,
  SettlementConfig,
} from "./settlementApi";
import { localReviewRPCURL } from "./localReviewWallet";

export { browserWalletCapability };

export function assertSettlementChain(
  config: SettlementConfig,
  snapshotChainId: number,
) {
  if (snapshotChainId !== config.chainId) {
    throw new Error(
      localizeFixedCopy(
        "The authorization snapshot chain {snapshot} differs from the current payment chain {current}.",
        "승인 snapshot의 chain {snapshot}과 현재 결제 chain {current}이 다릅니다.",
        { snapshot: snapshotChainId, current: config.chainId },
      ),
    );
  }
}

function clients(config: SettlementConfig) {
  if (!window.ethereum) throw new Error(localizeFixedCopy("An EVM wallet extension is required.", "EVM Wallet extension이 필요합니다."));
  const rpcURL = browserSettlementRPCURL(config);
  const chain = defineChain({
    id: config.chainId,
    name: config.environment === "LOCAL" ? "Vitlane Local" : "GIWA Sepolia",
    nativeCurrency: { name: "GIWA Test ETH", symbol: "ETH", decimals: 18 },
    rpcUrls: { default: { http: [rpcURL] } },
    blockExplorers: config.explorerUrl
      ? { default: { name: "Explorer", url: config.explorerUrl } }
      : undefined,
    testnet: true,
  });
  return {
    chain,
    wallet: createWalletClient({ chain, transport: custom(window.ethereum) }),
    publicClient: createPublicClient({ chain, transport: http(rpcURL, { timeout: 10_000 }) }),
  };
}

export async function connectWallet(config: SettlementConfig): Promise<Address> {
  const provider = browserWalletProvider();
  await switchToSettlementChain(provider, config);
  return await requestWalletAccount(provider) as Address;
}

export async function claimTestUSD(config: SettlementConfig, account: Address): Promise<Hex> {
  const { wallet, publicClient, chain } = clients(config);
  const hash = await wallet.writeContract({
    account,
    chain,
    address: config.faucetAddress,
    abi: faucetABI,
    functionName: "claim",
  });
  await publicClient.waitForTransactionReceipt({ hash });
  return hash;
}

export type SettlementAssetStatus = {
  account: Address;
  tokenBalance: bigint;
  nativeBalance: bigint;
  allowance: bigint;
};

export type TestAssetStatus = SettlementAssetStatus & {
  claimAmount: bigint;
  cooldownSeconds: number;
  lastClaimAtSeconds: number;
  claimableAtSeconds: number;
  paused: boolean;
};

export type SettlementAssetSnapshot = {
  chainId: number;
  tokenAddress: Address;
  settlementAddress: Address;
};

export async function readTestAssetStatus(
  config: SettlementConfig,
  account: Address,
): Promise<TestAssetStatus> {
  const settlement = await readSettlementAssetStatus(config, account, {
    chainId: config.chainId,
    tokenAddress: config.tokenAddress,
    settlementAddress: config.settlementAddress,
  });
  const { publicClient } = clientsForRead(config);
  const [claimAmount, cooldown, lastClaimAt, paused] = await Promise.all([
    publicClient.readContract({
      address: config.faucetAddress,
      abi: faucetABI,
      functionName: "claimAmount",
    }),
    publicClient.readContract({
      address: config.faucetAddress,
      abi: faucetABI,
      functionName: "cooldown",
    }),
    publicClient.readContract({
      address: config.faucetAddress,
      abi: faucetABI,
      functionName: "lastClaimAt",
      args: [account],
    }),
    publicClient.readContract({
      address: config.faucetAddress,
      abi: faucetABI,
      functionName: "paused",
    }),
  ]);
  const cooldownSeconds = Number(cooldown);
  const lastClaimAtSeconds = Number(lastClaimAt);
  return {
    ...settlement,
    claimAmount,
    cooldownSeconds,
    lastClaimAtSeconds,
    claimableAtSeconds: lastClaimAtSeconds + cooldownSeconds,
    paused,
  };
}

export async function readSettlementAssetStatus(
  config: SettlementConfig,
  account: Address,
  snapshot: SettlementAssetSnapshot,
): Promise<SettlementAssetStatus> {
  assertSettlementChain(config, snapshot.chainId);
  const { publicClient } = clientsForRead(config);
  const [tokenBalance, nativeBalance, allowance] = await Promise.all([
    publicClient.readContract({
      address: snapshot.tokenAddress,
      abi: testUSDABI,
      functionName: "balanceOf",
      args: [account],
    }),
    publicClient.getBalance({ address: account }),
    publicClient.readContract({
      address: snapshot.tokenAddress,
      abi: testUSDABI,
      functionName: "allowance",
      args: [account, snapshot.settlementAddress],
    }),
  ]);
  return {
    account,
    tokenBalance,
    nativeBalance,
    allowance,
  };
}

function clientsForRead(config: SettlementConfig) {
  const rpcURL = browserSettlementRPCURL(config);
  const chain = defineChain({
    id: config.chainId,
    name: config.environment === "LOCAL" ? "Vitlane Local" : "GIWA Sepolia",
    nativeCurrency: { name: "GIWA Test ETH", symbol: "ETH", decimals: 18 },
    rpcUrls: { default: { http: [rpcURL] } },
    testnet: true,
  });
  return {
    publicClient: createPublicClient({ chain, transport: http(rpcURL, { timeout: 10_000 }) }),
  };
}

export function browserSettlementRPCURL(
  config: SettlementConfig,
  browserURL = typeof window === "undefined" ? config.rpcUrl : window.location.href,
) {
  return config.environment === "LOCAL"
    ? localReviewRPCURL(config.rpcUrl, browserURL)
    : config.rpcUrl;
}

export async function approveExact(
  config: SettlementConfig,
  account: Address,
  amount: bigint,
  snapshotChainId: number,
  tokenAddress: Address,
  spender: Address,
): Promise<Hex> {
  assertSettlementChain(config, snapshotChainId);
  const { wallet, publicClient, chain } = clients(config);
  const hash = await wallet.writeContract({
    account,
    chain,
    address: tokenAddress,
    abi: testUSDABI,
    functionName: "approve",
    args: [spender, amount],
  });
  await publicClient.waitForTransactionReceipt({ hash });
  return hash;
}

export async function pay(
  config: SettlementConfig,
  account: Address,
  record: AuthorizationRecord,
): Promise<Hex> {
  assertSettlementChain(config, record.domain.chainId);
  const { wallet, chain } = clients(config);
  const a = record.authorization;
  return wallet.writeContract({
    account,
    chain,
    address: record.domain.verifyingContract,
    abi: settlementABI,
    functionName: "pay",
    args: [
      {
        payer: a.payer,
        token: a.token,
        passThroughAmount: BigInt(a.passThroughAmount),
        feeAmount: BigInt(a.feeAmount),
        orderHash: a.orderHash,
        merchantId: a.merchantId,
        merchantRegistryVersion: BigInt(a.merchantRegistryVersion),
        feeBps: a.feeBps,
        feeRecipient: a.feeRecipient,
        principalRecipient: a.principalRecipient,
        assuranceLevel: a.assuranceLevel,
        nonce: BigInt(a.nonce),
        payDeadline: BigInt(a.payDeadline),
        refundAfter: BigInt(a.refundAfter),
      },
      record.signature,
    ],
  });
}

export async function refundEscrow(
  config: SettlementConfig,
  account: Address,
  snapshotChainId: number,
  settlementAddress: Address,
  orderHash: Hex,
): Promise<Hex> {
  assertSettlementChain(config, snapshotChainId);
  const { wallet, chain } = clients(config);
  return wallet.writeContract({
    account,
    chain,
    address: settlementAddress,
    abi: settlementABI,
    functionName: "refund",
    args: [orderHash],
  });
}
