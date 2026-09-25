import { openAnalyticsSettings } from "../shared/analytics/analytics";
import type { Ref } from "react";
import { Link } from "react-router";
import type { CurrentUser } from "../shared/api/types";
import type { SidebarCuration } from "../products/curation/infra/curationApi";
import {
  BrandMark,
  Button,
  type ProductShellCartRegistration,
} from "../shared/ui";
import { UserMenu } from "../products/account/iface/UserMenu";
import type { AccountOverviewView } from "../products/account/iface/AccountOverviewPage";
import { operatorHomePath } from "./operatorHome";
import { useLocale } from "../shared/i18n";

type Props = {
  ref?: Ref<HTMLElement>;
  cart: ProductShellCartRegistration | null;
  collapsed: boolean;
  currentCurationId?: string;
  productNotices?: ReadonlySet<string>;
  // Newest first by creation time, exactly as the server ordered them (ADR-0079).
  curations: SidebarCuration[];
  failed?: boolean;
  hasMore?: boolean;
  loading: boolean;
  loadingMore?: boolean;
  onLoadMore?: () => void;
  mobileOpen: boolean;
  onMobileClose: () => void;
  onOpenAccountPanel: (view: AccountOverviewView) => void;
  onOpenSupport: () => void;
  onOpenTestAssets: () => void;
  onToggle: () => void;
  pathname: string;
  supportUnread: number;
  user: CurrentUser | null;
};

export function ProductSidebar({
  ref,
  cart,
  collapsed,
  currentCurationId,
  productNotices,
  curations,
  failed = false,
  hasMore = false,
  loading,
  loadingMore = false,
  onLoadMore,
  mobileOpen,
  onMobileClose,
  onOpenAccountPanel,
  onOpenSupport,
  onOpenTestAssets,
  onToggle,
  pathname,
  supportUnread,
  user,
}: Props) {
  const { t, l } = useLocale();

  return (
    <aside
      ref={ref}
      id="vitlane-product-sidebar"
      className={[
        "shell-product-sidebar",
        collapsed ? "is-collapsed" : "",
        mobileOpen ? "is-mobile-open" : "",
      ].filter(Boolean).join(" ")}
      aria-label={t("nav.sidebar")}
      aria-modal={mobileOpen ? "true" : undefined}
      role={mobileOpen ? "dialog" : undefined}
      tabIndex={mobileOpen ? -1 : undefined}
      onTransitionEnd={(event) => {
        if (
          mobileOpen &&
          event.currentTarget === event.target &&
          event.propertyName === "transform"
        ) {
          event.currentTarget
            .querySelector<HTMLButtonElement>("[data-mobile-sidebar-close]")
            ?.focus({ preventScroll: true });
        }
      }}
    >
      <header className="shell-product-sidebar__header">
        {collapsed ? (
          <Button
            className="shell-product-sidebar__brand-trigger"
            emphasis="quiet"
            type="button"
            aria-expanded="false"
            aria-label={t("nav.expandSidebar")}
            title={t("nav.expandSidebar")}
            onClick={onToggle}
          >
            <BrandMark compact inverted />
          </Button>
        ) : (
          <>
            <BrandMark
              className="shell-product-sidebar__brand"
              href="/"
              inverted
            />
            <Button
              className="shell-product-sidebar__toggle"
              emphasis="quiet"
              type="button"
              aria-expanded="true"
              aria-label={t("nav.collapseSidebar")}
              title={t("nav.collapseSidebar")}
              onClick={onToggle}
            >
              <SidebarCollapseIcon />
            </Button>
          </>
        )}
        <Button
          className="shell-product-sidebar__mobile-close"
          emphasis="quiet"
          type="button"
          aria-label={t("nav.closeSidebar")}
          data-mobile-sidebar-close
          onClick={onMobileClose}
        >
          <MobileSidebarCloseIcon />
        </Button>
      </header>

      <div className="shell-product-sidebar__body">
        <nav
          className="shell-product-sidebar__primary"
          aria-label={t("nav.primary")}
        >
          <SidebarLink
            current={pathname === "/" || pathname === "/plans/new"}
            icon="new"
            label={t("nav.newCuration")}
            to="/"
          />
          <SidebarLink
	        current={pathname.startsWith("/agencyOrder")}
            icon="orders"
            label={t("nav.orders")}
	        to="/agencyOrder"
          />
          {user?.marketingAdmin || user?.phase5Operator ? (
            <SidebarLink
              current={pathname.startsWith("/admin/")}
              icon="operator"
              label={t("nav.operator")}
              to={operatorHomePath(user)}
            />
          ) : null}
        </nav>

        {!collapsed ? (
          <section className="shell-product-sidebar__history">
            <header className="shell-product-sidebar__title">
              <h2>
                {t("nav.curations")} <span>{curations.length}{hasMore ? "+" : ""}</span>
              </h2>
            </header>

            <nav
              className="shell-product-sidebar__history-scroll"
              aria-label={t("nav.curationList")}
            >
              {curations.length === 0 && loading ? (
                <p className="shell-product-sidebar__status" role="status">
                  {t("nav.loadingCurations")}
                </p>
              ) : curations.length === 0 && failed ? (
                <p className="shell-product-sidebar__status is-error">
                  {t("nav.failedCurations")}
                </p>
              ) : curations.length === 0 ? (
                <p className="shell-product-sidebar__status">
                  {t("nav.emptyCurations")}
                </p>
              ) : (
                <>
                <ol className="shell-product-sidebar__list">
                  {curations.map((curation) => {
                    const current = curation.curationId === currentCurationId;
                    const currentCart =
                      current && cart?.curationId === curation.curationId
                        ? cart
                        : null;
                    return (
                      <li
                        key={curation.curationId}
                        className={current ? "is-current" : undefined}
                      >
                        <Link
                          className="shell-product-sidebar__curation"
                          to={`/curations/${curation.curationId}`}
                          aria-current={current ? "page" : undefined}
                          title={curation.intentSummary}
                        >
                          <span className="shell-product-sidebar__curation-copy">
                            <strong>{curation.intentSummary}</strong>
                          </span>
                          {!current && productNotices?.has(curation.curationId) && <span className="curation-product-notice" role="img" aria-label={l("New products added","새 상품 추가됨")}/>}
                        </Link>
                        {currentCart ? (
                          <Button
                            className="shell-product-sidebar__cart"
                            emphasis="quiet"
                            type="button"
                            aria-label={t("nav.openCart", { count: currentCart.count })}
                            title={t("nav.openCart", { count: currentCart.count })}
                            onClick={currentCart.onOpen}
                          >
                            <CartIcon />
                            <strong>{currentCart.count}</strong>
                          </Button>
                        ) : null}
                      </li>
                    );
                  })}
                </ol>
                {hasMore ? (
                  <Button
                    className="shell-product-sidebar__more"
                    emphasis="quiet"
                    type="button"
                    busy={loadingMore}
                    onClick={onLoadMore}
                  >
                    {loadingMore ? t("nav.loadingMoreCurations") : t("nav.moreCurations")}
                  </Button>
                ) : null}
                </>
              )}
            </nav>
          </section>
        ) : null}
      </div>

      <footer className="shell-product-sidebar__footer">
        {user ? (
          <UserMenu
            collapsed={collapsed}
            onOpenPanel={onOpenAccountPanel}
            onOpenAnalytics={() => { onMobileClose(); openAnalyticsSettings(); }}
            onOpenSupport={onOpenSupport}
            onOpenTestAssets={onOpenTestAssets}
            supportUnread={supportUnread}
          />
        ) : null}
      </footer>
    </aside>
  );
}

