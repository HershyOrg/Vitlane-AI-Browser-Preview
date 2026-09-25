import { request } from "../../../shared/api/client";
import type { CurrentUser } from "../../../shared/api/types";
import { invariantContent } from "../../../shared/i18n";

export type WalletRegistrationStatus = "REGISTERED" | "DEREGISTERED";

export type WalletRecord = {
  id: string;
  userId: string;
  address: string;
  accountId: string;
  chainId: string;
  registrationStatus: WalletRegistrationStatus;
  currentOwnershipProofId?: string;
  isDefault: boolean;
  registeredAt: string;
  deregisteredAt?: string;
  createdAt: string;
  updatedAt: string;
};

export type WalletNextAction = {
  kind:
    | "NONE"
    | "REGISTER"
    | "REAUTHENTICATE"
    | "START_KYC"
    | "CHECK_RESULT"
    | "RECHECK"
    | "CONTACT_SUPPORT";
  url?: string;
  expiresAt?: string;
  label?: string;
};

export type WalletOwnershipProjection = {
  status: "VALID" | "REAUTH_REQUIRED" | "REVOKED";
  proofId?: string;
  verifiedAt?: string;
  validUntil?: string;
  nextAction?: WalletNextAction;
};

export type KYCActiveCase = KYCVerificationCase;

export type KYCCredential = {
  id: string;
  userId: string;
  walletId: string;
  caseId: string;
  level: "MOCK_DOJANG_VERIFIED" | "DOJANG_VERIFIED_ADDRESS";
  providerKind: "DOJANG" | "MOCK_DOJANG";
  providerVersion: string;
  externalEffect: "LIVE" | "SIMULATED";
  subjectAccountId: string;
  issuerRef: string;
  schemaRef: string;
  evidenceHash: string;
  issuedAt: string;
  validUntil: string;
  createdAt: string;
};

export type KYCEvidenceObservation = {
  id: string;
  credentialId: string;
  userId: string;
  walletId: string;
  status: "VALID" | "EXPIRED" | "REVOKED";
  evidenceHash: string;
  sourceVersion: number;
  observedAt: string;
  validUntil: string;
  recheckAfter: string;
};

export type WalletKYCProjection = {
  eligibility:
    | "NONE"
    | "PENDING"
    | "VALID"
    | "EXPIRED"
    | "REVOKED"
    | "REJECTED"
    | "RECHECK_REQUIRED";
  actionEligible: boolean;
  providerKind?: "DOJANG" | "MOCK_DOJANG";
  externalEffect?: "LIVE" | "SIMULATED";
  disclosure?: string;
  activeCase?: KYCActiveCase;
  credential?: KYCCredential;
  observation?: KYCEvidenceObservation;
  failureCode?: string;
  retryable?: boolean;
  nextAction?: WalletNextAction;
};

export type WalletProjection = {
  wallet: WalletRecord;
  ownership: WalletOwnershipProjection;
  kyc: WalletKYCProjection;
  actions: {
    canSetDefault: boolean;
    canDeregister: boolean;
    canReauthenticate: boolean;
    canStartKYC: boolean;
    canCheckKYC: boolean;
  };
};

export type WalletRegistrationAttempt = {
  id: string;
  address: string;
  accountId: string;
  chainId: string;
  status:
    | "PENDING"
    | "COMPLETED"
    | "EXPIRED"
    | "LOCKED"
    | "CANCELLED"
    | "SUPERSEDED";
  nonce: string;
  message: string;
  messageHash: string;
  expiresAt: string;
};

export type WalletOwnershipProof = {
  id: string;
  walletId: string;
  verifiedAt: string;
  validUntil: string;
  revokedAt?: string;
};

export type KYCVerificationCase = {
  id: string;
  userId: string;
  walletId: string;
  startedWithOwnershipProofId: string;
  requestedLevel: "ADVANCED_TEST_KYC";
  providerVersion: string;
  state:
    | "CREATED"
    | "PENDING_PROVIDER"
    | "VERIFIED"
    | "REJECTED"
    | "EXPIRED"
    | "CANCELLED";
  providerKind: "DOJANG" | "MOCK_DOJANG";
  externalEffect: "LIVE" | "SIMULATED";
  providerCaseRef?: string;
  credentialId?: string;
  failureCode?: string;
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
};

export type BuyerProfile = {
  id: string;
  profileKind: "TEST_PROFILE";
  label: string;
  fixtureKey: string;
  country: string;
  city: string;
  version: number;
  snapshotHash: string;
  containsRealPii: false;
  isDefault: boolean;
};

export type ShippingProfile = {
  id: string;
  label: string;
  country: string;
  maskedSummary: string;
  keyVersion: string;
  version: number;
  isDefault: boolean;
  retiredAt?: string;
  createdAt: string;
  updatedAt: string;
};

export type ShippingAddressInput = {
  label: string;
  recipientName: string;
  addressLine1: string;
  addressLine2: string;
  city: string;
  region: string;
  postalCode: string;
  country: string;
  phone: string;
};

export type RevealedShippingAddress = Omit<ShippingAddressInput, "label">;

export type PolicyAcceptance = {
  policyId: string;
  policyVersion: string;
  acceptedAt: string;
};

