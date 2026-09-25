import { Modal, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, size, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";
import { AgentAvatar } from "./AgentAvatar";

export type HumanHandoffReason =
  | "login"
  | "verification"
  | "checkout-approval"
  | "final-action"
  | "unsupported"
  | "unknown-result";

export type HumanHandoffPresentation = {
  readonly reason: HumanHandoffReason;
  readonly merchantName: string;
  readonly detail?: string;
  readonly quoteSummary?: string;
};

export type HumanHandoffSheetProps = {
  readonly visible: boolean;
  readonly handoff: HumanHandoffPresentation | null;
  readonly onTakeOver: () => void;
  readonly onStop: () => void;
};

export function HumanHandoffSheet({
  visible,
  handoff,
  onTakeOver,
  onStop,
}: HumanHandoffSheetProps) {
  const { t } = useLocale();
  const insets = useSafeAreaInsets();
  if (!handoff) return null;
  const copy = handoffCopy[handoff.reason];

  return (
    <Modal animationType="slide" onRequestClose={onStop} statusBarTranslucent transparent visible={visible}>
      <View style={styles.root}>
        <Pressable accessible={false} onPress={onStop} style={styles.scrim} />
        <View
          accessibilityViewIsModal
          importantForAccessibility="yes"
          style={[styles.sheet, { paddingBottom: Math.max(insets.bottom, spacing[4]) }]}
          testID="handoff.sheet"
        >
          <View style={styles.handle} />
          <ScrollView contentContainerStyle={styles.content}>
            <View style={styles.agentRow}>
              <AgentAvatar size={42} />
              <View style={styles.agentCopy}>
                <Text style={styles.eyebrow}>{t("handoff.eyebrow")}</Text>
                <Text style={styles.merchant}>{handoff.merchantName}</Text>
              </View>
            </View>
            <Text accessibilityRole="header" style={styles.title} testID="handoff.reason">
              {t(copy.title)}
            </Text>
            <Text style={styles.body}>{handoff.detail ?? t(copy.body)}</Text>
            <View style={styles.privateCard}>
              <View style={styles.privateDot} />
              <Text style={styles.privateText}>{t("handoff.privateMode")}</Text>
            </View>
            {handoff.quoteSummary ? (
              <View style={styles.quote} testID="handoff.quote">
                <Text style={styles.quoteLabel}>{t("handoff.quote")}</Text>
                <Text style={styles.quoteValue}>{handoff.quoteSummary}</Text>
              </View>
            ) : null}
            <View style={styles.actions}>
              <ActionButton
                emphasis="secondary"
                label={t("handoff.stop")}
                onPress={onStop}
                style={styles.action}
                testID="handoff.stop"
              />
              <ActionButton
                emphasis="primary"
                label={t(copy.action)}
                onPress={onTakeOver}
                style={styles.action}
                testID="handoff.take-over"
              />
            </View>
          </ScrollView>
        </View>
      </View>
    </Modal>
  );
}

const handoffCopy = {
  login: { title: "handoff.loginTitle", body: "handoff.loginBody", action: "handoff.takeOver" },
  verification: { title: "handoff.verificationTitle", body: "handoff.verificationBody", action: "handoff.takeOver" },
  "checkout-approval": {
    title: "handoff.checkoutApprovalTitle",
    body: "handoff.checkoutApprovalBody",
    action: "handoff.checkoutApprovalAction",
  },
  "final-action": { title: "handoff.checkoutTitle", body: "handoff.checkoutBody", action: "handoff.continue" },
  unsupported: { title: "handoff.unsupportedTitle", body: "handoff.unsupportedBody", action: "handoff.takeOver" },
  "unknown-result": { title: "result.unknownTitle", body: "result.unknownBody", action: "handoff.takeOver" },
} as const;

const styles = StyleSheet.create({
  root: { flex: 1, justifyContent: "flex-end" },
  scrim: { ...StyleSheet.absoluteFill, backgroundColor: colors.scrim },
  sheet: {
    alignSelf: "center",
    backgroundColor: colors.surface,
    borderTopLeftRadius: radius.sheet,
    borderTopRightRadius: radius.sheet,
    maxHeight: "90%",
    maxWidth: size.contentMax + spacing[8] * 2,
    paddingHorizontal: spacing[4],
    width: "100%",
  },
  handle: { alignSelf: "center", backgroundColor: colors.borderStrong, borderRadius: radius.pill, height: 4, marginTop: spacing[2], width: 40 },
  content: { gap: spacing[4], paddingBottom: spacing[4], paddingTop: spacing[4] },
  agentRow: { alignItems: "center", flexDirection: "row", gap: spacing[3] },
  agentCopy: { flex: 1 },
  eyebrow: { color: colors.textAccent, fontSize: type.helper, fontWeight: "600", lineHeight: type.helperLine },
  merchant: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  title: { color: colors.text, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  body: { color: colors.textMuted, fontSize: type.body, lineHeight: type.bodyLine },
  privateCard: { alignItems: "flex-start", backgroundColor: colors.warningSoft, borderRadius: radius.overlay, flexDirection: "row", gap: spacing[2], padding: spacing[3] },
  privateDot: { backgroundColor: colors.warning, borderRadius: radius.pill, height: 8, marginTop: 6, width: 8 },
  privateText: { color: colors.text, flex: 1, fontSize: type.helper, lineHeight: type.helperLine },
  quote: { backgroundColor: colors.surfaceSubtle, borderRadius: radius.overlay, gap: spacing[1], padding: spacing[3] },
  quoteLabel: { color: colors.textMuted, fontSize: type.helper, fontWeight: "500", lineHeight: type.helperLine },
  quoteValue: { color: colors.text, fontSize: type.body, fontWeight: "600", lineHeight: type.bodyLine },
  actions: { flexDirection: "row", gap: spacing[2] },
  action: { flex: 1 },
});
