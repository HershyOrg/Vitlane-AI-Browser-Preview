import { StyleSheet, Text, View } from "react-native";

import { colors, radius, spacing, type } from "../theme/tokens";

export type StatusNoticeTone = "positive" | "warning" | "danger";

export function StatusNotice({
  message,
  tone,
  testID,
}: {
  message: string;
  tone: StatusNoticeTone;
  testID?: string;
}) {
  return (
    <View
      accessibilityLiveRegion="polite"
      accessibilityRole={tone === "positive" ? "summary" : "alert"}
      style={[styles.container, styles[`${tone}Container`]]}
      testID={testID}
    >
      <View style={[styles.dot, styles[`${tone}Dot`]]} />
      <Text style={styles.message}>{message}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    alignItems: "flex-start",
    borderRadius: radius.overlay,
    flexDirection: "row",
    gap: spacing[2],
    marginTop: spacing[3],
    padding: spacing[3],
  },
  positiveContainer: { backgroundColor: colors.positiveSoft },
  warningContainer: { backgroundColor: colors.warningSoft },
  dangerContainer: { backgroundColor: colors.dangerSoft },
  dot: {
    borderRadius: radius.pill,
    height: 8,
    marginTop: 6,
    width: 8,
  },
  positiveDot: { backgroundColor: colors.positive },
  warningDot: { backgroundColor: colors.warning },
  dangerDot: { backgroundColor: colors.danger },
  message: {
    color: colors.text,
    flex: 1,
    fontSize: type.helper,
    lineHeight: type.helperLine,
  },
});
