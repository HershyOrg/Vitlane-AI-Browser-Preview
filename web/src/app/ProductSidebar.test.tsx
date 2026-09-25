import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import type { SidebarCuration } from "../products/curation/infra/curationApi";
import { ProductSidebar } from "./ProductSidebar";

describe("ProductSidebar", () => {
  it("현재 큐레이션 항목 안에 장바구니 버튼을 포함한다", () => {
    const html = renderToStaticMarkup(
      <MemoryRouter>
        <ProductSidebar
          cart={{
            curationId: "curation-1",
            count: 2,
            onOpen: vi.fn(),
          }}
          collapsed={false}
          currentCurationId="curation-1"
          loading={false}
          mobileOpen={false}
          onMobileClose={vi.fn()}
          onOpenAccountPanel={vi.fn()}
          onOpenSupport={vi.fn()}
          onOpenTestAssets={vi.fn()}
          onToggle={vi.fn()}
          pathname="/curations/curation-1"
          curations={[curation]}
          supportUnread={0}
          user={null}
        />
      </MemoryRouter>,
    );

    const currentItem = html.match(
      /<li class="is-current">([\s\S]*?)<\/li>/,
    )?.[1];
    expect(currentItem).toContain("작업 공간 큐레이션");
    expect(currentItem).toContain('title="작업 공간 큐레이션"');
    expect(currentItem).not.toContain("현재 큐레이션");
    expect(currentItem).not.toContain("추천 확인 필요");
    expect(currentItem).not.toContain("구매 항목");
    expect(currentItem).not.toContain("조사 2개");
    expect(currentItem).toContain("장바구니 열기, 선택 2개");
    expect(currentItem).not.toContain("<span>장바구니</span>");
    expect(html).toContain('aria-expanded="true"');
    expect(html).toContain('aria-label="사이드바 접기"');
    expect(html).toContain("shell-product-sidebar__toggle");
    expect(html).toContain("vt-brand-mark__symbol");
    expect(html).toContain("vt-brand-mark__name");
    expect(html).not.toContain("phase6-product-sidebar__index");
    const historyTitle = html.match(
      /<header class="shell-product-sidebar__title">([\s\S]*?)<\/header>/,
    )?.[1] ?? "";
    expect(historyTitle).toContain("<h2>Curations <span>1</span></h2>");
    expect(historyTitle).not.toContain("<p>");
    expect(historyTitle).not.toContain("<small>");
    expect(html).toContain('x="5" y="3.5" width="10" height="13"');
    expect(html).not.toContain("M3.5 3.5h13v13h-13z");
  });

  it("접힌 sidebar는 펼치기 제어와 주요 navigation만 유지한다", () => {
    const html = renderToStaticMarkup(
      <MemoryRouter>
        <ProductSidebar
          cart={null}
          collapsed
          loading={false}
          mobileOpen={false}
          onMobileClose={vi.fn()}
          onOpenAccountPanel={vi.fn()}
          onOpenSupport={vi.fn()}
          onOpenTestAssets={vi.fn()}
          onToggle={vi.fn()}
          pathname="/"
          curations={[curation]}
          supportUnread={0}
          user={null}
        />
      </MemoryRouter>,
    );

    expect(html).toContain("is-collapsed");
    expect(html).toContain('aria-label="사이드바 펼치기"');
    expect(html).toContain("shell-product-sidebar__brand-trigger");
    expect(html).not.toContain("shell-product-sidebar__toggle");
    expect(html).not.toContain("shell-product-sidebar__history");
    expect(html).not.toContain("phase6-product-sidebar__index");
    expect(html).not.toContain("작업 공간 큐레이션");
    expect(html).not.toContain(">01<");
  });

  it("더 남은 큐레이션이 있으면 개수에 +를 붙이고 목록 끝에 더 보기를 둔다", () => {
    const render = (props: { hasMore: boolean; loadingMore?: boolean; loading?: boolean }) => renderToStaticMarkup(
      <MemoryRouter>
        <ProductSidebar
          cart={null}
          collapsed={false}
          curations={[curation]}
          hasMore={props.hasMore}
          loading={props.loading ?? false}
          loadingMore={props.loadingMore}
          mobileOpen={false}
          onLoadMore={vi.fn()}
          onMobileClose={vi.fn()}
          onOpenAccountPanel={vi.fn()}
          onOpenSupport={vi.fn()}
          onOpenTestAssets={vi.fn()}
          onToggle={vi.fn()}
          pathname="/"
          supportUnread={0}
          user={null}
        />
      </MemoryRouter>,
    );
    const more = render({ hasMore: true });
    expect(more).toContain("<h2>Curations <span>1+</span></h2>");
    expect(more).toMatch(/<\/ol><button[^>]*class="[^"]*shell-product-sidebar__more[^"]*"[^>]*>더 보기<\/button>/);

    const busy = render({ hasMore: true, loadingMore: true });
    expect(busy).toContain("더 불러오는 중");
    expect(busy).toContain('aria-busy="true"');
    expect(busy).toMatch(/<button[^>]*aria-busy="true"[^>]*disabled=""[^>]*>[\s\S]*더 불러오는 중<\/button>/);

    const complete = render({ hasMore: false });
    expect(complete).toContain("<h2>Curations <span>1</span></h2>");
    expect(complete).not.toContain("shell-product-sidebar__more");

    // A list the tab already shows stays while anything reloads.
    const reloading = render({ hasMore: false, loading: true });
    expect(reloading).toContain("작업 공간 큐레이션");
    expect(reloading).not.toContain("큐레이션을 불러오는 중");
  });

  it("mobile open 상태는 닫기 control을 가진 modal sidebar로 표시한다", () => {
    const html = renderToStaticMarkup(
      <MemoryRouter>
        <ProductSidebar
          cart={null}
          collapsed={false}
          loading={false}
          mobileOpen
          onMobileClose={vi.fn()}
          onOpenAccountPanel={vi.fn()}
          onOpenSupport={vi.fn()}
          onOpenTestAssets={vi.fn()}
          onToggle={vi.fn()}
          pathname="/"
          curations={[curation]}
          supportUnread={0}
          user={null}
        />
      </MemoryRouter>,
    );

    expect(html).toContain("is-mobile-open");
    expect(html).toContain('role="dialog"');
    expect(html).toContain('aria-modal="true"');
    expect(html).toContain('aria-label="사이드바 닫기"');
    expect(html).toContain("data-mobile-sidebar-close");
  });
});

const curation: SidebarCuration = {
  curationId: "curation-1",
  intentSummary: "작업 공간 큐레이션",
  createdAt: "2026-07-31T05:00:00Z",
};
