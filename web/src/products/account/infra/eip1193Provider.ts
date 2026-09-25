import { stringToHex } from "viem";
import type { SettlementConfig } from "../../payment/giwa/infra/settlementApi";
import { localizeFixedCopy } from "../../../shared/i18n";

export type EIP1193Event = "accountsChanged" | "chainChanged";
export type EIP1193Listener = (...args: unknown[]) => void;

export type EIP1193Provider = {
  request(args: { method: string; params?: unknown[] }): Promise<unknown>;
  on?(event: EIP1193Event, listener: EIP1193Listener): void;
  removeListener?(event: EIP1193Event, listener: EIP1193Listener): void;
};

declare global {
  interface Window {
    ethereum?: EIP1193Provider;
  }
}

export class BrowserWalletError extends Error {
  readonly code: string;
  readonly causeCode?: number;

  constructor(code: string, message: string, causeCode?: number) {
    super(message);
    this.code = code;
    this.causeCode = causeCode;
  }
}

export function browserWalletProvider(provider = window.ethereum) {
  if (!provider) {
    throw new BrowserWalletError(
      "WALLET_PROVIDER_MISSING",
      localizeFixedCopy("No supported browser wallet is available on this device. Try again in a desktop browser with a wallet installed.", "이 기기에는 지원되는 브라우저 지갑이 없습니다. 지갑이 설치된 데스크톱 브라우저에서 다시 시도해 주세요."),
    );
  }
  return provider;
}

export async function switchToSettlementChain(
  provider: EIP1193Provider,
  config: SettlementConfig,
) {
  const chainId = `0x${config.chainId.toString(16)}`;
  try {
    await provider.request({
      method: "wallet_switchEthereumChain",
      params: [{ chainId }],
    });
    return;
  } catch (caught) {
    const code = providerErrorCode(caught);
    if (code === 4001) {
      throw new BrowserWalletError(
        "WALLET_CHAIN_SWITCH_REJECTED",
        localizeFixedCopy("The wallet network switch was cancelled. Approve the switch to GIWA to register.", "지갑 네트워크 전환이 취소되었습니다. 등록하려면 GIWA 네트워크 전환을 승인해 주세요."),
        code,
      );
    }
    if (code !== 4902) throw caught;
  }

  await provider.request({
    method: "wallet_addEthereumChain",
    params: [{
      chainId,
      chainName: config.environment === "LOCAL" ? "Vitlane Local" : "GIWA Sepolia",
      nativeCurrency: {
        name: "GIWA Test ETH",
        symbol: "ETH",
        decimals: 18,
      },
      rpcUrls: [config.rpcUrl],
      blockExplorerUrls: config.explorerUrl ? [config.explorerUrl] : undefined,
    }],
  });
  await provider.request({
    method: "wallet_switchEthereumChain",
    params: [{ chainId }],
  });
}

export async function requestWalletAccount(provider: EIP1193Provider) {
  const accounts = await provider.request({
    method: "eth_requestAccounts",
  }) as unknown;
  const account = Array.isArray(accounts) && typeof accounts[0] === "string"
    ? accounts[0]
    : "";
  if (!account) {
    throw new BrowserWalletError(
      "WALLET_ACCOUNT_MISSING",
      localizeFixedCopy("Select the wallet account to register.", "등록할 지갑 계정을 선택해 주세요."),
    );
  }
  return account;
}

export async function signWalletRegistrationMessage(
  provider: EIP1193Provider,
  address: string,
  message: string,
) {
  try {
    const signature = await provider.request({
      method: "personal_sign",
      params: [stringToHex(message), address],
    });
    if (typeof signature !== "string") {
      throw new BrowserWalletError(
        "WALLET_SIGNATURE_INVALID",
        localizeFixedCopy("The wallet did not return a valid signature.", "지갑이 올바른 서명을 반환하지 않았습니다."),
      );
    }
    return signature;
  } catch (caught) {
    if (providerErrorCode(caught) === 4001) {
      throw new BrowserWalletError(
        "WALLET_SIGNATURE_REJECTED",
        localizeFixedCopy("Wallet-registration signing was cancelled. Trying again resumes the same request.", "지갑 등록 서명이 취소되었습니다. 다시 시도하면 같은 등록 요청을 이어갑니다."),
        4001,
      );
    }
    throw caught;
  }
}

export function watchWalletProvider(
  provider: EIP1193Provider,
  listener: () => void,
) {
  const onAccountsChanged: EIP1193Listener = () => listener();
  const onChainChanged: EIP1193Listener = () => listener();
  provider.on?.("accountsChanged", onAccountsChanged);
  provider.on?.("chainChanged", onChainChanged);
  return () => {
    provider.removeListener?.("accountsChanged", onAccountsChanged);
    provider.removeListener?.("chainChanged", onChainChanged);
  };
}

export function providerErrorCode(caught: unknown) {
  if (
    typeof caught === "object"
    && caught !== null
    && "code" in caught
    && typeof caught.code === "number"
  ) {
    return caught.code;
  }
  return undefined;
}
