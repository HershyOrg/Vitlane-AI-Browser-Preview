import { request } from "../../../shared/api/client";

// Shapes mirror server/cmd/vitlane's ops health/heartbeats/samples handlers
// (ADR-0040 §6). Everything here is PII-free aggregate data.

export type OpsDatabasePool = {
  maxOpenConnections: number;
  openConnections: number;
  inUse: number;
  idle: number;
};

export type OpsWorkerHeartbeat = {
  lastAttemptAt?: string;
  lastSuccessAt?: string;
  lastErrorCode?: string;
};

export type OpsSettlement = {
  status: string;
  workers: Record<string, OpsWorkerHeartbeat>;
  rpcSafeBlock: number;
  rpcFinalizedBlock: number;
  finalizedCursor: number;
  finalizedCursorSeen: boolean;
  outboxConflictCount: number;
  reasonCodes: string[];
};

export type OpsHostService = {
  state: string;
  restartCount: number;
};

export type OpsHostFacts = {
  generatedAt: string;
  diskUsedPct: number;
  memoryUsedPct: number;
  tlsDaysLeft: number | null;
  services: Record<string, OpsHostService>;
  ageSeconds: number;
  stale: boolean;
};

export type OpsHealth = {
  status: "ready" | "degraded" | "unavailable";
  core: {
    status: string;
    reasonCodes: string[];
    databasePool: OpsDatabasePool;
  };
  degradedReasonCodes: string[];
  settlement?: OpsSettlement;
  http: {
    totalRequests: number;
    totalServerErrors: number;
    requestsLast5m: number;
    serverErrorsLast5m: number;
    requestsLast60m: number;
    serverErrorsLast60m: number;
  };
  observed?: {
    piiAccess: {
      granted24h: number;
      denied24h: number;
      brokenChainLinks: number;
    };
    piiLifecycle: {
      pendingDeletions: number;
      staleKeyRows: number;
    };
    intelligence: {
      attempts24h: number;
      failed24h: number;
      failureRate24h: number;
      effectUnknownOpen: number;
    };
    costReservations: {
      unknownCount: number;
      oldestUnknownAgeSeconds: number;
    };
    accounts: {
      activeSessions: number;
      usersCreated24h: number;
      activeUsers24h: number;
    };
  };
  host?: OpsHostFacts;
};

export type HeartbeatCheck = {
  name: string;
  slug: string;
  status: string;
  lastPing?: string;
  timeoutSeconds?: number;
  graceSeconds?: number;
  schedule?: string;
};

export type OpsHeartbeats = {
  configured: boolean;
  fetchedAt?: string;
  checks: HeartbeatCheck[];
};

export type OpsSamplePoint = {
  sampledAt: string;
  status: string;
  requests5m: number;
  serverErrors5m: number;
  dbPoolInUse: number;
  activeSessions?: number;
  intelligenceFailureRate24h?: number;
  unknownReservations?: number;
  piiGranted24h?: number;
  piiDenied24h?: number;
  cursorLagBlocks?: number;
  diskUsedPct?: number;
  memoryUsedPct?: number;
};

export type OpsSamples = {
  hours: number;
  points: OpsSamplePoint[];
};

export type ActiveSession = {
  sessionId: string;
  userId: string;
  email: string;
  operator: boolean;
  createdAt: string;
  expiresAt: string;
};

export type ActiveSessions = {
  sessions: ActiveSession[];
  count: number;
};

export type LiveControlGate = {
  staticAllowed: boolean;
  runtimeKilled: boolean;
  effective: boolean;
};

export type LiveControl = {
  version: number;
  orderIssue: LiveControlGate;
  paypalMoney: LiveControlGate;
  merchantEffect: LiveControlGate;
  changedAt: string;
  changedBy: string;
  reason: string;
};

export type LiveControlScope = "ALL_NEW_LIVE_EFFECTS" | "LIVE_ORDER_ISSUE" |
  "PAYPAL_LIVE_MONEY_EFFECT" | "LIVE_MERCHANT_EFFECT";

export function getOpsHealth(): Promise<OpsHealth> {
  return request("/api/v1/admin/ops/health", {}, { acceptedStatuses: [503] });
}

export function getOpsHeartbeats(): Promise<OpsHeartbeats> {
  return request("/api/v1/admin/ops/heartbeats");
}

export function getOpsSamples(hours: number): Promise<OpsSamples> {
  return request(`/api/v1/admin/ops/samples?hours=${hours}`);
}

export function getActiveSessions(): Promise<ActiveSessions> {
  return request("/api/v1/admin/ops/sessions");
}

export function getLiveControl(): Promise<{ schemaVersion: string; liveControl: LiveControl }> {
  return request("/api/v1/admin/liveControl");
}

export function killLiveControl(input: {
  scope: LiveControlScope;
  confirmation: string;
  reason: string;
  expectedVersion: number;
}): Promise<{ schemaVersion: string; liveControl: LiveControl }> {
  return request("/api/v1/admin/liveControl/kill", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export function revokeUserSessions(
  userId: string,
  reasonDetail: string,
): Promise<{ userId: string; revokedSessions: number }> {
  return request(
    `/api/v1/admin/ops/users/${encodeURIComponent(userId)}/session-revocations`,
    {
      method: "POST",
      body: JSON.stringify({ reasonDetail }),
    },
  );
}
