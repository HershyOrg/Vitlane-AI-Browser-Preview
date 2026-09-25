import { StyleSheet, Text, View } from "react-native";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";
import { AgentAvatar } from "./AgentAvatar";

export type BrowserControlMode = "agent" | "user" | "paused";
export type BrowserRunState =
  | "preparing"
  | "needs-user"
  | "verifying"
  | "completed"
  | "result-unknown";

export type BrowserRunPresentation = {
  readonly id: string;
  readonly goal: string;
  readonly merchantName: string;
  readonly origin: string;
  readonly currentAction: string;
  readonly stepLabel: string;
  readonly controlMode: BrowserControlMode;
  readonly state: BrowserRunState;
  readonly dataScope?: string;
  /** True only when the browser host, rather than page or model data, supplied the origin. */
  readonly nativeOriginVerified: boolean;
};

export type BrowserRunPanelProps = {
  readonly run: BrowserRunPresentation;
  readonly onOpenActivity?: () => void;
  readonly onStop?: () => void;
  readonly onTakeOver?: () => void;
  readonly onResume?: () => void;
};

/**
 * Shared RN status surface. The browser host must render the trusted origin and
 * emergency stop in native chrome as well; this panel never grants authority.
 */
export function BrowserRunPanel({
  run,
  onOpenActivity,
  onStop,
  onTakeOver,
  onResume,
}: BrowserRunPanelProps) {
  const { t } = useLocale();
  const agentControlling = run.controlMode === "agent";

  return (
    <View style={styles.panel} testID="browser.agent-status">
      <View style={styles.header}>
        <AgentAvatar active={agentControlling} size={42} />
        <View style={styles.headerCopy}>
          <Text style={styles.eyebrow}>{t("browser.currentWork")}</Text>
          <Text accessibilityLiveRegion="polite" style={styles.currentAction} testID="browser.current-action">
            {run.currentAction}
          </Text>
        </View>
        <View style={[styles.controlChip, controlTone[run.controlMode]]}>
          <View style={[styles.controlDot, controlDotTone[run.controlMode]]} />
          <Text style={styles.controlText}>{t(controlMessageKeys[run.controlMode])}</Text>
        </View>
      </View>

      <View style={styles.goalCard}>
        <Text style={styles.goalLabel}>{t("browser.goal")}</Text>
        <Text numberOfLines={2} style={styles.goal}>{run.goal}</Text>
        <Text style={styles.step}>{run.stepLabel}</Text>
      </View>

      <View style={styles.scope} testID="browser.data-scope">
        <View style={styles.scopeDot} />
        <Text style={styles.scopeText}>{run.dataScope ?? t("browser.scope")}</Text>
      </View>

      <View style={styles.actions}>
        {onOpenActivity ? (
          <ActionButton
            compact
            emphasis="quiet"
            label={t("browser.activity")}
            onPress={onOpenActivity}
            style={styles.action}
            testID="run.open-activity"
          />
        ) : null}
        {agentControlling && onTakeOver ? (
          <ActionButton
            compact
            emphasis="secondary"
            label={t("browser.takeOver")}
            onPress={onTakeOver}
            style={styles.action}
            testID="browser.take-over"
          />
        ) : null}
        {run.controlMode === "user" && onResume ? (
          <ActionButton
            compact
            emphasis="primary"
            label={t("browser.resume")}
            onPress={onResume}
            style={styles.wideAction}
            testID="browser.resume-agent"
          />
        ) : null}
        {agentControlling && onStop ? (
          <ActionButton
            compact
            emphasis="danger"
            label={t("browser.stop")}
            onPress={onStop}
            style={styles.action}
            testID="browser.stop-agent"
          />
        ) : null}
      </View>
    </View>
  );
}

const controlMessageKeys = {
  agent: "browser.controlAgent",
  user: "browser.controlUser",
  paused: "browser.controlPaused",
} as const;

const controlTone = StyleSheet.create({
  agent: { backgroundColor: colors.surfaceSelected },
  user: { backgroundColor: colors.warningSoft },
  paused: { backgroundColor: colors.surfaceSubtle },
});

const controlDotTone = StyleSheet.create({
  agent: { backgroundColor: colors.action },
  user: { backgroundColor: colors.warning },
  paused: { backgroundColor: colors.textMuted },
});

const styles = StyleSheet.create({
  panel: {
    backgroundColor: colors.surface,
    borderColor: colors.border,
    borderRadius: radius.overlay,
    borderWidth: StyleSheet.hairlineWidth,
    gap: spacing[3],
    padding: spacing[4],
  },
  header: { alignItems: "center", flexDirection: "row", gap: spacing[3] },
  headerCopy: { flex: 1, minWidth: 0 },
  eyebrow: { color: colors.textMuted, fontSize: type.helper, fontWeight: "500", lineHeight: type.helperLine },
  currentAction: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  controlChip: {
    alignItems: "center",
    borderRadius: radius.pill,
    flexDirection: "row",
    gap: spacing[1],
    minHeight: 28,
    paddingHorizontal: spacing[2],
  },
  controlDot: { borderRadius: radius.pill, height: 7, width: 7 },
  controlText: { color: colors.text, fontSize: 12, fontWeight: "500", lineHeight: 18 },
  goalCard: { backgroundColor: colors.surfaceSubtle, borderRadius: radius.product, gap: spacing[1], padding: spacing[3] },
  goalLabel: { color: colors.textMuted, fontSize: 12, fontWeight: "500", lineHeight: 18 },
  goal: { color: colors.text, fontSize: type.body, lineHeight: type.bodyLine },
  step: { color: colors.textAccent, fontSize: type.helper, fontWeight: "500", lineHeight: type.helperLine },
  scope: { alignItems: "flex-start", flexDirection: "row", gap: spacing[2] },
  scopeDot: { backgroundColor: colors.positive, borderRadius: radius.pill, height: 7, marginTop: 6, width: 7 },
  scopeText: { color: colors.textMuted, flex: 1, fontSize: type.helper, lineHeight: type.helperLine },
  actions: { alignItems: "center", flexDirection: "row", flexWrap: "wrap", gap: spacing[2] },
  action: { flexGrow: 1 },
  wideAction: { flexBasis: "100%" },
});
