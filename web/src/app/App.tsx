import {
  type CSSProperties,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { Link, Outlet, useLocation } from "react-router";
import { useCurrentUser } from "../products/account/app/useCurrentUser";
import {
  AccountPanel,
} from "../products/account/iface/AccountPanel";
import { OperatorFreshAuthAlert } from "../products/account/iface/OperatorFreshAuthAlert";
import type {
  AccountOverviewView,
} from "../products/account/iface/AccountOverviewPage";
import {
  openTestAssetsEvent,
  TestAssetPanel,
} from "../products/payment/giwa/iface/TestAssetPanel";
import { useProductNotices } from "../products/curation/app/useProductNotices";
import { useSidebarCurations } from "../products/curation/app/useSidebarCurations";
import { listOperatorWorkItemCounts } from "../products/ordering/infra/agencyOrderOperatorApi";
import { getSupportCounts } from "../products/support/infra/supportOperatorApi";
import { getSupportSummary } from "../products/support/infra/supportApi";
import {
  openSupportChatEvent,
  SupportChatPanel,
  supportSummaryRefreshEvent,
} from "../products/support/iface/SupportChatPanel";
import { useLocale } from "../shared/i18n";
import {
  Button,
  productShellProvider as ProductShellProvider,
  useSwipeDismiss,
  type ProductShellCartRegistration,
} from "../shared/ui";
import { ProductSidebar } from "./ProductSidebar";

export function mobileSidebarSwipeIntent(
  start: { x: number; y: number },
  end: { x: number; y: number },
  sidebarOpen: boolean,
): "OPEN" | "CLOSE" | null {
  const deltaX = end.x - start.x;
  const deltaY = end.y - start.y;
  if (Math.abs(deltaX) < 56 || Math.abs(deltaX) <= Math.abs(deltaY) * 1.2) {
    return null;
  }
  if (!sidebarOpen && deltaX > 0) return "OPEN";
  if (sidebarOpen && deltaX < 0) return "CLOSE";
  return null;
}

export default function App() {
  const { l } = useLocale();
  const location = useLocation();
  const { user } = useCurrentUser();
  // Read once, then only on returning to the tab or creating a Curation here;
  // navigation never re-reads or blanks the list (ADR-0079).
  const sidebarCurations = useSidebarCurations();
  const [sidebarCollapsed, setSidebarCollapsed] = useState(
    readSidebarCollapsed,
  );
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);
  const [mobileSidebarDragX, setMobileSidebarDragX] = useState<number | null>(
    null,
  );
  const mobileSidebarPointer = useRef<{
    pointerId: number;
    startX: number;
    startY: number;
    width: number;
    captureTarget: HTMLElement | null;
    phase: "PENDING" | "DRAGGING";
  } | null>(null);
  const mobileSidebarRef = useRef<HTMLElement>(null);
  const mobileSidebarBackdropRef = useRef<HTMLDivElement>(null);
  // Opening stays the content swipe below; once open, the drawer itself
  // follows a leftward drag from anywhere on it, links and buttons included.
  useSwipeDismiss(mobileSidebarRef, {
    direction: "left",
    media: "(max-width: 52rem)",
    enabled: mobileSidebarOpen,
    backdropRef: mobileSidebarBackdropRef,
    onDismiss: () => setMobileSidebarOpen(false),
  });
  const [cartRegistration, setCartRegistration] =
    useState<ProductShellCartRegistration | null>(null);
  const [accountPanel, setAccountPanel] =
    useState<AccountOverviewView | null>(null);
  // 예외 처리 nav 뱃지(ADR-0057 2차 P2) — SQL COUNT 전역 카운트만 신뢰한다.
  const [exceptionTotal, setExceptionTotal] = useState(0);
  const [exceptionAlert, setExceptionAlert] = useState(false);
  // 주문 처리 nav의 실자금 신호 — PayPal 승인 경계를 지난 LIVE 주문 전역 수다.
  const [livePayPalOrderTotal, setLivePayPalOrderTotal] = useState(0);
  // 고객 대화 답변 대기 뱃지(ADR-0059) — 같은 원칙으로 SQL COUNT만 신뢰한다.
  const [supportAwaiting, setSupportAwaiting] = useState(0);
  // 도움 받기 안 읽음 뱃지(ADR-0059) — 서버 summary COUNT가 권위다.
  const [supportUnread, setSupportUnread] = useState(0);

  const registerCart = useCallback(
    (registration: ProductShellCartRegistration) => {
      setCartRegistration(registration);
      return () => {
        setCartRegistration((current) =>
          current?.curationId === registration.curationId &&
          current.onOpen === registration.onOpen
            ? null
            : current,
        );
      };
    },
    [],
  );

  function toggleSidebar() {
    setSidebarCollapsed((current) => {
      const next = !current;
      window.localStorage.setItem(
        "vitlane.product-sidebar-collapsed.v1",
        String(next),
      );
      return next;
    });
  }

  useEffect(() => {
    if (!user?.phase5Operator || !location.pathname.startsWith("/admin/")) return;
    let active = true;
    const pull = async () => {
      try {
        const { counts, livePayPalOrderCount } = await listOperatorWorkItemCounts();
        if (!active) return;
        setExceptionTotal(counts.REFUND_REVIEW + counts.DELIVERY_RESOLUTION +
          counts.RETURN_PROGRESS + counts.PROCESS_INTERVENTION +
          counts.PAYMENT_RECONCILIATION);
        setExceptionAlert(counts.PROCESS_INTERVENTION > 0 || counts.PAYMENT_RECONCILIATION > 0);
        setLivePayPalOrderTotal(livePayPalOrderCount ?? 0);
      } catch {
        // 뱃지는 보조 신호다 — 실패는 조용히 다음 주기로 넘긴다.
      }
    };
    void pull();
    const timer = window.setInterval(() => void pull(), 15000);
    return () => { active = false; window.clearInterval(timer); };
  }, [user?.phase5Operator, location.pathname]);

  useEffect(() => {
    if (!user) return;
    let active = true;
    const pull = async () => {
      try {
        const { unread } = await getSupportSummary();
        if (active) setSupportUnread(unread);
      } catch {
        // 뱃지는 보조 신호다 — 실패는 조용히 다음 주기로 넘긴다.
      }
    };
    void pull();
    const timer = window.setInterval(() => void pull(), 30000);
    // 위젯이 읽음 처리하면 즉시 갱신한다(폴 주기를 기다리지 않는다).
    const refresh = () => void pull();
    window.addEventListener(supportSummaryRefreshEvent, refresh);
    return () => {
      active = false;
      window.clearInterval(timer);
      window.removeEventListener(supportSummaryRefreshEvent, refresh);
    };
  }, [user]);

  useEffect(() => {
    const operator = Boolean(user?.phase5Operator || user?.marketingAdmin);
    if (!operator || !location.pathname.startsWith("/admin/")) return;
    let active = true;
    const pull = async () => {
      try {
        const { counts } = await getSupportCounts();
        if (active) setSupportAwaiting(counts.awaiting + counts.actionRequired);
      } catch {
        // 뱃지는 보조 신호다 — 실패는 조용히 다음 주기로 넘긴다.
      }
    };
    void pull();
    const timer = window.setInterval(() => void pull(), 15000);
    return () => { active = false; window.clearInterval(timer); };
  }, [user?.phase5Operator, user?.marketingAdmin, location.pathname]);

  useEffect(() => {
    setMobileSidebarOpen(false);
    setMobileSidebarDragX(null);
    mobileSidebarPointer.current = null;
  }, [location.pathname]);

  useEffect(() => {
    function onPointerDown(event: PointerEvent) {
      if (
        event.defaultPrevented ||
        !event.isPrimary ||
        (event.pointerType !== "touch" && event.pointerType !== "pen") ||
        window.innerWidth > 832
      ) {
        return;
      }
      // Dialogs own their gestures (the open drawer, sheets, the cart and
      // Messages dismiss themselves), and nothing opens under a modal.
      if (
        event.target instanceof Element &&
        event.target.closest(
          "a, button, input, textarea, select, [contenteditable='true'], [role='slider'], [data-horizontal-scroll], [data-mobile-sidebar-swipe='ignore'], [role='dialog'], [role='alertdialog']",
        )
      ) return;
      if (document.querySelector("[aria-modal='true']:not(#vitlane-product-sidebar)")) return;
      const sidebar = document.getElementById("vitlane-product-sidebar");
      const captureTarget =
        event.target instanceof HTMLElement ? event.target : null;
      mobileSidebarPointer.current = {
        pointerId: event.pointerId,
        startX: event.clientX,
        startY: event.clientY,
        width:
          sidebar?.getBoundingClientRect().width ||
          Math.min(window.innerWidth - 32, 288),
        captureTarget,
        phase: "PENDING",
      };
      try {
        captureTarget?.setPointerCapture?.(event.pointerId);
      } catch {
        // Synthetic browser fixtures do not own a native pointer. The gesture
        // still completes through the document listeners, while real touch
        // input keeps capture across the drawer boundary.
      }
    }

    function onPointerMove(event: PointerEvent) {
      const start = mobileSidebarPointer.current;
      if (!start || event.pointerId !== start.pointerId) return;
      const deltaX = event.clientX - start.startX;
      const deltaY = event.clientY - start.startY;
      if (Math.abs(deltaY) > 12 && Math.abs(deltaY) > Math.abs(deltaX) * 1.15) {
        cancelPointer();
        return;
      }
      if (start.phase === "PENDING") {
        if (Math.abs(deltaX) < 6 || Math.abs(deltaX) <= Math.abs(deltaY) * 1.15) {
          return;
        }
        if ((!mobileSidebarOpen && deltaX < 0) || (mobileSidebarOpen && deltaX > 0)) {
          cancelPointer();
          return;
        }
        start.phase = "DRAGGING";
      }
      event.preventDefault();
      setMobileSidebarDragX(
        mobileSidebarOpen
          ? Math.max(-start.width, Math.min(0, deltaX))
          : Math.max(0, Math.min(start.width, deltaX)),
      );
    }

    function onPointerUp(event: PointerEvent) {
      const start = mobileSidebarPointer.current;
      if (!start || event.pointerId !== start.pointerId) return;
      const intent = mobileSidebarSwipeIntent(
        { x: start.startX, y: start.startY },
        { x: event.clientX, y: event.clientY },
        mobileSidebarOpen,
      );
      releasePointer(start);
      mobileSidebarPointer.current = null;
      setMobileSidebarDragX(null);
      if (intent === "OPEN") setMobileSidebarOpen(true);
      if (intent === "CLOSE") setMobileSidebarOpen(false);
    }

    function cancelPointer() {
      const start = mobileSidebarPointer.current;
      if (start) releasePointer(start);
      mobileSidebarPointer.current = null;
      setMobileSidebarDragX(null);
    }

    function releasePointer(start: NonNullable<typeof mobileSidebarPointer.current>) {
      if (start.captureTarget?.hasPointerCapture?.(start.pointerId)) {
        try {
          start.captureTarget.releasePointerCapture(start.pointerId);
        } catch {
          // The browser may have released capture before pointercancel arrives.
        }
      }
    }

    document.addEventListener("pointerdown", onPointerDown, { passive: true });
    document.addEventListener("pointermove", onPointerMove, { passive: false });
    document.addEventListener("pointerup", onPointerUp, { passive: true });
    document.addEventListener("pointercancel", cancelPointer, { passive: true });
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("pointermove", onPointerMove);
      document.removeEventListener("pointerup", onPointerUp);
      document.removeEventListener("pointercancel", cancelPointer);
    };
  }, [mobileSidebarOpen]);

  useEffect(() => {
    if (!mobileSidebarOpen) return;

    const sidebar = document.getElementById("vitlane-product-sidebar");
    const trigger = document.querySelector<HTMLButtonElement>(
      ".shell-mobile-sidebar-trigger",
    );
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    sidebar
      ?.querySelector<HTMLButtonElement>("[data-mobile-sidebar-close]")
      ?.focus({ preventScroll: true });

    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        if (
          event.defaultPrevented ||
          document.querySelector(
            '[data-slot="dropdown-menu-content"][data-state="open"]',
          )
        ) {
          return;
        }
        event.preventDefault();
        setMobileSidebarOpen(false);
        return;
      }
      if (event.key !== "Tab" || !sidebar) return;

      const controls = [
        ...sidebar.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])',
        ),
      ].filter((element) => {
        const style = window.getComputedStyle(element);
        return style.display !== "none" && style.visibility !== "hidden";
      });
      if (controls.length === 0) {
        event.preventDefault();
        sidebar.focus();
        return;
      }
      const first = controls[0];
      const last = controls[controls.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    }

    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.body.style.overflow = previousOverflow;
      trigger?.focus();
    };
  }, [mobileSidebarOpen]);

  const openTestAssets = useCallback(() => {
    window.dispatchEvent(new CustomEvent(openTestAssetsEvent));
  }, []);
  const openSupport = useCallback(() => {
    window.dispatchEvent(new CustomEvent(openSupportChatEvent));
  }, []);
  const closeAccountPanel = useCallback(() => {
    setAccountPanel(null);
  }, []);

  const currentCurationId =
    location.pathname.match(/^\/curations\/([^/]+)/)?.[1];
 const productNotices=useProductNotices(user?.id,currentCurationId);

  return (
    <div className="app-shell shell-shell">
      <ProductShellProvider registerCart={registerCart}>
        <div
          className={[
            "shell-product-frame",
            "has-sidebar",
            sidebarCollapsed ? "is-sidebar-collapsed" : "",
            mobileSidebarDragX !== null
              ? "is-mobile-sidebar-dragging"
              : "",
            location.pathname === "/" ? "is-home" : "",
            location.pathname.startsWith("/curations/")
              ? "is-curation"
              : "",
          ].filter(Boolean).join(" ")}
          style={
            mobileSidebarDragX === null
              ? undefined
              : ({
                  "--shell-mobile-sidebar-drag-x": `${mobileSidebarDragX}px`,
                } as CSSProperties)
          }
        >
          <div
            ref={mobileSidebarBackdropRef}
            className={[
              "shell-mobile-sidebar-backdrop vt-scrim",
              mobileSidebarOpen ? "is-open" : "",
            ].filter(Boolean).join(" ")}
            aria-hidden="true"
            onMouseDown={(event) => {
              if (event.currentTarget === event.target) {
                setMobileSidebarOpen(false);
              }
            }}
          />
          <ProductSidebar
            ref={mobileSidebarRef}
            cart={cartRegistration}
            collapsed={mobileSidebarOpen ? false : sidebarCollapsed}
            currentCurationId={currentCurationId}
            productNotices={productNotices}
            curations={sidebarCurations.curations}
            failed={sidebarCurations.failed}
            hasMore={sidebarCurations.hasMore}
            loading={sidebarCurations.loading}
            loadingMore={sidebarCurations.loadingMore}
            onLoadMore={sidebarCurations.loadMore}
            mobileOpen={mobileSidebarOpen}
            onMobileClose={() => setMobileSidebarOpen(false)}
            onOpenAccountPanel={setAccountPanel}
            onOpenSupport={openSupport}
            onOpenTestAssets={openTestAssets}
            onToggle={toggleSidebar}
            pathname={location.pathname}
            supportUnread={supportUnread}
            user={user}
          />

          <div
            className="shell-product-body"
            inert={mobileSidebarOpen ? true : undefined}
          >
            <Button
              className="shell-mobile-sidebar-trigger"
              emphasis="quiet"
              type="button"
              aria-controls="vitlane-product-sidebar"
              aria-expanded={mobileSidebarOpen}
              aria-label={l("Open sidebar", "사이드바 열기")}
              onClick={() => setMobileSidebarOpen(true)}
            >
              <MobileMenuIcon />
            </Button>
            {location.pathname.startsWith("/admin/") && (
              <nav
                className="account-ui-section-tabs is-admin"
                aria-label={l("Operator menu", "운영자 메뉴")}
              >
                {user?.phase5Operator && (
                  <Link
	                aria-current={location.pathname === "/admin/agencyOrder" || (location.pathname.startsWith("/admin/agencyOrder/") && location.pathname !== "/admin/agencyOrder/exceptions") ? "page" : undefined}
	                to="/admin/agencyOrder"
                  >
                    {l("Order processing", "주문 처리")}
                    {livePayPalOrderTotal > 0 ? (
                      <span
                        aria-label={l(
                          "{count} Live PayPal orders",
                          "Live PayPal 주문 {count}건",
                          { count: livePayPalOrderTotal },
                        )}
                        className="account-ui-nav-count is-live-paypal"
                        title={l(
                          "Live PayPal orders: {count}",
                          "Live PayPal 주문: {count}건",
                          { count: livePayPalOrderTotal },
                        )}
                      >
                        {livePayPalOrderTotal}
                      </span>
                    ) : null}
                  </Link>
                )}
                {user?.phase5Operator && (
                  <Link
                    aria-current={location.pathname === "/admin/agencyOrder/exceptions" ? "page" : undefined}
                    className={exceptionAlert ? "is-alert" : undefined}
                    to="/admin/agencyOrder/exceptions"
                  >
                    {l("Exceptions", "예외 처리")}{exceptionTotal > 0 ? <span className="account-ui-nav-count">{exceptionTotal}</span> : null}
                  </Link>
                )}
                {user?.phase5Operator && (
                  <Link
                    aria-current={location.pathname === "/admin/order-accounting" ? "page" : undefined}
                    to="/admin/order-accounting"
                  >
                    {l("Order accounting", "주문 회계")}
                  </Link>
                )}
                {(user?.marketingAdmin || user?.phase5Operator) && (
                  <Link
                    aria-current={location.pathname === "/admin/support" ? "page" : undefined}
                    to="/admin/support"
                  >
                    {l("Customer conversations", "고객 대화")}{supportAwaiting > 0 ? <span className="account-ui-nav-count">{supportAwaiting}</span> : null}
                  </Link>
                )}
                {(user?.marketingAdmin || user?.phase5Operator) && (
                  <Link
                    aria-current={location.pathname === "/admin/api-usage" ? "page" : undefined}
                    to="/admin/api-usage"
                  >
                    {l("API usage", "API 사용량")}
                  </Link>
                )}
                {(user?.marketingAdmin || user?.phase5Operator) && (
                  <Link
                    aria-current={location.pathname === "/admin/ops" ? "page" : undefined}
                    to="/admin/ops"
                  >
                    {l("Operations", "운영 현황")}
                  </Link>
                )}
              </nav>
            )}

            <main className="shell-main">
              <section className="shell-work-area">
                <Outlet />
              </section>
            </main>
          </div>
        </div>

        <TestAssetPanel showTrigger={false} />
        <SupportChatPanel unread={supportUnread} />
        <OperatorFreshAuthAlert />
        {accountPanel && (
          <AccountPanel
            onClose={closeAccountPanel}
            view={accountPanel}
          />
        )}
      </ProductShellProvider>
    </div>
  );
}

function MobileMenuIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 20 20">
      <path d="M3.5 5.5h13M3.5 10h13M3.5 14.5h13" />
    </svg>
  );
}

function readSidebarCollapsed() {
  if (typeof window === "undefined") return false;
  return (
    window.localStorage.getItem("vitlane.product-sidebar-collapsed.v1") ===
    "true"
  );
}
