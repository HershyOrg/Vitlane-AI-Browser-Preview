import { request } from "../../../../shared/api/client";

export type SettlementConfig = {
  environment: "LOCAL" | "GIWA_TESTNET";
  chainId: number;
  chainCaip2: string;
  rpcUrl: string;
  explorerUrl: string;
  tokenAddress: `0x${string}`;
  faucetAddress: `0x${string}`;
  settlementAddress: `0x${string}`;
  tokenSymbol: "tVITUSD";
  tokenDecimals: 6;
  feeBps: 100;
  feeRecipient: `0x${string}`;
  claimAmountBaseUnits: string;
};

// v2 (ADR-0050): 서명은 {passThroughAmount, feeAmount} 정확값을 고정한다.
// payer가 지불하는 총액은 두 값의 합이다.
export type PaymentAuthorization = {
  payer: `0x${string}`;
  token: `0x${string}`;
  passThroughAmount: string;
  feeAmount: string;
  orderHash: `0x${string}`;
  merchantId: `0x${string}`;
  merchantRegistryVersion: number;
  feeBps: number;
  feeRecipient: `0x${string}`;
  principalRecipient: `0x${string}`;
  assuranceLevel: `0x${string}`;
  nonce: string;
  payDeadline: number;
  refundAfter: number;
};

export type AuthorizationRecord = {
  id: string;
  agencyOrderId: string;
  authorization: PaymentAuthorization;
  domain: {
    name: "Vitlane Settlement";
    version: "2";
    chainId: number;
    verifyingContract: `0x${string}`;
  };
  signer: `0x${string}`;
  typedDataHash: `0x${string}`;
  signature: `0x${string}`;
  createdAt: string;
};

export type SettlementPayment = {
  id: string;
  agencyOrderId: string;
  orderHash: `0x${string}`;
  chainId: number;
  payer: `0x${string}`;
  settlementAddress: `0x${string}`;
  amountBaseUnits: string;
  claimTxHash?: `0x${string}`;
  approveTxHash?: `0x${string}`;
  state: string;
  payTxHash?: `0x${string}`;
  completeTxHash?: `0x${string}`;
  refundTxHash?: `0x${string}`;
  safeBlock?: number;
  finalizedBlock?: number;
  lastReasonCode?: string;
  observationExhaustedAt?: string;
  createdAt: string;
  updatedAt: string;
};

export function getSettlementConfig(): Promise<{ settlement: SettlementConfig }> {
  return request("/api/v1/settlement/config");
}

// authorizedTotalBaseUnits는 payer가 승인·지불하는 총액(passThrough + fee)이다.
export function authorizedTotalBaseUnits(authorization: PaymentAuthorization): bigint {
  return BigInt(authorization.passThroughAmount) + BigInt(authorization.feeAmount);
}
