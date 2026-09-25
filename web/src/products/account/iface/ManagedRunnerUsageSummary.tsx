import { useEffect, useState } from "react";
import { useLocale } from "../../../shared/i18n";
import {
  fetchManagedRunnerUsage,
  type ManagedRunnerUsage,
} from "../../curation/planning/infra/planningApi";

/**
 * Today's managed research allowance, as a share of the daily limit.
 *
 * Percentages rather than amounts: the user is not billed, so a figure in
 * dollars would invite them to reason about a cost they never pay. What they
 * need to know is how much of today's allowance is left.
 */
export function ManagedRunnerUsageSummary({ open }: { open: boolean }) {
  const { l } = useLocale();
  const [usage, setUsage] = useState<ManagedRunnerUsage | null>(null);

  useEffect(() => {
    if (!open) return;
    // The menu stays mounted while the Radix dropdown is open. Keying the
    // read to each opening is what makes the figure
    // current: a number fetched when the page first loaded would still be
    // showing after a curation has spent against the allowance.
    let active = true;
    fetchManagedRunnerUsage()
      .then((result) => {
        if (active) setUsage(result);
      })
      .catch(() => {
        // The menu must still work when usage cannot be read. The previous
        // figure is dropped rather than left to look current.
        if (active) setUsage(null);
      });
    return () => {
      active = false;
    };
  }, [open]);

  if (!usage?.enabled || usage.userLimitMicros <= 0) return null;

  const usedPercent = clampPercent(
    (usage.userSpentMicros / usage.userLimitMicros) * 100,
  );
  const remainingPercent = 100 - usedPercent;

  return (
    <section
      className="shell-sidebar-profile__usage"
      aria-label={l("Today's research usage", "오늘 조사 사용량")}
    >
      <h3>{l("Usage", "사용량")}</h3>
      <dl>
        <div>
          <dt>{l("Used today", "오늘 사용")}</dt>
          <dd>{usedPercent}%</dd>
        </div>
        <div>
          <dt>{l("Remaining allowance", "남은 한도")}</dt>
          <dd>{remainingPercent}%</dd>
        </div>
      </dl>
      {usage.serverExhausted ? (
        <p>{l(
          "The server-wide limit has been reached, so new research cannot start right now.",
          "서버 전체 한도에 도달해 지금은 새 조사를 시작할 수 없습니다.",
        )}</p>
      ) : usage.userExhausted ? (
        <p>{l(
          "You have used today's allowance. You can start again tomorrow.",
          "오늘 한도를 모두 사용했습니다. 내일 다시 시작할 수 있습니다.",
        )}</p>
      ) : null}
    </section>
  );
}

/**
 * Rounds to a whole percent but never reports 0% for spend that happened, or
 * 100% used while the user still has room. Either would contradict what the
 * composer lets them do next.
 */
function clampPercent(value: number): number {
  if (!Number.isFinite(value) || value <= 0) return 0;
  if (value >= 100) return 100;
  return Math.min(99, Math.max(1, Math.round(value)));
}
