// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useCurrentUser } from "../../account/app/useCurrentUser";
import { APIError } from "../../../shared/api/client";
import { resolveUnknownReservation } from "../../curation/intelligence/managedrunner/infra/adminUsageApi";
import {
  getActiveSessions,
  getOpsHealth,
  getOpsHeartbeats,
  getOpsSamples,
  revokeUserSessions,
  type OpsHealth,
} from "../infra/opsApi";
import { OperatorOpsPage } from "./OperatorOpsPage";

vi.mock("../../account/app/useCurrentUser", () => ({
  useCurrentUser: vi.fn(),
}));
vi.mock("../infra/opsApi", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../infra/opsApi")>()),
  getOpsHealth: vi.fn(),
  getOpsHeartbeats: vi.fn(),
  getOpsSamples: vi.fn(),
  getActiveSessions: vi.fn(),
  revokeUserSessions: vi.fn(),
}));
vi.mock("../../curation/intelligence/managedrunner/infra/adminUsageApi", async (importOriginal) => ({
  ...(await importOriginal<
    typeof import("../../curation/intelligence/managedrunner/infra/adminUsageApi")
  >()),
  resolveUnknownReservation: vi.fn(),
}));

const operator = {
  id: "operator",
  email: "operator@example.com",
  displayName: "Operator",
  createdAt: "2026-08-01T00:00:00Z",
  marketingAdmin: false,
  phase5Operator: true,
};

const health: OpsHealth = {
  status: "degraded",
  core: {
    status: "ready",
    reasonCodes: [],
    databasePool: {
      maxOpenConnections: 40,
      openConnections: 4,
      inUse: 1,
      idle: 3,
    },
  },
  degradedReasonCodes: ["GIWA_RPC_UNAVAILABLE"],
  settlement: {
    status: "degraded",
    workers: {},
    rpcSafeBlock: 1000,
    rpcFinalizedBlock: 990,
    finalizedCursor: 940,
    finalizedCursorSeen: true,
    outboxConflictCount: 0,
    reasonCodes: ["GIWA_RPC_UNAVAILABLE"],
  },
  http: {
    totalRequests: 100,
    totalServerErrors: 2,
    requestsLast5m: 40,
    serverErrorsLast5m: 1,
    requestsLast60m: 90,
    serverErrorsLast60m: 2,
  },
  observed: {
    piiAccess: { granted24h: 3, denied24h: 1, brokenChainLinks: 0 },
    piiLifecycle: { pendingDeletions: 0, staleKeyRows: 0 },
    intelligence: {
      attempts24h: 45,
      failed24h: 17,
      failureRate24h: 0.3778,
      effectUnknownOpen: 0,
    },
    costReservations: { unknownCount: 2, oldestUnknownAgeSeconds: 7200 },
    accounts: { activeSessions: 5, usersCreated24h: 0, activeUsers24h: 1 },
  },
  host: {
    generatedAt: "2026-08-08T11:55:00Z",
    diskUsedPct: 43,
    memoryUsedPct: 67,
    tlsDaysLeft: 55,
    services: {
      postgres: { state: "running", restartCount: 0 },
      vitlane: { state: "running", restartCount: 1 },
      caddy: { state: "running", restartCount: 0 },
    },
    ageSeconds: 240,
    stale: false,
  },
};

const heartbeats = {
  configured: true,
  fetchedAt: "2026-08-08T12:00:00Z",
  checks: [
    {
      name: "vitlane-ops-watch",
      slug: "vitlane-ops-watch",
      status: "up",
      lastPing: "2026-08-08T11:58:00Z",
      timeoutSeconds: 300,
      graceSeconds: 600,
    },
    {
      name: "vitlane-postgres-backup",
      slug: "vitlane-postgres-backup",
      status: "down",
      lastPing: "2026-08-06T09:00:00Z",
    },
  ],
};

const samples = {
  hours: 24,
  points: Array.from({ length: 12 }, (_, index) => ({
    sampledAt: `2026-08-08T${String(index).padStart(2, "0")}:00:00Z`,
    status: "ready",
    requests5m: 10 + index,
    serverErrors5m: index % 3,
    dbPoolInUse: 1,
    activeSessions: 4 + (index % 2),
    intelligenceFailureRate24h: 0.2,
    unknownReservations: 0,
    piiGranted24h: 1,
    piiDenied24h: 0,
    diskUsedPct: 42 + (index % 2),
    memoryUsedPct: 66,
  })),
};