function SidebarLink({
  current,
  icon,
  label,
  to,
}: {
  current: boolean;
  icon: "new" | "operator" | "orders";
  label: string;
  to: string;
}) {
  return (
    <Link
      className="shell-product-sidebar__primary-link"
      to={to}
      aria-current={current ? "page" : undefined}
      title={label}
    >
      <PrimaryIcon kind={icon} />
      <span>{label}</span>
    </Link>
  );
}

function SidebarCollapseIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 20 20">
      <path d="m12 5-5 5 5 5" />
    </svg>
  );
}

function MobileSidebarCloseIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 20 20">
      <path d="m5 5 10 10M15 5 5 15" />
    </svg>
  );
}

function PrimaryIcon({
  kind,
}: {
  kind: "new" | "operator" | "orders";
}) {
  const content = {
    new: (
      <>
        <path d="M10 3.5v13M3.5 10h13" />
      </>
    ),
    operator: (
      <>
        <path d="m4 9 6-5 6 5v7h-4v-4H8v4H4Z" />
      </>
    ),
    orders: (
      <>
        <rect x="5" y="3.5" width="10" height="13" rx="1.5" />
        <path d="M7.5 7.5h5M7.5 10.5h5" />
      </>
    ),
  };

  return (
    <svg
      className="shell-product-sidebar__primary-icon"
      aria-hidden="true"
      viewBox="0 0 20 20"
    >
      {content[kind]}
    </svg>
  );
}

function CartIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 20 20">
      <path d="M3 4h2l1.4 8h7.7l1.4-5.5H5.5" />
      <circle cx="8" cy="15.5" r="1" />
      <circle cx="13.5" cy="15.5" r="1" />
    </svg>
  );
}
