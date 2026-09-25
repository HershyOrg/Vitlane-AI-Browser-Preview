// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useCurrentUser } from "../../../../account/app/useCurrentUser";
import { getServerAPIUsage } from "../infra/adminUsageApi";
import { OperatorAPIUsagePage } from "./OperatorAPIUsagePage";

vi.mock("../../../../account/app/useCurrentUser", () => ({
  useCurrentUser: vi.fn(),
}));
vi.mock("../infra/adminUsageApi", () => ({
  getServerAPIUsage: vi.fn(),
}));

const operator = {
  id: "operator",
  email: "operator@example.com",
  displayName: "Operator",
  createdAt: "2026-07-31T00:00:00Z",
  marketingAdmin: false,
  phase5Operator: true,
};

const usage = {
  enabled: true,
  timezone: "UTC" as const,
  from: "2026-07-30",
  through: "2026-07-31",
  dailyLimitMicros: 10_000_000,
  admissionLimitMicros: 8_000_000,
  totals: {
    inputTokens: 15_000,
    outputTokens: 5_000,
    totalTokens: 20_000,
    requestCount: 8,
    reservedMicros: 250_000,
    settledMicros: 1_500_000,
    spentMicros: 1_750_000,
    usagePercent: 8.75,
  },
  days: [
    {
      usageDate: "2026-07-30",
      inputTokens: 5_000,
      outputTokens: 2_000,
      totalTokens: 7_000,
      requestCount: 3,
      reservedMicros: 0,
      settledMicros: 500_000,
      spentMicros: 500_000,
      usagePercent: 5,
    },
    {
      usageDate: "2026-07-31",
      inputTokens: 10_000,
      outputTokens: 3_000,
      totalTokens: 13_000,
      requestCount: 5,
      reservedMicros: 250_000,
      settledMicros: 1_000_000,
      spentMicros: 1_250_000,
      usagePercent: 12.5,
    },
  ],
};

async function renderPage() {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <MemoryRouter>
        <OperatorAPIUsagePage />
      </MemoryRouter>,
    );
  });
  await act(async () => Promise.resolve());
  return { container, root };
}

describe("OperatorAPIUsagePage", () => {
  afterEach(() => {
    document.body.innerHTML = "";
    vi.clearAllMocks();
  });

  it("서버 일별 토큰·비용·예산 점유를 함께 표시한다", async () => {
    vi.mocked(useCurrentUser).mockReturnValue({
      user: operator,
      loading: false,
      error: null,
      refresh: vi.fn(),
      refreshAnalyticsIdentity: vi.fn(),
      logout: vi.fn(),
    });
    vi.mocked(getServerAPIUsage).mockResolvedValue(usage);

    const { container, root } = await renderPage();

    expect(getServerAPIUsage).toHaveBeenCalledWith(30);
    expect(container.textContent).toContain("오늘 토큰");
    expect(container.textContent).toContain("13,000");
    expect(container.textContent).toContain("$1.00");
    expect(container.textContent).toContain("12.5%");
    expect(container.querySelector('[role="img"]')).not.toBeNull();
    expect(container.textContent).toContain("일별 원장");
    // The browser E2E counts the daily ledger rows under this class; other
    // tables on the page (the research round summary) must not be counted.
    expect(
      container.querySelectorAll(".product-ui-api-usage__ledger tbody tr").length,
    ).toBe(usage.days.length);

    await act(async () => root.unmount());
  });

  it("조회 기간 토글을 바꾸면 서버 시계열을 다시 조회한다", async () => {
    vi.mocked(useCurrentUser).mockReturnValue({
      user: operator,
      loading: false,
      error: null,
      refresh: vi.fn(),
      refreshAnalyticsIdentity: vi.fn(),
      logout: vi.fn(),
    });
    vi.mocked(getServerAPIUsage).mockResolvedValue(usage);

    const { container, root } = await renderPage();
    const sevenDays = container.querySelector<HTMLButtonElement>(
      '[aria-label="7일 조회"]',
    );
    expect(sevenDays).not.toBeNull();
    await act(async () => {
      sevenDays?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    await act(async () => Promise.resolve());

    expect(getServerAPIUsage).toHaveBeenLastCalledWith(7);
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
    expect(container.textContent).toContain("API 사용량을 볼 수 없습니다");
    expect(getServerAPIUsage).not.toHaveBeenCalled();
    await act(async () => root.unmount());
  });
});
