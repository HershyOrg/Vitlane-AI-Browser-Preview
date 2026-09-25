import { Modal, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, size, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";
import { AgentAvatar } from "./AgentAvatar";

export type AgentActivityState =
  | "running"
  | "completed"
  | "waiting"
  | "failed"
  | "cancelled"
  | "review-required";

export type AgentActivityPresentation = {
  readonly id: string;
  readonly title: string;
  readonly detail?: string;
  readonly state: AgentActivityState;
  readonly timeLabel?: string;
};

export type AgentActivityPanelProps = {
  readonly status: string;
  readonly items: readonly AgentActivityPresentation[];
  readonly onStop?: () => void;
  readonly stopping?: boolean;
};

export function AgentActivityPanel({
  status,
  items,
  onStop,
  stopping = false,
}: AgentActivityPanelProps) {
  const { t } = useLocale();

  return (
    <View style={styles.panel}>
      <View style={styles.currentWork}>
        <AgentAvatar active={items.some((item) => item.state === "running")} size={44} />
        <View style={styles.currentCopy}>
          <Text style={styles.eyebrow}>{t("activity.currentWork")}</Text>
          <Text accessibilityLiveRegion="polite" style={styles.currentStatus}>{status}</Text>
        </View>
      </View>

      <View style={styles.timelineHeader}>
        <Text accessibilityRole="header" style={styles.timelineTitle}>{t("activity.timeline")}</Text>
        <Text style={styles.timelineCount}>{t("activity.itemCount", { count: items.length })}</Text>
      </View>

      {items.length ? (
        <View style={styles.timeline}>
          {items.map((item, index) => (
            <View key={item.id} style={styles.item} testID={`activity.${item.id}`}>
              <View style={styles.rail}>
                <View style={[styles.dot, stateDots[item.state]]} />
                {index < items.length - 1 ? <View style={styles.line} /> : null}
              </View>
              <View style={styles.itemCopy}>
                <View style={styles.itemTitleRow}>
                  <Text style={styles.itemTitle}>{item.title}</Text>
                  {item.timeLabel ? <Text style={styles.time}>{item.timeLabel}</Text> : null}
                </View>
                {item.detail ? <Text style={styles.detail}>{item.detail}</Text> : null}
                <Text style={styles.itemState}>{t(activityStateKeys[item.state])}</Text>
              </View>
            </View>
          ))}
        </View>
      ) : (
        <Text style={styles.empty}>{t("activity.empty")}</Text>
      )}

      {onStop ? (
        <ActionButton
          busy={stopping}
          emphasis="danger"
          label={t("activity.stop")}
          onPress={onStop}
          style={styles.stopButton}
        />
      ) : null}
    </View>
  );
}

export type AgentActivitySheetProps = AgentActivityPanelProps & {
  visible: boolean;
  onClose: () => void;
};

export function AgentActivitySheet({ visible, onClose, ...panelProps }: AgentActivitySheetProps) {
  const { t } = useLocale();
  const insets = useSafeAreaInsets();
  return (
    <Modal
      animationType="slide"
      onRequestClose={onClose}
      statusBarTranslucent
      transparent
      visible={visible}
    >
      <View style={styles.modalRoot}>
        <Pressable accessible={false} onPress={onClose} style={styles.scrim} />
        <View
          accessibilityViewIsModal
          importantForAccessibility="yes"
          style={[styles.sheet, { paddingBottom: Math.max(insets.bottom, spacing[3]) }]}
        >
          <View style={styles.handle} />
          <View style={styles.sheetHeader}>
            <Text accessibilityRole="header" style={styles.sheetTitle}>{t("activity.title")}</Text>
            <ActionButton compact emphasis="quiet" label={t("common.close")} onPress={onClose} />
          </View>
          <ScrollView contentContainerStyle={styles.scrollContent}>
            <AgentActivityPanel {...panelProps} />
          </ScrollView>
        </View>
      </View>
    </Modal>
  );
}

const activityStateKeys = {
  running: "activity.stateRunning",
  completed: "activity.stateCompleted",
  waiting: "activity.stateWaiting",
  failed: "activity.stateFailed",
  cancelled: "activity.stateCancelled",
  "review-required": "activity.stateReviewRequired",
} as const;

const stateDots = StyleSheet.create({
  running: { backgroundColor: colors.action },
  completed: { backgroundColor: colors.positive },
  waiting: { backgroundColor: colors.warning },
  failed: { backgroundColor: colors.danger },
  cancelled: { backgroundColor: colors.textMuted },
  "review-required": { backgroundColor: colors.warning },
});

const styles = StyleSheet.create({
  panel: { gap: spacing[5] },
  currentWork: {
    alignItems: "center",
    backgroundColor: colors.surfaceSelected,
    borderRadius: radius.overlay,
    flexDirection: "row",
    gap: spacing[3],
    padding: spacing[3],
  },
  currentCopy: { flex: 1, minWidth: 0 },
  eyebrow: { color: colors.textMuted, fontSize: type.helper, fontWeight: "500", lineHeight: type.helperLine },
  currentStatus: { color: colors.text, fontSize: type.body, fontWeight: "500", lineHeight: type.bodyLine },
  timelineHeader: { alignItems: "center", flexDirection: "row", justifyContent: "space-between" },
  timelineTitle: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  timelineCount: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  timeline: { gap: 0 },
  item: { flexDirection: "row", gap: spacing[3], minHeight: 76 },
  rail: { alignItems: "center", width: 16 },
  dot: { borderRadius: radius.pill, height: 10, marginTop: 6, width: 10 },
  line: { backgroundColor: colors.border, flex: 1, marginVertical: spacing[1], width: StyleSheet.hairlineWidth },
  itemCopy: { flex: 1, gap: spacing[1], paddingBottom: spacing[4] },
  itemTitleRow: { alignItems: "flex-start", flexDirection: "row", gap: spacing[2] },
  itemTitle: { color: colors.text, flex: 1, fontSize: type.body, fontWeight: "500", lineHeight: type.bodyLine },
  time: { color: colors.textMuted, fontSize: 12, lineHeight: 18 },
  detail: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  itemState: { color: colors.textAccent, fontSize: 12, fontWeight: "500", lineHeight: 18 },
  empty: {
    backgroundColor: colors.surfaceSubtle,
    borderRadius: radius.overlay,
    color: colors.textMuted,
    fontSize: type.body,
    lineHeight: type.bodyLine,
    padding: spacing[4],
  },
  stopButton: { alignSelf: "flex-start" },
  modalRoot: { flex: 1, justifyContent: "flex-end" },
  scrim: { ...StyleSheet.absoluteFill, backgroundColor: colors.scrim },
  sheet: {
    alignSelf: "center",
    backgroundColor: colors.surface,
    borderTopLeftRadius: radius.sheet,
    borderTopRightRadius: radius.sheet,
    maxHeight: "88%",
    maxWidth: size.contentMax + spacing[8] * 2,
    width: "100%",
  },
  handle: {
    alignSelf: "center",
    backgroundColor: colors.borderStrong,
    borderRadius: radius.pill,
    height: 4,
    marginTop: spacing[2],
    width: 40,
  },
  sheetHeader: {
    alignItems: "center",
    flexDirection: "row",
    justifyContent: "space-between",
    minHeight: size.header,
    paddingHorizontal: spacing[4],
  },
  sheetTitle: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  scrollContent: { padding: spacing[4], paddingBottom: spacing[6] },
});
