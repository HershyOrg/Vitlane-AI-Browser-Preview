import { type ReactNode, useState } from "react";
import { BrandMark } from "./BrandMark";
import { Button } from "./Button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "./primitives/dropdown-menu";
import "./design-system/components.css";
import { useLocale } from "../i18n";

export interface AppHeaderNavigationItem {
  current?: boolean;
  href: string;
  label: string;
}

export interface AppHeaderProps {
  actions?: ReactNode;
  compact?: boolean;
  navigation: AppHeaderNavigationItem[];
  onNavigate?: (href: string) => void;
}

export function AppHeader({
  actions,
  compact = false,
  navigation,
  onNavigate,
}: AppHeaderProps) {
  const { t } = useLocale();
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const navigationLink = (item: AppHeaderNavigationItem) => (
    <a
      key={item.href}
      href={item.href}
      aria-current={item.current ? "page" : undefined}
      onClick={(event) => {
        if (!onNavigate) return;
        event.preventDefault();
        setMobileMenuOpen(false);
        onNavigate(item.href);
      }}
    >
      {item.label}
    </a>
  );

  return (
    <header className={`vt-app-header ${compact ? "is-compact" : ""}`}>
      <BrandMark href="/" />
      <nav className="vt-app-header__desktop-nav" aria-label={t("nav.primary")}>
        {navigation.map(navigationLink)}
      </nav>
      <DropdownMenu
        open={mobileMenuOpen}
        onOpenChange={setMobileMenuOpen}
      >
        <DropdownMenuTrigger asChild>
          <Button
            className="vt-app-header__menu"
            emphasis="secondary"
            size="compact"
            type="button"
          >
            {t("common.menu")}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          className="vt-app-header__menu-content"
        >
          <nav aria-label={t("nav.mobilePrimary")}>
            {navigation.map((item) => (
              <DropdownMenuItem key={item.href} asChild>
                {navigationLink(item)}
              </DropdownMenuItem>
            ))}
          </nav>
        </DropdownMenuContent>
      </DropdownMenu>
      {actions && <div className="vt-app-header__actions">{actions}</div>}
    </header>
  );
}
