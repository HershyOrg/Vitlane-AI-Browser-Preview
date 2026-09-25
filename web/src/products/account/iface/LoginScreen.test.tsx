// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useCurrentUser } from "../app/useCurrentUser";
import {
  createDevelopmentSession,
  getAuthenticationCapabilities,
  resetDevelopmentProfile,
} from "../infra/accountApi";
import { LoginScreen } from "./LoginScreen";

vi.mock("../app/useCurrentUser", () => ({ useCurrentUser: vi.fn() }));
vi.mock("../infra/accountApi", () => ({
  createDevelopmentSession: vi.fn(),
  getAuthenticationCapabilities: vi.fn(),
  resetDevelopmentProfile: vi.fn(),
}));

describe("LoginScreen", () => {
  let container: HTMLDivElement | undefined;

  beforeEach(() => {
    // jsdom has no 2D canvas; ReededGlass then keeps its token background.
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(() => null);
  });

  afterEach(() => {
    container?.remove();
    container = undefined;
    vi.clearAllMocks();
  });

  it("브랜드와 구매 복귀 목적, Google 로그인을 간결하게 표시한다", async () => {
    vi.mocked(getAuthenticationCapabilities).mockResolvedValue({
      googleEnabled: true,
      localReviewEnabled: false,
      localReviewSeeded: false,
      localReviewProfiles: [],
      merchantEffectMode: "SANDBOX",
    });
    vi.mocked(useCurrentUser).mockReturnValue({
      user: null,
      loading: false,
      error: null,
      refresh: vi.fn(),
      refreshAnalyticsIdentity: vi.fn(),
      logout: vi.fn(),
    });
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(
        <MemoryRouter
          initialEntries={["/login?returnTo=%2Fpurchases"]}
        >
          <LoginScreen />
        </MemoryRouter>,
      );
    });

    expect(container.querySelectorAll("h1")).toHaveLength(1);
    expect(container.querySelector("h1")?.textContent).toBe("로그인");
    expect(container.querySelector(".vt-brand-mark")?.textContent).toContain(
      "Vitlane",
    );
    // ADR-0078: the lane sits behind still reeded glass instead of Beam lines.
    const glass = container.querySelector(".catalog-ui-login__glass");
    expect(glass?.classList.contains("vt-reeded-glass")).toBe(true);
    expect(glass?.getAttribute("aria-hidden")).toBe("true");
    expect(glass?.getAttribute("data-motion")).toBe("still");
    expect(glass?.querySelector("canvas")).not.toBeNull();
    expect(container.querySelector("[data-static-beam]")).toBeNull();
    expect(container.textContent).toContain(
      "로그인이 필요한 서비스입니다. 계속 진행하려면 로그인 해주세요.",
    );
    expect(container.textContent).not.toContain(
      "구매 요청과 진행 상태를 이어서 확인하세요",
    );
    const google = [...container.querySelectorAll("a")].find((link) =>
      link.textContent?.includes("Google로 계속"),
    );
    const loginBox = container.querySelector(".catalog-ui-login__content");
    expect(container.querySelector(".catalog-ui-login-shell")).not.toBeNull();
    expect(loginBox?.contains(container.querySelector("h1"))).toBe(true);
    expect(loginBox?.contains(google ?? null)).toBe(true);
    expect(google?.getAttribute("href")).toBe(
      "/api/v1/auth/google/start?returnTo=%2Fpurchases",
    );
    expect(container.textContent).not.toContain("Identity checkpoint");
    expect(container.textContent).not.toContain("세션 cookie");
    expect(container.textContent).not.toContain("지갑 연결과 서명");
    expect(container.textContent).not.toContain("로그인 이후 흐름");

    await act(async () => root.unmount());
  });

  it("로컬 검수에서는 Google 대신 준비된 TEST 계정을 주 진입점으로 표시한다", async () => {
    vi.mocked(getAuthenticationCapabilities).mockResolvedValue({
      googleEnabled: false,
      localReviewEnabled: true,
      localReviewSeeded: true,
      localReviewProfiles: [],
      merchantEffectMode: "SANDBOX",
    });
    vi.mocked(createDevelopmentSession).mockResolvedValue({
      user: {
        id: "local-review-user",
        email: "operator@example.com",
        displayName: "Vitlane Local Review",
        createdAt: "2026-07-26T00:00:00Z",
        marketingAdmin: true,
        phase5Operator: true,
      },
    });
    vi.mocked(useCurrentUser).mockReturnValue({
      user: null,
      loading: false,
      error: null,
      refresh: vi.fn(),
      refreshAnalyticsIdentity: vi.fn(),
      logout: vi.fn(),
    });
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(
        <MemoryRouter initialEntries={["/login?error=AUTH_PROVIDER_FAILED"]}>
          <LoginScreen />
        </MemoryRouter>,
      );
    });

    await vi.waitFor(() => {
      expect(container?.textContent).toContain(
        "고정된 샘플 데이터로 시작합니다",
      );
    });
    expect(container.textContent).toContain("로컬 TEST 계정으로 시작");
    expect(container.textContent).not.toContain("Google로 계속");

    await act(async () => root.unmount());
  });

  it("명시한 복귀 경로와 일치하는 로컬 TEST 프로필로 자동 진입한다", async () => {
    const refresh = vi.fn().mockResolvedValue(undefined);
    vi.mocked(getAuthenticationCapabilities).mockResolvedValue({
      googleEnabled: false,
      localReviewEnabled: true,
      localReviewSeeded: true,
      localReviewProfiles: [
        {
          key: "multi-product",
          label: "다중 상품 구매 시나리오",
          description: "완료된 Target 3개를 검수합니다.",
          operator: false,
          resettable: false,
          startPath: "/curations/curation-2",
        },
      ],
      merchantEffectMode: "SANDBOX",
    });
    vi.mocked(createDevelopmentSession).mockResolvedValue({
      user: {
        id: "local-review-user",
        email: "review@example.com",
        displayName: "다중 상품 구매 시나리오",
        createdAt: "2026-08-25T00:00:00Z",
        marketingAdmin: false,
        phase5Operator: false,
      },
    });
    vi.mocked(useCurrentUser).mockReturnValue({
      user: null,
      loading: false,
      error: null,
      refresh,
      refreshAnalyticsIdentity: vi.fn(),
      logout: vi.fn(),
    });
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(
        <MemoryRouter
          initialEntries={[
            "/login?returnTo=%2Fcurations%2Fcuration-2",
          ]}
        >
          <LoginScreen />
        </MemoryRouter>,
      );
    });

    await vi.waitFor(() => {
      expect(createDevelopmentSession).toHaveBeenCalledWith("multi-product");
      expect(refresh).toHaveBeenCalledOnce();
    });
    expect(createDevelopmentSession).toHaveBeenCalledTimes(1);

    await act(async () => root.unmount());
  });

  it("빈 일반 사용자와 빈 운영자 프로필을 분리하고 초기화를 제공한다", async () => {
    vi.mocked(getAuthenticationCapabilities).mockResolvedValue({
      googleEnabled: false,
      localReviewEnabled: true,
      localReviewSeeded: true,
      localReviewProfiles: [
        {
          key: "empty-user",
          label: "빈 일반 사용자",
          description: "지갑·배송지·KYC·구매 내역 없이 시작합니다.",
          operator: false,
          resettable: true,
        },
        {
          key: "empty-operator",
          label: "빈 운영자 사용자",
          description: "개인 데이터는 비어 있고 운영자 메뉴에 접근할 수 있습니다.",
          operator: true,
          resettable: true,
        },
      ],
      merchantEffectMode: "SANDBOX",
    });
    vi.mocked(resetDevelopmentProfile).mockResolvedValue();
    vi.mocked(useCurrentUser).mockReturnValue({
      user: null,
      loading: false,
      error: null,
      refresh: vi.fn(),
      refreshAnalyticsIdentity: vi.fn(),
      logout: vi.fn(),
    });
    vi.spyOn(window, "confirm").mockReturnValue(true);
    container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => {
      root.render(
        <MemoryRouter initialEntries={["/login"]}>
          <LoginScreen />
        </MemoryRouter>,
      );
    });

    await vi.waitFor(() => {
      expect(container?.textContent).toContain("빈 일반 사용자");
    });
    expect(container.textContent).toContain("빈 운영자 사용자");
    expect(container.textContent).toContain("운영자");
    const resetButtons = [...container.querySelectorAll("button")].filter(
      (button) =>
        button.textContent?.includes("신규 사용자 상태로 초기화"),
    );
    expect(resetButtons).toHaveLength(2);
    await act(async () => {
      resetButtons[0]?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(resetDevelopmentProfile).toHaveBeenCalledWith("empty-user");

    await act(async () => root.unmount());
  });
});
