// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getAuthenticationCapabilities } from "../products/account/infra/accountApi";
import { ModeBanner, resetMerchantEffectModeCacheForTests } from "./ModeBanner";

vi.mock("../products/account/infra/accountApi", () => ({
  getAuthenticationCapabilities: vi.fn(),
}));

const capabilities = (merchantEffectMode: "SANDBOX" | "LIVE") => ({
  googleEnabled: false, localReviewEnabled: true, localReviewSeeded: true,
  localReviewProfiles: [], merchantEffectMode,
});

describe("ModeBanner", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    resetMerchantEffectModeCacheForTests();
    container = document.createElement("div");
    document.body.append(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    container.remove();
    vi.clearAllMocks();
  });

  it("Sandbox 배포는 주황 경고 배너로 반드시 인지된다", async () => {
    vi.mocked(getAuthenticationCapabilities).mockResolvedValue(capabilities("SANDBOX") as never);
    await act(async () => root.render(<ModeBanner />));
    await act(async () => Promise.resolve());
    const banner = container.querySelector(".vt-mode-banner");
    expect(banner?.classList.contains("is-sandbox")).toBe(true);
    expect(banner?.textContent).toContain("SANDBOX");
    expect(banner?.textContent).toContain("가치 이동은 실행되지 않습니다");
  });

  it("Live 배포는 초록 배너로 실지출을 명시한다", async () => {
    vi.mocked(getAuthenticationCapabilities).mockResolvedValue(capabilities("LIVE") as never);
    await act(async () => root.render(<ModeBanner />));
    await act(async () => Promise.resolve());
    const banner = container.querySelector(".vt-mode-banner");
    expect(banner?.classList.contains("is-live")).toBe(true);
    expect(banner?.textContent).toContain("실제 판매처 주문과 지출");
  });

  // LIVE를 SANDBOX로(또는 반대로) 오인 표기하지 않는다 — 모드를 모르면 비표시.
  it("모드를 확인하지 못하면 아무 배너도 그리지 않는다", async () => {
    vi.mocked(getAuthenticationCapabilities).mockRejectedValue(new Error("down"));
    await act(async () => root.render(<ModeBanner />));
    await act(async () => Promise.resolve());
    expect(container.querySelector(".vt-mode-banner")).toBeNull();
  });
});
