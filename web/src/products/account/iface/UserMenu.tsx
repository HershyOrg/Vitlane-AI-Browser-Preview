import { analyticsConfig, openAnalyticsSettings } from "../../../shared/analytics/analytics";
import { useState } from "react";
import { useNavigate } from "react-router";
import {
  AppearanceControls,
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../../../shared/ui";
import { useCurrentUser } from "../app/useCurrentUser";
import type { AccountOverviewView } from "./AccountOverviewPage";
import { ManagedRunnerUsageSummary } from "./ManagedRunnerUsageSummary";
import { LanguageControls, useLocale } from "../../../shared/i18n";

type Props = {
  collapsed: boolean;
  onOpenPanel: (view: AccountOverviewView) => void;
  onOpenSupport: () => void;
  onOpenTestAssets: () => void;
  onOpenAnalytics?: () => void;
  supportUnread: number;
};

export function UserMenu({
  collapsed,
  onOpenPanel,
  onOpenSupport,
  onOpenTestAssets,
  supportUnread,
  onOpenAnalytics = openAnalyticsSettings,
}: Props) {
  const { user, logout } = useCurrentUser();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [view, setView] = useState<"menu" | "appearance" | "language">("menu");
  const { t, l } = useLocale();
  if (!user) return null;

  function changeOpen(nextOpen: boolean) {
    setOpen(nextOpen);
    if (!nextOpen) setView("menu");
  }

  function openPanel(view: AccountOverviewView) {
    changeOpen(false);
    onOpenPanel(view);
  }

  async function signOut() {
    changeOpen(false);
    await logout();
    navigate("/login", { replace: true });
  }

  return (
    <div
      className={[
        "shell-sidebar-profile",
        collapsed ? "is-collapsed" : "",
      ].filter(Boolean).join(" ")}
    >
      <DropdownMenu open={open} onOpenChange={changeOpen}>
        <DropdownMenuTrigger asChild>
          <Button
            className="shell-sidebar-profile__trigger"
            emphasis="quiet"
            type="button"
            aria-label={t("userMenu.label")}
            title={collapsed ? user.displayName || t("common.profile") : undefined}
          >
            <span className="shell-user-menu__avatar" aria-hidden="true">
              {(user.displayName || user.email || "V").slice(0, 1).toUpperCase()}
              {supportUnread > 0 ? (
                <span className="shell-user-menu__avatar-dot" />
              ) : null}
            </span>
            <span className="shell-sidebar-profile__copy">
              <strong>{user.displayName || t("common.vitlaneUser")}</strong>
              <small>{t("userMenu.testBoundary")}</small>
            </span>
          </Button>
        </DropdownMenuTrigger>

        <DropdownMenuContent
          align="start"
          className="shell-sidebar-profile__menu"
          side="top"
          sideOffset={8}
        >
          {view === "menu" ? (
            <>
              <header>
                <strong>{user.displayName || t("common.vitlaneUser")}</strong>
                <small>{user.email || t("common.developmentAccount")}</small>
              </header>
              <nav aria-label={t("userMenu.label")}>
                {/* 메시지(ADR-0063) — 주문 안내·행동 요청·고객 문의의 단일 대화. 맨 위. */}
                <DropdownMenuItem
                  onSelect={() => {
                    changeOpen(false);
                    onOpenSupport();
                  }}
                >
                  <MenuIcon kind="help" />
                  <span>{t("userMenu.help")}</span>
                  {supportUnread > 0 ? (
                    <span className="shell-sidebar-profile__unread">
                      {supportUnread}
                    </span>
                  ) : null}
                </DropdownMenuItem>
                <DropdownMenuItem
                  onSelect={() => {
                    changeOpen(false);
                    onOpenTestAssets();
                  }}
                >
                  <MenuIcon kind="asset" />
                  <span>{t("userMenu.testAssets")}</span>
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => openPanel("account")}>
                  <MenuIcon kind="account" />
                  <span>{t("userMenu.account")}</span>
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => openPanel("liked")}>
                  <MenuIcon kind="liked" />
                  <span>{t("userMenu.liked")}</span>
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => openPanel("purchased")}>
                  <MenuIcon kind="purchased" />
                  <span>{t("userMenu.purchased")}</span>
                </DropdownMenuItem>
                {analyticsConfig()?.mode && analyticsConfig()?.mode !== "disabled" ? <DropdownMenuItem onSelect={() => { changeOpen(false); onOpenAnalytics(); }}><MenuIcon kind="settings" /><span>{l("Usage information preferences", "정보 제공 설정")}</span></DropdownMenuItem> : null}
                <DropdownMenuItem onSelect={() => openPanel("management")}>
                  <MenuIcon kind="settings" />
                  <span>{t("userMenu.manage")}</span>
                </DropdownMenuItem>
                <DropdownMenuItem
                  onSelect={(event) => {
                    event.preventDefault();
                    setView("appearance");
                  }}
                >
                  <MenuIcon kind="appearance" />
                  <span>{t("userMenu.appearance")}</span>
                  <ForwardIcon />
                </DropdownMenuItem>
                <DropdownMenuItem
                  onSelect={(event) => {
                    event.preventDefault();
                    setView("language");
                  }}
                >
                  <MenuIcon kind="language" />
                  <span>{t("userMenu.language")}</span>
                  <ForwardIcon />
                </DropdownMenuItem>
              </nav>

              <ManagedRunnerUsageSummary open={open} />
              <DropdownMenuSeparator />
              <DropdownMenuItem
                className="phase6-sidebar-profile__logout"
                variant="destructive"
                onSelect={() => void signOut()}
              >
                <MenuIcon kind="logout" />
                <span>{t("userMenu.signOut")}</span>
              </DropdownMenuItem>
            </>
          ) : (
            <>
              <DropdownMenuItem
                className="shell-sidebar-profile__back"
                onSelect={(event) => {
                  event.preventDefault();
                  setView("menu");
                }}
              >
                <BackIcon />
                <span>{t("userMenu.label")}</span>
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <div
                className="shell-sidebar-profile__appearance"
                onKeyDown={(event) => event.stopPropagation()}
              >
                {view === "appearance" ? <AppearanceControls compact /> : <LanguageControls compact />}
              </div>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

function ForwardIcon() {
  return (
    <svg
      className="shell-sidebar-profile__forward"
      aria-hidden="true"
      viewBox="0 0 20 20"
    >
      <path d="m8 5 5 5-5 5" />
    </svg>
  );
}

function BackIcon() {
  return (
    <svg
      className="shell-sidebar-profile__menu-icon"
      aria-hidden="true"
      viewBox="0 0 20 20"
    >
      <path d="m12 5-5 5 5 5" />
    </svg>
  );
}

function MenuIcon({
  kind,
}: {
  kind:
    | "account"
    | "appearance"
    | "asset"
    | "help"
    | "liked"
    | "language"
    | "logout"
    | "purchased"
    | "settings";
}) {
  const paths = {
    account: (
      <>
        <circle cx="10" cy="7" r="3" />
        <path d="M4.5 16c.8-3 2.6-4.5 5.5-4.5s4.7 1.5 5.5 4.5" />
      </>
    ),
    help: (
      <>
        <circle cx="10" cy="10" r="6.5" />
        <path d="M8.1 8.2a1.9 1.9 0 1 1 2.6 1.8c-.6.3-.7.7-.7 1.3M10 13.6h.01" />
      </>
    ),
    asset: (
      <>
        <circle cx="10" cy="10" r="6.5" />
        <path d="M7 10h6M10 7v6" />
      </>
    ),
    appearance: (
      <>
        <path d="M4 6h12M4 14h12" />
        <circle cx="8" cy="6" r="1.8" />
        <circle cx="13" cy="14" r="1.8" />
      </>
    ),
    language: (
      <>
        <circle cx="10" cy="10" r="6.5" />
        <path d="M3.8 10h12.4M10 3.5c2 2 3 4.2 3 6.5s-1 4.5-3 6.5M10 3.5c-2 2-3 4.2-3 6.5s1 4.5 3 6.5" />
      </>
    ),
    liked: (
      <path d="M10 16.2 4.3 10.8C1.3 7.9 5.5 3.8 8.4 6.7L10 8.2l1.6-1.5c2.9-2.9 7.1 1.2 4.1 4.1Z" />
    ),
    logout: (
      <>
        <path d="M8 4H4.5v12H8M12 7l3 3-3 3M7 10h8" />
      </>
    ),
    purchased: (
      <>
        <path d="M5.5 7.5h9l-.8 7.5H6.3Z" />
        <path d="M7.8 7.5V6.2a2.2 2.2 0 0 1 4.4 0v1.3M8 11.3l1.4 1.4 2.7-2.9" />
      </>
    ),
    settings: (
      <>
        <circle cx="10" cy="10" r="2.5" />
        <path d="M10 3.5v2M10 14.5v2M3.5 10h2M14.5 10h2M5.4 5.4l1.4 1.4M13.2 13.2l1.4 1.4M14.6 5.4l-1.4 1.4M6.8 13.2l-1.4 1.4" />
      </>
    ),
  };

  return (
    <svg
      className="shell-sidebar-profile__menu-icon"
      aria-hidden="true"
      viewBox="0 0 20 20"
    >
      {paths[kind]}
    </svg>
  );
}