const sessions = {
  count: 3,
  sessions: [
    {
      sessionId: "s-operator",
      userId: "u-operator",
      email: "operator@example.com",
      operator: true,
      createdAt: "2026-08-08T04:00:00Z",
      expiresAt: "2026-08-08T16:00:00Z",
    },
    {
      sessionId: "s-user",
      userId: "u-user",
      email: "user@example.com",
      operator: false,
      createdAt: "2026-08-07T04:00:00Z",
      expiresAt: "2026-08-14T04:00:00Z",
    },
    {
      sessionId: "s-user-mobile",
      userId: "u-user",
      email: "user@example.com",
      operator: false,
      createdAt: "2026-08-07T06:00:00Z",
      expiresAt: "2026-08-13T04:00:00Z",
    },
  ],
};

function mockOperator() {
  vi.mocked(useCurrentUser).mockReturnValue({
    user: operator,
    loading: false,
    error: null,
    refresh: vi.fn(),
    refreshAnalyticsIdentity: vi.fn(),
    logout: vi.fn(),
  });
  vi.mocked(getOpsHealth).mockResolvedValue(health);
  vi.mocked(getOpsHeartbeats).mockResolvedValue(heartbeats);
  vi.mocked(getOpsSamples).mockResolvedValue(samples);
  vi.mocked(getActiveSessions).mockResolvedValue(sessions);
}

async function renderPage() {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <MemoryRouter>
        <OperatorOpsPage />
      </MemoryRouter>,
    );
  });
  await act(async () => Promise.resolve());
  return { container, root };
}

function setValue(element: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const prototype = element instanceof HTMLTextAreaElement
    ? HTMLTextAreaElement.prototype
    : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(prototype, "value")?.set?.call(element, value);
  element.dispatchEvent(new Event("input", { bubbles: true }));
}

function clickByText(text: string) {
  const buttons = [...document.querySelectorAll<HTMLButtonElement>("button")];
  const button = buttons.find(
    (candidate) => candidate.textContent?.trim() === text,
  ) ?? buttons.find((candidate) => candidate.textContent?.includes(text));
  expect(button).toBeDefined();
  button?.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, button: 0 }));
  button?.click();
}

