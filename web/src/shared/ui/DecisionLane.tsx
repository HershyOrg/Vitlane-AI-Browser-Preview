import "./design-system/components.css";
import { useLocale } from "../i18n";

export interface DecisionLaneSegment {
  actor: string;
  description: string;
  title: string;
}

export interface DecisionLaneProps {
  current: DecisionLaneSegment;
  next: DecisionLaneSegment;
  prepared: DecisionLaneSegment;
}

export function DecisionLane({
  current,
  next,
  prepared,
}: DecisionLaneProps) {
  const { l } = useLocale();
  const segments = [
    { ...prepared, state: "prepared", label: l("Prepared", "준비됨") },
    { ...current, state: "current", label: l("Review now", "지금 확인") },
    { ...next, state: "next", label: l("Next", "다음") },
  ] as const;

  return (
    <section
      className="vt-decision-lane"
      aria-label={l(
        "Responsibilities and next steps before your decision",
        "결정 전 책임과 다음 단계",
      )}
    >
      <ol>
        {segments.map((segment) => (
          <li
            key={segment.state}
            className={`vt-decision-lane__segment is-${segment.state}`}
            aria-current={segment.state === "current" ? "step" : undefined}
          >
            <div className="vt-decision-lane__meta">
              <span>{segment.label}</span>
              <strong>{segment.actor}</strong>
            </div>
            <h3>{segment.title}</h3>
            <p>{segment.description}</p>
          </li>
        ))}
      </ol>
    </section>
  );
}
