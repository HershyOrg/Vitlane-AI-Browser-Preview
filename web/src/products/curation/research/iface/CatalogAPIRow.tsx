import { useId, useState, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { Button, Switch } from "../../../../shared/ui";
import { useLocale } from "../../../../shared/i18n";
import "./catalog-api-usage.css";

/** Presentation only. Each API controller retains its own transport and version. */
export function CatalogAPIRow({ name, enabled, disabled, onToggle, requests, remaining, notices, children }: {
  name: string; enabled: boolean; disabled: boolean; onToggle: () => void;
  requests?: number; remaining?: number | null; notices?: ReactNode; children: ReactNode;
}) {
  const { l } = useLocale();
  const [expanded, setExpanded] = useState(false);
  const detailsId = useId();
  return <section className="catalog-api-row" aria-label={name}>
    <div className="catalog-api-row__summary">
      <span className="catalog-api-row__name">{name}</span>
      <span className="catalog-api-row__usage">
        <span>{requests === undefined ? l("Calls unconfirmed", "호출 미확인") : l("{count} calls in 24h", "24시간 {count}회", { count: requests })}</span>
        <span>{remaining == null ? l("Remaining unknown", "잔여 미확인") : l("{count} remaining", "잔여 {count}", { count: remaining })}</span>
      </span>
      <div className="catalog-api-row__controls">
        <Switch checked={enabled} disabled={disabled} onCheckedChange={onToggle} aria-label={l("Access to {api}", "{api} 사용", { api: name })} />
        <Button emphasis="quiet" size="compact" aria-expanded={expanded} aria-controls={detailsId}
          aria-label={l("Details for {api}", "{api} 상세", { api: name })} onClick={() => setExpanded(value => !value)}>
          <ChevronDown size={16} aria-hidden="true" />
        </Button>
      </div>
    </div>
    {notices && <div className="catalog-api-row__notices">{notices}</div>}
    <div id={detailsId} className="catalog-api-row__details" hidden={!expanded}>{children}</div>
  </section>;
}
