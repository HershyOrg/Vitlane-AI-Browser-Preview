import type { ReactNode } from "react";
import "./design-system/components.css";
import { useLocale } from "../i18n";

export interface OrderSummaryRow {
  detail?: ReactNode;
  label: string;
  monospaced?: boolean;
  value: ReactNode;
}

export interface OrderSummaryProps {
  boundary?: ReactNode;
  headingLevel?: "h2" | "h3";
  rows: OrderSummaryRow[];
  title: string;
}

export function OrderSummary({
  boundary,
  headingLevel = "h2",
  rows,
  title,
}: OrderSummaryProps) {
  const { l } = useLocale();
  const Heading = headingLevel;

  return (
    <section className="vt-order-summary">
      <header>
        <p>{l("Purchase terms", "구매 조건")}</p>
        <Heading>{title}</Heading>
      </header>
      <dl>
        {rows.map((row) => (
          <div className="vt-order-summary__row" key={row.label}>
            <dt>{row.label}</dt>
            <dd className={row.monospaced ? "is-monospaced" : undefined}>
              <strong>{row.value}</strong>
              {row.detail && <span>{row.detail}</span>}
            </dd>
          </div>
        ))}
      </dl>
      {boundary && (
        <div className="vt-order-summary__boundary">{boundary}</div>
      )}
    </section>
  );
}