describe("OperatorOpsPage", () => {
  afterEach(() => {
    document.body.innerHTML = "";
    vi.clearAllMocks();
  });

  it("L1·L2·L3와 시계열을 상태 tab에, 세션을 사용자 단위로 표시한다", async () => {
    mockOperator();
    const { container, root } = await renderPage();

    // 종합 상태와 저하 사유
    expect(container.textContent).toContain("부분 저하");
    expect(container.textContent).toContain("GIWA_RPC_UNAVAILABLE");
    // L1 하트비트: up과 down이 함께 보인다
    expect(container.textContent).toContain("vitlane-ops-watch");
    expect(container.textContent).toContain("중단");
    // L2 readback: 오류율, intelligence, PII, UNKNOWN cost
    expect(container.textContent).toContain("finalized cursor 지연");
    expect(container.textContent).toContain("50 blocks");
    expect(container.textContent).toContain("37.8%");
    expect(container.textContent).toContain("UNKNOWN 건수");
    // L3 호스트
    expect(container.textContent).toContain("디스크 사용률");
    expect(container.textContent).toContain("43%");
    expect(container.textContent).toContain("55일 남음");
    // 시계열 차트가 그려진다
    expect(getOpsSamples).toHaveBeenCalledWith(24);
    expect(
      container.querySelectorAll(".product-ui-ops__trend svg").length,
    ).toBeGreaterThanOrEqual(6);
    await act(async () => clickByText("세션 2"));
    // 같은 사용자의 여러 session은 실제 revoke subject인 한 행으로 묶인다.
    expect(container.textContent).toContain("user@example.com");
    expect(container.textContent).toContain("2개");

    await act(async () => root.unmount());
  });

  it("기간 토글을 바꾸면 샘플을 다시 조회한다", async () => {
    mockOperator();
    const { container, root } = await renderPage();
    const week = container.querySelector<HTMLButtonElement>(
      '[aria-label="7일 조회"]',
    );
    expect(week).not.toBeNull();
    await act(async () => {
      week?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await act(async () => Promise.resolve());
    expect(getOpsSamples).toHaveBeenLastCalledWith(168);
    await act(async () => root.unmount());
  });

  it("세션 해지는 8자 이상 사유를 요구하고 감사 API를 호출한다", async () => {
    mockOperator();
    vi.mocked(revokeUserSessions).mockResolvedValue({
      userId: "u-user",
      revokedSessions: 1,
    });
    const { container, root } = await renderPage();

    await act(async () => clickByText("세션 2"));

    const revokeButtons = Array.from(
      container.querySelectorAll<HTMLButtonElement>("button"),
    ).filter((button) => button.textContent?.includes("세션 모두 해지"));
    expect(revokeButtons.length).toBe(2);
    await act(async () => {
      revokeButtons[1].dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    const textarea = container.querySelector<HTMLTextAreaElement>(
      '[aria-label="세션 해지 사유"]',
    );
    expect(textarea).not.toBeNull();
    const confirm = () =>
      Array.from(container.querySelectorAll<HTMLButtonElement>("button")).find(
        (button) => button.textContent?.includes("해지 실행"),
      );
    expect(confirm()?.disabled).toBe(true);

    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLTextAreaElement.prototype,
        "value",
      )?.set;
      setter?.call(textarea, "분실 기기 신고에 따른 선제 차단");
      textarea?.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(confirm()?.disabled).toBe(false);

    await act(async () => {
      confirm()?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await act(async () => Promise.resolve());
    expect(revokeUserSessions).toHaveBeenCalledWith(
      "u-user",
      "분실 기기 신고에 따른 선제 차단",
    );
    expect(container.textContent).toContain("세션 1개를 해지했습니다");
    await act(async () => root.unmount());
  });

  it("UNKNOWN reservation을 증거 참조와 감사 사유로 판정한다", async () => {
    mockOperator();
    vi.mocked(resolveUnknownReservation).mockResolvedValue({
      reservationId: "reservation-1",
      status: "SETTLED",
      amountMicros: 2_000,
      settledAmountMicros: 1_500,
      completedAt: "2026-08-08T04:00:00Z",
    });
    const { root } = await renderPage();

    await act(async () => clickByText("비용 판정"));
    const id = document.querySelector<HTMLInputElement>("#unknown-reservation-id");
    const amount = document.querySelector<HTMLInputElement>("#unknown-reservation-amount");
    const evidence = document.querySelector<HTMLInputElement>("#unknown-reservation-evidence");
    const reason = document.querySelector<HTMLTextAreaElement>("#unknown-reservation-reason");
    await act(async () => {
      if (id) setValue(id, "reservation-1");
      if (amount) setValue(amount, "1500");
      if (evidence) setValue(evidence, "provider-dashboard/event-42");
      if (reason) setValue(reason, "provider 원장에서 비용 발생을 확인했습니다.");
    });
    await act(async () => clickByText("비용 발생으로 판정"));
    await act(async () => Promise.resolve());

    expect(resolveUnknownReservation).toHaveBeenCalledWith({
      reservationId: "reservation-1",
      outcome: "SETTLED",
      reasonDetail: "provider 원장에서 비용 발생을 확인했습니다.",
      evidenceReference: "provider-dashboard/event-42",
      settledAmountMicros: 1_500,
    });
    expect(document.body.textContent).toContain("판정이 기록됐습니다");
    await act(async () => root.unmount());
  });

  it("일반 사용자는 운영 API를 호출하지 않는다", async () => {
    vi.mocked(useCurrentUser).mockReturnValue({
      user: { ...operator, phase5Operator: false },
      loading: false,
      error: null,
      refresh: vi.fn(),
      refreshAnalyticsIdentity: vi.fn(),
      logout: vi.fn(),
    });
    const { container, root } = await renderPage();
    expect(container.textContent).toContain("운영 현황을 볼 수 없습니다");
    expect(getOpsHealth).not.toHaveBeenCalled();
    expect(getActiveSessions).not.toHaveBeenCalled();
    await act(async () => root.unmount());
  });
});
