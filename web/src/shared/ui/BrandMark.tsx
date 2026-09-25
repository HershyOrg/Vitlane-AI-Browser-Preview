import "./design-system/components.css";
import { useLocale } from "../i18n";

export interface BrandMarkProps {
  className?: string;
  compact?: boolean;
  href?: string;
  inverted?: boolean;
}

export function BrandMark({
  className,
  compact = false,
  href,
  inverted = false,
}: BrandMarkProps) {
  const { l, t } = useLocale();
  const classes = [
    "vt-brand-mark",
    compact ? "vt-brand-mark--compact" : "",
    inverted ? "vt-brand-mark--inverted" : "",
    className ?? "",
  ]
    .filter(Boolean)
    .join(" ");
  const content = (
    <>
      <span className="vt-brand-mark__symbol" aria-hidden="true" />
      {!compact && <span className="vt-brand-mark__name">{l("Vitlane", "Vitlane")}</span>}
    </>
  );

  if (href) {
    return (
      <a className={classes} href={href} aria-label={t("brand.purchaseHome")}>
        {content}
      </a>
    );
  }

  return (
    <span className={classes} role="img" aria-label={l("Vitlane", "Vitlane")}>
      {content}
    </span>
  );
}
