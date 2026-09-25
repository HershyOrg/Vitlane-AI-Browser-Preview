import { StyleSheet, Text, View } from "react-native";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, spacing, type } from "../theme/tokens";

export type RunResultState = "completed" | "partial-success" | "result-unknown";
export type RunEvidenceGrade = "merchant-api" | "merchant-page" | "user-reported" | "unconfirmed";

export type RunResultPresentation = {
  readonly id: string;
  readonly title: string;
  readonly detail: string;
  readonly state: RunResultState;
  readonly evidence: RunEvidenceGrade;
};

export function RunResultCard({ result }: { readonly result: RunResultPresentation }) {
  const { t } = useLocale();
  const tone = stateTone[result.state];
  return (
    <View style={[styles.card, tone.card]} testID={`run.result.${result.id}`}>
      <View style={styles.header}>
        <View style={[styles.dot, tone.dot]} />
        <Text accessibilityRole="header" style={styles.title}>{result.title}</Text>
      </View>
      <Text style={styles.detail}>{result.detail}</Text>
      <View style={styles.evidence} testID={`run.result.evidence.${result.id}`}>
        <Text style={styles.evidenceLabel}>{t("result.evidence")}</Text>
        <Text style={styles.evidenceValue}>{t(evidenceMessageKeys[result.evidence])}</Text>
      </View>
      {result.state === "result-unknown" ? (
        <Text style={styles.unknown}>{t("result.unknownBody")}</Text>
      ) : null}
    </View>
  );
}

const evidenceMessageKeys = {
  "merchant-api": "result.evidenceMerchantApi",
  "merchant-page": "result.evidenceMerchantPage",
  "user-reported": "result.evidenceUserReported",
  unconfirmed: "result.evidenceUnconfirmed",
} as const;

const stateTone = {
  completed: { card: { backgroundColor: colors.positiveSoft }, dot: { backgroundColor: colors.positive } },
  "partial-success": { card: { backgroundColor: colors.warningSoft }, dot: { backgroundColor: colors.warning } },
  "result-unknown": { card: { backgroundColor: colors.dangerSoft }, dot: { backgroundColor: colors.danger } },
} as const;

const styles = StyleSheet.create({
  card: { borderRadius: radius.overlay, gap: spacing[3], padding: spacing[4] },
  header: { alignItems: "center", flexDirection: "row", gap: spacing[2] },
  dot: { borderRadius: radius.pill, height: 9, width: 9 },
  title: { color: colors.text, flex: 1, fontSize: type.heading, fontWeight: "600", lineHeight: type.headingLine },
  detail: { color: colors.text, fontSize: type.body, lineHeight: type.bodyLine },
  evidence: { alignItems: "center", borderTopColor: colors.border, borderTopWidth: StyleSheet.hairlineWidth, flexDirection: "row", gap: spacing[3], justifyContent: "space-between", paddingTop: spacing[3] },
  evidenceLabel: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  evidenceValue: { color: colors.textAccent, fontSize: type.helper, fontWeight: "500", lineHeight: type.helperLine, textAlign: "right" },
  unknown: { color: colors.danger, fontSize: type.helper, lineHeight: type.helperLine },
});
