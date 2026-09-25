import { describe, expect, it } from "vitest";
import { selectTestAssetAddress } from "./TestAssetPanel";

describe("TEST asset target selection", () => {
  const defaultWallet = {
    wallet: {
      address: `0x${"a".repeat(40)}`,
      isDefault: true,
      registrationStatus: "REGISTERED",
    },
  };
  const nonDefaultWallet = {
    wallet: {
      address: `0x${"b".repeat(40)}`,
      isDefault: false,
      registrationStatus: "REGISTERED",
    },
  };

  it("승인 구매가 지정한 non-default payer를 기본 지갑보다 우선한다", () => {
    expect(selectTestAssetAddress(
      [defaultWallet, nonDefaultWallet],
      nonDefaultWallet.wallet.address,
    )).toBe(nonDefaultWallet.wallet.address);
  });

  it("구매 대상이 없을 때만 등록된 기본 지갑을 사용한다", () => {
    expect(selectTestAssetAddress(
      [defaultWallet, nonDefaultWallet],
      null,
    )).toBe(defaultWallet.wallet.address);
  });

  it("승인 구매의 immutable payer는 현재 등록 projection에서 사라져도 유지한다", () => {
    expect(selectTestAssetAddress(
      [defaultWallet],
      nonDefaultWallet.wallet.address,
    )).toBe(nonDefaultWallet.wallet.address);
  });
});
