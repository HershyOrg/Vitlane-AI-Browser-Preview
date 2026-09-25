import type { ReactNode } from "react";
import { Button, type ButtonEmphasis } from "./Button";
import { Notice, type NoticeTone } from "./Notice";
import "./design-system/components.css";
import { useLocale } from "../i18n";

export type FeedbackStateKind =
  | "loading"
  | "empty"
  | "partial"
  | "error"
  | "expired"
  | "reconnect"
  | "success";

export interface FeedbackStateAction {
  emphasis?: Exclude<ButtonEmphasis, "danger">;
  label: string;
  onAction?: () => void;
}

export interface FeedbackStateProps {
  action?: FeedbackStateAction;
  // Optional since Still Water (ADR-0073 PR E): an empty state may be a title
  // alone; the how-to sentence that used to follow it is gone.
  description?: ReactNode;
  state: FeedbackStateKind;
  title?: ReactNode;
}

export function FeedbackState({
  action,
  description,
  state,
  title,
}: FeedbackStateProps) {
  const { l } = useLocale();
  const statePresentation: Record<
    FeedbackStateKind,
    { icon: string; label: string; tone: NoticeTone }
  > = {
    loading: { icon: "", label: l("Loading", "불러오는 중"), tone: "neutral" },
    empty: { icon: "—", label: l("Nothing yet", "아직 없음"), tone: "neutral" },
    partial: { icon: "△", label: l("Partially verified", "일부 확인"), tone: "warning" },
    error: { icon: "!", label: l("Needs attention", "확인 필요"), tone: "danger" },
    expired: { icon: "↻", label: l("Check again", "다시 확인"), tone: "warning" },
    reconnect: { icon: "↗", label: l("Reconnect", "다시 연결"), tone: "neutral" },
    success: { icon: "✓", label: l("Complete", "완료"), tone: "neutral" },
  };
  const presentation = statePresentation[state];
  const isLoading = state === "loading";

  return (
    <div
      className={`vt-feedback-state is-${state}`}
      aria-busy={isLoading || undefined}
      data-state={state}
    >
      <span className="vt-feedback-state__icon" aria-hidden="true">
        {presentation.icon}
      </span>
      <Notice
        announce={state === "error" || state === "success"}
        tone={presentation.tone}
        title={title ?? presentation.label}
        action={
          action ? (
            <Button
              emphasis={action.emphasis ?? "secondary"}
              onClick={action.onAction}
            >
              {action.label}
            </Button>
          ) : undefined
        }
      >
        {description}
      </Notice>
    </div>
  );
}
