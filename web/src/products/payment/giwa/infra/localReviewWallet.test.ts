// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";
import {
  installLocalReviewWallet,
  localReviewRPCURL,
} from "./localReviewWallet";
import { browserSettlementRPCURL } from "./wallet";

describe("localReviewRPCURL", () => {
  it("Windows 앱 브라우저에서는 Docker 전용 RPC host를 현재 localhost로 바꾼다", () => {
    expect(localReviewRPCURL(
      "http://host.docker.internal:28545",
      "http://127.0.0.1:18083/agencyOrder/order-1/payment",
    )).toBe("http://127.0.0.1:28545/");
  });

  it("Docker Firefox에서는 host.docker.internal 경로와 Anvil port를 유지한다", () => {
    expect(localReviewRPCURL(
      "http://host.docker.internal:28545",
      "http://host.docker.internal:18083/agencyOrder/order-1/payment",
    )).toBe("http://host.docker.internal:28545/");
  });

  it("public read와 receipt client도 Windows에서 도달 가능한 LOCAL RPC를 사용한다", () => {
    expect(browserSettlementRPCURL({
      environment: "LOCAL",
      rpcUrl: "http://host.docker.internal:28545",
    } as never, "http://127.0.0.1:18083/agencyOrder/order-1/payment")).toBe(
      "http://127.0.0.1:28545/",
    );
    expect(browserSettlementRPCURL({
      environment: "GIWA_SEPOLIA",
      rpcUrl: "https://rpc.example.test",
    } as never, "http://127.0.0.1:18083/agencyOrder/order-1/payment")).toBe(
      "https://rpc.example.test",
    );
  });
});

describe("installLocalReviewWallet", () => {
  const originalFetch = globalThis.fetch;

  afterEach(() => {
    globalThis.fetch = originalFetch;
    delete window.ethereum;
    delete window.__vitlaneReviewWallet;
    vi.restoreAllMocks();
  });

  it("로컬 검수에서는 불완전한 browser wallet stub을 결정적 TEST 지갑으로 교체한다", async () => {
    const brokenProvider = {
      request: vi.fn().mockRejectedValue(new Error("extension unavailable")),
    };
    window.ethereum = brokenProvider;
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ localReviewEnabled: true }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );

    await installLocalReviewWallet();

    expect(window.ethereum).not.toBe(brokenProvider);
    await expect(
      window.ethereum?.request({ method: "eth_accounts" }),
    ).resolves.toEqual(["0xa0Ee7A142d267C1f36714E4a8F75612F20a79720"]);
    await expect(
      window.ethereum?.request({ method: "eth_chainId" }),
    ).resolves.toBe("0x164ce");
  });

  it("Firefox E2E가 account/chain 변경과 provider 거절을 결정적으로 제어할 수 있다", async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ localReviewEnabled: true }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
    await installLocalReviewWallet();
    const controls = window.__vitlaneReviewWallet!;
    const accountsChanged = vi.fn();
    const chainChanged = vi.fn();
    window.ethereum?.on?.("accountsChanged", accountsChanged);
    window.ethereum?.on?.("chainChanged", chainChanged);

    controls.setAccount(1);
    controls.setChainId("0x1");
    expect(accountsChanged).toHaveBeenCalledWith([controls.accounts[1]]);
    expect(chainChanged).toHaveBeenCalledWith("0x1");

    controls.rejectNext("wallet_switchEthereumChain", 4001);
    await expect(window.ethereum?.request({
      method: "wallet_switchEthereumChain",
      params: [{ chainId: "0x164ce" }],
    })).rejects.toMatchObject({ code: 4001 });
    expect(controls.calls.wallet_switchEthereumChain).toBe(1);
  });

  it("local review RPC는 브라우저가 앱에 접속한 hostname으로 호출한다", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ localReviewEnabled: true }), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({
          settlement: {
            environment: "LOCAL",
            rpcUrl: "http://host.docker.internal:28545",
          },
        }), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ jsonrpc: "2.0", id: 1, result: "0xsigned" }), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      );
    globalThis.fetch = fetchMock;

    await installLocalReviewWallet();
    await expect(window.ethereum?.request({
      method: "personal_sign",
      params: ["0xmessage", "0xaccount"],
    })).resolves.toBe("0xsigned");

    expect(fetchMock).toHaveBeenNthCalledWith(
      3,
      new URL("http://host.docker.internal:28545").toString().replace(
        "host.docker.internal",
        window.location.hostname,
      ),
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("첫 capabilities 연결 실패 뒤 재시도해 검수 지갑을 설치한다", async () => {
    const fetchCapabilities = vi.fn()
      .mockRejectedValueOnce(new TypeError("proxy is starting"))
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ localReviewEnabled: true }), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      );
    const sleep = vi.fn().mockResolvedValue(undefined);

    await installLocalReviewWallet({ fetchCapabilities, sleep });

    expect(fetchCapabilities).toHaveBeenCalledTimes(2);
    expect(sleep).toHaveBeenCalledWith(100);
    expect(window.__vitlaneReviewWallet).toBeDefined();
    await expect(
      window.ethereum?.request({ method: "eth_accounts" }),
    ).resolves.toEqual(["0xa0Ee7A142d267C1f36714E4a8F75612F20a79720"]);
  });

  it("정상 응답이 local review를 끄면 재시도하거나 기존 지갑을 덮어쓰지 않는다", async () => {
    const installedProvider = {
      request: vi.fn().mockResolvedValue([]),
    };
    window.ethereum = installedProvider;
    const fetchCapabilities = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ localReviewEnabled: false }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
    const sleep = vi.fn().mockResolvedValue(undefined);

    await installLocalReviewWallet({ fetchCapabilities, sleep });

    expect(fetchCapabilities).toHaveBeenCalledTimes(1);
    expect(sleep).not.toHaveBeenCalled();
    expect(window.ethereum).toBe(installedProvider);
    expect(window.__vitlaneReviewWallet).toBeUndefined();
  });
});
