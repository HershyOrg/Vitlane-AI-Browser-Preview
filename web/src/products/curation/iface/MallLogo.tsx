import { useState } from "react";
import { useLocale } from "../../../shared/i18n";
import { mallLogos } from "../../../shared/ui";
import { sourceLabel } from "../domain/sourceLabels";

/**
 * A mall shown with a source-owned generic merchant badge (mall-logos.source.json). Next to the mall's
 * name it is decorative; on its own — right after a product's name in a result row — it carries the
 * name in its alternative text and tooltip. A mall with no registered badge, or one that fails to load,
 * is its name in plain text instead.
 */
export function MallLogo({ source, decorative = false, className }: { source?: string; decorative?: boolean; className?: string }) {
  const { l } = useLocale();
  const code = source || "SHOPIFY";
  const name = sourceLabel(code, l);
  const src = mallLogos[code];
  const [failed, setFailed] = useState<string>();
  const classes = (base: string) => [base, className].filter(Boolean).join(" ");
  if (!src || failed === src) return decorative ? null : <span className={classes("curation-mall-name")} data-mall={code}>{name}</span>;
  return <img className={classes("curation-mall-logo")} src={src} alt={decorative ? "" : name} title={decorative ? undefined : name}
    width={16} height={16} decoding="async" data-mall={code} onError={() => setFailed(src)} />;
}