export type AccountOverview = {
  wallets: WalletProjection[];
  buyerProfiles: BuyerProfile[];
  shippingProfiles: ShippingProfile[];
  policyAcceptances: PolicyAcceptance[];
  assurancePolicy: Record<string, string>;
};

export type AuthenticationCapabilities = {
  googleEnabled: boolean;
  localReviewEnabled: boolean;
  localReviewSeeded: boolean;
  localReviewProfiles: LocalReviewProfile[];
  // 배포 전역 merchant effect 모드(ADR-0057 D3) — 모드 배너의 단일 소스.
  merchantEffectMode: "SANDBOX" | "LIVE";
};

export type LocalReviewProfile = {
  key: string;
  label: string;
  description: string;
  operator: boolean;
  resettable: boolean;
  startPath?: string;
};

export const TEST_SETTLEMENT_POLICY_VERSION = "2026-07-24";

export function createWalletRegistrationAttempt(
  address: string,
  chainId: string,
  clientOperationId: string,
): Promise<{ attempt: WalletRegistrationAttempt; replay?: boolean }> {
  return request("/api/v1/account/wallet-registration-attempts", {
    method: "POST",
    body: JSON.stringify({ address, chainId, clientOperationId }),
  });
}

export function completeWalletRegistrationAttempt(
  attemptId: string,
  nonce: string,
  signature: string,
  clientOperationId: string,
): Promise<{
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
  replay: boolean;
}> {
  return request(
    `/api/v1/account/wallet-registration-attempts/${encodeURIComponent(attemptId)}/complete`,
    {
      method: "POST",
      body: JSON.stringify({ nonce, signature, clientOperationId }),
    },
  );
}

export function getCurrentUser(): Promise<{ user: CurrentUser }> {
  return request("/api/v1/me");
}

export function getAuthenticationCapabilities(): Promise<AuthenticationCapabilities> {
  return request("/api/v1/auth/capabilities");
}

export function getAccountOverview(): Promise<{ account: AccountOverview }> {
  return request("/api/v1/account/overview");
}

export function deregisterWallet(walletId: string): Promise<void> {
  return request(
    `/api/v1/account/wallets/${encodeURIComponent(walletId)}/deregister`,
    { method: "POST", body: "{}" },
  );
}

export function saveDefaultShippingProfile(
  input: ShippingAddressInput,
): Promise<{ shippingProfile: ShippingProfile }> {
  return request("/api/v1/account/shipping-profiles/default", {
    method: "PUT",
    body: JSON.stringify(input),
  });
}

export function retireShippingProfile(profileId: string): Promise<void> {
  return request(`/api/v1/account/shipping-profiles/${profileId}`, {
    method: "DELETE",
  });
}

export function revealShippingProfile(
  profileId: string,
): Promise<{ address: RevealedShippingAddress }> {
  return request(
    `/api/v1/account/shipping-profiles/${encodeURIComponent(profileId)}/reveal`,
    { method: "POST", body: "{}" },
  );
}

export function startKYCVerification(
  walletId: string,
  ownershipProofId: string,
  clientOperationId: string,
): Promise<{ kycCase: KYCVerificationCase; replay: boolean }> {
  return request(
    `/api/v1/account/wallets/${encodeURIComponent(walletId)}/kyc-cases`,
    {
      method: "POST",
      body: JSON.stringify({ ownershipProofId, clientOperationId }),
    },
  );
}

export function checkKYC(
  caseId: string,
  clientOperationId: string,
): Promise<{
  kycCase: KYCVerificationCase;
  credential?: KYCCredential;
  observation?: KYCEvidenceObservation;
  replay: boolean;
}> {
  return request(
    `/api/v1/account/kyc-cases/${encodeURIComponent(caseId)}/check`,
    {
      method: "POST",
      body: JSON.stringify({ clientOperationId }),
    },
  );
}

export function acceptTestSettlementPolicy(
  policyVersion = TEST_SETTLEMENT_POLICY_VERSION,
): Promise<{ acceptance: PolicyAcceptance }> {
  return request("/api/v1/account/policy-acceptances/test-settlement", {
    method: "POST",
    body: JSON.stringify({ policyVersion }),
  });
}

export function logout(): Promise<void> {
  return request("/api/v1/auth/logout", { method: "POST", body: "{}" });
}

export function logoutAll(): Promise<void> {
  return request("/api/v1/auth/logout-all", { method: "POST", body: "{}" });
}

export function requestAccountDeletion(): Promise<void> {
  return request("/api/v1/account", {
    method: "DELETE",
    body: JSON.stringify({ confirmation: invariantContent("계정 삭제") }),
  });
}

export function createDevelopmentSession(
  profileKey?: string,
): Promise<{ user: CurrentUser }> {
  return request("/api/v1/dev/auth/session", {
    method: "POST",
    body: JSON.stringify(profileKey ? { profileKey } : {}),
  });
}

export function resetDevelopmentProfile(profileKey: string): Promise<void> {
  return request(
    `/api/v1/dev/auth/profiles/${encodeURIComponent(profileKey)}/reset`,
    { method: "POST", body: "{}" },
  );
}
