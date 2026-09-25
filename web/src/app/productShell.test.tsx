import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter, Route, Routes } from "react-router";
import { describe, expect, it } from "vitest";
import { CurrentUserProvider } from "../products/account/app/useCurrentUser";
import App, { mobileSidebarSwipeIntent } from "./App";

function renderPath(path: string) {
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={[path]}>
      <CurrentUserProvider>
        <Routes>
          <Route element={<App />}>
            <Route path="*" element={<div>화면 내용</div>} />
          </Route>
        </Routes>
      </CurrentUserProvider>
    </MemoryRouter>,
  );
}

describe("Phase 6 app shell", () => {
  it("모바일 전역 swipe는 시작 위치와 무관하게 수평 의도가 분명할 때 sidebar를 연다", () => {
    expect(mobileSidebarSwipeIntent(
      { x: 12, y: 300 }, { x: 100, y: 306 }, false,
    )).toBe("OPEN");
    expect(mobileSidebarSwipeIntent(
      { x: 220, y: 300 }, { x: 120, y: 306 }, true,
    )).toBe("CLOSE");
    expect(mobileSidebarSwipeIntent(
      { x: 80, y: 300 }, { x: 170, y: 302 }, false,
    )).toBe("OPEN");
    expect(mobileSidebarSwipeIntent(
      { x: 28, y: 300 }, { x: 120, y: 302 }, false,
    )).toBe("OPEN");
    expect(mobileSidebarSwipeIntent(
      { x: 196, y: 300 }, { x: 288, y: 302 }, false,
    )).toBe("OPEN");
    expect(mobileSidebarSwipeIntent(
      { x: 12, y: 300 }, { x: 80, y: 380 }, false,
    )).toBeNull();
    expect(mobileSidebarSwipeIntent(
      { x: 196, y: 300 }, { x: 104, y: 302 }, false,
    )).toBeNull();
  });

  it("새 요청에서 header와 중복 단계 navigation 없이 sidebar를 렌더링한다", () => {
    const html = renderPath("/plans/new");

    expect(html).toContain('aria-label="Vitlane 사이드바"');
    expect(html).toContain('aria-label="사이드바 열기"');
    expect(html).toContain('aria-controls="vitlane-product-sidebar"');
    expect(html).toContain('aria-expanded="false"');
    expect(html).not.toContain("shell-mobile-shell-bar");
    expect(html).not.toContain("shell-mobile-shell-bar__brand");
    expect(html).toContain("새 큐레이션");
    expect(html).toContain("주문·결제");
    expect(html).not.toContain("vt-app-header");
    expect(html).not.toContain(">새 구매 요청<");
    expect(html).not.toContain(">결제·주문<");
    expect(html).not.toContain('aria-label="의도·큐레이션 진행 단계"');
    expect(html).not.toContain("shell-task-nav");
    expect(html).not.toContain(">Session<");
    expect(html).not.toContain(">Workspace<");
  });

  it("구매 홈에서는 구매 요청 내부 단계 navigation을 반복하지 않는다", () => {
    const html = renderPath("/");

    expect(html).toContain('aria-label="주요 메뉴"');
    expect(html).not.toContain('aria-label="모바일 주요 메뉴"');
    expect(html).toContain('aria-label="Vitlane 사이드바"');
    expect(html).toContain('aria-label="큐레이션 목록"');
    expect(html).toContain('aria-label="사이드바 접기"');
    expect(html).not.toContain('aria-label="의도·큐레이션 진행 단계"');
  });

  it("계정 직접 주소에서도 공통 sidebar를 유지한다", () => {
    const html = renderPath("/account");

    expect(html).toContain('aria-label="Vitlane 사이드바"');
    expect(html).not.toContain('aria-label="계정 메뉴"');
  });

  it("큐레이션 직접 주소도 전역 mobile sidebar shell 안에서 렌더링한다", () => {
    const html = renderPath("/curations/curation-example");

    expect(html).toContain("shell-product-frame");
    expect(html).toContain("is-curation");
    expect(html).toContain('aria-label="Vitlane 사이드바"');
    expect(html).toContain('aria-label="사이드바 열기"');
    expect(html).toContain('aria-controls="vitlane-product-sidebar"');
  });
});
