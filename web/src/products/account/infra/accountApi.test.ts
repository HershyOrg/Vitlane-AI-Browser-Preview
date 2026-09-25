// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  checkKYC,
  completeWalletRegistrationAttempt,
  createWalletRegistrationAttempt,
  deregisterWallet,
  startKYCVerification,
} from "./accountApi";

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("account wallet API contract", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async () =>
        new Response(JSON.stringify({ replay: false }), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      ),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("creates a registration attempt with a stable client operation ID", async () => {
    await createWalletRegistrationAttempt(
      "0x1111111111111111111111111111111111111111",
      "eip155:91342",
      "wallet-registration-attempt:operation-1",
    );

    expect(fetch).toHaveBeenCalledWith(
      "/api/v1/account/wallet-registration-attempts",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          address: "0x1111111111111111111111111111111111111111",
          chainId: "eip155:91342",
          clientOperationId: "wallet-registration-attempt:operation-1",
        }),
      }),
    );
  });

  it("completes the same attempt with nonce, signature and a separate operation ID", async () => {
    await completeWalletRegistrationAttempt(
      "attempt/1",
      "nonce-1",
      "0xsignature",
      "wallet-registration-complete:operation-1",
    );

    expect(fetch).toHaveBeenCalledWith(
      "/api/v1/account/wallet-registration-attempts/attempt%2F1/complete",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          nonce: "nonce-1",
          signature: "0xsignature",
          clientOperationId: "wallet-registration-complete:operation-1",
        }),
      }),
    );
  });

  it("starts and checks KYC with proof-bound stable operations", async () => {
    await startKYCVerification(
      "wallet/1",
      "proof-1",
      "kyc-start:operation-1",
    );
    await checkKYC("case/1", "kyc-check:operation-1");

    expect(fetch).toHaveBeenNthCalledWith(
      1,
      "/api/v1/account/wallets/wallet%2F1/kyc-cases",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          ownershipProofId: "proof-1",
          clientOperationId: "kyc-start:operation-1",
        }),
      }),
    );
    expect(fetch).toHaveBeenNthCalledWith(
      2,
      "/api/v1/account/kyc-cases/case%2F1/check",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          clientOperationId: "kyc-check:operation-1",
        }),
      }),
    );
  });

  it("uses the explicit current Wallet deregistration route", async () => {
    await deregisterWallet("wallet/2");

    expect(fetch).toHaveBeenCalledWith(
      "/api/v1/account/wallets/wallet%2F2/deregister",
      expect.objectContaining({ method: "POST", body: "{}" }),
    );
  });
});
