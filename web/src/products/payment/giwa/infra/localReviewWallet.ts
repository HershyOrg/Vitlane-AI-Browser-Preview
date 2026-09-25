import type {
  EIP1193Event,
  EIP1193Listener,
  EIP1193Provider,
} from "../../../account/infra/eip1193Provider";
import { localizeFixedCopy } from "../../../../shared/i18n";

const localReviewAccounts = [
  "0xa0Ee7A142d267C1f36714E4a8F75612F20a79720",
  "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
] as const;

type RPCEnvelope = {
  result?: unknown;
  error?: { code?: number; message?: string; data?: unknown };
};

type QueuedProviderFailure = {
  code: number;
  message: string;
};

type LocalReviewWalletInstallOptions = {
  fetchCapabilities?: typeof fetch;
  sleep?: (milliseconds: number) => Promise<void>;
};

const capabilityRetryDelaysMilliseconds = [100, 250, 500, 1_000] as const;

export function localReviewRPCURL(
  configuredRPCURL: string,
  browserURL: string,
) {
  const rpcURL = new URL(configuredRPCURL);
  const browserLocation = new URL(browserURL);

  // The local-review server is shared by two browser locations: Docker Firefox
  // reaches it through host.docker.internal, while the Windows app browser uses
  // 127.0.0.1. Keep the configured Anvil port but route it through the hostname
  // that already reached the app. This adapter is installed only after the
  // server explicitly reports localReviewEnabled and SETTLEMENT_ENV=LOCAL.
  rpcURL.hostname = browserLocation.hostname;
  return rpcURL.toString();
}

export type LocalReviewWalletControls = {
  accounts: readonly string[];
  calls: Readonly<Record<string, number>>;
  activeAccount(): string;
  chainId(): string;
  setAccount(accountOrIndex: string | number): void;
  setChainId(chainId: string): void;
  rejectNext(method: string, code: number, message?: string): void;
  resetCalls(): void;
};

declare global {
  interface Window {
    __vitlaneReviewWallet?: LocalReviewWalletControls;
  }
}

export async function installLocalReviewWallet(
  options: LocalReviewWalletInstallOptions = {},
) {
  if (
    typeof window === "undefined" ||
    !(
      import.meta.env.DEV ||
      import.meta.env.VITE_ALLOW_DEV_AUTH_UI === "true"
    )
  ) {
    return;
  }
  const capabilities = await loadLocalReviewCapabilities(options);
  if (capabilities?.localReviewEnabled !== true) return;

  // 로컬 검수는 설치된 extension이나 불완전한 browser stub에 좌우되지 않는다.
  // production build에는 이 adapter가 활성화되지 않으며, local review에서만
  // Anvil unlocked TEST EOA를 결정적으로 사용한다.
  let requestID = 0;
  let rpcURL = "";
  async function settlementRPCURL() {
    if (rpcURL) return rpcURL;
    const response = await fetch("/api/v1/settlement/config", {
      credentials: "same-origin",
      cache: "no-store",
    });
    if (!response.ok) {
      throw new Error(localizeFixedCopy("We couldn't load the local TEST chain configuration.", "로컬 TEST 체인 설정을 불러오지 못했습니다."));
    }
    const payload = await response.json();
    if (
      payload?.settlement?.environment !== "LOCAL" ||
      typeof payload?.settlement?.rpcUrl !== "string"
    ) {
      throw new Error(localizeFixedCopy("The review wallet is available only on the local TEST chain.", "로컬 TEST 체인이 아닌 환경에서는 검수 지갑을 사용할 수 없습니다."));
    }
    rpcURL = localReviewRPCURL(
      payload.settlement.rpcUrl,
      window.location.href,
    );
    return rpcURL;
  }

  let activeAccount = localReviewAccounts[0] as string;
  let activeChainId = "0x164ce";
  const listeners = new Map<EIP1193Event, Set<EIP1193Listener>>();
  const calls: Record<string, number> = {};
  const queuedFailures = new Map<string, QueuedProviderFailure>();

  function emit(event: EIP1193Event, value: unknown) {
    for (const listener of listeners.get(event) ?? []) listener(value);
  }

  const provider: EIP1193Provider = {
    async request({ method, params = [] }) {
      calls[method] = (calls[method] ?? 0) + 1;
      const queuedFailure = queuedFailures.get(method);
      if (queuedFailure) {
        queuedFailures.delete(method);
        const error = new Error(queuedFailure.message) as Error & {
          code?: number;
        };
        error.code = queuedFailure.code;
        throw error;
      }
      if (method === "eth_requestAccounts" || method === "eth_accounts") {
        return [activeAccount];
      }
      if (method === "wallet_switchEthereumChain") {
        const requested = (
          params[0] as { chainId?: string } | undefined
        )?.chainId;
        if (requested && requested !== activeChainId) {
          activeChainId = requested;
          emit("chainChanged", activeChainId);
        }
        return null;
      }
      if (method === "wallet_addEthereumChain") return null;
      if (method === "eth_chainId") return activeChainId;

      const response = await fetch(await settlementRPCURL(), {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          jsonrpc: "2.0",
          id: ++requestID,
          method,
          params,
        }),
      });
      const envelope = await response.json() as RPCEnvelope;
      if (envelope.error) {
        const error = new Error(
          envelope.error.message ?? `Local RPC ${method} failed`,
        ) as Error & { code?: number; data?: unknown };
        error.code = envelope.error.code;
        error.data = envelope.error.data;
        throw error;
      }
      return envelope.result;
    },
    on(event, listener) {
      const current = listeners.get(event) ?? new Set<EIP1193Listener>();
      current.add(listener);
      listeners.set(event, current);
    },
    removeListener(event, listener) {
      listeners.get(event)?.delete(listener);
    },
  };
  window.ethereum = provider;
  window.__vitlaneReviewWallet = {
    accounts: localReviewAccounts,
    calls,
    activeAccount: () => activeAccount,
    chainId: () => activeChainId,
    setAccount(accountOrIndex) {
      const next = typeof accountOrIndex === "number"
        ? localReviewAccounts[accountOrIndex]
        : accountOrIndex;
      if (!next || next === activeAccount) return;
      activeAccount = next;
      emit("accountsChanged", [activeAccount]);
    },
    setChainId(chainId) {
      if (chainId === activeChainId) return;
      activeChainId = chainId;
      emit("chainChanged", activeChainId);
    },
    rejectNext(method, code, message = `Local review ${method} rejected`) {
      queuedFailures.set(method, { code, message });
    },
    resetCalls() {
      for (const method of Object.keys(calls)) delete calls[method];
    },
  };
}

async function loadLocalReviewCapabilities({
  fetchCapabilities = fetch,
  sleep = sleepForCapabilityRetry,
}: LocalReviewWalletInstallOptions) {
  for (let attempt = 0; attempt <= capabilityRetryDelaysMilliseconds.length; attempt += 1) {
    try {
      const response = await fetchCapabilities("/api/v1/auth/capabilities", {
        credentials: "same-origin",
        cache: "no-store",
      });
      if (response.ok) return await response.json();
      if (response.status < 500) return null;
    } catch {
      // A local-review Windows proxy can accept the page navigation just before
      // the WSL Server route is ready. Only this DEV-only bootstrap retries.
    }
    const delay = capabilityRetryDelaysMilliseconds[attempt];
    if (delay === undefined) return null;
    await sleep(delay);
  }
  return null;
}

function sleepForCapabilityRetry(milliseconds: number) {
  return new Promise<void>((resolve) => window.setTimeout(resolve, milliseconds));
}
