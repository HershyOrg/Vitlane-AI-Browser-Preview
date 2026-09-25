import { StyleSheet, Text, View } from "react-native";

import { useLocale } from "../i18n/LocaleProvider";
import { colors, radius, spacing, type } from "../theme/tokens";
import { ActionButton } from "./ActionButton";
import type { BlockingQuestionSurface } from "./surfaceRegistry";

type BlockingQuestionCardProps = {
  surface: BlockingQuestionSurface;
  busyOptionId?: string | null;
  disabled?: boolean;
  error?: string;
  onChoose: (optionId: string) => void;
  onOpenEditor: () => void;
};

export function BlockingQuestionCard({
  surface,
  busyOptionId,
  disabled = false,
  error,
  onChoose,
  onOpenEditor,
}: BlockingQuestionCardProps) {
  const { t } = useLocale();
  const inlineChoices = surface.kind === "question.choice";

  return (
    <View
      accessibilityLabel={`${surface.title}. ${surface.reason}`}
      style={styles.card}
      testID="question.blocking"
    >
      <View style={styles.heading}>
        <View accessibilityElementsHidden style={styles.spark}>
          <Text style={styles.sparkText}>✦</Text>
        </View>
        <View style={styles.headingCopy}>
          <Text style={styles.eyebrow}>{t("workspace.blockingQuestionEyebrow")}</Text>
          <Text accessibilityRole="header" style={styles.title}>{surface.title}</Text>
        </View>
      </View>
      <Text style={styles.reason}>{surface.reason}</Text>
      {error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
      {inlineChoices ? (
        <View style={styles.actions}>
          {surface.options.map((option, index) => (
            <ActionButton
              busy={busyOptionId === option.id}
              disabled={disabled && busyOptionId !== option.id}
              emphasis={index === 0 ? "primary" : "secondary"}
              key={option.id}
              label={option.label}
              onPress={() => onChoose(option.id)}
              style={styles.action}
              testID={`question.blocking.option.${option.id}`}
            />
          ))}
          {surface.allowCustom ? (
            <ActionButton
              disabled={disabled}
              emphasis="quiet"
              label={t("question.custom")}
              onPress={onOpenEditor}
              style={styles.action}
              testID="question.blocking.custom"
            />
          ) : null}
        </View>
      ) : (
        <ActionButton
          disabled={disabled}
          emphasis="primary"
          label={t("workspace.openQuestion")}
          onPress={onOpenEditor}
          testID="question.blocking.open"
        />
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  card: {
    backgroundColor: colors.surfaceSelected,
    borderColor: colors.agentHighlight,
    borderRadius: radius.overlay,
    borderWidth: StyleSheet.hairlineWidth,
    gap: spacing[3],
    marginBottom: spacing[4],
    padding: spacing[4],
  },
  heading: { alignItems: "center", flexDirection: "row", gap: spacing[3] },
  headingCopy: { flex: 1 },
  spark: {
    alignItems: "center",
    backgroundColor: colors.surface,
    borderRadius: radius.pill,
    height: 40,
    justifyContent: "center",
    width: 40,
  },
  sparkText: { color: colors.textAccent, fontSize: 18 },
  eyebrow: { color: colors.textAccent, fontSize: 11, fontWeight: "600", letterSpacing: 0.35, lineHeight: 16 },
  title: { color: colors.text, fontSize: 18, fontWeight: "600", lineHeight: 25, marginTop: 2 },
  reason: { color: colors.textMuted, fontSize: type.helper, lineHeight: type.helperLine },
  error: { color: colors.danger, fontSize: type.helper, lineHeight: type.helperLine },
  actions: { flexDirection: "row", flexWrap: "wrap", gap: spacing[2] },
  action: { flexGrow: 1, minWidth: 120 },
});
