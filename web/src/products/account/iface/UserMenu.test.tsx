// @vitest-environment jsdom

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppearanceProvider } from "../../../shared/ui";
import { LocaleProvider } from "../../../shared/i18n";
import { UserMenu } from "./UserMenu";

vi.mock("../app/useCurrentUser", () => ({
  useCurrentUser: () => ({
    user: {
      id: "user-1",
      email: "user@vitlane.test",
      displayName: "테스트 사용자",
      createdAt: "2026-07-31T00:00:00Z",
      marketingAdmin: false,
      phase5Operator: false,
    },
    logout: vi.fn(),
  }),
}));

describe("UserMenu", () => {
  let root: Root | undefined;
  let host: HTMLDivElement | undefined;

  beforeEach(() => {
    document.documentElement.className = "";
    delete document.documentElement.dataset.theme;
    delete document.documentElement.dataset.accent;
    window.localStorage.clear();
  });

  afterEach(async () => {
    if (root) {
      await act(async () => root?.unmount());
    }
    host?.remove();
    root = undefined;
    host = undefined;
    document.body.innerHTML = "";
  });

  it("sidebar 하단 프로필에서 패널 기능과 로그아웃을 제공한다", async () => {
    await renderMenu();
    expect(
      document.querySelector(".shell-user-menu__avatar"),
    ).not.toBeNull();
    expect(
      document.querySelector(".phase6-sidebar-profile__chevron"),
    ).toBeNull();
    await openMenu();

    expect(document.body.textContent).toContain("메시지");
    expect(document.body.textContent).toContain("TEST 자산 Faucet 받기");
    expect(document.body.textContent).toContain("계정");
    expect(document.body.textContent).toContain("좋아요한 상품");
    expect(document.body.textContent).toContain("구매 체크한 상품");
    expect(document.body.textContent).toContain("계정 관리");
    expect(document.body.textContent).toContain("화면 설정");
    expect(document.body.textContent).toContain("로그아웃");
  });

  it("메시지가 맨 위 항목이고 선택 시 단일 대화를 연다", async () => {
    const onOpenSupport = vi.fn();
    await renderMenu({ onOpenSupport, supportUnread: 3 });
    await openMenu();

    const items = Array.from(
      document.querySelectorAll<HTMLElement>('[data-slot="dropdown-menu-item"]'),
    );
    expect(items[0]?.textContent).toContain("메시지");
    // 안 읽은 답변 수가 항목 pill과 아바타 dot으로 보인다.
    expect(items[0]?.textContent).toContain("3");
    expect(
      document.querySelector(".shell-user-menu__avatar-dot"),
    ).not.toBeNull();

    await act(async () => {
      items[0]?.click();
    });
    expect(onOpenSupport).toHaveBeenCalledTimes(1);
  });

  it("화면 설정 패널에서 선택한 테마와 강조색을 브라우저에 저장한다", async () => {
    await renderMenu();
    await openMenu();

    await act(async () => {
      Array.from(
        document.querySelectorAll<HTMLElement>(
          '[data-slot="dropdown-menu-item"]',
        ),
      ).find((item) => item.textContent?.includes("화면 설정"))?.click();
    });

    expect(document.body.textContent).toContain("이 브라우저에만 저장됩니다.");

    await act(async () => {
      document
        .querySelector<HTMLButtonElement>('[aria-label="다크 모드"]')
        ?.click();
      document
        .querySelector<HTMLButtonElement>(
          '[aria-label="Water 강조색"]',
        )
        ?.click();
    });

    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(document.documentElement.dataset.accent).toBe("blue");
    expect(
      JSON.parse(
        window.localStorage.getItem("vitlane.appearance.v2") ?? "{}",
      ),
    ).toEqual({ theme: "dark", accent: "blue" });
  });

  it("carries only the theme over from a v1 preference and restarts every user on the still accent", async () => {
    window.localStorage.setItem(
      "vitlane.appearance.v1",
      JSON.stringify({ theme: "dark", accent: "neutral" }),
    );
    await renderMenu();
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(document.documentElement.dataset.accent).toBe("blue");
    expect(
      JSON.parse(
        window.localStorage.getItem("vitlane.appearance.v2") ?? "{}",
      ),
    ).toEqual({ theme: "dark", accent: "blue" });
  });

  it("keeps a neutral accent that was chosen after the v2 migration", async () => {
    window.localStorage.setItem(
      "vitlane.appearance.v2",
      JSON.stringify({ theme: "light", accent: "neutral" }),
    );
    await renderMenu();
    expect(document.documentElement.dataset.accent).toBe("neutral");
  });

  it("offers the five palettes as dots with an English name caption and stores the chosen one", async () => {
    await renderMenu();
    await openMenu();
    await act(async () => {
      Array.from(
        document.querySelectorAll<HTMLElement>('[data-slot="dropdown-menu-item"]'),
      ).find((item) => item.textContent?.includes("화면 설정"))?.click();
    });

    const dots = Array.from(
      document.querySelectorAll<HTMLButtonElement>(
        '.vt-appearance-controls__dots [data-slot="toggle-group-item"]',
      ),
    );
    expect(dots.map((dot) => dot.getAttribute("aria-label"))).toEqual([
      "Water 강조색",
      "Moss 강조색",
      "Maple 강조색",
      "Iris 강조색",
      "Ink 강조색",
    ]);
    // Dots carry no text; the caption names the current palette in English only.
    expect(dots.every((dot) => dot.textContent === "")).toBe(true);
    expect(
      document.querySelector(".vt-appearance-controls__caption")?.textContent,
    ).toBe("Water · 기본");

    await act(async () => {
      dots[2]?.click();
    });
    expect(document.documentElement.dataset.accent).toBe("maple");
    expect(
      document.querySelector(".vt-appearance-controls__caption")?.textContent,
    ).toBe("Maple");
    expect(
      JSON.parse(window.localStorage.getItem("vitlane.appearance.v2") ?? "{}"),
    ).toEqual({ theme: "light", accent: "maple" });
  });

  it("reads an unknown stored accent as the still default", async () => {
    window.localStorage.setItem(
      "vitlane.appearance.v2",
      JSON.stringify({ theme: "dark", accent: "sunset" }),
    );
    await renderMenu();
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(document.documentElement.dataset.accent).toBe("blue");
  });

  it("언어 패널에서 English를 선택하면 UI와 cookie를 함께 바꾸다", async () => {
    await renderMenu();
    await openMenu();
    await act(async () => {
      Array.from(document.querySelectorAll<HTMLElement>('[data-slot="dropdown-menu-item"]'))
        .find((item) => item.textContent?.includes("언어"))?.click();
    });
    await act(async () => {
      Array.from(document.querySelectorAll<HTMLButtonElement>('button'))
        .find((button) => button.textContent?.includes("영어"))?.click();
    });
    expect(document.documentElement.lang).toBe("en");
    expect(document.cookie).toContain("vt_locale_choice=en-US");
    expect(document.cookie).toContain("vt_locale_seen=en-US");
    expect(document.body.textContent).toContain("Profile menu");
  });

  async function renderMenu(overrides: {
    onOpenSupport?: () => void;
    supportUnread?: number;
  } = {}) {
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    await act(async () => {
      root?.render(
        <LocaleProvider>
          <MemoryRouter>
            <AppearanceProvider>
              <UserMenu
                collapsed={false}
                onOpenPanel={vi.fn()}
                onOpenSupport={overrides.onOpenSupport ?? vi.fn()}
                onOpenTestAssets={vi.fn()}
                supportUnread={overrides.supportUnread ?? 0}
              />
            </AppearanceProvider>
          </MemoryRouter>
        </LocaleProvider>,
      );
    });
  }

  async function openMenu() {
    await act(async () => {
      host
        ?.querySelector<HTMLButtonElement>('[aria-label="프로필 메뉴"]')
        ?.dispatchEvent(
          new PointerEvent("pointerdown", {
            bubbles: true,
            button: 0,
            pointerType: "mouse",
          }),
        );
    });
  }
});
