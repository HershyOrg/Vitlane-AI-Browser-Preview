import type { ReactNode } from "react";
import { Button, type ButtonProps } from "./Button";
import "./design-system/components.css";

export interface PageHeaderAction
  extends Pick<ButtonProps, "busy" | "disabled" | "onClick"> {
  label: string;
}

export interface PageHeaderProps {
  description?: ReactNode;
  eyebrow?: ReactNode;
  primaryAction?: PageHeaderAction;
  secondaryActions?: PageHeaderAction[];
  summary?: ReactNode;
  title: ReactNode;
}

export function PageHeader({
  description,
  eyebrow,
  primaryAction,
  secondaryActions = [],
  summary,
  title,
}: PageHeaderProps) {
  return (
    <header className="vt-page-header">
      <div className="vt-page-header__copy">
        {eyebrow && <p className="vt-page-header__eyebrow vt-eyebrow">{eyebrow}</p>}
        <h1>{title}</h1>
        {description && (
          <div className="vt-page-header__description">{description}</div>
        )}
      </div>
      {(primaryAction || secondaryActions.length > 0) && (
        <div className="vt-page-header__actions">
          {secondaryActions.map(({ label, ...action }) => (
            <Button key={label} emphasis="secondary" {...action}>
              {label}
            </Button>
          ))}
          {primaryAction && (
            <Button
              emphasis="primary"
              busy={primaryAction.busy}
              disabled={primaryAction.disabled}
              onClick={primaryAction.onClick}
            >
              {primaryAction.label}
            </Button>
          )}
        </div>
      )}
      {summary && <div className="vt-page-header__summary">{summary}</div>}
    </header>
  );
}
