import type { ReactNode } from "react";
import { CircleAlert, FlaskConical, Info, TriangleAlert } from "lucide-react";
import { invariantContent } from "../i18n";
import "./design-system/components.css";

export type NoticeTone = "neutral" | "test" | "warning" | "danger";

export interface NoticeProps {
  action?: ReactNode;
  announce?: boolean;
  children: ReactNode;
  className?: string;
  title?: ReactNode;
  tone?: NoticeTone;
}

// Still Water (ADR-0073): an icon, one sentence and at most one quiet action.
// There is no stripe and no separate title row; a title is the bold lead of the
// sentence. A TEST notice always carries the mode marker "TEST", which is the
// same word in every UI language.
const toneIcons = {
  danger: CircleAlert,
  neutral: Info,
  test: FlaskConical,
  warning: TriangleAlert,
} as const;

export function Notice({
  action,
  announce = false,
  children,
  className,
  title,
  tone = "neutral",
}: NoticeProps) {
  const Icon = toneIcons[tone];
  return (
    <section
      className={[
        "vt-notice",
        `vt-notice--${tone}`,
        className ?? "",
      ]
        .filter(Boolean)
        .join(" ")}
      role={announce ? (tone === "danger" ? "alert" : "status") : undefined}
    >
      <Icon aria-hidden="true" className="vt-notice__icon" />
      <div className="vt-notice__body">
        <div className="vt-notice__message">
          {tone === "test" && !title && (
            <strong className="vt-notice__title">{invariantContent("TEST")}</strong>
          )}
          {title && <strong className="vt-notice__title">{title}</strong>}
          {children}
        </div>
      </div>
      {action && <div className="vt-notice__action">{action}</div>}
    </section>
  );
}
