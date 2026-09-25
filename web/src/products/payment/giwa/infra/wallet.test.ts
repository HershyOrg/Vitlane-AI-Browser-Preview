import { describe, expect, it } from "vitest";
import type { SettlementConfig } from "./settlementApi";
import { assertSettlementChain } from "./wallet";

const config: SettlementConfig = {
  environment: "LOCAL",
  chainId: 31_337,
  chainCaip2: "eip155:31337",
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
};

describe("settlement snapshot chain guard", () => {
  it("accepts the immutable snapshot chain", () => {
    expect(() => assertSettlementChain(config, 31_337)).not.toThrow();
  });

  it("rejects config rotation before a wallet transaction can be built", () => {
    expect(() => assertSettlementChain(config, 91_342)).toThrow(
      "승인 snapshot의 chain 91342과 현재 결제 chain 31337이 다릅니다.",
    );
  });
});
