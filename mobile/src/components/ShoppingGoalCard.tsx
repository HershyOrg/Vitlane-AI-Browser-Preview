import { StyleSheet, Text, View } from "react-native";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";

export type ShoppingGoalState =
  | "planning"
  | "researching"
  | "needs-input"
  | "review-required"
  | "ready"
  | "paused"
  | "failed";

export type ShoppingGoalPresentation = {
  readonly id: string;
  readonly title: string;
  readonly summary?: string;
  readonly state: ShoppingGoalState;
  /** Set only when the domain has an exact normalized measure. */
  readonly progress?: number;
  readonly progressDetail?: string;
  readonly updatedLabel?: string;
};

export type ShoppingGoalCardProps = {
  goal: ShoppingGoalPresentation;
  onOpenActivity?: (goalId: string) => void;
};

export function ShoppingGoalCard({ goal, onOpenActivity }: ShoppingGoalCardProps) {
  const { t } = useLocale();
  const progress = goal.progress !== undefined && Number.isFinite(goal.progress)
    ? Math.min(1, Math.max(0, goal.progress))
    : undefined;
  const percent = progress === undefined ? undefined : Math.round(progress * 100);
  const tone = stateTone[goal.state];

  return (
    <View accessibilityLabel={goal.title} style={styles.card} testID={`shopping-goal.${goal.id}`}>
      <View style={styles.header}>
        <View style={styles.copy}>
          <Text style={styles.eyebrow}>{t("goal.eyebrow")}</Text>
          <Text accessibilityRole="header" style={styles.title}>{goal.title}</Text>
        </View>
        <View style={[styles.stateChip, tone.chip]}>
          <View style={[styles.stateDot, tone.dot]} />
          <Text style={styles.stateLabel}>{t(stateMessageKeys[goal.state])}</Text>
        </View>
      </View>

      {goal.summary ? <Text style={styles.summary}>{goal.summary}</Text> : null}

      {percent !== undefined ? (
        <View
          accessibilityLabel={t("goal.progressAccessibility", { percent })}
          accessibilityRole="progressbar"
          accessibilityValue={{ min: 0, max: 100, now: percent }}
          style={styles.progressTrack}
        >
          <View style={[styles.progressFill, { width: `${percent}%` }]} />
        </View>
      ) : null}
      <View style={styles.metaRow}>
        <Text style={styles.progressText}>
          {goal.progressDetail ?? (percent === undefined ? "" : t("goal.progress", { percent }))}
        </Text>
        {goal.updatedLabel ? <Text style={styles.updated}>{goal.updatedLabel}</Text> : null}
      </View>

      {onOpenActivity ? (
        <ActionButton
          compact
          emphasis="quiet"
          label={t("goal.openActivity")}
          onPress={() => onOpenActivity(goal.id)}
          style={styles.activityButton}
        />
      ) : null}
    </View>
  );
}

const stateMessageKeys = {
  planning: "goal.statePlanning",
  researching: "goal.stateResearching",
  "needs-input": "goal.stateNeedsInput",
  "review-required": "goal.stateReviewRequired",
  ready: "goal.stateReady",
  paused: "goal.statePaused",
  failed: "goal.stateFailed",
} as const;

const stateTone = {
  planning: { chip: { backgroundColor: colors.surfaceSelected }, dot: { backgroundColor: colors.action } },
  researching: { chip: { backgroundColor: colors.surfaceSelected }, dot: { backgroundColor: colors.action } },
  "needs-input": { chip: { backgroundColor: colors.warningSoft }, dot: { backgroundColor: colors.warning } },
  "review-required": { chip: { backgroundColor: colors.warningSoft }, dot: { backgroundColor: colors.warning } },
  ready: { chip: { backgroundColor: colors.positiveSoft }, dot: { backgroundColor: colors.positive } },
  paused: { chip: { backgroundColor: colors.surfaceSubtle }, dot: { backgroundColor: colors.textMuted } },
  failed: { chip: { backgroundColor: colors.dangerSoft }, dot: { backgroundColor: colors.danger } },
} as const;

const styles = StyleSheet.create({
  card: {
    backgroundColor: colors.surface,
    borderColor: colors.border,
    borderRadius: radius.product,
    borderWidth: StyleSheet.hairlineWidth,
    gap: spacing[3],
    padding: spacing[4],
  },
  header: { alignItems: "flex-start", flexDirection: "row", gap: spacing[3] },
  copy: { flex: 1, minWidth: 0 },
  eyebrow: {
    color: colors.textMuted,
    fontSize: type.helper,
    fontWeight: "500",
    lineHeight: type.helperLine,
  },
  title: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  stateChip: {
    alignItems: "center",
    borderRadius: radius.pill,
    flexDirection: "row",
    gap: spacing[1],
    minHeight: 28,
    paddingHorizontal: spacing[2],
  },
  stateDot: { borderRadius: radius.pill, height: 7, width: 7 },
  stateLabel: { color: colors.text, fontSize: 12, fontWeight: "500", lineHeight: 18 },
  summary: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine },
  progressTrack: {
    backgroundColor: colors.surfaceSubtle,
    borderRadius: radius.pill,
    height: 8,
    overflow: "hidden",
  },
  progressFill: { backgroundColor: colors.action, borderRadius: radius.pill, height: "100%" },
  metaRow: { alignItems: "flex-start", flexDirection: "row", gap: spacing[3], justifyContent: "space-between" },
  progressText: { color: colors.text, flex: 1, fontSize: type.helper, lineHeight: type.helperLine },
  updated: { color: colors.textMuted, fontSize: 12, lineHeight: 18, textAlign: "right" },
  activityButton: { alignSelf: "flex-start", marginHorizontal: -spacing[4], marginBottom: -spacing[3] },
});
